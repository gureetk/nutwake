// nutwake status page. Reads /api/status and draws it. It never sends anything
// but the range you pick, and the server never writes anything.
"use strict";

(function () {
  var SVGNS = "http://www.w3.org/2000/svg";
  var data = null; // last good response
  var lastGood = 0;
  var range = (location.hash.match(/range=([\w]+)/) || [])[1] || "auto";
  var timer = 0;

  // ---------------------------------------------------------------- helpers

  function el(tag, props) {
    var node = document.createElement(tag);
    apply(node, props, Array.prototype.slice.call(arguments, 2));
    return node;
  }

  function svg(tag, props) {
    var node = document.createElementNS(SVGNS, tag);
    apply(node, props, Array.prototype.slice.call(arguments, 2));
    return node;
  }

  function apply(node, props, kids) {
    Object.keys(props || {}).forEach(function (k) {
      var v = props[k];
      if (v === undefined || v === null || v === false) return;
      if (k === "style") {
        Object.keys(v).forEach(function (s) { node.style.setProperty(s, v[s]); });
      } else if (k === "text") {
        node.textContent = v;
      } else if (k.slice(0, 2) === "on") {
        node.addEventListener(k.slice(2), v);
      } else {
        node.setAttribute(k, v === true ? "" : v);
      }
    });
    kids.forEach(function add(c) {
      if (c === undefined || c === null || c === false) return;
      if (Array.isArray(c)) { c.forEach(add); return; }
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
  }

  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

  function put(node) {
    clear(node);
    Array.prototype.slice.call(arguments, 1).forEach(function (c) { if (c) node.appendChild(c); });
  }

  // ------------------------------------------------------------- formatting

  function intl(opts) { return new Intl.DateTimeFormat(undefined, opts); }
  var F = {
    time: intl({ hour: "numeric", minute: "2-digit" }),
    timeSec: intl({ hour: "numeric", minute: "2-digit", second: "2-digit" }),
    date: intl({ weekday: "short", month: "short", day: "numeric" }),
    day: intl({ month: "short", day: "numeric" }),
    stamp: intl({ month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }),
    hour: intl({ hour: "numeric" })
  };

  function lower(s) { return s.replace(/[  ]/g, " ").replace(/ ?([AP])M\b/g, function (_, a) { return " " + a.toLowerCase() + "m"; }); }
  function fTime(t) { return lower(F.time.format(new Date(t))); }
  function fTimeSec(t) { return lower(F.timeSec.format(new Date(t))); }
  function fStamp(t) { return lower(F.stamp.format(new Date(t))); }
  function fDate(t) { return F.date.format(new Date(t)).replace(",", ""); }

  // Text from the server may carry {{time:..}} tokens, so the Pi does not have
  // to guess the reader's time zone.
  function words(s) {
    return String(s || "").replace(/\{\{(time|date|datetime):([^}]+)\}\}/g, function (_, kind, iso) {
      if (kind === "time") return fTime(iso);
      if (kind === "date") return fDate(iso);
      return fDate(iso) + ", " + fTime(iso);
    });
  }

  function dur(sec) {
    sec = Math.round(sec);
    if (sec < 60) return sec + " s";
    if (sec < 3600) return Math.floor(sec / 60) + " min";
    var h = Math.floor(sec / 3600), m = Math.floor((sec - h * 3600) / 60);
    return h + " h " + (m < 10 ? "0" : "") + m + " min";
  }

  function span(a, b) { return (new Date(b) - new Date(a)) / 1000; }
  function pctText(v) { return Math.round(v) + "%"; }

  // ------------------------------------------------------------------ icons

  function icon(tone, size) {
    size = size || 16;
    var common = { width: size, height: size, viewBox: "0 0 16 16", "aria-hidden": "true", "class": "icon" };
    if (tone === "ok") {
      return svg("svg", common,
        svg("circle", { cx: 8, cy: 8, r: 8, fill: "var(--ok)" }),
        svg("path", { d: "M4.5 8.2l2.4 2.4 4.6-5", fill: "none", stroke: "var(--on-icon)", "stroke-width": 1.8, "stroke-linecap": "round", "stroke-linejoin": "round" }));
    }
    if (tone === "warn") {
      return svg("svg", common,
        svg("path", { d: "M8 1.5L15 14H1Z", fill: "var(--warn)", stroke: "var(--warn)", "stroke-width": 1.5, "stroke-linejoin": "round" }),
        svg("path", { d: "M8 6v4", stroke: "#12202b", "stroke-width": 1.6, "stroke-linecap": "round" }),
        svg("circle", { cx: 8, cy: 12, r: 0.9, fill: "#12202b" }));
    }
    if (tone === "crit") {
      return svg("svg", common,
        svg("circle", { cx: 8, cy: 8, r: 8, fill: "var(--crit)" }),
        svg("path", { d: "M5.3 5.3l5.4 5.4M10.7 5.3l-5.4 5.4", stroke: "#ffffff", "stroke-width": 1.8, "stroke-linecap": "round" }));
    }
    if (tone === "wait") {
      return svg("svg", common,
        svg("circle", { cx: 8, cy: 8, r: 6.7, fill: "none", stroke: "var(--warn)", "stroke-width": 1.8 }),
        svg("path", { d: "M8 4.2V8l2.6 1.7", fill: "none", stroke: "var(--ink)", "stroke-width": 1.6, "stroke-linecap": "round", "stroke-linejoin": "round" }));
    }
    return svg("svg", common,
      svg("circle", { cx: 8, cy: 8, r: 6.6, fill: "none", stroke: "var(--ink3)", "stroke-width": 1.6 }),
      svg("circle", { cx: 8, cy: 8, r: 1.8, fill: "var(--ink3)" }));
  }

  var LEVEL_TONE = { ok: "ok", warn: "warn", crit: "crit", info: "info" };

  // ----------------------------------------------------------------- render

  function render(d) {
    document.title = d.headline.title + " – nutwake";
    renderTop(d);
    renderStatus(d);
    renderWake(d);
    renderRecorder(d);
    renderOutages(d);
    renderLog(d);
    renderSettings(d);
    put($("foot"), el("p", {}, "nutwake " + (d.version || "dev") + ". Refreshes every " + d.refresh_s + " s. This page is read only."));
  }

  function $(id) { return document.getElementById(id); }

  function renderTop(d) {
    var u = d.ups;
    var model = [u.manufacturer, u.model].filter(Boolean).join(" ");
    var who = el("div", { "class": "who" },
      el("span", { "class": "name", text: "nutwake" }),
      el("span", { text: d.ups_name }),
      model ? el("span", { text: model }) : null);
    var meta = el("div", { "class": "meta" },
      el("span", { text: "Updated " + fTimeSec(d.now) }),
      el("span", { text: "Pi up " + uptime(d.pi_uptime_s) }),
      d.clock_set ? null : el("span", { text: "Clock not set yet, so times may be off" }));
    put($("top"), who, meta);
  }

  function uptime(s) {
    var days = Math.floor(s / 86400), hours = Math.floor((s % 86400) / 3600), mins = Math.floor((s % 3600) / 60);
    if (days > 0) return days + " d " + hours + " h";
    if (hours > 0) return hours + " h " + mins + " min";
    return mins + " min";
  }

  function renderStatus(d) {
    var hl = d.headline;
    var lead = el("div", { "class": "lead" },
      icon(hl.tone === "wait" ? "wait" : hl.tone, 44),
      el("div", {},
        el("h1", { id: "headline", text: hl.title }),
        el("p", { text: words(hl.text) })));

    var u = d.ups, figs = [];
    if (u.reachable) {
      if (u.charge != null) figs.push([pctText(u.charge), "Battery"]);
      if (u.load != null) figs.push([pctText(u.load), "Load"]);
      if (u.runtime_s != null) figs.push([dur(u.runtime_s), "Runtime left"]);
      if (u.input_voltage != null) figs.push([Math.round(u.input_voltage) + " V", "Input voltage"]);
    }
    var figures = el("div", { "class": "figures" }, figs.map(function (f) {
      return el("div", { "class": "figure" }, el("b", { text: f[0] }), el("span", { text: f[1] }));
    }));
    put($("status"), lead, figures);
  }

  function renderWake(d) {
    var w = d.wake, node = $("wake");
    if (!w.active) { node.hidden = true; clear(node); return; }
    node.hidden = false;

    var waiting = !!w.waiting;
    var kids = [el("h2", { id: "wake-title", text: waiting ? "Waiting to wake the nodes" : "Waking the nodes" })];
    if (w.message) kids.push(el("p", { text: words(w.message) }));

    if (w.waiting === "battery" && d.ups.charge != null) {
      var need = w.min_battery;
      kids.push(el("div", { "class": "meter", role: "img", "aria-label": "Battery " + pctText(d.ups.charge) + ", needs " + pctText(need) },
        el("div", { "class": "fill", style: { width: Math.min(100, d.ups.charge) + "%" } }),
        el("div", { "class": "need", style: { left: need + "%" } }, el("span", { text: "wake at " + pctText(need) }))));
    }
    var chips = (w.nodes || []).map(function (n) {
      var state = { waiting: "waiting", queued: "next in line", sent: "packet sent" }[n.state] || n.state;
      if (n.state === "sent" && n.attempts > 1) state = n.attempts + " packets sent";
      return el("div", { "class": "chip" }, el("b", { text: n.name }), el("span", { text: state }));
    });
    kids.push(el("div", { "class": "chips" }, chips));
    put.apply(null, [node].concat(kids));
  }

  // ---- recorder

  function renderRecorder(d) {
    var tl = d.timeline, node = $("recorder");
    var t0 = new Date(tl.from).getTime(), t1 = new Date(tl.to).getTime(), len = t1 - t0;
    var pc = function (t) { return Math.max(0, Math.min(100, (new Date(t).getTime() - t0) / len * 100)); };

    var head = el("div", { "class": "rec-head" },
      el("div", {}, el("h2", { id: "recorder-title", text: words(tl.title) }), el("p", { text: words(tl.sub) })),
      rangeButtons(d));
    var kids = [head];

    if (!tl.has_data) {
      kids.push(el("p", { "class": "empty", text: "Nothing has been recorded yet. The chart fills in as nutwake runs." }));
      put.apply(null, [node].concat(kids));
      return;
    }

    var ticks = niceTicks(t0, t1);
    // bands and ticks go behind the bars, so they are put first
    function deco(track) {
      var behind = [];
      tl.bands.forEach(function (b) {
        behind.push(el("div", { "class": "band", style: { left: pc(b.from) + "%", width: (pc(b.to) - pc(b.from)) + "%" } }));
      });
      ticks.forEach(function (t) { behind.push(el("div", { "class": "tick", style: { left: t.pos + "%" } })); });
      behind.reverse().forEach(function (n) { track.insertBefore(n, track.firstChild); });
      return track;
    }
    function marks(track) {
      tl.flags.forEach(function (f) { track.appendChild(el("div", { "class": "mark", style: { left: pc(f.t) + "%" } })); });
      return track;
    }
    function row(name, sub, track, cls) {
      return el("div", { "class": "row " + (cls || "") },
        el("div", { "class": "label" }, el("b", { text: name }), sub ? el("span", { text: sub }) : null), track);
    }

    // flags
    var flagTrack = el("div", { "class": "flags" });
    var flagEls = tl.flags.map(function (f) {
      var left = pc(f.t);
      var when = (f.approx ? "about " : "") + fTime(f.t);
      var e = el("div", { "class": "flag" + (left > 82 ? " late" : ""), style: { left: left + "%" }, title: f.label + ", " + when },
        el("b", { text: f.label }), el("span", { text: when }));
      flagTrack.appendChild(e);
      return e;
    });
    kids.push(el("div", { "class": "row flagsrow", style: { "margin-top": "0" } }, el("div"), flagTrack));

    var lanes = [];
    var lanesNode = el("div", { role: "group", "aria-label": "Timeline of power, battery and machines" });
    kids.push(lanesNode);

    // mains
    lanesNode.appendChild(row("Mains", "from the UPS", marks(deco(barTrack(tl.mains, "Mains", pc, lanes)))));
    // battery
    lanesNode.appendChild(row("Battery", "charge", marks(deco(batteryTrack(tl, pc, t0, t1))), "chart"));
    // nodes
    tl.nodes.forEach(function (n) {
      lanesNode.appendChild(row(n.name, n.host, marks(deco(barTrack(n.segments, n.name, pc, lanes)))));
    });
    // pi
    lanesNode.appendChild(row("This Pi", location.hostname, marks(deco(barTrack(tl.pi, "Pi", pc, lanes)))));

    // axis
    var axis = el("div", { "class": "axis" }, ticks.map(function (t) {
      return el("span", { "class": t.pos > 94 ? "end" : t.pos < 6 ? "start" : "", style: { left: t.pos + "%" }, text: t.label });
    }));
    kids.push(el("div", { "class": "row" }, el("div"), axis));

    if (tl.pi.some(function (s) { return s.kind === "gap"; })) {
      kids.push(el("p", { "class": "note", text: "Hatched areas are times the Pi was off, so nothing was recorded. The Pi is the thing doing the recording." }));
    }
    kids.push(tableView(tl, d));

    put.apply(null, [node].concat(kids));

    var fit = function () {
      fitLabels(node);
      fitFlags(flagTrack, flagEls);
      fitAxis(axis);
    };
    fit();
    // text widths change once the font has loaded
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(fit);
  }

  // Drops tick labels that would run into the one before them.
  function fitAxis(axis) {
    var right = -Infinity;
    Array.prototype.forEach.call(axis.children, function (s) {
      s.style.visibility = "visible";
      var r = s.getBoundingClientRect();
      if (r.left < right + 12) { s.style.visibility = "hidden"; return; }
      right = r.right;
    });
  }

  function rangeButtons(d) {
    var names = { outage: d.outages[0] && d.outages[0].in_progress ? "This outage" : "Outage", "24h": "24 hours", "7d": "7 days" };
    return el("div", { "class": "ranges", role: "group", "aria-label": "Time range" },
      (d.timeline.ranges || []).map(function (r) {
        return el("button", {
          type: "button", "aria-pressed": r === d.timeline.range ? "true" : "false", text: names[r] || r,
          onclick: function () { range = r; location.hash = "range=" + r; load(); }
        });
      }));
  }

  var LABELS = { mains: "On mains", battery: "On battery", other: "Not on mains", unknown: "Unknown", up: "Up" };

  function segLabel(s, lane) {
    if (s.kind === "gap") return lane === "Mains" ? "Pi off" : "Off";
    if (s.kind === "off") return "Off for " + dur(span(s.from, s.to));
    return LABELS[s.kind] || s.kind;
  }

  function barTrack(segs, lane, pc, lanes) {
    var track = el("div", { "class": "track" });
    segs.forEach(function (s) {
      var left = pc(s.from), width = pc(s.to) - left;
      var label = segLabel(s, lane);
      var node = el("div", {
        "class": "seg " + s.kind, style: { left: left + "%", width: width + "%" },
        title: label + ", " + fTime(s.from) + " to " + fTime(s.to) + " (" + dur(span(s.from, s.to)) + ")"
      });
      node.dataset.label = label;
      track.appendChild(node);
    });
    lanes.push(track);
    return track;
  }

  // Segments are drawn in percent of the width, so a label is shown only when
  // the segment is wide enough to hold it.
  function fitLabels(root) {
    Array.prototype.forEach.call(root.querySelectorAll(".seg"), function (n) {
      if (n.dataset.label === undefined) return;
      var label = n.dataset.label;
      n.textContent = n.getBoundingClientRect().width >= label.length * 7 + 24 ? label : "";
    });
  }

  function fitFlags(track, flags) {
    var right = -Infinity, rect = track.getBoundingClientRect();
    flags.forEach(function (f) {
      f.style.visibility = "visible";
      var r = f.getBoundingClientRect();
      var late = f.classList.contains("late");
      var l = r.left - rect.left;
      if (l < right) {
        // not enough room for the words: keep the line, drop the label
        f.querySelector("b").style.visibility = "hidden";
        f.querySelector("span").style.visibility = "hidden";
      } else {
        right = Math.max(right, r.right - rect.left);
      }
    });
  }

  function batteryTrack(tl, pc, t0, t1) {
    var H = 96;
    var track = el("div", { "class": "track tall" });
    var b = tl.battery;
    var x = function (t) { return (pc(t) * 10).toFixed(1); };
    var y = function (c) { return (100 - c).toFixed(1); };

    var box = svg("svg", { "class": "chartbox", viewBox: "0 0 1000 100", preserveAspectRatio: "none", "aria-hidden": "true" });
    [b.wake_at, b.shutdown_at].forEach(function (g) {
      if (!g) return;
      box.appendChild(svg("line", { x1: 0, x2: 1000, y1: y(g), y2: y(g), stroke: "var(--guide)", "stroke-width": 1, "vector-effect": "non-scaling-stroke" }));
    });
    var line = "", area = "";
    b.runs.forEach(function (run) {
      if (!run.length) return;
      var d = run.map(function (p, i) { return (i ? "L" : "M") + x(p.t) + " " + y(p.c); }).join(" ");
      line += d + " ";
      area += d + " L" + x(run[run.length - 1].t) + " 100 L" + x(run[0].t) + " 100 Z ";
    });
    box.appendChild(svg("path", { d: area, fill: "var(--area)", stroke: "none" }));
    box.appendChild(svg("path", { d: line, fill: "none", stroke: "var(--ink)", "stroke-width": 2, "stroke-linecap": "round", "stroke-linejoin": "round" }));
    if (b.projection) {
      box.appendChild(svg("path", {
        d: "M" + x(b.projection.from.t) + " " + y(b.projection.from.c) + " L" + x(b.projection.to.t) + " " + y(b.projection.to.c),
        fill: "none", stroke: "var(--ink)", "stroke-width": 2, "stroke-dasharray": "2 6", "stroke-linecap": "round"
      }));
    }

    // "not recorded" where the Pi was off
    var gaps = tl.pi.filter(function (s) { return s.kind === "gap"; }).map(function (s) {
      var left = pc(s.from), width = pc(s.to) - left;
      var g = el("div", { "class": "seg gap", style: { left: left + "%", width: width + "%", top: "0", bottom: "auto", height: H + "px", "border-radius": "0", "justify-content": "center" }, title: "Not recorded: the Pi was off" });
      g.appendChild(el("span", { "class": "gapnote", text: "not recorded" }));
      return g;
    });

    track.appendChild(box);
    gaps.forEach(function (g) { track.appendChild(g); });

    if (b.wake_at) track.appendChild(el("div", { "class": "guidelabel", style: { top: ((100 - b.wake_at) * H / 100 - 18).toFixed(0) + "px" }, text: "wake nodes at " + pctText(b.wake_at) }));
    if (b.shutdown_at) track.appendChild(el("div", { "class": "guidelabel", style: { top: ((100 - b.shutdown_at) * H / 100 + 3).toFixed(0) + "px" }, text: "shut down at " + pctText(b.shutdown_at) }));

    // the latest reading, and where the shutdown is expected
    var last = null;
    b.runs.forEach(function (r) { if (r.length) last = r[r.length - 1]; });
    if (last) {
      var lx = pc(last.t), top = ((100 - last.c) * H / 100 - 4);
      track.appendChild(el("span", { "class": "dot", style: { left: "calc(" + lx + "% - 4px)", top: top.toFixed(1) + "px" } }));
      var right = lx > 88; // the label sits above the dot: to its right, or ending at it near the right edge
      track.appendChild(el("span", {
        "class": "dotlabel",
        style: right ? { left: "calc(" + lx + "% + 4px)", top: Math.max(0, top - 22).toFixed(1) + "px", transform: "translateX(-100%)" } : { left: "calc(" + lx + "% + 6px)", top: Math.max(0, top - 22).toFixed(1) + "px" },
        text: pctText(last.c)
      }));
    }
    if (b.projection) {
      var px = pc(b.projection.to.t), py = ((100 - b.projection.to.c) * H / 100 - 4);
      track.appendChild(el("span", { "class": "dot hollow", style: { left: "calc(" + px + "% - 4px)", top: py.toFixed(1) + "px" }, title: "Where the battery is expected to be when the shutdown starts" }));
    }

    // hover
    var cross = el("div", { "class": "cross", hidden: true });
    var tip = el("div", { "class": "tip", hidden: true });
    track.appendChild(cross);
    track.appendChild(tip);
    var points = [];
    b.runs.forEach(function (r) { r.forEach(function (p) { points.push({ t: new Date(p.t).getTime(), c: p.c }); }); });
    track.addEventListener("pointermove", function (e) {
      var rect = track.getBoundingClientRect();
      var t = t0 + (e.clientX - rect.left) / rect.width * (t1 - t0);
      var best = null, bestD = Infinity;
      points.forEach(function (p) { var dd = Math.abs(p.t - t); if (dd < bestD) { best = p; bestD = dd; } });
      if (!best || bestD > (t1 - t0) * 0.03) { cross.hidden = true; tip.hidden = true; return; }
      var left = (best.t - t0) / (t1 - t0) * 100;
      cross.hidden = false; tip.hidden = false;
      cross.style.left = left + "%";
      tip.style.left = left + "%";
      tip.style.top = Math.max(18, (100 - best.c) * H / 100 - 8) + "px";
      tip.textContent = pctText(best.c) + " at " + fTime(best.t);
    });
    track.addEventListener("pointerleave", function () { cross.hidden = true; tip.hidden = true; });
    return track;
  }

  var STEPS = [5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880];

  // Ticks on round local times: every 30 minutes, every 3 hours, every day.
  function niceTicks(t0, t1) {
    var spanMin = (t1 - t0) / 60000, step = STEPS[STEPS.length - 1];
    for (var i = 0; i < STEPS.length; i++) { if (spanMin / STEPS[i] <= 7) { step = STEPS[i]; break; } }
    var off = new Date(t0).getTimezoneOffset(); // minutes behind UTC
    var ms = step * 60000, base = Math.ceil((t0 / 60000 - off) / step) * step + off; // local multiples of step
    var out = [];
    for (var m = base; m * 60000 <= t1; m += step) {
      var t = m * 60000, d = new Date(t);
      var label;
      if (step >= 1440) label = F.day.format(d);
      else if (d.getHours() === 0 && d.getMinutes() === 0) label = F.day.format(d);
      else label = lower(step >= 60 ? F.hour.format(d) : F.time.format(d));
      out.push({ pos: (t - t0) / (t1 - t0) * 100, label: label });
    }
    // drop labels that would run into the next one
    return out;
  }

  function tableView(tl, d) {
    var rows = [];
    function add(lane, segs) {
      segs.forEach(function (s) {
        rows.push(el("tr", {}, el("td", { text: lane }), el("td", { text: segLabel(s, lane) }),
          el("td", { text: fStamp(s.from) }), el("td", { text: fStamp(s.to) })));
      });
    }
    add("Mains", tl.mains);
    tl.nodes.forEach(function (n) { add(n.name, n.segments); });
    add("This Pi", tl.pi);
    return el("details", { "class": "table" },
      el("summary", { text: "Show this chart as a table" }),
      el("table", {},
        el("thead", {}, el("tr", {}, ["Lane", "State", "From", "To"].map(function (h) { return el("th", { text: h, scope: "col" }); }))),
        el("tbody", {}, rows)));
  }

  // ---- outages and log

  function renderOutages(d) {
    var node = $("outages");
    var table = el("div", { "class": "otable" },
      el("div", { "class": "orow head" }, ["When", "On battery", "Battery", "What happened"].map(function (h) { return el("div", { text: h }); })));
    if (!d.outages.length) {
      table.appendChild(el("div", { "class": "orow" }, el("div", { text: "No power cuts recorded yet." })));
    }
    d.outages.forEach(function (o, i) {
      var cur = i === 0 && (o.in_progress || (d.timeline.range === "outage"));
      var onbat = o.in_progress ? dur(o.duration_s) + " so far" : o.end ? (o.approximate ? "about " : "") + dur(o.duration_s) : "unknown";
      var batt = o.start_charge != null && o.min_charge != null ? Math.round(o.start_charge) + "% to " + Math.round(o.min_charge) + "%" : "";
      table.appendChild(el("div", { "class": "orow" + (cur ? " current" : "") },
        el("div", { "class": "when", text: fDate(o.start) + ", " + fTime(o.start) }),
        el("div", { text: onbat, "data-l": "On battery" }),
        el("div", { text: batt, "data-l": "Battery" }),
        el("div", { "class": "what" }, icon(o.tone), el("span", { text: words(o.result) }))));
    });
    put(node, el("h2", { id: "outages-title", text: "Outages" }), el("div", { "class": "card scroll" }, table));
  }

  function renderLog(d) {
    var node = $("log");
    var card = el("div", { "class": "card" });
    if (!d.events.length) card.appendChild(el("div", { "class": "lrow" }, el("span"), el("span"), el("p", { text: "Nothing has happened yet." })));
    d.events.forEach(function (e) {
      var tone = LEVEL_TONE[e.level] || "info";
      var unsynced = e.data && e.data.clock_unsynced;
      card.appendChild(el("div", { "class": "lrow" },
        el("time", { datetime: e.time, text: fStamp(e.time) }),
        icon(tone),
        el("p", {}, e.message, unsynced ? el("small", { text: " (the Pi's clock was not set yet, so this time may be off)" }) : null)));
    });
    put(node, el("h2", { id: "log-title", text: "Event log" }), card);
  }

  // ---- settings

  function renderSettings(d) {
    var s = d.settings;
    function group(title, items) {
      return el("div", {}, el("h3", { text: title }),
        el("dl", {}, items.map(function (i) {
          return [el("dt", {}, i.name, i.hint ? el("small", { text: i.hint }) : null), el("dd", { text: i.value })];
        }).reduce(function (a, b) { return a.concat(b); }, [])));
    }
    var nodes = s.nodes.map(function (n) { return { name: n.name, value: n.host + " (" + n.probe + ")" }; });
    var groups = el("div", { "class": "groups" },
      group("UPS and nodes", [{ name: "NUT server", value: s.nut }, { name: "UPS", value: s.ups }, { name: "Checked every", value: s.poll_interval }].concat(nodes)),
      group("Waking nodes", s.wake),
      group("Shutting down this Pi", s.shutdown),
      group("History", s.history));
    put($("settings"), el("details", {},
      el("summary", { text: "Settings (read only)" }),
      el("p", { "class": "lead" }, "These are the values nutwake is running with. To change one, edit ",
        el("code", { text: d.config_path || "the config file" }), " on the Pi, then run ", el("code", { text: "sudo systemctl reload nutwake" }),
        ". This page cannot change them: the shutdown command runs as root."),
      groups));
  }

  // ------------------------------------------------------------------- loop

  function banner(msg) {
    var b = $("banner");
    if (!msg) { b.hidden = true; return; }
    b.hidden = false;
    b.textContent = msg;
  }

  function load() {
    clearTimeout(timer);
    var wait = 5000;
    fetch("/api/status?range=" + encodeURIComponent(range), { cache: "no-store" })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
      .then(function (d) {
        data = d; lastGood = Date.now();
        banner("");
        render(d);
        wait = Math.max(3, d.refresh_s || 5) * 1000;
      })
      .catch(function (err) {
        if (window.console) console.error(err && err.stack ? err.stack : String(err));
        banner("Cannot reach nutwake right now" + (lastGood ? ". Showing what it said at " + fTimeSec(lastGood) : "") + ". Trying again.");
      })
      .then(function () { timer = setTimeout(load, wait); });
  }

  var resizeTimer = 0;
  window.addEventListener("resize", function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () { if (data) renderRecorder(data); }, 150);
  });
  window.addEventListener("hashchange", function () {
    var m = location.hash.match(/range=([\w]+)/);
    if (m && m[1] !== range) { range = m[1]; load(); }
  });

  load();
})();
