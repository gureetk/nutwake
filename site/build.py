#!/usr/bin/env python3
"""Build the nutwake website into _site/.

The landing page is a template in site/templates. The guide pages are the Markdown files in docs/guide,
so the guide is written once and read in two places: on GitHub, and on the site.

    python3 site/build.py [--out DIR] [--version vX.Y.Z]

The output is plain HTML and CSS with relative links, so it works at gureetk.github.io/nutwake/ and
on any other address. It has no JavaScript and loads nothing from another site.
"""

import argparse
import html
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

import markdown

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
GUIDE = ROOT / "docs" / "guide"
REPO = "https://github.com/gureetk/nutwake"

sys.path.insert(0, str(HERE))
import chart  # noqa: E402


def fail(msg):
    sys.exit(f"build: {msg}")


# ---- version -------------------------------------------------------------------------------------
def find_version(arg):
    """The newest release tag, so the download button names a real file. Falls back to a plain 'latest'."""
    for v in (arg, os.environ.get("NUTWAKE_VERSION")):
        if v:
            return v
    try:
        out = subprocess.run(
            ["git", "describe", "--tags", "--abbrev=0"], cwd=ROOT, capture_output=True, text=True, check=True
        ).stdout.strip()
        if re.fullmatch(r"v\d+\.\d+\.\d+\S*", out):
            return out
    except (subprocess.CalledProcessError, FileNotFoundError):
        pass
    return None


# ---- templates -----------------------------------------------------------------------------------
def fill(template, values, name):
    """Replace {{name}} tokens in one pass. A token with no value is a mistake, so it stops the build."""

    def sub(m):
        key = m.group(1)
        if key not in values:
            fail(f"{name}: no value for {{{{{key}}}}}")
        return values[key]

    return re.sub(r"\{\{(\w+)\}\}", sub, template)


def read_template(name):
    return (HERE / "templates" / name).read_text(encoding="utf-8")


# ---- logo and mark, themed by the page's own colours ----------------------------------------------
def themed_svg(path, drop_aria=True):
    s = (ROOT / "docs" / path).read_text(encoding="utf-8")
    steps = [
        ("<title>nutwake</title>", ""),
        ('fill="#2a6f86" stroke="#2a6f86"', 'class="logo-teal"'),
        ('role="img" aria-label="nutwake"', 'aria-hidden="true" focusable="false"'),
    ]
    if path == "logo.svg":
        steps += [('fill="#12202b" transform', 'class="logo-word" transform'), ('width="341" height="78"', "")]
    else:
        # The footer mark and the logo sit on one page, so their clip paths need different ids.
        steps += [('id="km"', 'id="kf"'), ("url(#km)", "url(#kf)"), ('width="256" height="256"', "")]
    for old, new in steps:
        if s.count(old) != 1:
            fail(f"docs/{path}: expected exactly one {old!r}, found {s.count(old)}")
        s = s.replace(old, new)
    return s


# ---- the guide -----------------------------------------------------------------------------------
class Page:
    def __init__(self, slug, src):
        self.slug = slug
        self.src = src
        self.text = src.read_text(encoding="utf-8")
        m = re.match(r"#\s+(.+?)\s*\n", self.text)
        if not m:
            fail(f"{src.relative_to(ROOT)}: the first line must be a '# Title'")
        self.title = m.group(1)
        self.href = "index.html" if slug == "README" else f"{slug}.html"
        self.body = ""
        self.summary = ""


def load_nav():
    groups = json.loads((HERE / "nav.json").read_text(encoding="utf-8"))
    pages = {p.stem: Page(p.stem, p) for p in sorted(GUIDE.glob("*.md"))}
    listed = [slug for g in groups for slug in g["pages"]]
    for slug in listed:
        if slug not in pages:
            fail(f"site/nav.json lists {slug!r} but docs/guide/{slug}.md does not exist")
    for slug in pages:
        if slug != "README" and slug not in listed:
            fail(f"docs/guide/{slug}.md is not in site/nav.json, so nothing on the site would link to it")
    return groups, pages


LINK = re.compile(r'(<a\s[^>]*?href=")([^"]+)(")')


def rewrite_link(href):
    """Links in the Markdown work on GitHub. On the site, .md becomes .html and repo files go to GitHub."""
    if re.match(r"[a-z][a-z0-9+.-]*:", href) or href.startswith("#"):
        return href
    path, _, frag = href.partition("#")
    frag = "#" + frag if frag else ""
    if path.startswith("../../"):
        return f"{REPO}/blob/main/{path[6:]}{frag}"
    if path.endswith(".md"):
        name = path[:-3]
        if "/" in name:
            fail(f"link {href!r} points outside docs/guide")
        return ("index" if name == "README" else name) + ".html" + frag
    return href


def render(pages):
    md = markdown.Markdown(extensions=["tables", "fenced_code", "sane_lists", "toc"])
    for page in pages.values():
        md.reset()
        out = md.convert(page.text)
        out = LINK.sub(lambda m: m.group(1) + html.escape(rewrite_link(html.unescape(m.group(2))), quote=True) + m.group(3), out)
        page.body = out
        p = re.search(r"<p>(.*?)</p>", out, re.S)
        if p:
            page.summary = re.sub(r"\s+", " ", html.unescape(re.sub(r"<[^>]+>", "", p.group(1)))).strip()


def sidebar(groups, pages, current):
    def link(page):
        cur = ' aria-current="page"' if page.slug == current else ""
        return f'<li><a href="{page.href}"{cur}>{html.escape(page.title)}</a></li>'

    overview = pages["README"]
    parts = [f'<ul>{link(overview)}</ul>']
    for g in groups:
        items = "".join(link(pages[s]) for s in g["pages"])
        parts.append(f'<h2>{html.escape(g["group"])}</h2><ul>{items}</ul>')
    return "".join(parts)


def pager(groups, pages, current):
    order = ["README"] + [s for g in groups for s in g["pages"]]
    i = order.index(current)
    out = []
    if i > 0:
        prev = pages[order[i - 1]]
        label = "Guide overview" if prev.slug == "README" else prev.title
        out.append(f'<a class="prev" href="{prev.href}"><span>Previous</span>{html.escape(label)}</a>')
    if i < len(order) - 1:
        nxt = pages[order[i + 1]]
        out.append(f'<a class="next" href="{nxt.href}"><span>Next</span>{html.escape(nxt.title)}</a>')
    return f'<nav class="pager" aria-label="Guide pages in order">{"".join(out)}</nav>'


# ---- assemble -----------------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", default=str(ROOT / "_site"), help="where to write the site (replaced each time)")
    ap.add_argument("--version", help="release tag to show, for example v0.1.0 (default: the newest tag)")
    args = ap.parse_args()
    out = Path(args.out).resolve()
    # Only clear a directory this script made: it leaves a .nojekyll file in everything it writes.
    if out.exists() and any(out.iterdir()) and not (out / ".nojekyll").exists():
        fail(f"refusing to clear {out}: it is not empty and was not made by this script")

    version = find_version(args.version)
    if version:
        values_release = dict(
            release_url=f"{REPO}/releases/tag/{version}",
            download_label=f"Download {version}",
            version_label=version,
            prerelease_note=(
                f"{version} is a pre-release for Linux on arm64, armv7 and x86-64. It has been tested with a fake UPS "
                "and fake nodes, and has not run on real hardware yet."
            ),
        )
    else:
        values_release = dict(
            release_url=f"{REPO}/releases/latest",
            download_label="Download the latest release",
            version_label="This release",
            prerelease_note=(
                "nutwake is for Linux on arm64, armv7 and x86-64. It has been tested with a fake UPS "
                "and fake nodes, and has not run on real hardware yet."
            ),
        )

    groups, pages = load_nav()
    render(pages)

    logo = themed_svg("logo.svg")
    mark = themed_svg("mark.svg")
    base = read_template("base.html")

    def page_html(content, *, root, title, description, guide_current=""):
        return fill(
            base,
            dict(
                root=root, title=html.escape(title), description=html.escape(description, quote=True), logo=logo,
                mark=mark, repo=REPO, guide_current=guide_current, content=content,
            ),
            "base.html",
        )

    if out.exists():
        shutil.rmtree(out)
    (out / "guide").mkdir(parents=True)
    (out / "assets" / "fonts").mkdir(parents=True)

    # landing page
    landing = fill(
        read_template("index.html"),
        dict(root="", chart=chart.chart(), **values_release),
        "index.html",
    )
    (out / "index.html").write_text(
        page_html(
            landing,
            root="",
            title="nutwake: clean shutdowns and wake-ups for a homelab on a UPS",
            description="nutwake runs on a Raspberry Pi, shuts your nodes down clean when the power goes, "
            "wakes only the ones it shut down, and records it all on a read-only status page.",
        ),
        encoding="utf-8",
    )

    # guide pages
    guide_t = read_template("guide.html")
    for page in pages.values():
        content = fill(
            guide_t,
            dict(
                menu=sidebar(groups, pages, page.slug), body=page.body, pager=pager(groups, pages, page.slug),
            ),
            "guide.html",
        )
        (out / "guide" / page.href).write_text(
            page_html(
                content,
                root="../",
                title=f"{page.title} · nutwake guide" if page.slug != "README" else "nutwake guide",
                description=page.summary or "How to set nutwake up, and what it does.",
                guide_current=' aria-current="true"',
            ),
            encoding="utf-8",
        )

    # assets: the stylesheet and screenshot live in site/assets, the font and mark come from the repo
    for f in (HERE / "assets").iterdir():
        shutil.copy2(f, out / "assets" / f.name)
    fonts = ROOT / "internal" / "web" / "assets" / "fonts"
    for f in fonts.iterdir():
        shutil.copy2(f, out / "assets" / "fonts" / f.name)
    for name in ("mark.svg", "mark.png"):
        shutil.copy2(ROOT / "docs" / name, out / "assets" / name)
    (out / ".nojekyll").write_text("", encoding="utf-8")

    n = sum(1 for _ in out.rglob("*.html"))
    print(f"build: wrote {n} pages to {out} (release shown: {version or 'latest'})")


if __name__ == "__main__":
    main()
