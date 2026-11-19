#!/usr/bin/env python3
"""Check the built site in _site/.

    python3 site/check.py [DIR]

It fails when a link or an #anchor points at nothing, when a page loads something from another site or runs a
script, or when a page is missing the basics (language, title, description, one h1, alt text). Links that
go to another site are listed but not fetched, so the check works offline and gives the same answer every time.
"""

import re
import sys
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent


class Page(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.ids = []
        self.links = []  # (tag, attr, value)
        self.scripts = 0
        self.h1 = 0
        self.title = ""
        self.in_title = False
        self.description = ""
        self.lang = ""
        self.imgs_without_alt = []
        self.has_main = False
        self.style_attrs = 0

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if "id" in a:
            self.ids.append(a["id"])
        if tag == "html":
            self.lang = a.get("lang", "")
        elif tag == "title":
            self.in_title = True
        elif tag == "h1":
            self.h1 += 1
        elif tag == "main":
            self.has_main = True
        elif tag == "meta" and a.get("name") == "description":
            self.description = a.get("content", "")
        elif tag == "script":
            self.scripts += 1
            if "src" in a:
                self.links.append((tag, "src", a["src"]))
        elif tag == "a" and "href" in a:
            self.links.append(("a", "href", a["href"]))
        elif tag == "link" and "href" in a:
            self.links.append(("link", "href", a["href"]))
        elif tag in ("img", "source", "iframe", "video", "audio", "embed", "object", "input") and ("src" in a or "data" in a):
            self.links.append((tag, "src", a.get("src") or a.get("data")))
            if tag == "img" and not a.get("alt"):
                self.imgs_without_alt.append(a.get("src"))
        if tag in ("form",):
            self.links.append(("form", "action", a.get("action", "")))
        if "style" in a and "url(" in a["style"]:
            self.links.append((tag, "style", a["style"]))

    def handle_endtag(self, tag):
        if tag == "title":
            self.in_title = False

    def handle_data(self, data):
        if self.in_title:
            self.title += data


def is_external(url):
    return bool(re.match(r"[a-z][a-z0-9+.-]*:|//", url, re.I))


def main():
    site = Path(sys.argv[1] if len(sys.argv) > 1 else ROOT / "_site").resolve()
    if not (site / "index.html").is_file():
        sys.exit(f"check: {site}/index.html is missing; run 'python3 site/build.py' first")

    errors = []
    external = {}
    pages = {}
    for f in sorted(site.rglob("*.html")):
        p = Page()
        text = f.read_text(encoding="utf-8")
        p.feed(text)
        pages[f] = p

    linked_to = set()
    for f, p in pages.items():
        rel = f.relative_to(site)
        if p.lang != "en":
            errors.append(f"{rel}: <html> needs lang=\"en\"")
        if not p.title.strip():
            errors.append(f"{rel}: no <title>")
        if not p.description.strip():
            errors.append(f"{rel}: no meta description")
        if p.h1 != 1:
            errors.append(f"{rel}: expected one <h1>, found {p.h1}")
        if not p.has_main:
            errors.append(f"{rel}: no <main>")
        if p.scripts:
            errors.append(f"{rel}: has a <script>; the site has no JavaScript")
        for src in p.imgs_without_alt:
            errors.append(f"{rel}: image {src} has no alt text")
        dup = {i for i in p.ids if p.ids.count(i) > 1}
        if dup:
            errors.append(f"{rel}: duplicate ids: {', '.join(sorted(dup))}")

        for tag, attr, value in p.links:
            if attr == "style":
                errors.append(f"{rel}: inline style with url(): {value}")
                continue
            if is_external(value):
                if tag != "a":
                    errors.append(f"{rel}: <{tag}> loads {value} from another site")
                else:
                    external.setdefault(value, set()).add(str(rel))
                continue
            if tag == "form":
                errors.append(f"{rel}: has a <form>")
                continue
            u = urlsplit(value)
            if u.path == "":
                target = f
            else:
                target = (f.parent / unquote(u.path)).resolve()
                if target.is_dir() or u.path.endswith("/"):
                    target = target / "index.html"
            if site not in target.parents and target != site:
                errors.append(f"{rel}: {value} points outside the site")
                continue
            if not target.exists():
                errors.append(f"{rel}: {value} does not exist")
                continue
            if tag == "a":
                linked_to.add(target)
            if u.fragment and target.suffix == ".html":
                tp = pages.get(target)
                if tp is None or u.fragment not in tp.ids:
                    errors.append(f"{rel}: {value} has no #{u.fragment} on that page")

    # pages nothing links to
    for f in pages:
        if f != site / "index.html" and f not in linked_to:
            errors.append(f"{f.relative_to(site)}: no page links here")

    # the stylesheet
    for css in sorted(site.rglob("*.css")):
        text = css.read_text(encoding="utf-8")
        rel = css.relative_to(site)
        if re.search(r"@import", text):
            errors.append(f"{rel}: has an @import")
        for m in re.finditer(r"url\(\s*['\"]?([^'\")]+)['\"]?\s*\)", text):
            url = m.group(1)
            if is_external(url):
                errors.append(f"{rel}: loads {url} from another site")
            elif not (css.parent / url).resolve().is_file():
                errors.append(f"{rel}: url({url}) does not exist")

    if errors:
        print("check: found problems\n")
        for e in errors:
            print("  " + e)
        sys.exit(1)

    print(f"check: {len(pages)} pages, no broken links or anchors, no scripts, nothing loaded from another site")
    print(f"check: {len(external)} external links (not fetched):")
    for url in sorted(external):
        print("  " + url)


if __name__ == "__main__":
    main()
