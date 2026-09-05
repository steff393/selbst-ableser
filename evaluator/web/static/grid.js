// Spreadsheet-like bulk editor for master-data tables (STAMM-06/UI-05):
// multi-cell paste from the clipboard, and "fill down" per column. No
// framework, no build step — this is loaded as a plain script. Each call
// to SAGrid.create() sets up one independent grid; a page may have several.
window.SAGrid = (function () {
  "use strict";

  function create(opts) {
    var columns = opts.columns;
    var hiddenColumns = opts.hiddenColumns || [];
    // Read-only columns (e.g. a server-generated access token) render
    // before the editable ones, visible but not a form field — see
    // addRow. colOffset shifts every position-based lookup below by how
    // many of them there are; it is 0 for a grid that doesn't use the
    // option, which is what keeps the two existing grids unaffected.
    var readOnlyColumns = opts.readOnlyColumns || [];
    var colOffset = readOnlyColumns.length;
    var allColumns = readOnlyColumns.concat(columns).concat(hiddenColumns);
    var columnTypes = opts.columnTypes || {};
    var selectOptions = opts.selectOptions || {};
    var placeholders = opts.placeholders || {};

    var body = document.getElementById(opts.bodyId);
    var addRowButton = document.getElementById(opts.addRowId);
    var form = document.getElementById(opts.formId);
    var hiddenInput = document.getElementById(opts.hiddenInputId);
    if (!body || !form || !hiddenInput) return;

    // A select column (e.g. Verbrauchsart) renders as <select>, everything
    // else as <input>; both expose the value/paste-target uniformly via
    // [data-col], so the rest of the grid logic doesn't need to care which
    // one a given column is.
    function makeField(col) {
      if (selectOptions[col]) {
        var select = document.createElement("select");
        select.dataset.col = col;
        selectOptions[col].forEach(function (opt) {
          var option = document.createElement("option");
          option.value = opt.value;
          option.textContent = opt.label;
          select.appendChild(option);
        });
        return select;
      }
      var input = document.createElement("input");
      input.type = columnTypes[col] || "text";
      if (columnTypes[col] === "number") input.step = "any";
      input.dataset.col = col;
      if (placeholders[col]) input.placeholder = placeholders[col];
      input.addEventListener("paste", onPaste);
      return input;
    }

    function addRow(values) {
      values = values || {};
      var tr = document.createElement("tr");
      // The row's pristine, as-loaded values (or {} for a freshly added
      // blank row) — a full revert target for anything that needs to undo
      // an in-progress edit to this specific row (see meters-grid's
      // Zählerwechsel handling in masterdata.html).
      tr.dataset.original = JSON.stringify(values);
      readOnlyColumns.forEach(function (col) {
        var td = document.createElement("td");
        var span = document.createElement("span");
        span.dataset.col = col;
        span.textContent = values[col] || "";
        td.appendChild(span);
        tr.appendChild(td);
      });
      columns.forEach(function (col) {
        var td = document.createElement("td");
        var field = makeField(col);
        if (values[col]) field.value = values[col];
        td.appendChild(field);
        tr.appendChild(td);
      });
      hiddenColumns.forEach(function (col) {
        var input = document.createElement("input");
        input.type = "hidden";
        input.dataset.col = col;
        if (values[col]) input.value = values[col];
        tr.appendChild(input);
      });
      var actionsTd = document.createElement("td");
      var removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.textContent = "✕";
      removeBtn.title = "Zeile entfernen";
      removeBtn.addEventListener("click", function () { tr.remove(); });
      actionsTd.appendChild(removeBtn);
      tr.appendChild(actionsTd);
      body.appendChild(tr);
      return tr;
    }

    function rowIndex(tr) {
      return Array.prototype.indexOf.call(body.children, tr);
    }

    function ensureRowCount(n) {
      while (body.children.length < n) addRow();
    }

    function cellAt(rowIdx, colIdx) {
      var tr = body.children[rowIdx];
      if (!tr) return null;
      return tr.children[colIdx].querySelector("[data-col]");
    }

    // Pasting a multi-cell range (from Excel/Sheets/anything using
    // tab-separated, newline-separated clipboard text) fills it starting at
    // the focused cell, adding rows as needed — the actual point of
    // STAMM-06's requirement.
    function onPaste(e) {
      var text = (e.clipboardData || window.clipboardData).getData("text");
      if (!text || (text.indexOf("\t") === -1 && text.indexOf("\n") === -1)) {
        return; // a single value: let the browser's default paste happen
      }
      e.preventDefault();

      var startInput = e.target;
      var startTd = startInput.parentElement;
      var startTr = startTd.parentElement;
      var startRow = rowIndex(startTr);
      var startCol = Array.prototype.indexOf.call(startTr.children, startTd);

      var rows = text.replace(/\r/g, "").split("\n");
      if (rows.length > 0 && rows[rows.length - 1] === "") rows.pop();

      ensureRowCount(startRow + rows.length);
      rows.forEach(function (line, rOffset) {
        var cells = line.split("\t");
        cells.forEach(function (value, cOffset) {
          var col = startCol + cOffset;
          if (col < colOffset || col >= colOffset + columns.length) return;
          var field = cellAt(startRow + rOffset, col);
          if (field) field.value = value;
        });
      });
    }

    function addFillDownHeader() {
      var headerRow = document.querySelector("#" + opts.gridId + " thead tr");
      if (!headerRow) return;
      columns.forEach(function (col, i) {
        var th = headerRow.children[i + colOffset];
        var btn = document.createElement("button");
        btn.type = "button";
        btn.className = "fill-down";
        btn.textContent = "↓";
        btn.title = "Wert der ersten Zeile nach unten ausfüllen";
        btn.addEventListener("click", function () {
          var first = cellAt(0, i + colOffset);
          if (!first) return;
          for (var r = 1; r < body.children.length; r++) {
            var field = cellAt(r, i + colOffset);
            if (field) field.value = first.value;
          }
        });
        th.appendChild(btn);
      });
    }

    var initial = opts.initial || [];
    if (initial.length === 0) {
      addRow();
    } else {
      initial.forEach(function (row) { addRow(row); });
    }
    addRow(); // one blank trailing row, ready to type or paste into
    addFillDownHeader();

    if (addRowButton) addRowButton.addEventListener("click", function () { addRow(); });

    form.addEventListener("submit", function () {
      var rows = [];
      Array.prototype.forEach.call(body.children, function (tr) {
        var row = {};
        allColumns.forEach(function (col) {
          var field = tr.querySelector('[data-col="' + col + '"]');
          if (!field) { row[col] = ""; return; }
          // A read-only cell is a <span> (no .value); every editable or
          // hidden field is an <input>/<select> (no meaningful
          // .textContent), so exactly one of these is ever populated.
          var raw = field.value !== undefined ? field.value : field.textContent;
          row[col] = raw.trim();
        });
        rows.push(row);
      });
      hiddenInput.value = JSON.stringify(rows);
    });
  }

  return { create: create };
})();
