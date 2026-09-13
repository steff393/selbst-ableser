package webapp

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

// The Tabellen tab holds one grid per thing — units, meter points,
// meters — and the same three as CSV, out and in. It is for everything
// that is a value rather than a structure: forty keys from a supplier,
// forty kc factors, a Stichtag month for a whole batch. Which device sits
// at which meter point is not editable here; that belongs to the Anlage
// tab, so the assignment has exactly one place it can be changed.

// meterPointRow is one row of the Zählerplätze grid, every field a plain
// string so the browser can send back whatever was typed — validation
// happens server-side when the grid is submitted (STAMM-05).
type meterPointRow struct {
	ID     string `json:"id"`
	UnitID string `json:"unit_id"`
	Room   string `json:"room"`
	Kind   string `json:"kind"`
}

func meterPointRowsFrom(points []masterdata.MeterPoint) []meterPointRow {
	rows := make([]meterPointRow, 0, len(points))
	for _, mp := range points {
		rows = append(rows, meterPointRow{
			ID:     mp.ID,
			UnitID: mp.UnitID,
			Room:   mp.Room,
			Kind:   kindToString(mp.Kind),
		})
	}
	return rows
}

// meterRowsInTableOrder lists every device, current and long since
// removed, grouped by meter point and oldest first — so one place's
// history reads as a block rather than being scattered.
func meterRowsInTableOrder(points []masterdata.MeterPoint, meters []masterdata.Meter) []meterRow {
	order := make(map[string]int, len(points))
	for i, mp := range points {
		order[mp.ID] = i
	}
	sorted := append([]masterdata.Meter(nil), meters...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.MeterPointID != b.MeterPointID {
			return order[a.MeterPointID] < order[b.MeterPointID]
		}
		return a.InstalledAt.Before(b.InstalledAt)
	})

	rows := make([]meterRow, 0, len(sorted))
	for _, m := range sorted {
		rows = append(rows, meterRowFromMeter(m, kindOfPlace(points, m.MeterPointID)))
	}
	return rows
}

func parseMeterPointRows(rows []meterPointRow) ([]masterdata.MeterPoint, string) {
	points := make([]masterdata.MeterPoint, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for i, row := range rows {
		// The Kind column is a <select> and therefore always carries a
		// value, even in an untouched blank row — so it deliberately does
		// not count towards deciding whether a row is blank.
		if row.ID == "" && row.UnitID == "" && row.Room == "" {
			continue
		}
		label := row.ID
		if label == "" {
			label = fmt.Sprintf("Zeile %d", i+1)
		}
		if row.ID == "" {
			return nil, fmt.Sprintf("Zählerplatz %s: ID darf nicht leer sein.", label)
		}
		if seen[row.ID] {
			return nil, fmt.Sprintf("Zählerplatz %s: die ID kommt in mehreren Zeilen vor.", label)
		}
		kind, err := parseKind(row.Kind)
		if err != nil {
			return nil, fmt.Sprintf("Zählerplatz %s: %v", label, err)
		}
		seen[row.ID] = true
		points = append(points, masterdata.MeterPoint{ID: row.ID, UnitID: row.UnitID, Room: row.Room, Kind: kind})
	}
	return points, ""
}

// parseMeterTableRows converts the Zähler grid's rows into meters. Number
// and meter point are editable here too — a typo in either is exactly
// the kind of thing worth correcting where the row already is, without a
// detour through the Anlage tab — but the grid replaces the whole list,
// so two rows racing for the same meter point (both without an
// Ausbaudatum) is not something this function alone can rule out; that
// is what masterdata.Validate's chain check catches before anything is
// written.
//
// points resolves each row's reading convention (m³ with a comma for
// water, a plain integer otherwise, see reading_value.go) via its meter
// point — the one it is being saved to, not necessarily the one it came
// from.
func parseMeterTableRows(rows []meterRow, points []masterdata.MeterPoint) ([]masterdata.Meter, string) {
	meters := make([]masterdata.Meter, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Number == "" && row.MeterPointID == "" && row.AESKey == "" && row.InstalledAt == "" {
			continue // a blank trailing row from the grid
		}
		if row.Number == "" {
			return nil, "Eine Zeile im Zähler-Raster hat keine Zählernummer."
		}
		if !meterNumberPattern.MatchString(row.Number) {
			return nil, fmt.Sprintf("Zähler %s: die Zählernummer besteht aus genau acht Ziffern.", row.Number)
		}
		if seen[row.Number] {
			return nil, fmt.Sprintf("Zähler %s: die Zählernummer kommt in mehreren Zeilen vor.", row.Number)
		}
		seen[row.Number] = true
		if row.MeterPointID == "" {
			return nil, fmt.Sprintf("Zähler %s: kein Zählerplatz angegeben.", row.Number)
		}

		meter, msg := meterFromRow(row, kindOfPlace(points, row.MeterPointID))
		if msg != "" {
			return nil, msg
		}
		meters = append(meters, meter)
	}
	return meters, ""
}

// meterFromRow is the shared reader for a meter's values, whether they
// arrive from the grid or from a CSV line.
func meterFromRow(row meterRow, kind masterdata.Kind) (masterdata.Meter, string) {
	m := masterdata.Meter{Number: row.Number, MeterPointID: row.MeterPointID}

	if row.AESKey != "" {
		key, msg := parseAESKey(row.Number, row.AESKey)
		if msg != "" {
			return masterdata.Meter{}, msg
		}
		m.AESKey = key
	}
	if row.InstalledAt != "" {
		day, err := parseGridDay(row.InstalledAt)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiges Einbaudatum (TT.MM.JJJJ erwartet).", row.Number)
		}
		m.InstalledAt = day
	}
	start, msg := parseReadingValue(kind, "Anfangsstand", row.StartReading)
	if msg != "" {
		return masterdata.Meter{}, fmt.Sprintf("Zähler %s: %s", row.Number, msg)
	}
	m.StartReading = start

	if row.KCFactor != "" {
		kc, err := strconv.ParseFloat(strings.Replace(row.KCFactor, ",", ".", 1), 64)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiger kc-Faktor.", row.Number)
		}
		m.KCFactor = kc
	}
	if row.FreezeMonth != "" {
		fm, err := strconv.Atoi(row.FreezeMonth)
		if err != nil || fm < 1 || fm > 12 {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiger Stichtag-Monat (1-12 erwartet).", row.Number)
		}
		m.FreezeMonth = fm
	}
	if (row.RemovedAt == "") != (row.EndReading == "") {
		return masterdata.Meter{}, fmt.Sprintf("Zähler %s: Ausbaudatum und Endstand gehören zusammen — beide oder keines.", row.Number)
	}
	if row.RemovedAt != "" {
		day, err := parseGridDay(row.RemovedAt)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiges Ausbaudatum.", row.Number)
		}
		end, msg := parseReadingValue(kind, "Endstand", row.EndReading)
		if msg != "" {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: %s", row.Number, msg)
		}
		m.RemovedAt = &day
		m.EndReading = &end
	}
	return m, ""
}

// handleMasterDataImport shows what a pasted CSV would do, without doing
// any of it.
func (a *App) handleMasterDataImport(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	preview, msg := planImport(md, r.PostFormValue("table"), r.PostFormValue("csv"))
	if msg != "" {
		a.renderMasterDataTab(w, sess, md, normalizeTab(r.PostFormValue("table")), msg)
		return
	}
	a.renderMasterDataImport(w, sess, md, &preview, "")
}

// handleMasterDataImportApply writes what the preview showed — by reading
// the pasted text again rather than trusting a plan that went through the
// browser and came back. It costs a second parse and buys the guarantee
// that what is written follows from the operator's own text against the
// data as it stands now.
func (a *App) handleMasterDataImportApply(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	table := r.PostFormValue("table")
	text := r.PostFormValue("csv")
	records, _, msg := parseCSV(text)
	if msg != "" {
		a.renderMasterDataTab(w, sess, md, normalizeTab(table), msg)
		return
	}
	updated, rows := applyRecords(md, table, records)

	// Lines the plan refused are simply not carried out; the rest is.
	// Refusing the whole file over one bad line would be the wrong kind
	// of strictness here — a supplier's list of forty keys is worth
	// having even if one row names a device nobody entered yet.
	if d := masterdata.Validate(updated); !d.OK() {
		preview, _ := planImport(md, table, text)
		a.renderMasterDataImport(w, sess, md, &preview, "Prüfung fehlgeschlagen: "+joinErrors(d.Errors))
		return
	}
	if err := a.Vault.Save(a.MasterDataPath, updated); err != nil {
		a.renderMasterDataTab(w, sess, md, normalizeTab(table), "Speichern fehlgeschlagen: "+err.Error())
		return
	}

	taken := 0
	for _, row := range rows {
		if row.OK() {
			taken++
		}
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), fmt.Sprintf("csv import %s: %d rows", table, taken))
	http.Redirect(w, r, "/operator/masterdata?tab="+normalizeTab(table), http.StatusSeeOther)
}
