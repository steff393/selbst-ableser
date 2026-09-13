// The Anlage tab's dialogs. Every ID and every meter number on the page
// carries the values of the thing it names as data attributes; clicking
// one fills the matching dialog and opens it. Nothing is fetched, nothing
// is rendered client-side — the forms post like any other form, and the
// server answers with the page again.
(function () {
  "use strict";

  var today = (document.currentScript && document.currentScript.dataset.today) || "";

  function dialogFor(name) {
    return document.getElementById("sa-dlg-" + name);
  }

  function field(dlg, name) {
    return dlg.querySelector('[name="' + name + '"]');
  }

  function setField(dlg, name, value) {
    var el = field(dlg, name);
    if (el) el.value = value || "";
  }

  function show(el, visible) {
    if (el) el.hidden = !visible;
  }

  // ---- Zählerplatz-IDs: Vorschlag für einen neuen Platz ----------------
  //
  // Wohnungsnummer plus zwei Stellen, Wasser im 2x-Block, weitergezählt ab
  // dem höchsten schon vergebenen. Das ist ein Vorschlag und sonst nichts:
  // Er entsteht genau einmal, beim Öffnen. Ihn später noch einmal zu
  // berechnen — etwa wenn der Typ wechselt — würde eine ID austauschen,
  // die der Betreiber schon gelesen hat, ohne dass es auffällt.
  function takenPlaceIDs() {
    var taken = {};
    document.querySelectorAll('[data-dialog="meterpoint"][data-id]').forEach(function (b) {
      taken[b.dataset.id] = true;
    });
    return taken;
  }

  function suggestPlaceID(unitID, kind) {
    var taken = takenPlaceIDs();
    if (!unitID) {
      for (var h = 1; h < 100; h++) {
        var houseID = "H" + (h < 10 ? "0" + h : h);
        if (!taken[houseID]) return houseID;
      }
      return "";
    }
    var water = kind === "hot_water" || kind === "cold_water";
    var base = Number(unitID) * 100 + (water ? 20 : 0);
    if (!isFinite(base) || base === 0) return "";
    for (var n = base + 1; n < base + 20; n++) {
      if (!taken[String(n)]) return String(n);
    }
    return "";
  }

  // ---- Vorschlagsliste empfangener Zähler ------------------------------

  function fillCandidates(dlg, kind) {
    var box = dlg.querySelector(".sa-suggest");
    var list = dlg.querySelector(".sa-cand-list");
    var search = dlg.querySelector('.sa-suggest input[type="search"]');
    var source = document.getElementById("sa-candidates");
    if (!box || !list || !source) return;

    var all = Array.prototype.slice.call(source.children);
    if (all.length === 0) {
      show(box, false);
      return;
    }
    show(box, true);
    if (search) search.value = "";

    function draw() {
      var typed = search ? search.value.trim() : "";
      var matching = [];
      var rest = [];
      all.forEach(function (item) {
        if (typed && item.dataset.number.indexOf(typed) === -1) return;
        (item.dataset.kind === kind ? matching : rest).push(item);
      });

      list.textContent = "";
      if (matching.length === 0 && rest.length === 0) {
        var none = document.createElement("p");
        none.className = "sa-why";
        none.textContent = "Kein empfangener Zähler passt auf diese Ziffernfolge.";
        list.appendChild(none);
        return;
      }
      matching.concat(rest).forEach(function (item) {
        var copy = item.cloneNode(true);
        if (item.dataset.kind === kind) copy.classList.add("sa-cand-match");
        copy.addEventListener("click", function () {
          setField(dlg, "number", copy.dataset.number);
          var numberField = field(dlg, "number");
          if (numberField) numberField.focus();
        });
        list.appendChild(copy);
      });
    }

    if (search && !search.dataset.wired) {
      search.dataset.wired = "1";
      search.addEventListener("input", draw);
    }
    if (search) search.dataset.kind = kind;
    draw();
  }

  // ---- Dialoge füllen ---------------------------------------------------

  function openUnit(data) {
    var dlg = dialogFor("unit");
    setField(dlg, "original_id", data.id || "");
    setField(dlg, "id", data.id || "");
    setField(dlg, "name", data.name || "");
    setField(dlg, "area", data.area || "");
    show(dlg.querySelector(".sa-delete"), !!data.id);
    dlg.showModal();
    field(dlg, "id").focus();
  }

  function openMeterPoint(data) {
    var dlg = dialogFor("meterpoint");
    var isNew = !data.id;
    var kind = data.kind || "heating";
    setField(dlg, "original_id", data.id || "");
    setField(dlg, "unit_id", data.unit || "");
    setField(dlg, "room", data.room || "");
    setField(dlg, "kind", kind);
    setField(dlg, "id", isNew ? suggestPlaceID(data.unit || "", kind) : data.id);

    var why = dlg.querySelector('[data-why="id"]');
    if (why) {
      why.textContent = isNew
        ? "Vorschlag, überschreibbar — er ändert sich nicht mehr von selbst."
        : "Änderbar; die Zähler dieses Platzes ziehen mit.";
      why.classList.toggle("sa-filled", isNew);
    }
    show(dlg.querySelector(".sa-delete"), !isNew);
    dlg.showModal();
    field(dlg, "id").focus();
  }

  function openMeter(data) {
    var dlg = dialogFor("meter");
    var isNew = !data.number;
    setField(dlg, "original_number", data.number || "");
    setField(dlg, "meter_point_id", data.place || "");
    setField(dlg, "number", data.number || "");
    setField(dlg, "aes_key", data.aes || "");
    setField(dlg, "installed_at", data.installed || "");
    setField(dlg, "start_reading", data.start || "");
    setField(dlg, "kc_factor", data.kc || "");
    setField(dlg, "freeze_month", data.freeze || "");
    setField(dlg, "removed_at", data.removed || "");
    setField(dlg, "end_reading", data.end || "");

    var place = dlg.querySelector("[data-place]");
    if (place) place.textContent = data.placeLabel || "";
    // Ausbau and Endstand are two separate elements now that Anfangsstand
    // and Endstand share a row — both still only make sense together, so
    // both still carry the same "sa-removal" toggle.
    dlg.querySelectorAll(".sa-removal").forEach(function (el) { show(el, !!data.removed); });
    show(dlg.querySelector(".sa-delete"), !isNew);
    // Umhängen gibt es nur für ein Gerät, das schon irgendwo hängt: Beim
    // Anlegen steht der Platz ohnehin fest, weil man ihn angeklickt hat.
    show(dlg.querySelector(".sa-move"), !isNew);

    if (isNew) {
      fillCandidates(dlg, data.kind || "");
    } else {
      show(dlg.querySelector(".sa-suggest"), false);
    }
    dlg.showModal();
    field(dlg, "number").focus();
  }

  function openSwap(data) {
    var dlg = dialogFor("swap");
    setField(dlg, "meter_point_id", data.place || "");
    setField(dlg, "day", today);
    setField(dlg, "outgoing_end_reading", "");
    setField(dlg, "number", "");
    setField(dlg, "aes_key", "");
    setField(dlg, "start_reading", "0");
    setField(dlg, "kc_factor", data.kc || "");
    setField(dlg, "freeze_month", data.freeze || "");

    var place = dlg.querySelector("[data-place]");
    if (place) {
      place.textContent = (data.placeLabel || "") + " — ausgebaut wird " + (data.outgoing || "") + ".";
    }
    var kcWhy = dlg.querySelector('[data-why="kc"]');
    if (kcWhy) {
      kcWhy.textContent = data.kc
        ? "Übernommen von " + data.outgoing + " — beim neuen Modell oft ein anderer."
        : "War beim alten Gerät leer.";
    }
    var freezeWhy = dlg.querySelector('[data-why="freeze"]');
    if (freezeWhy) {
      freezeWhy.textContent = data.freeze
        ? "Übernommen von " + data.outgoing + "."
        : "War beim alten Gerät leer.";
    }
    fillCandidates(dlg, data.kind || "");
    dlg.showModal();
    field(dlg, "outgoing_end_reading").focus();
  }

  // ---- Suchen und "was fehlt noch" --------------------------------------
  //
  // Alles hier ist im Browser: Die Seite steht schon vollständig da, und
  // eine Suche, die den Server fragt, wäre bei vierzig Zählerplätzen
  // langsamer als das Tippen. Gefiltert werden Zählerplatz-Zeilen; eine
  // Wohnung, in der keine übrig bleibt, verschwindet mit.

  var search = document.getElementById("sa-search");
  var noMeter = document.getElementById("sa-no-meter");
  var noKey = document.getElementById("sa-no-key");
  var neverSeen = document.getElementById("sa-never-seen");
  var filterCount = document.getElementById("sa-filter-count");

  function applyFilter() {
    if (!search) return;
    var typed = search.value.trim().toLowerCase();
    var wantMissing = noMeter.checked || noKey.checked || neverSeen.checked;
    var shown = 0;
    var total = 0;

    document.querySelectorAll(".sa-place[data-search]").forEach(function (row) {
      total++;
      var matchesText = typed === "" || row.dataset.search.toLowerCase().indexOf(typed) !== -1;
      // Die Häkchen sind ein Oder: "zeig mir, wo irgendetwas davon
      // fehlt" ist die Frage, nicht "wo alles davon zugleich fehlt".
      var matchesMissing = !wantMissing ||
        (noMeter.checked && row.dataset.noMeter === "1") ||
        (noKey.checked && row.dataset.noKey === "1") ||
        (neverSeen.checked && row.dataset.neverSeen === "1");
      var visible = matchesText && matchesMissing;
      row.hidden = !visible;
      if (visible) shown++;
    });

    document.querySelectorAll(".sa-unit").forEach(function (unit) {
      var any = unit.querySelector(".sa-place[data-search]:not([hidden])");
      unit.hidden = !any;
      // Eine Wohnung, die nur noch wegen der Suche eine Zeile zeigt,
      // aufzuklappen erspart das Klicken.
      if (any && (typed !== "" || wantMissing)) unit.open = true;
    });

    var filtering = typed !== "" || wantMissing;
    filterCount.textContent = filtering ? shown + " von " + total + " Zählerplätzen" : "";
  }

  if (search) {
    search.addEventListener("input", applyFilter);
    [noMeter, noKey, neverSeen].forEach(function (box) {
      box.addEventListener("change", applyFilter);
    });
  }

  // ---- Verdrahtung ------------------------------------------------------

  document.addEventListener("click", function (e) {
    var trigger = e.target.closest("[data-dialog]");
    if (trigger) {
      e.preventDefault();
      var data = trigger.dataset;
      switch (data.dialog) {
        case "unit": openUnit(data); break;
        case "meterpoint": openMeterPoint(data); break;
        case "meter": openMeter(data); break;
        case "swap": openSwap(data); break;
      }
      return;
    }
    var close = e.target.closest("[data-close]");
    if (close) {
      e.preventDefault();
      close.closest("dialog").close();
    }
  });
})();
