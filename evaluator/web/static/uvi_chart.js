// Year-over-year heating chart for the UVI page (UI-01's "Verlauf ... im
// Vergleich zum Vorjahr"). Static SVG, hand-built, no charting library —
// this system is meant to run unattended for decades (QUAL-02), and a
// dependency-free renderer has no CDN, no framework version, and no build
// step to rot out from under it. Redrawn synchronously from
// window.SA_UVI_CHART; there is nothing to animate between states since
// each month/Wohnung navigation is a full page load (see tenant_handlers.go
// — the calendar-year window is chosen specifically so that stays stable
// across ordinary month-to-month paging, only the active-month marker
// moves).
window.SAUviChart = (function () {
  "use strict";

  var MONTH_ABBR = ["Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"];
  var MONTH_FULL = ["Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"];

  var W = 960, H = 380, ML = 54, MR = 28, MT = 28, MB = 40;
  var PLOT_W = W - ML - MR, PLOT_H = H - MT - MB;

  // Matches internal/webapp/templates.go's "consumption" func: heating
  // shows one decimal, water three (m3, down to the liter). The server
  // sends the decimal count with the data so the chart and the table
  // beneath it can never disagree about precision.
  function fmt(v, decimals) {
    return v.toFixed(typeof decimals === "number" ? decimals : 1);
  }

  // axisDecimals picks the precision for gridline labels from the axis
  // range rather than from the series' own precision: heating runs in the
  // hundreds (where decimals are noise) and water in single m3 (where
  // dropping them would print five identical zeroes).
  function axisDecimals(yMax) {
    if (yMax >= 20) return 0;
    if (yMax >= 2) return 1;
    return 2;
  }

  // niceMax rounds up to a "clean" axis maximum (1/2/5 x 10^n) so gridline
  // labels are round numbers, never e.g. "137.4".
  function niceMax(v) {
    if (v <= 0) return 10;
    var exp = Math.floor(Math.log(v) / Math.LN10);
    var base = Math.pow(10, exp);
    var steps = [1, 2, 2.5, 5, 10];
    for (var i = 0; i < steps.length; i++) {
      var candidate = steps[i] * base;
      if (candidate >= v) return candidate;
    }
    return 10 * base;
  }

  function svgEl(tag, attrs) {
    var el = document.createElementNS("http://www.w3.org/2000/svg", tag);
    for (var k in attrs) el.setAttribute(k, attrs[k]);
    return el;
  }

  function textEl(tag, attrs, text) {
    var el = svgEl(tag, attrs);
    el.textContent = text;
    return el;
  }

  // x position of month index i (0=January), center of its slot.
  function xOf(i) { return ML + (i + 0.5) * (PLOT_W / 12); }

  // Splits 12 points into runs of consecutive Found points — a gap
  // (FACH-08, or a month that hasn't happened yet) breaks the line rather
  // than being interpolated across, which would misrepresent a value that
  // was never measured.
  function foundRuns(points) {
    var runs = [], current = null;
    points.forEach(function (p, i) {
      if (!p.found) { current = null; return; }
      if (!current) { current = []; runs.push(current); }
      current.push(i);
    });
    return runs;
  }

  function buildChart(container, data) {
    var yMax = niceMax(Math.max(1, Math.max.apply(null, data.current.concat(data.prior).map(function (p) { return p.found ? p.value : 0; }))));
    function y(v) { return MT + PLOT_H - (v / yMax) * PLOT_H; }

    var hasAnyData = data.current.concat(data.prior).some(function (p) { return p.found; });

    var svg = svgEl("svg", {
      class: "uvi-chart-svg", viewBox: "0 0 " + W + " " + H,
      role: "img",
      "aria-label": "Jahresverlauf Heizung " + data.year + " im Vergleich zu " + data.prior_year
    });

    if (!hasAnyData) {
      svg.appendChild(textEl("text", {
        x: W / 2, y: H / 2, "text-anchor": "middle", fill: "var(--chart-muted)", "font-size": 14
      }, "Noch keine Verbrauchsdaten für " + data.year + " oder " + data.prior_year + "."));
      container.appendChild(svg);
      return;
    }

    // --- y gridlines + labels ---
    var steps = 4;
    for (var s = 0; s <= steps; s++) {
      var v = (yMax / steps) * s;
      var yy = y(v);
      svg.appendChild(svgEl("line", { x1: ML, y1: yy, x2: W - MR, y2: yy, stroke: "var(--chart-grid)", "stroke-width": 1 }));
      svg.appendChild(textEl("text", {
        x: ML - 10, y: yy, fill: "var(--chart-muted)", "font-size": 11, "text-anchor": "end", "dominant-baseline": "middle"
      }, fmt(v, axisDecimals(yMax))));
    }

    // --- x axis month labels ---
    MONTH_ABBR.forEach(function (label, i) {
      svg.appendChild(textEl("text", {
        x: xOf(i), y: H - MB + 20, fill: "var(--chart-muted)", "font-size": 11, "text-anchor": "middle"
      }, label));
    });
    svg.appendChild(svgEl("line", { x1: ML, y1: MT + PLOT_H, x2: W - MR, y2: MT + PLOT_H, stroke: "var(--chart-axis)", "stroke-width": 1 }));

    // --- active-month guide (persistent "you are here", independent of hover) ---
    var activeX = xOf(data.active_index);
    var guide = svgEl("line", {
      class: "uvi-chart-active-guide", x1: activeX, y1: MT, x2: activeX, y2: MT + PLOT_H,
      stroke: "var(--fg-faint)", "stroke-width": 1, "stroke-dasharray": "3,3"
    });
    svg.appendChild(guide);

    // --- prior year: dashed reference line, no fill ---
    drawSeries(svg, data.prior, y, {
      stroke: "var(--chart-muted)", dash: "6,4", markerR: 3, fillOpacity: 0
    });
    // --- current year: solid line, filled wash, on top ---
    drawSeries(svg, data.current, y, {
      stroke: "var(--accent)", dash: null, markerR: 4, fillOpacity: 0.1
    });

    // --- active-month highlight marker + callout ---
    var activePoint = data.current[data.active_index];
    if (activePoint && activePoint.found) {
      var ax = activeX, ay = y(activePoint.value);
      svg.appendChild(svgEl("circle", { cx: ax, cy: ay, r: 11, fill: "var(--accent)", opacity: 0.18 }));
      svg.appendChild(svgEl("circle", { cx: ax, cy: ay, r: 5.5, fill: "var(--accent)", stroke: "var(--surface)", "stroke-width": 2.5 }));

      var labelAbove = ay - 16 > MT + 10;
      var calloutY = labelAbove ? ay - 16 : ay + 24;
      var callout = svgEl("g", {});
      var valueText = fmt(activePoint.value, data.decimals) + " " + data.value_unit;
      callout.appendChild(textEl("text", {
        x: ax, y: calloutY, "text-anchor": "middle", "font-size": 12, "font-weight": 650, fill: "var(--fg)"
      }, valueText));
      svg.appendChild(callout);
    } else {
      svg.appendChild(textEl("text", {
        x: activeX, y: MT - 10, "text-anchor": "middle", "font-size": 10, fill: "var(--fg-faint)"
      }, "kein Wert"));
    }

    // --- hover crosshair (separate from the persistent active-month guide) ---
    var hoverLine = svgEl("line", {
      class: "uvi-chart-hover-guide", x1: 0, y1: MT, x2: 0, y2: MT + PLOT_H,
      stroke: "var(--fg-muted)", "stroke-width": 1, visibility: "hidden"
    });
    svg.appendChild(hoverLine);

    container.appendChild(svg);

    // --- tooltip (HTML, not SVG — positioned over the chart) ---
    var tooltip = document.createElement("div");
    tooltip.className = "uvi-chart-tooltip";
    tooltip.hidden = true;
    container.appendChild(tooltip);

    function showAt(i) {
      var rect = svg.getBoundingClientRect();
      var scale = rect.width / W;
      hoverLine.setAttribute("x1", xOf(i));
      hoverLine.setAttribute("x2", xOf(i));
      hoverLine.setAttribute("visibility", "visible");

      tooltip.innerHTML = "";
      var title = document.createElement("div");
      title.className = "uvi-chart-tooltip-title";
      title.textContent = MONTH_FULL[i] + (i === data.active_index ? " (aktueller Monat)" : "");
      tooltip.appendChild(title);

      [{ label: String(data.year), point: data.current[i], color: "var(--accent)" },
       { label: String(data.prior_year), point: data.prior[i], color: "var(--chart-muted)" }].forEach(function (s) {
        var row = document.createElement("div");
        row.className = "uvi-chart-tooltip-row";
        var key = document.createElement("span");
        key.className = "uvi-chart-tooltip-key";
        key.style.background = s.color;
        row.appendChild(key);
        var value = document.createElement("span");
        value.className = "uvi-chart-tooltip-value";
        value.textContent = s.point.found ? fmt(s.point.value, data.decimals) + " " + data.value_unit : "keine Daten";
        row.appendChild(value);
        var label = document.createElement("span");
        label.className = "uvi-chart-tooltip-label";
        label.textContent = s.label;
        row.appendChild(label);
        tooltip.appendChild(row);
      });

      if (data.current[i].found && data.prior[i].found && data.prior[i].value !== 0) {
        var delta = (data.current[i].value - data.prior[i].value) / data.prior[i].value * 100;
        var deltaRow = document.createElement("div");
        // Reuses the page's own delta/delta-up/delta-down classes (see
        // uvi.html's Vormonat/Vorjahresmonat stats): more consumption
        // than last year reads as the same "up = red" the rest of the
        // page already uses, not a chart-local color choice.
        deltaRow.className = "uvi-chart-tooltip-delta delta " + (delta >= 0 ? "delta-up" : "delta-down");
        deltaRow.textContent = (delta >= 0 ? "▲ " : "▼ ") + fmt(Math.abs(delta)) + " % ggü. " + data.prior_year;
        tooltip.appendChild(deltaRow);
      }

      tooltip.hidden = false;
      var left = xOf(i) * scale;
      var containerW = container.clientWidth;
      tooltip.style.left = Math.min(Math.max(left, 90), containerW - 90) + "px";
      tooltip.style.top = (MT * scale) + "px";
    }

    function hide() {
      hoverLine.setAttribute("visibility", "hidden");
      tooltip.hidden = true;
    }

    // 12 invisible full-height hit columns — the whole month slot is the
    // target, not the thin line/marker (see interaction.md), and each is
    // independently focusable so keyboard users get the same detail
    // hover gives, via aria-label alone even before any tooltip shows.
    for (var i = 0; i < 12; i++) {
      var slotX = ML + i * (PLOT_W / 12);
      var slotW = PLOT_W / 12;
      var cur = data.current[i], pri = data.prior[i];
      var label = MONTH_FULL[i] + " " + data.year + ": " +
        (cur.found ? fmt(cur.value, data.decimals) + " " + data.value_unit : "keine Daten") +
        ". " + MONTH_FULL[i] + " " + data.prior_year + ": " +
        (pri.found ? fmt(pri.value, data.decimals) + " " + data.value_unit : "keine Daten") + ".";
      var hit = svgEl("rect", {
        x: slotX, y: MT, width: slotW, height: PLOT_H, fill: "transparent",
        tabindex: "0", "aria-label": label
      });
      hit.style.cursor = "pointer";
      (function (idx) {
        hit.addEventListener("pointerenter", function () { showAt(idx); });
        hit.addEventListener("pointermove", function () { showAt(idx); });
        hit.addEventListener("pointerleave", hide);
        hit.addEventListener("focus", function () { showAt(idx); });
        hit.addEventListener("blur", hide);
      })(i);
      svg.appendChild(hit);
    }

    // --- legend ---
    var legend = document.createElement("div");
    legend.className = "uvi-chart-legend";
    legend.appendChild(legendItem("var(--accent)", false, data.year + " (aktueller Zeitraum)"));
    legend.appendChild(legendItem("var(--chart-muted)", true, data.prior_year + " (Vorjahr)"));
    container.appendChild(legend);
  }

  function legendItem(color, dashed, label) {
    var item = document.createElement("span");
    item.className = "uvi-chart-legend-item";
    var swatch = document.createElement("svg");
    swatch.setAttribute("width", "18");
    swatch.setAttribute("height", "10");
    swatch.setAttribute("aria-hidden", "true");
    var line = svgEl("line", { x1: 0, y1: 5, x2: 18, y2: 5, stroke: color, "stroke-width": 2.5 });
    if (dashed) line.setAttribute("stroke-dasharray", "5,3");
    swatch.appendChild(line);
    item.appendChild(swatch);
    var text = document.createElement("span");
    text.textContent = label;
    item.appendChild(text);
    return item;
  }

  function drawSeries(svg, points, y, opts) {
    var runs = foundRuns(points);
    runs.forEach(function (run) {
      if (opts.fillOpacity > 0 && run.length > 1) {
        var baseline = y(0);
        var top = run.map(function (i) { return xOf(i) + "," + y(points[i].value); }).join(" L ");
        var d = "M " + top + " L " + xOf(run[run.length - 1]) + "," + baseline +
          " L " + xOf(run[0]) + "," + baseline + " Z";
        svg.appendChild(svgEl("path", { d: d, fill: opts.stroke, opacity: opts.fillOpacity, stroke: "none" }));
      }
      // A lone found point with gaps on both sides has nothing to connect
      // (run.length === 1) — the marker below still shows it happened.
      if (run.length > 1) {
        var d2 = run.map(function (i, idx) {
          return (idx === 0 ? "M" : "L") + xOf(i) + "," + y(points[i].value);
        }).join(" ");
        var lineAttrs = { d: d2, fill: "none", stroke: opts.stroke, "stroke-width": 2, "stroke-linecap": "round", "stroke-linejoin": "round" };
        if (opts.dash) lineAttrs["stroke-dasharray"] = opts.dash;
        svg.appendChild(svgEl("path", lineAttrs));
      }
      run.forEach(function (i) {
        svg.appendChild(svgEl("circle", {
          cx: xOf(i), cy: y(points[i].value), r: opts.markerR,
          fill: opts.stroke, stroke: "var(--surface)", "stroke-width": 2
        }));
      });
    });
  }

  // One page can now carry a chart per Verbrauchsart, each with its own
  // scale and unit, so init walks the list the server emitted rather than
  // looking for a single fixed element.
  function init() {
    var charts = window.SA_UVI_CHARTS;
    if (!charts || !charts.length) return;
    charts.forEach(function (data) {
      var container = document.getElementById(data.dom_id);
      if (container) buildChart(container, data);
    });
  }

  document.addEventListener("DOMContentLoaded", init);

  return {};
})();
