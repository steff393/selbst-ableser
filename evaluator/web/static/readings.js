// Zählerstände page: a meter list on the left, the selected meter's
// archived readings on the right — current value, value over time, the
// derived daily increase, RSSI over time, and the telegram list. Only
// Von/Bis reload the page (they decide what was fetched and decrypted
// server-side, see readings_handler.go); search, type filter and meter
// selection run against the already-loaded rows.
//
// Every number shown is either an archived entry's decoded value or RSSI
// as the server sent it, or — marked "abgeleitet" on the page — a plain
// difference between two such entries. Nothing is interpolated.
(function () {
  "use strict";

  var rows = window.SA_READINGS || [];
  var summaries = window.SA_METER_SUMMARY || [];
  var unitOrder = window.SA_UNIT_ORDER || [];
  var range = window.SA_READINGS_RANGE || { from: "", to: "" };

  // 127 is internal/correction.RSSI: a maximal positive dBm value that
  // never occurs from a real reception, marking a manually corrected entry.
  var CORRECTION_RSSI = 127;
  // Below this the reception is getting marginal; the same band the
  // RSSI chart shades.
  var WEAK_RSSI = -90;

  var TYPE_ORDER = { HKV: 0, WMZ: 1, WWZ: 2, KWZ: 3 };
  var MONTHS = ["Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"];

  // ---- model ----

  var meters = summaries.map(function (s) {
    return { s: s, rows: [], unit: "" };
  });
  var meterByID = {};
  meters.forEach(function (m) { meterByID[m.s.meter_id] = m; });
  rows.forEach(function (r) {
    var m = meterByID[r.meter_id];
    if (!m) return;
    m.rows.push(r);
    if (r.evaluable && !m.unit) m.unit = r.value_unit;
  });
  meters.forEach(function (m) {
    m.rows.sort(function (a, b) { return a.day < b.day ? -1 : a.day > b.day ? 1 : 0; });
    m.ok = m.rows.filter(function (r) { return r.evaluable; });
    m.breaks = findBreaks(m.ok);
  });

  // findBreaks decides, for each pair of consecutive entries with a value,
  // whether the series breaks between them: breaks[i] describes the gap
  // between ok[i-1] and ok[i] (null = continuous).
  //
  // A "reset" is the meter's Stichtag lying in that gap — the last day of
  // its master-data Stichtag month, the same rule internal/billing applies
  // (an entry on the Stichtag itself still belongs to the closing period).
  // It counts whether or not the value went down: when the meter has
  // already counted past its old level again by the next archived
  // telegram, the values alone don't show it, and their difference would
  // wrongly read as consumption.
  //
  // A "drop" is a lower value without a Stichtag in between — for a meter
  // that counts straight through, or one past its Stichtag, nothing the
  // device should do.
  //
  // A reset is "exact" when the earlier entry is the Stichtag itself: then
  // its value is the closing one and the meter started from zero right
  // after it, so the later value is exactly what was counted since — the
  // same reading internal/billing.CalculateMonth gives a reset month. When
  // the earlier entry is older, whatever was counted between it and the
  // Stichtag is unknown, and no difference can be given.
  function findBreaks(ok) {
    return ok.map(function (b, i) {
      if (i === 0) return null;
      var a = ok[i - 1], day = stichtagBetween(a.freeze_month, a.day, b.day);
      if (day) return { kind: "reset", day: day, exact: a.day === day };
      if (b.value < a.value) return { kind: "drop" };
      return null;
    });
  }
  // increment is what the meter counted between ok[i-1] and ok[i], or null
  // where the series breaks in a way no difference can bridge.
  function increment(m, i) {
    var brk = m.breaks[i];
    if (!brk) return m.ok[i].value - m.ok[i - 1].value;
    if (brk.kind === "reset" && brk.exact) return m.ok[i].value;
    return null;
  }
  function stichtagBetween(freezeMonth, from, to) {
    if (!freezeMonth) return null;
    for (var y = +from.slice(0, 4); y <= +to.slice(0, 4); y++) {
      var d = numDay(Date.UTC(y, freezeMonth, 0) / 864e5); // day 0 of the next month = last day of freezeMonth
      if (d >= from && d < to) return d;
    }
    return null;
  }
  function breakCounts(m) {
    var c = { reset: 0, drop: 0 };
    m.breaks.forEach(function (b) { if (b) c[b.kind]++; });
    return c;
  }

  var state = { q: "", types: null, sel: null };

  // ---- formatting ----

  function isWater(m) { return m.unit === "l"; }
  // A drop is implausible for a meter whose Stichtag the master data
  // knows; for one without master data it may just be an unknown reset.
  function dropColor(m) { return isAssigned(m.s) ? "var(--danger)" : "var(--chart-7)"; }

  // Same convention as internal/webapp/reading_value.go: water in m³ with a
  // decimal comma and three decimals, everything else as a plain integer.
  function fmtVal(m, v) {
    if (m.unit === "l") return (v / 1000).toFixed(3).replace(".", ",") + " m³";
    if (m.unit === "kWh") return v + " kWh";
    return String(v);
  }
  function fmtInt(n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, "."); }
  function fmtDelta(m, v) {
    var sign = v >= 0 ? "+" : "−";
    if (isWater(m)) return sign + fmtInt(Math.abs(v)) + " l";
    return sign + Math.abs(v) + (m.unit === "kWh" ? " kWh" : "");
  }
  function p2(n) { return (n < 10 ? "0" : "") + n; }
  function fmtDecimal(v) { return (Math.round(v * 10) / 10).toString().replace(".", ","); }
  function dayNum(d) { var p = d.split("-"); return Date.UTC(+p[0], +p[1] - 1, +p[2]) / 864e5; }
  function numDay(n) { return new Date(n * 864e5).toISOString().slice(0, 10); }
  function fmtDay(d) { var p = d.split("-"); return p[2] + "." + p[1] + "." + p[0]; }
  function fmtDayShort(d) { var p = d.split("-"); return p[2] + "." + p[1] + "."; }
  function timeOf(r) { return r.received_at.slice(11); }
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function colorOf(t) {
    return t === "HKV" ? "var(--chart-2)" : t === "WWZ" ? "var(--chart-8)" : t === "KWZ" ? "var(--chart-1)" : "var(--chart-4)";
  }
  function badge(s) {
    var cls = ["HKV", "WWZ", "KWZ"].indexOf(s.device_type) >= 0 ? "rd-b-" + s.device_type : "rd-b-x";
    return '<span class="rd-badge ' + cls + '" title="' + esc(s.device_type_name || "Gerätetyp nicht erkannt") + '">' + esc(s.device_type || "?") + "</span>";
  }
  function isAssigned(s) { return !!s.meter_point_id; }

  // ---- meter list ----

  function visibleMeters() {
    var q = state.q.trim().toLowerCase();
    return meters.filter(function (m) {
      if (state.types && !state.types[m.s.device_type || "?"]) return false;
      if (!q) return true;
      var s = m.s;
      return (s.unit_name + " " + s.room + " " + s.meter_point_id + " " + s.meter_id + " " + s.device_type).toLowerCase().indexOf(q) !== -1;
    });
  }

  function groupKey(s) { return isAssigned(s) ? "u:" + s.unit_id : "unassigned"; }
  function groupRank(s) {
    if (!isAssigned(s)) return unitOrder.length + 2;
    var i = unitOrder.indexOf(s.unit_id);
    return i === -1 ? unitOrder.length + 1 : i; // house meters (no unit) after every unit
  }
  function compareMeters(a, b) {
    var ga = groupRank(a.s), gb = groupRank(b.s);
    if (ga !== gb) return ga - gb;
    var ta = TYPE_ORDER[a.s.device_type], tb = TYPE_ORDER[b.s.device_type];
    ta = ta === undefined ? 9 : ta; tb = tb === undefined ? 9 : tb;
    if (ta !== tb) return ta - tb;
    var c = a.s.meter_point_id.localeCompare(b.s.meter_point_id, "de", { numeric: true });
    return c || a.s.meter_id.localeCompare(b.s.meter_id);
  }

  function sparkline(m) {
    var ok = m.ok, w = 70, h = 16;
    if (ok.length < 2) return "";
    var x0 = dayNum(range.from), x1 = Math.max(dayNum(range.to), x0 + 1);
    var lo = Infinity, hi = -Infinity;
    ok.forEach(function (r) { lo = Math.min(lo, r.value); hi = Math.max(hi, r.value); });
    if (lo === hi) { lo -= 1; hi += 1; }
    var d = ok.map(function (r, i) {
      return (i === 0 || m.breaks[i] ? "M" : "L") + ((dayNum(r.day) - x0) / (x1 - x0) * (w - 2) + 1).toFixed(1) + "," + (h - 1 - (r.value - lo) / (hi - lo) * (h - 2)).toFixed(1);
    }).join("");
    return '<svg width="' + w + '" height="' + h + '" aria-hidden="true"><path d="' + d + '" fill="none" stroke="' + colorOf(m.s.device_type) + '" stroke-width="1.4"/></svg>';
  }

  function renderList() {
    var list = visibleMeters().sort(compareMeters);
    var html = "", lastGroup = null;
    list.forEach(function (m) {
      var s = m.s, g = groupKey(s);
      if (g !== lastGroup) {
        html += g === "unassigned"
          ? '<div class="rd-group rd-group-unk">Nicht in den Stammdaten</div>'
          : '<div class="rd-group">' + esc(s.unit_name) + "</div>";
        lastGroup = g;
      }
      var last = m.ok[m.ok.length - 1];
      html += '<button type="button" class="rd-item' + (state.sel === s.meter_id ? " on" : "") + '" data-id="' + esc(s.meter_id) + '">' +
        badge(s) +
        '<span class="rd-room">' + (isAssigned(s) ? esc(s.room) + " <small>" + esc(s.meter_point_id) + "</small>" : "<em>unbekannt</em>") + "</span>" +
        '<span class="rd-val">' + (last ? esc(fmtVal(m, last.value)) : "–") + "</span>" +
        '<span class="rd-sub"><span class="rd-mono">' + esc(s.meter_id) + "</span>" + sparkline(m) + "</span></button>";
    });
    var el = document.getElementById("rd-list");
    el.innerHTML = html || '<div class="rd-group">' + (meters.length ? "Keine Treffer" : "Keine Telegramme im Zeitraum") + "</div>";
    el.querySelectorAll(".rd-item").forEach(function (b) {
      b.addEventListener("click", function () { select(b.getAttribute("data-id")); });
    });
    updateExportLink(list);
  }

  function renderTypeChips() {
    var counts = {};
    meters.forEach(function (m) { var t = m.s.device_type || "?"; counts[t] = (counts[t] || 0) + 1; });
    var types = Object.keys(counts).sort();
    var el = document.getElementById("rd-typechips");
    if (types.length < 2) { el.innerHTML = ""; return; }
    el.innerHTML = types.map(function (t) {
      var on = !state.types || state.types[t];
      return '<button type="button" class="rd-chip' + (on ? " on" : "") + '" data-t="' + esc(t) + '" aria-pressed="' + (on ? "true" : "false") + '">' + esc(t) + " " + counts[t] + "</button>";
    }).join("");
    el.querySelectorAll(".rd-chip").forEach(function (b) {
      b.addEventListener("click", function () {
        var cur = {};
        types.forEach(function (t) { if (!state.types || state.types[t]) cur[t] = true; });
        var t = b.getAttribute("data-t");
        if (cur[t]) delete cur[t]; else cur[t] = true;
        state.types = Object.keys(cur).length === types.length ? null : cur;
        renderTypeChips();
        renderList();
      });
    });
  }

  // The export follows the list: no meter parameter while nothing narrows
  // it, the exact visible meters otherwise.
  function updateExportLink(list) {
    var params = new URLSearchParams();
    params.set("from", range.from);
    params.set("to", range.to);
    if (list.length !== meters.length) {
      list.forEach(function (m) { params.append("meter", m.s.meter_id); });
    }
    document.getElementById("readings-export-link").href = "/operator/readings/export?" + params.toString();
  }

  function select(id) {
    state.sel = id;
    var url = new URL(window.location.href);
    url.searchParams.set("sel", id);
    window.history.replaceState(null, "", url.toString());
    // Keep the selected meter across a Von/Bis change or a quick-pick range.
    var hidden = document.getElementById("rd-meter-param");
    if (hidden) hidden.value = id;
    document.querySelectorAll(".rd-presets:not(.rd-csv) .rd-preset").forEach(function (a) {
      var u = new URL(a.href);
      u.searchParams.set("sel", id);
      a.href = u.toString();
    });
    renderList();
    renderDetail();
  }

  // ---- charts ----

  var W = 900, ML = 64, MR = 16;
  var PW = W - ML - MR;

  function niceTicks(lo, hi, n) {
    var step0 = (hi - lo) / n, mag = Math.pow(10, Math.floor(Math.log10(step0))), r = step0 / mag;
    var step = (r < 1.5 ? 1 : r < 3 ? 2 : r < 7 ? 5 : 10) * mag, out = [];
    for (var v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) out.push(v);
    return out;
  }
  function xScale() {
    var x0 = dayNum(range.from), x1 = Math.max(dayNum(range.to), x0 + 1);
    return { x0: x0, x1: x1, X: function (d) { return ML + (dayNum(d) - x0) / (x1 - x0) * PW; } };
  }
  function xAxis(o, sc, H) {
    var span = sc.x1 - sc.x0, step = span <= 31 ? 7 : span <= 120 ? 14 : 0, d, s;
    if (step) {
      for (d = sc.x0; d <= sc.x1; d += step) o.push(text(sc.X(numDay(d)), H - 6, fmtDayShort(numDay(d)), "middle"));
      return;
    }
    var everyMonths = span <= 400 ? 1 : span <= 1200 ? 3 : 6;
    for (d = sc.x0; d <= sc.x1; d++) {
      s = numDay(d);
      if (s.slice(8) === "01" && (+s.slice(5, 7) - 1) % everyMonths === 0) o.push(text(sc.X(s), H - 6, MONTHS[+s.slice(5, 7) - 1] + " " + s.slice(2, 4), "middle"));
    }
  }
  function text(x, y, s, anchor, extra) {
    return '<text x="' + x + '" y="' + y + '" font-size="11" fill="var(--chart-muted)" text-anchor="' + (anchor || "end") + '"' + (extra || "") + ">" + esc(s) + "</text>";
  }
  function gridLine(y) { return '<line x1="' + ML + '" x2="' + (W - MR) + '" y1="' + y + '" y2="' + y + '" stroke="var(--chart-grid)"/>'; }
  function yTitle(y, s) { return '<text x="10" y="' + y + '" font-size="11" fill="var(--chart-muted)" transform="rotate(-90 10 ' + y + ')" text-anchor="middle">' + esc(s) + "</text>"; }

  function valueChart(m, sc) {
    var ok = m.ok, H = 260, MT = 14, ph = H - MT - 30, col = colorOf(m.s.device_type);
    var lo = Infinity, hi = -Infinity;
    ok.forEach(function (r) { lo = Math.min(lo, r.value); hi = Math.max(hi, r.value); });
    if (lo === hi) { lo -= 1; hi += 1; }
    var pad = (hi - lo) * 0.06; lo = Math.max(0, lo - pad); hi += pad;
    function Y(v) { return MT + ph - (v - lo) / (hi - lo) * ph; }
    var o = ['<svg class="rd-chart" id="rd-ch-val" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="Zählerstand über Zeit">'];
    niceTicks(lo, hi, 5).forEach(function (v) {
      o.push(gridLine(Y(v)) + text(ML - 8, Y(v), isWater(m) ? (v / 1000).toLocaleString("de-DE", { maximumFractionDigits: 3 }) : fmtInt(Math.round(v)), "end", ' dominant-baseline="middle"'));
    });
    o.push(yTitle(MT + ph / 2, isWater(m) ? "m³" : m.unit === "kWh" ? "kWh" : "Einheiten"));
    xAxis(o, sc, H);
    o.push('<line x1="' + ML + '" x2="' + (W - MR) + '" y1="' + (MT + ph) + '" y2="' + (MT + ph) + '" stroke="var(--chart-axis)"/>');
    var path = "", anyBreak = false;
    ok.forEach(function (r, i) {
      var brk = m.breaks[i], x = sc.X(r.day);
      path += (i === 0 || brk ? "M" : "L") + x.toFixed(1) + "," + Y(r.value).toFixed(1);
      if (!brk) return;
      anyBreak = true;
      // A reset is drawn at the Stichtag itself, between the two entries;
      // a drop at the entry that came in lower.
      var reset = brk.kind === "reset", c = reset ? "var(--chart-7)" : dropColor(m), bx = reset ? sc.X(brk.day) : x;
      o.push('<line x1="' + bx + '" x2="' + bx + '" y1="' + MT + '" y2="' + (MT + ph) + '" stroke="' + c + '" stroke-dasharray="3 3"><title>' +
        esc(reset ? "Stichtag " + fmtDay(brk.day) + " (Stammdaten): Zähler beginnt neu" : "Rückgang am " + fmtDay(r.day) + " ohne Stichtag dazwischen") + "</title></line>" +
        '<text x="' + (bx + 4) + '" y="' + (MT + 10) + '" font-size="10.5" fill="' + c + '">' + (reset ? "Stichtag " + fmtDayShort(brk.day) : "Rückgang") + "</text>");
    });
    if (!anyBreak) {
      o.push('<defs><linearGradient id="rd-area" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="' + col + '" stop-opacity=".22"/><stop offset="1" stop-color="' + col + '" stop-opacity="0"/></linearGradient></defs>');
      o.push('<path class="rd-fade" fill="url(#rd-area)" d="M' + sc.X(ok[0].day) + "," + (MT + ph) + ok.map(function (r) { return "L" + sc.X(r.day).toFixed(1) + "," + Y(r.value).toFixed(1); }).join("") + "L" + sc.X(ok[ok.length - 1].day) + "," + (MT + ph) + 'Z"/>');
    }
    o.push('<path class="rd-draw" d="' + path + '" fill="none" stroke="' + col + '" stroke-width="2.2" stroke-linejoin="round" pathLength="1000"/>');
    var rad = ok.length > 60 ? 2 : 3.2;
    ok.forEach(function (r) {
      var corr = r.rssi === CORRECTION_RSSI;
      o.push('<circle class="rd-fade" cx="' + sc.X(r.day).toFixed(1) + '" cy="' + Y(r.value).toFixed(1) + '" r="' + (corr ? 5 : rad) + '" fill="' + (corr ? "var(--surface)" : col) + '"' + (corr ? ' stroke="var(--chart-7)" stroke-width="2.5"' : "") + "/>");
    });
    m.rows.forEach(function (r) {
      if (!r.evaluable) o.push('<rect x="' + (sc.X(r.day) - 2.5) + '" y="' + (MT + ph - 8) + '" width="5" height="5" fill="none" stroke="var(--warning)" stroke-width="1.5"/>');
    });
    o.push('<line class="rd-xh" x1="0" x2="0" y1="' + MT + '" y2="' + (MT + ph) + '" stroke="var(--fg-faint)" stroke-dasharray="2 3" opacity="0"/><circle class="rd-xd" r="5" fill="none" stroke="var(--fg)" stroke-width="2" opacity="0"/>');
    o.push('<rect class="rd-hit" x="' + ML + '" y="' + MT + '" width="' + PW + '" height="' + ph + '" fill="transparent"/></svg>');
    return { svg: o.join(""), Y: Y };
  }

  // Derived: what was counted between consecutive entries with a value, per
  // day of the gap between them (see increment). Right after an exact
  // Stichtag that is the new value counted from zero, marked ↺ above its
  // bar; where no difference exists (a Stichtag between older entries, a
  // drop) the gap gets only its marker, never a bar.
  function intervals(m) {
    var iv = [];
    for (var i = 1; i < m.ok.length; i++) {
      var a = m.ok[i - 1], b = m.ok[i];
      iv.push({ a: a, b: b, d: increment(m, i), days: dayNum(b.day) - dayNum(a.day), brk: m.breaks[i] });
    }
    return iv;
  }
  function incrementChart(m, sc, iv) {
    var H = 120, top = 14, ph = H - top - 20, mx = 1e-9;
    iv.forEach(function (v) { if (v.d !== null && v.days > 0) mx = Math.max(mx, v.d / v.days); });
    var o = ['<svg class="rd-chart" id="rd-ch-inc" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="Zuwachs je Tag">'];
    niceTicks(0, mx, 2).forEach(function (v) {
      var y = top + ph - v / mx * ph;
      o.push(gridLine(y) + text(ML - 8, y, isWater(m) ? fmtInt(Math.round(v)) : fmtDecimal(v), "end", ' dominant-baseline="middle"'));
    });
    o.push(yTitle(top + ph / 2, isWater(m) ? "l / Tag" : "je Tag"));
    iv.forEach(function (v, i) {
      var xa = sc.X(v.a.day), xb = sc.X(v.b.day), w = Math.max(1, xb - xa - (xb - xa > 4 ? 2 : 0));
      var h = v.d === null ? 0 : v.d / v.days / mx * ph;
      if (v.brk) {
        var reset = v.brk.kind === "reset";
        o.push('<text x="' + (xa + xb) / 2 + '" y="' + (v.d === null ? top + ph - 3 : top + ph - h - 3) + '" font-size="12" text-anchor="middle" fill="' + (reset ? "var(--chart-7)" : dropColor(m)) + '">' + (reset ? "↺" : "↓") + "</text>");
        if (v.d === null) return;
      }
      o.push('<rect class="rd-grow" x="' + (xa + 1) + '" y="' + (top + ph - h) + '" width="' + w + '" height="' + Math.max(0.5, h) + '" rx="1.5" fill="var(--chart-7)" opacity=".75" data-iv="' + i + '" style="transform-origin:0 ' + (top + ph) + "px;animation-delay:" + (i / iv.length * 0.5).toFixed(2) + 's"/>');
    });
    o.push('<line x1="' + ML + '" x2="' + (W - MR) + '" y1="' + (top + ph) + '" y2="' + (top + ph) + '" stroke="var(--chart-axis)"/></svg>');
    return o.join("");
  }

  function rssiChart(m, sc) {
    var real = m.rows.filter(function (r) { return r.rssi !== CORRECTION_RSSI; });
    var H = 130, top = 10, ph = H - top - 30, lo = -105, hi = -50;
    function Y(v) { return top + ph - (Math.max(lo, Math.min(hi, v)) - lo) / (hi - lo) * ph; }
    var o = ['<svg class="rd-chart" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="Empfangsstärke über Zeit">'];
    o.push('<rect x="' + ML + '" y="' + Y(WEAK_RSSI) + '" width="' + PW + '" height="' + (Y(lo) - Y(WEAK_RSSI)) + '" fill="var(--danger)" opacity=".07"/>');
    [-100, -90, -80, -70, -60].forEach(function (v) { o.push(gridLine(Y(v)) + text(ML - 8, Y(v), String(v), "end", ' dominant-baseline="middle"')); });
    o.push(yTitle(top + ph / 2, "dBm"));
    xAxis(o, sc, H);
    if (real.length > 1) {
      o.push('<path d="' + real.map(function (r, i) { return (i ? "L" : "M") + sc.X(r.day).toFixed(1) + "," + Y(r.rssi).toFixed(1); }).join("") + '" fill="none" stroke="var(--chart-1)" stroke-width="1.4" opacity=".5"/>');
    }
    real.forEach(function (r) {
      o.push('<circle cx="' + sc.X(r.day).toFixed(1) + '" cy="' + Y(r.rssi).toFixed(1) + '" r="2.6" fill="' + (r.rssi < WEAK_RSSI ? "var(--danger)" : "var(--chart-1)") + '"><title>' + esc(fmtDay(r.day) + ": " + r.rssi + " dBm") + "</title></circle>");
    });
    o.push("</svg>");
    return o.join("");
  }

  // ---- detail ----

  function renderDetail() {
    var el = document.getElementById("rd-detail");
    var m = meterByID[state.sel];
    if (!m) {
      el.innerHTML = '<div class="card"><p class="hint">' + (meters.length ? "Links einen Zähler auswählen." : "Im gewählten Zeitraum ist kein Telegramm archiviert.") + "</p></div>";
      return;
    }
    var s = m.s, es = m.rows, ok = m.ok, sc = xScale();
    var last = es[es.length - 1], lastOk = ok[ok.length - 1], firstOk = ok[0];
    var bc = breakCounts(m);
    var spanDays = firstOk && lastOk ? dayNum(lastOk.day) - dayNum(firstOk.day) : 0;
    // The period's total is the sum of its increments — and only exists if
    // every one of them does.
    var gain = !firstOk || spanDays === 0 ? null : 0;
    for (var gi = 1; gain !== null && gi < ok.length; gi++) {
      var inc = increment(m, gi);
      gain = inc === null ? null : gain + inc;
    }
    var okIndex = {};
    ok.forEach(function (r, i) { okIndex[r.day] = i; });
    var rs = es.filter(function (r) { return r.rssi !== CORRECTION_RSSI; }).map(function (r) { return r.rssi; });
    var rsMin = rs.length ? Math.min.apply(null, rs) : null, rsMax = rs.length ? Math.max.apply(null, rs) : null;
    var rsAvg = rs.length ? Math.round(rs.reduce(function (a, b) { return a + b; }, 0) / rs.length) : null;
    var na = es.length - ok.length;
    var iv = intervals(m);
    var vc = ok.length ? valueChart(m, sc) : null;

    var pills = ['<span class="rd-pill">' + es.length + " Telegramme · " + fmtDay(es[0].day) + " – " + fmtDay(last.day) + "</span>"];
    if (na) pills.push('<span class="rd-pill warn">' + na + " ohne Wert" + (isAssigned(s) ? "" : " — kein Schlüssel") + "</span>");
    if (bc.reset) pills.push('<span class="rd-pill">' + bc.reset + "× Stichtag " + p2(s.freeze_month) + " (Rücksetzung laut Stammdaten)</span>");
    if (bc.drop) pills.push('<span class="rd-pill' + (isAssigned(s) ? " bad" : "") + '">' + bc.drop + "× Rückgang" + (isAssigned(s) ? (s.freeze_month ? " außerhalb des Stichtags" : " — Zähler ohne Stichtag") + ", unplausibel" : "") + "</span>");
    if (!isAssigned(s)) pills.push('<span class="rd-pill bad">nicht in den Stammdaten</span>');

    var derived = '<span class="rd-derived" title="Aus zwei Archiveinträgen berechnet, kein Wert aus einem Telegramm">abgeleitet</span>';
    var hero =
      '<div class="rd-hero">' +
      '<div class="main"><div class="l">Zählerstand</div><div class="v">' + (lastOk ? esc(fmtVal(m, lastOk.value)) : "–") + '</div><div class="s">' + (lastOk ? "Telegramm vom " + fmtDay(lastOk.day) + ", " + esc(timeOf(lastOk)) + " Uhr" : "kein auswertbares Telegramm") + "</div></div>" +
      '<div><div class="l">Im Zeitraum</div><div class="v">' + (gain === null ? "–" : "+ " + esc(fmtVal(m, gain))) + '</div><div class="s">' + (gain === null ? (bc.drop ? "Rückgang dazwischen" : bc.reset ? "Stichtag ohne Wert am Stichtag" : "zu wenige Werte") : "seit " + fmtDay(firstOk.day) + (bc.reset ? ", über den Stichtag" : "")) + " " + derived + "</div></div>" +
      '<div><div class="l">Ø je Tag</div><div class="v">' + (gain === null ? "–" : isWater(m) ? fmtInt(Math.round(gain / spanDays)) + " l" : fmtDecimal(gain / spanDays)) + '</div><div class="s">' + (gain === null ? "" : "über " + spanDays + " Tage ") + derived + "</div></div>" +
      '<div><div class="l">Empfang</div><div class="v' + (rsMin !== null && rsMin < WEAK_RSSI ? " weak" : "") + '">' + (rsAvg === null ? "–" : rsAvg + " dBm") + '</div><div class="s">' + (rsAvg === null ? "" : "Ø · von " + rsMin + " bis " + rsMax) + "</div></div>" +
      "</div>";

    var tableRows = es.map(function (r) {
      var k = r.evaluable ? okIndex[r.day] : undefined;
      var brk = k ? m.breaks[k] : null;
      var d = k ? increment(m, k) : null;
      var deltaCell = "";
      if (brk && brk.kind === "reset") {
        deltaCell = d === null
          ? '<span class="rd-st corr" title="Stichtag ' + fmtDay(brk.day) + ' dazwischen, aber kein Wert vom Stichtag selbst: keine Differenz">↺ Stichtag</span>'
          : '<span class="rd-st corr" title="Nach dem Stichtag ' + fmtDay(brk.day) + ' ab 0 gezählt">↺ ' + esc(fmtDelta(m, d)) + "</span>";
      } else if (k) {
        deltaCell = esc(fmtDelta(m, r.value - ok[k - 1].value));
      }
      var corr = r.rssi === CORRECTION_RSSI;
      return '<tr data-day="' + esc(r.day) + '"><td>' + fmtDay(r.day) + "</td><td>" + (corr ? "—" : esc(timeOf(r))) + '</td><td class="num">' +
        (corr ? '<span class="rd-st corr" title="Über „Einzelnen Zählerstand korrigieren“ nachträglich geändert">Korrektur</span>' : r.rssi) + '</td><td class="num">' +
        (r.evaluable ? esc(fmtVal(m, r.value)) : '<span class="rd-st na">' + (isAssigned(s) ? "nicht dekodierbar" : "kein Schlüssel") + "</span>") + '</td><td class="num' + (brk && brk.kind === "drop" && isAssigned(s) ? " neg" : "") + '">' +
        deltaCell + '</td><td><a href="' + esc(r.decode_url) + '" target="_blank" rel="noopener" class="analyze-link" title="Zur Prüfung bei wmbusmeters.org öffnen" aria-label="Zur Prüfung bei wmbusmeters.org öffnen">↗</a></td></tr>';
    }).reverse().join("");

    el.innerHTML =
      '<div class="card rd-panel"><div class="rd-dhead">' + badge(s) + "<h2>" +
      (isAssigned(s) ? esc(s.unit_name) + " · " + esc(s.room) + " · Platz " + esc(s.meter_point_id) : "Unbekannter Zähler") +
      '</h2><span class="rd-mono rd-muted">' + esc(s.meter_id) + '</span><span class="rd-faint" title="' + esc(s.manufacturer_name) + '">' + esc(s.manufacturer) + (s.manufacturer_name ? " " + esc(s.manufacturer_name) : "") + (s.device_type_name ? " · " + esc(s.device_type_name) : "") + "</span></div>" +
      '<div class="rd-pills">' + pills.join("") + "</div>" + hero + "</div>" +
      '<div class="card rd-panel"><h2>Zählerstand im Zeitverlauf</h2><p class="hint">Jeder Punkt ist ein Archiveintrag mit dem Wert, den das Programm dekodiert hat' + (na ? "; Kästchen auf der Grundlinie: Eintrag ohne Wert" : "") + ".</p>" +
      (vc ? vc.svg : '<p class="hint">Kein Eintrag mit Wert im Zeitraum.</p>') +
      (iv.length ? '<h3 class="rd-h3">Zuwachs je Tag ' + derived + '</h3><p class="hint">Differenz zweier aufeinanderfolgender Einträge durch die Tage dazwischen — keine Verbrauchsberechnung (die macht die UVI). ↺ Stichtag laut Stammdaten: danach ab 0 gezählt; ohne Wert vom Stichtag selbst bleibt die Lücke leer. ↓ Rückgang: keine Differenz.</p>' + incrementChart(m, sc, iv) : "") +
      "</div>" +
      '<div class="card rd-panel"><h2>Empfangsstärke</h2><p class="hint">RSSI je Telegramm; rot hinterlegt: unter ' + WEAK_RSSI + " dBm, der Empfang wird knapp.</p>" + rssiChart(m, sc) + "</div>" +
      '<div class="card rd-panel"><h2>Telegramme <span class="rd-faint">' + es.length + '</span></h2><p class="hint">Neueste zuerst. ↗ öffnet das Telegramm bei wmbusmeters.org, um seinen vollständigen Inhalt anzusehen.</p>' +
      '<div class="rd-tg-wrap"><table class="list rd-tg"><thead><tr><th>Tag</th><th>Empfangen</th><th class="num">RSSI</th><th class="num">Zählerstand</th><th class="num">Δ zum Vorgänger</th><th></th></tr></thead><tbody>' +
      tableRows + "</tbody></table></div></div>";

    if (vc) attachValueHover(m, sc, vc.Y);
    if (iv.length) attachIncrementHover(m, iv);
  }

  function attachValueHover(m, sc, Y) {
    var svg = document.getElementById("rd-ch-val");
    var xh = svg.querySelector(".rd-xh"), xd = svg.querySelector(".rd-xd"), hit = svg.querySelector(".rd-hit");
    var detail = document.getElementById("rd-detail");
    function clearRow() { detail.querySelectorAll("tr.hl").forEach(function (r) { r.classList.remove("hl"); }); }
    function nearest(ev) {
      var pt = svg.createSVGPoint(); pt.x = ev.clientX; pt.y = ev.clientY;
      var p = pt.matrixTransform(svg.getScreenCTM().inverse()), best = null, bd = Infinity;
      m.ok.forEach(function (r) { var d = Math.abs(sc.X(r.day) - p.x); if (d < bd) { bd = d; best = r; } });
      return best;
    }
    hit.addEventListener("mousemove", function (ev) {
      var r = nearest(ev);
      if (!r) return;
      var x = sc.X(r.day);
      xh.setAttribute("x1", x); xh.setAttribute("x2", x); xh.setAttribute("opacity", 1);
      xd.setAttribute("cx", x); xd.setAttribute("cy", Y(r.value)); xd.setAttribute("opacity", 1);
      clearRow();
      var row = detail.querySelector('tr[data-day="' + r.day + '"]');
      if (row) row.classList.add("hl");
      var corr = r.rssi === CORRECTION_RSSI;
      var k = m.ok.indexOf(r), note = "";
      var after = m.breaks[k + 1], before = m.breaks[k];
      if (after && after.kind === "reset" && after.day === r.day) note = "Stichtag: letzter Wert vor der Rücksetzung";
      else if (before && before.kind === "reset") note = "nach dem Stichtag " + fmtDay(before.day) + " ab 0 gezählt";
      showTip('<div class="h">' + fmtDay(r.day) + (corr ? "" : " · " + esc(timeOf(r))) + "</div><table><tr><td class=\"m\">Zählerstand</td><td><b>" + esc(fmtVal(m, r.value)) + "</b></td></tr>" +
        (isWater(m) ? '<tr><td class="m">roh</td><td>' + fmtInt(r.value) + " l</td></tr>" : "") +
        '<tr><td class="m">RSSI</td><td>' + (corr ? "Korrektur" : r.rssi + " dBm") + "</td></tr></table>" +
        (note ? '<div class="m" style="margin-top:.2rem">' + esc(note) + "</div>" : ""), ev);
    });
    hit.addEventListener("mouseleave", function () {
      hideTip(); clearRow();
      xh.setAttribute("opacity", 0); xd.setAttribute("opacity", 0);
    });
  }

  function attachIncrementHover(m, iv) {
    var svg = document.getElementById("rd-ch-inc");
    svg.addEventListener("mousemove", function (ev) {
      var rect = ev.target.closest("rect[data-iv]");
      if (!rect) { hideTip(); return; }
      var v = iv[+rect.getAttribute("data-iv")];
      var fromZero = v.brk && v.brk.kind === "reset";
      showTip('<div class="h">' + fmtDay(v.a.day) + " → " + fmtDay(v.b.day) + '</div><table><tr><td class="m">Zuwachs</td><td><b>' + esc(fmtDelta(m, v.d)) + '</b></td></tr><tr><td class="m">Abstand</td><td>' + v.days + " Tag" + (v.days > 1 ? "e" : "") + '</td></tr></table><div class="m">' +
        (fromZero ? "Stichtag " + fmtDay(v.brk.day) + ": von 0 auf " + esc(fmtVal(m, v.b.value)) : "aus " + esc(fmtVal(m, v.a.value)) + " und " + esc(fmtVal(m, v.b.value))) + "</div>", ev);
    });
    svg.addEventListener("mouseleave", hideTip);
  }

  // ---- tooltip ----

  var tip = document.getElementById("rd-tip");
  function showTip(html, ev) {
    tip.innerHTML = html;
    tip.classList.add("on");
    var w = tip.offsetWidth, h = tip.offsetHeight, x = ev.clientX + 14, y = ev.clientY + 14;
    if (x + w > window.innerWidth - 8) x = ev.clientX - w - 14;
    if (y + h > window.innerHeight - 8) y = ev.clientY - h - 14;
    tip.style.left = x + "px";
    tip.style.top = y + "px";
  }
  function hideTip() { tip.classList.remove("on"); }

  // ---- init ----

  function init() {
    // Keep the selected meter across a Von/Bis reload.
    var form = document.querySelector("form.rd-range");
    var hidden = document.createElement("input");
    hidden.type = "hidden"; hidden.name = "sel"; hidden.id = "rd-meter-param";
    form.appendChild(hidden);

    document.getElementById("rd-q").addEventListener("input", function (e) { state.q = e.target.value; renderList(); });
    renderTypeChips();

    var wanted = new URLSearchParams(window.location.search).get("sel");
    var first = meters.slice().sort(compareMeters)[0];
    var start = wanted && meterByID[wanted] ? wanted : first ? first.s.meter_id : null;
    if (start) { select(start); } else { renderList(); renderDetail(); }
  }

  document.addEventListener("DOMContentLoaded", init);
})();
