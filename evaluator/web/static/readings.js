// Client-side filtering, meter selection, and chart for the Zählerstände
// page. The "Zähler" table doubles as the filter UI (Excel-style per-column
// checkbox filters) and the chart's meter picker: a column filter narrows
// both the Zähler table and the Telegramme table below it; clicking a row
// additionally narrows Telegramme to just the clicked meters and draws
// them on the chart. Only Von/Bis reload the page — they decide what was
// even fetched and decrypted server-side (see readings_handler.go);
// everything else here runs against the already-loaded result.
window.SAReadings = (function () {
  "use strict";

  var readings = window.SA_READINGS || [];
  var meters = window.SA_METER_SUMMARY || [];
  var range = window.SA_READINGS_RANGE || { from: "", to: "" };
  var chartSelected = {};

  var CHART_COLORS = [
    "var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)",
    "var(--chart-5)", "var(--chart-6)", "var(--chart-7)", "var(--chart-8)"
  ];
  // Fixed by each meter's position in the full list, not in the current
  // chart selection, so toggling which meters are charted never repaints
  // the ones that stay selected.
  var colorOf = {};
  meters.forEach(function (m, i) { colorOf[m.meter_id] = CHART_COLORS[i % CHART_COLORS.length]; });

  // ---- Excel-style column filters ----

  var FILTER_FIELDS = ["unit_name", "meter_point_id", "meter_id", "device_type", "manufacturer"];
  var EMPTY_LABEL = "(nicht zugeordnet)";

  // columnFilters[field] === null means "no restriction" (every value
  // passes); otherwise it holds the Set of values that stay checked.
  // Distinct-value lists are computed once from the full meter list, not
  // re-narrowed by each other's current filter state (unlike Excel's own
  // cross-filtered dropdowns) — a deliberate simplification.
  var columnFilters = {};
  FILTER_FIELDS.forEach(function (f) { columnFilters[f] = null; });

  // "Bekannt" means the telegram's meter has a Zählerplatz on file
  // (meter_point_id set) — the same signal readings_handler.go already
  // leaves blank for a meter nothing has been assigned to yet (STAMM-06
  // onboarding). Not unit_name: a meter point can exist without a unit
  // assigned yet (STAMM-02), and would wrongly count as "unknown" too if
  // that were the check instead.
  var knownOnly = false;

  function displayValue(v) {
    return v === "" || v == null ? EMPTY_LABEL : v;
  }

  function distinctValues(field) {
    var counts = {};
    meters.forEach(function (m) {
      var v = displayValue(m[field]);
      counts[v] = (counts[v] || 0) + 1;
    });
    return Object.keys(counts).sort().map(function (v) { return { value: v, count: counts[v] }; });
  }

  function columnPasses(row, field) {
    var allowed = columnFilters[field];
    return allowed === null || allowed.has(displayValue(row[field]));
  }

  function matchesColumns(row) {
    if (knownOnly && !row.meter_point_id) return false;
    return FILTER_FIELDS.every(function (f) { return columnPasses(row, f); });
  }

  function isUnfiltered() {
    return FILTER_FIELDS.every(function (f) { return columnFilters[f] === null; }) && Object.keys(chartSelected).length === 0 && !knownOnly;
  }

  // ---- popover ----

  var openPopover = null; // {field, el}

  function closePopover() {
    if (openPopover) { openPopover.el.remove(); openPopover = null; }
  }

  function togglePopover(field, buttonEl) {
    if (openPopover && openPopover.field === field) { closePopover(); return; }
    closePopover();
    var panel = buildPopover(field);
    document.body.appendChild(panel);
    positionPopover(panel, buttonEl);
    openPopover = { field: field, el: panel };
  }

  function positionPopover(panel, buttonEl) {
    var rect = buttonEl.getBoundingClientRect();
    var left = rect.left;
    var maxLeft = window.innerWidth - panel.offsetWidth - 8;
    if (left > maxLeft) left = Math.max(8, maxLeft);
    panel.style.top = rect.bottom + 4 + "px";
    panel.style.left = left + "px";
  }

  function buildPopover(field) {
    var panel = document.createElement("div");
    panel.className = "col-filter-popover";
    panel.addEventListener("click", function (e) { e.stopPropagation(); });

    var search = document.createElement("input");
    search.type = "text";
    search.placeholder = "Suchen…";
    search.className = "col-filter-search";
    panel.appendChild(search);

    var actions = document.createElement("div");
    actions.className = "col-filter-actions";
    var allBtn = document.createElement("button");
    allBtn.type = "button";
    allBtn.textContent = "Alle";
    allBtn.addEventListener("click", function () {
      columnFilters[field] = null;
      renderList();
      applyFilters();
    });
    var noneBtn = document.createElement("button");
    noneBtn.type = "button";
    noneBtn.textContent = "Keine";
    noneBtn.addEventListener("click", function () {
      columnFilters[field] = new Set();
      renderList();
      applyFilters();
    });
    actions.appendChild(allBtn);
    actions.appendChild(noneBtn);
    panel.appendChild(actions);

    var list = document.createElement("div");
    list.className = "col-filter-list";
    panel.appendChild(list);

    function renderList() {
      list.innerHTML = "";
      var all = distinctValues(field);
      var term = search.value.trim().toLowerCase();
      all.filter(function (entry) {
        return !term || entry.value.toLowerCase().indexOf(term) !== -1;
      }).forEach(function (entry) {
        var item = document.createElement("div");
        var checked = columnFilters[field] === null || columnFilters[field].has(entry.value);
        item.className = "col-filter-item" + (checked ? " selected" : "");
        item.textContent = entry.value + " (" + entry.count + ")";
        item.addEventListener("click", function () {
          var current = columnFilters[field] === null
            ? new Set(all.map(function (e) { return e.value; }))
            : new Set(columnFilters[field]);
          if (checked) current.delete(entry.value); else current.add(entry.value);
          columnFilters[field] = current.size === all.length ? null : current;
          renderList();
          applyFilters();
        });
        list.appendChild(item);
      });
    }
    search.addEventListener("input", renderList);
    renderList();
    return panel;
  }

  document.addEventListener("click", closePopover);
  window.addEventListener("resize", closePopover);

  function updateFilterButtons() {
    document.querySelectorAll(".col-filter-btn").forEach(function (btn) {
      var field = btn.getAttribute("data-field");
      btn.classList.toggle("active", columnFilters[field] !== null);
    });
    document.getElementById("clear-filters-btn").disabled = isUnfiltered();
  }

  function clearAllFilters() {
    FILTER_FIELDS.forEach(function (f) { columnFilters[f] = null; });
    chartSelected = {};
    knownOnly = false;
    document.getElementById("known-only-checkbox").checked = false;
    closePopover();
    applyFilters();
    renderChart();
  }

  // ---- tables ----

  function cell(text) {
    var td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  function cellWithTitle(text, title) {
    var td = cell(text);
    if (title) td.title = title;
    return td;
  }

  function renderMeterTable() {
    var tbody = document.getElementById("meters-select-body");
    tbody.innerHTML = "";
    var shown = 0;
    meters.filter(matchesColumns).forEach(function (m) {
      shown++;
      var tr = document.createElement("tr");
      tr.className = "readings-selectable-row";
      if (chartSelected[m.meter_id]) tr.classList.add("selected-row");
      tr.appendChild(cell(m.unit_name));
      tr.appendChild(cell(m.meter_point_id));
      tr.appendChild(cell(m.meter_id));
      tr.appendChild(cellWithTitle(m.device_type, m.device_type_name));
      tr.appendChild(cellWithTitle(m.manufacturer, m.manufacturer_name));
      tr.addEventListener("click", function () {
        if (chartSelected[m.meter_id]) {
          delete chartSelected[m.meter_id];
        } else {
          chartSelected[m.meter_id] = true;
        }
        tr.classList.toggle("selected-row");
        renderChart();
        renderReadingsTable();
        updateExportLink();
      });
      tbody.appendChild(tr);
    });
    document.getElementById("meters-count").textContent = String(shown);
  }

  function renderReadingsTable() {
    var tbody = document.getElementById("readings-body");
    tbody.innerHTML = "";
    var selectedIDs = Object.keys(chartSelected);
    var shown = 0;
    readings.filter(function (r) {
      if (!matchesColumns(r)) return false;
      if (selectedIDs.length && !chartSelected[r.meter_id]) return false;
      return true;
    }).forEach(function (r) {
      shown++;
      var tr = document.createElement("tr");
      tr.appendChild(cell(r.meter_point_id));
      tr.appendChild(cell(r.unit_name));
      tr.appendChild(cell(r.room));
      tr.appendChild(cellWithTitle(r.device_type, r.device_type_name));
      tr.appendChild(cellWithTitle(r.manufacturer, r.manufacturer_name));
      tr.appendChild(cell(r.meter_id));
      tr.appendChild(cell(r.received_at));
      // 127 is internal/correction.RSSI: a maximal positive dBm value that
      // never occurs from a real reception, used to flag a manually
      // corrected entry inline instead of a signal-strength reading.
      if (r.rssi === 127) {
        tr.appendChild(cellWithTitle("Korrektur", "Dieser Eintrag wurde über „Einzelnen Zählerstand korrigieren“ nachträglich geändert."));
      } else {
        tr.appendChild(cell(String(r.rssi)));
      }

      var valueTd = document.createElement("td");
      if (r.evaluable) {
        valueTd.textContent = String(r.value);
      } else {
        var span = document.createElement("span");
        span.className = "empty-row";
        span.title = "nicht auswertbar";
        span.textContent = "n.a.";
        valueTd.appendChild(span);
      }
      tr.appendChild(valueTd);

      var linkTd = document.createElement("td");
      var a = document.createElement("a");
      a.href = r.decode_url;
      a.target = "_blank";
      a.rel = "noopener";
      a.className = "analyze-link";
      a.title = "Zur Prüfung bei wmbusmeters.org öffnen";
      a.setAttribute("aria-label", "Zur Prüfung bei wmbusmeters.org öffnen");
      a.textContent = "↗";
      linkTd.appendChild(a);
      tr.appendChild(linkTd);

      tbody.appendChild(tr);
    });
    document.getElementById("readings-count").textContent = String(shown);
    if (window.SATable) {
      window.SATable.reapply(document.getElementById("readings-table"));
    }
  }

  function effectiveMeterIDs() {
    var selectedIDs = Object.keys(chartSelected);
    var ids = {};
    readings.forEach(function (r) {
      if (!matchesColumns(r)) return;
      if (selectedIDs.length && !chartSelected[r.meter_id]) return;
      ids[r.meter_id] = true;
    });
    return Object.keys(ids);
  }

  function updateExportLink() {
    var params = new URLSearchParams();
    params.set("from", range.from);
    params.set("to", range.to);
    if (!isUnfiltered()) {
      effectiveMeterIDs().forEach(function (id) { params.append("meter", id); });
    }
    document.getElementById("readings-export-link").href = "/operator/readings/export?" + params.toString();
  }

  function applyFilters() {
    renderMeterTable();
    renderReadingsTable();
    updateExportLink();
    updateFilterButtons();
  }

  // ---- chart: a static (no animation, no library) SVG line chart of raw
  // counter values, redrawn synchronously on every click ----

  function dayToUTC(d) {
    var p = d.split("-");
    return Date.UTC(+p[0], +p[1] - 1, +p[2]);
  }
  function daysBetween(a, b) {
    return Math.round((dayToUTC(b) - dayToUTC(a)) / 86400000);
  }
  function addDays(d, n) {
    var t = new Date(dayToUTC(d));
    t.setUTCDate(t.getUTCDate() + n);
    return t.toISOString().slice(0, 10);
  }
  function formatGridDay(d) {
    var p = d.split("-");
    return p[2] + "." + p[1] + "." + p[0];
  }
  function escapeText(s) {
    var div = document.createElement("div");
    div.textContent = s;
    return div.innerHTML;
  }

  var CHART_W = 880, CHART_H = 360, CHART_ML = 56, CHART_MR = 16, CHART_MT = 16, CHART_MB = 32;

  function renderChart() {
    var container = document.getElementById("readings-chart-container");
    var selected = Object.keys(chartSelected);

    var series = {};
    readings.forEach(function (r) {
      if (!r.evaluable || !chartSelected[r.meter_id]) return;
      (series[r.meter_id] = series[r.meter_id] || []).push({ day: r.day, value: r.value });
    });
    Object.keys(series).forEach(function (m) {
      series[m].sort(function (a, b) { return a.day < b.day ? -1 : a.day > b.day ? 1 : 0; });
    });

    var hasData = selected.some(function (m) { return series[m] && series[m].length; });
    if (!hasData) {
      container.innerHTML = '<p class="hint">Oben in der Zähler-Tabelle eine oder mehrere Zeilen anklicken, um sie hier darzustellen.</p>';
      return;
    }

    var minDay, maxDay, minVal, maxVal, first = true;
    selected.forEach(function (m) {
      (series[m] || []).forEach(function (p) {
        if (first) { minDay = maxDay = p.day; minVal = maxVal = p.value; first = false; return; }
        if (p.day < minDay) minDay = p.day;
        if (p.day > maxDay) maxDay = p.day;
        if (p.value < minVal) minVal = p.value;
        if (p.value > maxVal) maxVal = p.value;
      });
    });
    if (minVal === maxVal) { minVal -= 1; maxVal += 1; }
    var valPad = Math.floor((maxVal - minVal) / 20);
    minVal -= valPad;
    maxVal += valPad;

    var plotW = CHART_W - CHART_ML - CHART_MR;
    var plotH = CHART_H - CHART_MT - CHART_MB;
    var dayRange = daysBetween(minDay, maxDay) || 1;
    var valRange = maxVal - minVal;

    function x(d) { return CHART_ML + daysBetween(minDay, d) / dayRange * plotW; }
    function y(v) { return CHART_MT + plotH - (v - minVal) / valRange * plotH; }

    var svg = ['<svg class="readings-chart" viewBox="0 0 ' + CHART_W + ' ' + CHART_H + '" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Rohwerte über Zeit">'];

    for (var i = 0; i <= 2; i++) {
      var v = Math.round(minVal + i / 2 * valRange);
      var yy = y(v).toFixed(1);
      svg.push('<line x1="' + CHART_ML + '" y1="' + yy + '" x2="' + (CHART_W - CHART_MR) + '" y2="' + yy + '" stroke="var(--chart-grid)" stroke-width="1"/>');
      svg.push('<text x="' + (CHART_ML - 6) + '" y="' + yy + '" fill="var(--chart-muted)" font-size="11" text-anchor="end" dominant-baseline="middle">' + v + '</text>');
    }
    [minDay, addDays(minDay, Math.floor(dayRange / 2)), maxDay].forEach(function (d) {
      var xx = x(d).toFixed(1);
      svg.push('<text x="' + xx + '" y="' + (CHART_H - 8) + '" fill="var(--chart-muted)" font-size="11" text-anchor="middle">' + formatGridDay(d) + '</text>');
    });
    svg.push('<line x1="' + CHART_ML + '" y1="' + (CHART_H - CHART_MB) + '" x2="' + (CHART_W - CHART_MR) + '" y2="' + (CHART_H - CHART_MB) + '" stroke="var(--chart-axis)" stroke-width="1"/>');

    selected.forEach(function (m) {
      var pts = series[m] || [];
      if (!pts.length) return;
      var color = colorOf[m] || CHART_COLORS[0];
      var path = pts.map(function (p, i) {
        return (i === 0 ? "M" : "L") + x(p.day).toFixed(1) + "," + y(p.value).toFixed(1);
      }).join(" ");
      svg.push('<path d="' + path + '" fill="none" stroke="' + color + '" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>');
      pts.forEach(function (p) {
        svg.push('<circle cx="' + x(p.day).toFixed(1) + '" cy="' + y(p.value).toFixed(1) + '" r="3" fill="' + color + '"><title>' + escapeText(m) + ': ' + p.value + ' am ' + formatGridDay(p.day) + '</title></circle>');
      });
    });
    svg.push('</svg>');

    var legend = ['<div class="readings-chart-legend">'];
    selected.forEach(function (m) {
      legend.push('<span class="readings-chart-legend-item"><span class="readings-chart-swatch" style="background:' + (colorOf[m] || CHART_COLORS[0]) + '"></span>' + escapeText(m) + '</span>');
    });
    legend.push('</div>');

    container.innerHTML = svg.join("") + legend.join("");
  }

  function init() {
    document.querySelectorAll(".col-filter-btn").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        togglePopover(btn.getAttribute("data-field"), btn);
      });
    });
    document.getElementById("clear-filters-btn").addEventListener("click", clearAllFilters);
    document.getElementById("known-only-checkbox").addEventListener("change", function (e) {
      knownOnly = e.target.checked;
      applyFilters();
    });

    // A ?chart=meterID (repeatable) in the URL pre-selects meters, so a
    // link to this page can still open with a chart already drawn.
    var params = new URLSearchParams(window.location.search);
    params.getAll("chart").forEach(function (m) { chartSelected[m] = true; });

    applyFilters();
    renderChart();
  }

  document.addEventListener("DOMContentLoaded", init);

  return {};
})();
