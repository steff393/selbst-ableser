package webapp

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"selbst-ableser/internal/masterdata"
)

// CSV in and out, for the three tables. There is no standard format for
// this data, so this one is ours: semicolons, a header line, German dates
// and decimal commas — what a German-locale spreadsheet writes and reads
// without a dialog full of questions.
//
// Two rules make it safe to use with a file that is not the whole truth:
//
//   - The header decides which columns are present, so a file may carry
//     any subset in any order. A supplier's list of numbers and keys is
//     the normal case, not a special one.
//   - An import only adds and changes. A row that is missing from the
//     file leaves its record alone; nothing is ever deleted by importing.
//     Deleting stays a deliberate act in the interface, where it is
//     visible what goes.
const csvSeparator = ";"

// parseAESKey reads one meter's key, empty being a valid answer: an
// unencrypted device has none, and crypto.Decrypt never looks at the key
// for a telegram that carries no encryption.
func parseAESKey(number, text string) ([16]byte, string) {
	var key [16]byte
	raw, err := hex.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != 16 {
		return key, fmt.Sprintf("Zähler %s: AES-Schlüssel muss leer oder genau 32 Hex-Zeichen sein.", number)
	}
	copy(key[:], raw)
	return key, ""
}

type csvTable struct {
	Key    string // form value and URL parameter
	Label  string // what the operator calls it
	Header string
}

var csvTables = map[string]csvTable{
	"units":       {Key: "units", Label: "Wohnungen", Header: "Wohnung;Bezeichnung;Fläche"},
	"meterpoints": {Key: "meterpoints", Label: "Zählerplätze", Header: "Zählerplatz;Wohnung;Raum;Typ"},
	"meters": {Key: "meters", Label: "Zähler",
		Header: "Zählernummer;Zählerplatz;AES-Schlüssel;Einbau;Anfangsstand;kc-Faktor;Stichtagsmonat;Ausbau;Endstand"},
}

func csvLine(fields ...string) string { return strings.Join(fields, csvSeparator) }

func csvUnits(md masterdata.MasterData) string {
	lines := []string{csvTables["units"].Header}
	for _, u := range md.Units {
		lines = append(lines, csvLine(u.ID, u.Name, germanNumber(u.AreaM2)))
	}
	return strings.Join(lines, "\n")
}

func csvMeterPoints(md masterdata.MasterData) string {
	lines := []string{csvTables["meterpoints"].Header}
	for _, mp := range md.MeterPoints {
		lines = append(lines, csvLine(mp.ID, mp.UnitID, mp.Room, kindShortLabel(mp.Kind)))
	}
	return strings.Join(lines, "\n")
}

func csvMeters(md masterdata.MasterData) string {
	lines := []string{csvTables["meters"].Header}
	for _, row := range meterRowsInTableOrder(md.MeterPoints, md.Meters) {
		kc := strings.Replace(row.KCFactor, ".", ",", 1)
		lines = append(lines, csvLine(row.Number, row.MeterPointID, row.AESKey, row.InstalledAt,
			row.StartReading, kc, row.FreezeMonth, row.RemovedAt, row.EndReading))
	}
	return strings.Join(lines, "\n")
}

func germanNumber(f float64) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', -1, 64), ".", ",", 1)
}

// csvColumn normalizes a header cell so that "AES-Schlüssel", "aes
// schluessel" and "AESSchlüssel" all name the same column. A file that
// travelled through a spreadsheet and back rarely keeps its spelling.
func csvColumn(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	folded := strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss",
		"-", "", " ", "", "_", "", ".", "")
	return folded.Replace(name)
}

// csvColumnName resolves one header cell to the name this code uses, or
// reports that nobody asked for it.
//
// It tries the spelled-out umlauts as a second guess rather than folding
// them away outright: "zaehlernummer" is the same column as
// "Zählernummer", but folding every "ae" would also turn "AES-Schlüssel"
// into "as-schlussel" — the acronym is not a spelled-out umlaut.
func csvColumnName(cell string) (string, bool) {
	key := csvColumn(cell)
	if name, ok := csvAliases[key]; ok {
		return name, true
	}
	spelled := strings.NewReplacer("ae", "a", "oe", "o", "ue", "u")
	name, ok := csvAliases[spelled.Replace(key)]
	return name, ok
}

// csvAliases maps every accepted spelling of a column onto one name this
// code uses.
var csvAliases = map[string]string{
	"wohnung": "unit", "wohnungsid": "unit", "unit": "unit",
	"bezeichnung": "name", "name": "name",
	"flache": "area", "flacheinm2": "area", "qm": "area",
	"zahlerplatz": "place", "zahlerplatzid": "place", "platz": "place",
	"raum": "room",
	"typ":  "kind", "art": "kind", "verbrauchsart": "kind",
	"zahlernummer": "number", "nummer": "number", "zahler": "number",
	"aesschlussel": "aes", "aes": "aes", "schlussel": "aes",
	"einbau": "installed", "einbaudatum": "installed",
	"anfangsstand": "start", "startwert": "start",
	"kcfaktor": "kc", "kc": "kc",
	"stichtagsmonat": "freeze", "stichtagmonat": "freeze", "stichtag": "freeze",
	"ausbau": "removed", "ausbaudatum": "removed",
	"endstand": "end",
}

// csvRecord is one data line, addressed by column name. Only the columns
// the file actually carries are present — that is what lets a partial
// file leave everything else alone.
type csvRecord struct {
	Line   int
	Values map[string]string
}

func (r csvRecord) has(column string) bool {
	_, ok := r.Values[column]
	return ok
}

func parseCSV(text string) ([]csvRecord, []string, string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var header []string
	var unknown []string
	var records []csvRecord

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		// A byte order mark is what a German Excel writes; it belongs to
		// the file, not to the first column's name.
		line = strings.TrimPrefix(line, "\uFEFF")
		if line == "" {
			continue
		}
		cells := strings.Split(line, csvSeparator)
		if header == nil {
			for _, cell := range cells {
				name, ok := csvColumnName(cell)
				if !ok {
					name = ""
					unknown = append(unknown, strings.TrimSpace(cell))
				}
				header = append(header, name)
			}
			continue
		}
		values := make(map[string]string, len(header))
		for j, cell := range cells {
			if j >= len(header) || header[j] == "" {
				continue
			}
			values[header[j]] = strings.TrimSpace(cell)
		}
		records = append(records, csvRecord{Line: i + 1, Values: values})
	}

	if header == nil {
		return nil, nil, "Die eingefügten Zeilen enthalten nichts, was sich lesen ließe."
	}
	return records, unknown, ""
}

// importRow is one line of the preview: what would happen, or why not.
type importRow struct {
	Line    int
	Key     string
	Action  string
	Values  string
	Problem string
}

func (r importRow) OK() bool { return r.Problem == "" }

type importPreview struct {
	Table   string
	Label   string
	CSV     string
	Unknown []string
	Rows    []importRow
	Accept  int
}

// planImport works out, without changing anything, what each line of the
// file would do. applyImport then reads the same text again rather than
// trusting a plan that travelled through the browser — the same rule the
// swap list follows.
func planImport(md masterdata.MasterData, table, text string) (importPreview, string) {
	spec, ok := csvTables[table]
	if !ok {
		return importPreview{}, "Unbekannte Tabelle."
	}
	records, unknown, msg := parseCSV(text)
	if msg != "" {
		return importPreview{}, msg
	}

	preview := importPreview{Table: table, Label: spec.Label, CSV: text, Unknown: unknown}
	_, rows := applyRecords(md, table, records)
	preview.Rows = rows
	for _, r := range rows {
		if r.OK() {
			preview.Accept++
		}
	}
	return preview, ""
}

// applyRecords is the single place that decides what a CSV line means. It
// both plans and applies, so a preview can never promise something the
// apply step then does differently.
func applyRecords(md masterdata.MasterData, table string, records []csvRecord) (masterdata.MasterData, []importRow) {
	out := md
	out.Units = append([]masterdata.Unit(nil), md.Units...)
	out.MeterPoints = append([]masterdata.MeterPoint(nil), md.MeterPoints...)
	out.Meters = append([]masterdata.Meter(nil), md.Meters...)

	rows := make([]importRow, 0, len(records))
	for _, rec := range records {
		var row importRow
		switch table {
		case "units":
			row = applyUnitRecord(&out, rec)
		case "meterpoints":
			row = applyMeterPointRecord(&out, rec)
		case "meters":
			row = applyMeterRecord(&out, rec)
		}
		rows = append(rows, row)
	}
	return out, rows
}

func changed(parts []string) string {
	if len(parts) == 0 {
		return "nichts"
	}
	return strings.Join(parts, ", ")
}

// Every apply*Record follows the same shape, and the shape is the point:
// build the record to the side, and put it into the master data only at
// the very end, once nothing has objected. Writing as you go and then
// returning a problem leaves half a record behind — a meter point that
// the preview refused would still be created, silently and with default
// values.
func applyUnitRecord(md *masterdata.MasterData, rec csvRecord) importRow {
	id := rec.Values["unit"]
	row := importRow{Line: rec.Line, Key: id}
	if id == "" {
		row.Problem = "ohne Wohnungs-ID"
		return row
	}

	index := -1
	for i := range md.Units {
		if md.Units[i].ID == id {
			index = i
		}
	}
	unit := masterdata.Unit{ID: id}
	row.Action = "wird angelegt"
	if index >= 0 {
		unit = md.Units[index]
		row.Action = "wird geändert"
	}

	var took []string
	if rec.has("name") {
		unit.Name = rec.Values["name"]
		took = append(took, "Bezeichnung")
	}
	if rec.has("area") {
		area, err := strconv.ParseFloat(strings.Replace(rec.Values["area"], ",", ".", 1), 64)
		if err != nil || area <= 0 {
			row.Problem = "die Fläche muss eine Zahl größer als 0 sein"
			return row
		}
		unit.AreaM2 = area
		took = append(took, "Fläche")
	}
	if unit.AreaM2 <= 0 {
		row.Problem = "eine neue Wohnung braucht eine Fläche"
		return row
	}

	if index >= 0 {
		md.Units[index] = unit
	} else {
		md.Units = append(md.Units, unit)
	}
	row.Values = changed(took)
	return row
}

func applyMeterPointRecord(md *masterdata.MasterData, rec csvRecord) importRow {
	id := rec.Values["place"]
	row := importRow{Line: rec.Line, Key: id}
	if id == "" {
		row.Problem = "ohne Zählerplatz-ID"
		return row
	}

	index := -1
	for i := range md.MeterPoints {
		if md.MeterPoints[i].ID == id {
			index = i
		}
	}
	point := masterdata.MeterPoint{ID: id}
	row.Action = "wird angelegt"
	if index >= 0 {
		point = md.MeterPoints[index]
		row.Action = "wird geändert"
	}

	var took []string
	if rec.has("unit") {
		unit := rec.Values["unit"]
		if unit != "" && !unitExists(*md, unit) {
			row.Problem = fmt.Sprintf("die Wohnung %s gibt es nicht", unit)
			return row
		}
		point.UnitID = unit
		if unit == "" {
			took = append(took, "ohne Wohnung, also Hauszähler")
		} else {
			took = append(took, "Wohnung")
		}
	}
	if rec.has("room") {
		point.Room = rec.Values["room"]
		took = append(took, "Raum")
	}
	if rec.has("kind") {
		kind, ok := kindFromCSV(rec.Values["kind"])
		if !ok {
			row.Problem = fmt.Sprintf("unbekannter Typ %q", rec.Values["kind"])
			return row
		}
		point.Kind = kind
		took = append(took, "Typ")
	} else if index < 0 {
		// Der Typ entscheidet Einheit und Auswertung. Ihn für einen neuen
		// Zählerplatz stillschweigend auf den ersten Wert der Aufzählung
		// zu setzen wäre eine Annahme, die niemand sieht.
		row.Problem = "ein neuer Zählerplatz braucht eine Spalte Typ"
		return row
	}

	if index >= 0 {
		md.MeterPoints[index] = point
	} else {
		md.MeterPoints = append(md.MeterPoints, point)
	}
	row.Values = changed(took)
	return row
}

// kindFromCSV accepts both what the export writes ("HKV") and the wire
// values the forms use ("heating"), so a file that came out of this
// system goes back in unchanged.
func kindFromCSV(text string) (masterdata.Kind, bool) {
	if kind, err := parseKind(strings.ToLower(strings.TrimSpace(text))); err == nil {
		return kind, true
	}
	for _, kind := range []masterdata.Kind{
		masterdata.KindHeatCostAllocator, masterdata.KindHotWater, masterdata.KindColdWater,
		masterdata.KindHeatMeter, masterdata.KindGas,
	} {
		if strings.EqualFold(strings.TrimSpace(text), kindShortLabel(kind)) {
			return kind, true
		}
	}
	return 0, false
}

func unitExists(md masterdata.MasterData, id string) bool {
	for _, u := range md.Units {
		if u.ID == id {
			return true
		}
	}
	return false
}

// applyMeterRecord is the one with rules worth stating. A device may sit
// at one meter point only, so the file may place a device that has none
// but never move one that has: moving is a swap, and a swap needs the
// outgoing device's end reading, which no list of values carries.
func applyMeterRecord(md *masterdata.MasterData, rec csvRecord) importRow {
	number := rec.Values["number"]
	row := importRow{Line: rec.Line, Key: number}
	if !meterNumberPattern.MatchString(number) {
		row.Problem = "keine achtstellige Zählernummer"
		return row
	}

	index := -1
	for i := range md.Meters {
		if md.Meters[i].Number == number {
			index = i
		}
	}

	place := rec.Values["place"]
	existing := masterdata.Meter{Number: number, MeterPointID: place}
	row.Action = "wird angelegt"
	if index >= 0 {
		existing = md.Meters[index]
		row.Action = "wird geändert"
		if rec.has("place") && place != "" && place != existing.MeterPointID {
			row.Problem = fmt.Sprintf("hängt bereits am Zählerplatz %s — ein Umzug ist ein Zählerwechsel", existing.MeterPointID)
			return row
		}
	} else {
		if place == "" {
			row.Problem = "neuer Zähler ohne Zählerplatz — im Reiter Gesamtansicht zuordnen oder eine Spalte Zählerplatz ergänzen"
			return row
		}
		if !meterPointExists(*md, place) {
			row.Problem = fmt.Sprintf("den Zählerplatz %s gibt es nicht", place)
			return row
		}
		if occupant, taken := activeMeterAt(*md, place); taken {
			row.Problem = fmt.Sprintf("am Zählerplatz %s hängt %s — das wäre ein Zählerwechsel", place, occupant)
			return row
		}
	}

	// Read the values through the same row type the grid uses, so a CSV
	// line and a typed row cannot disagree about what a value means.
	kind := kindOfPlace(md.MeterPoints, existing.MeterPointID)
	current := meterRowFromMeter(existing, kind)
	var took []string
	take := func(column, field string, into *string) {
		if rec.has(column) {
			*into = rec.Values[column]
			took = append(took, field)
		}
	}
	take("aes", "Schlüssel", &current.AESKey)
	take("installed", "Einbau", &current.InstalledAt)
	take("start", "Anfangsstand", &current.StartReading)
	take("kc", "kc-Faktor", &current.KCFactor)
	take("freeze", "Stichtagsmonat", &current.FreezeMonth)
	take("removed", "Ausbau", &current.RemovedAt)
	take("end", "Endstand", &current.EndReading)

	meter, msg := meterFromRow(current, kind)
	if msg != "" {
		row.Problem = strings.TrimPrefix(msg, fmt.Sprintf("Zähler %s: ", number))
		return row
	}
	meter.MeterPointID = existing.MeterPointID

	if index >= 0 {
		md.Meters[index] = meter
	} else {
		md.Meters = append(md.Meters, meter)
		took = append(took, "Zählerplatz "+place)
	}
	row.Values = changed(took)
	return row
}

func meterPointExists(md masterdata.MasterData, id string) bool {
	for _, mp := range md.MeterPoints {
		if mp.ID == id {
			return true
		}
	}
	return false
}

func activeMeterAt(md masterdata.MasterData, placeID string) (string, bool) {
	for _, m := range md.Meters {
		if m.MeterPointID == placeID && m.RemovedAt == nil {
			return m.Number, true
		}
	}
	return "", false
}
