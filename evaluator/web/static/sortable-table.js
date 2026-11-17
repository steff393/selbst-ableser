// Click-to-sort for <table data-sortable>: click a header cell to sort by
// that column, click again to reverse. A header with data-sort="number"
// sorts by the leading number in each cell (parseFloat stops at the first
// non-numeric character, so "-72 dBm (gut)" sorts as -72); anything else
// sorts as plain text. No framework, no build step — loaded as a plain
// script (UI-06's "Suche, Sortierung ... erlauben").
window.SATable = (function () {
  "use strict";

  function cellText(tr, i) {
    var cell = tr.children[i];
    return cell ? cell.textContent.trim() : "";
  }

  function compare(va, vb) {
    if (va < vb) return -1;
    if (va > vb) return 1;
    return 0;
  }

  // In a numeric column, a cell without a number ("n.a.", "–") goes to
  // the bottom in either direction rather than sorting as 0 among the
  // real values.
  function sortTable(table, colIndex, numeric, ascending) {
    var tbody = table.tBodies[0];
    if (!tbody) return;
    var rows = Array.prototype.slice.call(tbody.rows);
    rows.sort(function (a, b) {
      var va = cellText(a, colIndex);
      var vb = cellText(b, colIndex);
      if (numeric) {
        va = parseFloat(va);
        vb = parseFloat(vb);
        if (isNaN(va) || isNaN(vb)) return isNaN(va) - isNaN(vb);
      }
      var c = compare(va, vb);
      return ascending ? c : -c;
    });
    rows.forEach(function (r) { tbody.appendChild(r); });
  }

  // reapply re-sorts a table by whatever column was last clicked. Call
  // this after replacing a table's rows from outside (e.g. an auto-
  // refresh poll), so the active sort survives the refresh.
  function reapply(table) {
    var state = table._saSortState;
    if (!state || state.col < 0) return;
    sortTable(table, state.col, state.numeric, state.asc);
  }

  function init(table) {
    var headRow = table.tHead && table.tHead.rows[0];
    if (!headRow) return;
    table._saSortState = { col: -1, asc: true, numeric: false };

    Array.prototype.forEach.call(headRow.cells, function (th, i) {
      th.classList.add("sortable-col");
      th.addEventListener("click", function () {
        var numeric = th.dataset.sort === "number";
        var ascending = table._saSortState.col === i ? !table._saSortState.asc : true;
        table._saSortState = { col: i, asc: ascending, numeric: numeric };
        sortTable(table, i, numeric, ascending);
        Array.prototype.forEach.call(headRow.cells, function (h) {
          h.classList.remove("sorted-asc", "sorted-desc");
        });
        th.classList.add(ascending ? "sorted-asc" : "sorted-desc");
      });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    Array.prototype.forEach.call(document.querySelectorAll("table[data-sortable]"), init);
  });

  return { init: init, reapply: reapply };
})();
