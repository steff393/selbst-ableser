package webapp

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

var (
	isoDayPattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	germanDayPattern = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})$`)
)

// parseGridDay accepts a day typed or pasted into the grid in either the
// display format (DD.MM.YYYY — what a German-locale spreadsheet produces
// on copy) or the internal ISO form (YYYY-MM-DD), so pasting a column of
// dates out of Excel doesn't first need reformatting into ISO by hand.
func parseGridDay(s string) (telegram.Day, error) {
	if isoDayPattern.MatchString(s) {
		return telegram.ParseDay(s)
	}
	if m := germanDayPattern.FindStringSubmatch(s); m != nil {
		day, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		if d, err := telegram.ParseDay(fmt.Sprintf("%04d-%02d-%02d", year, month, day)); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("ungültiges Datum, erwartet TT.MM.JJJJ")
}

// formatGridDay is parseGridDay's inverse for redisplaying a stored day in
// the grid: always DD.MM.YYYY, regardless of which accepted form it was
// originally entered in.
func formatGridDay(d telegram.Day) string {
	s := string(d)
	if len(s) != 10 {
		return s // defensive: a Day is always already-validated YYYY-MM-DD
	}
	return s[8:10] + "." + s[5:7] + "." + s[0:4]
}

type masterDataPageData struct {
	Base
	Building masterdata.Building

	// The Anlage tab: the installation as a structure, laid out by
	// structureView.
	Units       []unitView
	HousePlaces []placeView
	Detected    []detectedView
	Kinds       []kindOption

	// The Tabellen tab: the same data as grid rows, one grid per thing,
	// plus each of them as CSV to read out.
	UnitsJSON       template.JS // pre-serialized for the bulk-edit grid's initial state
	MeterPointsJSON template.JS
	MetersJSON      template.JS
	CSVUnits        string
	CSVMeterPoints  string
	CSVMeters       string

	// The Zugänge tab: tenant access grants plus the operator's own
	// vault password, next to each other because both are credentials of
	// the installation.
	AccessGridJSON template.JS

	// Import and Swap are set while a pasted block is being previewed:
	// what each of its lines would do, before any of it is done.
	Import *importPreview
	Swap   *swapPreview

	// Tab is which of the two the page opens on. It travels in the URL
	// rather than in a cookie, so a reload, a bookmark and the browser's
	// back button all land where the operator was.
	Tab string
	// Today is the German form for the dialogs, TodayISO the one an
	// <input type="date"> insists on.
	Today    string
	TodayISO string
	Error    string
}

// kindOption is one entry of every consumption-type picker on the page.
type kindOption struct {
	Value string
	Label string
	Name  string
}

func kindOptions() []kindOption {
	kinds := []masterdata.Kind{
		masterdata.KindHeatCostAllocator,
		masterdata.KindHotWater,
		masterdata.KindColdWater,
		masterdata.KindHeatMeter,
		masterdata.KindGas,
	}
	out := make([]kindOption, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, kindOption{Value: kindToString(k), Label: kindShortLabel(k), Name: kindDisplayName(k)})
	}
	return out
}

// handleMasterDataView is UI-05: the operator's view of units and of meter
// points combined with their meters, each as a spreadsheet-like bulk
// editor (STAMM-06 originally required this only for meters, as a
// condition for retiring the Excel workflow; the same grid — paste
// multi-cell ranges, fill down, full-table replace on save — turned out to
// be exactly what units needed too, since it would otherwise only ever
// have been appendable, never actually editable, through a single-row
// form). Meter points and meters share one grid rather than two: a meter
// point is meaningless without at least one meter (STAMM-02), so keeping
// them as one row makes that pairing the natural shape of the data entry
// instead of something the operator has to get right across two separate
// tables.
func (a *App) handleMasterDataView(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}
	a.renderMasterDataTab(w, sess, md, r.URL.Query().Get("tab"), "")
}

// renderMasterData re-renders the Gesamtansicht tab with an error — the
// home of every dialog in structureActionThen, so that is where a failed
// unit, meter point, meter or swap edit lands the operator back on.
func (a *App) renderMasterData(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, errMsg string) {
	a.renderMasterDataTab(w, sess, md, tabOverview, errMsg)
}

// Tab names, as they appear in the URL. One per thing rather than one
// big Tabellen tab: each of them fills a screen on its own, and a table
// that is saved on its own should not sit next to two others that are
// not.
//
// tabBuilding ("Anlage") holds the installation-wide settings and the
// export — it is not where a meter point is edited, which is
// tabOverview's job ("Gesamtansicht", the clickable Wohnungen/
// Zählerplätze/Zähler tree). The two used to be one tab; splitting them
// gave the settings a name of their own without losing the one operators
// actually work in day to day.
//
// tabAccess ("Zugänge") used to be its own page at /operator/access; it
// moved in here so every credential and every editable table of the
// installation lives behind the same set of tabs. GET /operator/access
// now only redirects, and its three POST routes are unchanged — they
// still render this page, just through the shared masterdata.html
// template instead of the retired access.html.
const (
	tabBuilding    = "anlage"
	tabOverview    = "uebersicht"
	tabUnits       = "units"
	tabMeterPoints = "meterpoints"
	tabMeters      = "meters"
	tabSwap        = "swap"
	tabAccess      = "access"
)

func normalizeTab(tab string) string {
	switch tab {
	case tabBuilding, tabUnits, tabMeterPoints, tabMeters, tabSwap, tabAccess:
		return tab
	default:
		return tabOverview
	}
}

func (a *App) renderMasterDataTab(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, tab, errMsg string) {
	a.renderMasterDataPage(w, sess, md, gridsOf(md), tab, nil, nil, errMsg, "")
}

func (a *App) renderMasterDataImport(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, preview *importPreview, errMsg string) {
	a.renderMasterDataPage(w, sess, md, gridsOf(md), preview.Table, preview, nil, errMsg, "")
}

func (a *App) renderMasterDataSwap(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, preview *swapPreview, errMsg string) {
	a.renderMasterDataPage(w, sess, md, gridsOf(md), tabSwap, nil, preview, errMsg, "")
}

// renderAccessTab redraws the Zugänge tab with the given rows — either the
// stored grants, or (after a rejected save) exactly what the operator
// typed or pasted, so one bad row doesn't discard the rest of the edit.
// The other two grids and the structure view always come fresh from md,
// since a Zugänge action never touches them.
func (a *App) renderAccessTab(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, rows []accessGridRow, errMsg, flash string) {
	grids := gridsOf(md)
	grids.Accesses = rows
	a.renderMasterDataPage(w, sess, md, grids, tabAccess, nil, nil, errMsg, flash)
}

func gridsOf(md masterdata.MasterData) masterDataGrids {
	return masterDataGrids{
		Units:       unitRowsFrom(md.Units),
		MeterPoints: meterPointRowsFrom(md.MeterPoints),
		Meters:      meterRowsInTableOrder(md.MeterPoints, md.Meters),
		Accesses:    accessGridRowsFrom(md.Accesses),
	}
}

// masterDataGrids is what the four grids of the page hold at one moment:
// either read out of the master data, or exactly as the browser submitted
// them, to be handed back after a rejected save.
type masterDataGrids struct {
	Units       []unitRow
	MeterPoints []meterPointRow
	Meters      []meterRow
	Accesses    []accessGridRow
}

// renderMasterDataPage is the one place the page is rendered from, so
// that a rejected save shows what the operator actually typed rather than
// the unchanged old data — losing a bulk edit to one bad row would defeat
// the point of a grid.
func (a *App) renderMasterDataPage(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, grids masterDataGrids, tab string, preview *importPreview, swap *swapPreview, errMsg, flash string) {
	unitsJSON, _ := json.Marshal(grids.Units)
	pointsJSON, _ := json.Marshal(grids.MeterPoints)
	metersJSON, _ := json.Marshal(grids.Meters)
	accessesJSON, _ := json.Marshal(grids.Accesses)

	units, housePlaces := structureView(md, a.Store)
	var detected []detectedView
	for _, d := range detectedMeters(a.Store, md, a.today()) {
		view := detectedView{Number: d.Number, TypeName: d.TypeName, LastSeen: d.LastSeen}
		if d.KindOK {
			view.Kind = kindToString(d.Kind)
		}
		detected = append(detected, view)
	}

	data := masterDataPageData{
		Base:            a.base("Stammdaten", sess),
		Building:        md.Building,
		Units:           units,
		HousePlaces:     housePlaces,
		Detected:        detected,
		Kinds:           kindOptions(),
		UnitsJSON:       template.JS(unitsJSON),
		MeterPointsJSON: template.JS(pointsJSON),
		MetersJSON:      template.JS(metersJSON),
		CSVUnits:        csvUnits(md),
		CSVMeterPoints:  csvMeterPoints(md),
		CSVMeters:       csvMeters(md),
		AccessGridJSON:  template.JS(accessesJSON),
		Import:          preview,
		Swap:            swap,
		Tab:             normalizeTab(tab),
		Today:           formatGridDay(a.today()),
		TodayISO:        a.today().String(),
		Error:           errMsg,
	}
	data.Flash = flash
	a.render(w, "masterdata.html", data)
}

// handleBuildingSave replaces the installation-wide settings (FACH-06):
// the building's name and the two informational kWh conversion factors,
// which the operator re-derives from the actual annual statement and
// must be able to update without editing the file directly.
func (a *App) handleBuildingSave(w http.ResponseWriter, r *http.Request) {
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

	heating, err := strconv.ParseFloat(r.PostFormValue("heating_kwh_per_unit"), 64)
	if err != nil || heating < 0 {
		a.renderMasterDataTab(w, sess, md, tabBuilding, "Ungültiger Umrechnungsfaktor Heizung.")
		return
	}
	hotWater, err := strconv.ParseFloat(r.PostFormValue("hot_water_kwh_per_m3"), 64)
	if err != nil || hotWater < 0 {
		a.renderMasterDataTab(w, sess, md, tabBuilding, "Ungültiger Umrechnungsfaktor Warmwasser.")
		return
	}

	md.Building.Name = r.PostFormValue("name")
	md.Building.HeatingKWhPerUnit = heating
	md.Building.HotWaterKWhPerM3 = hotWater

	if err := a.Vault.Save(a.MasterDataPath, md); err != nil {
		a.renderMasterDataTab(w, sess, md, tabBuilding, "Speichern fehlgeschlagen: "+err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "building settings")
	http.Redirect(w, r, "/operator/masterdata?tab="+tabBuilding, http.StatusSeeOther)
}

// unitRow is one row of the Wohnungen bulk-edit grid, mirroring meterRow:
// every masterdata.Unit field as a plain string, round-tripped through the
// browser as-is.
type unitRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	AreaM2 string `json:"area_m2"`
}

func unitRowFromUnit(u masterdata.Unit) unitRow {
	return unitRow{ID: u.ID, Name: u.Name, AreaM2: strconv.FormatFloat(u.AreaM2, 'f', -1, 64)}
}

func unitRowsFrom(units []masterdata.Unit) []unitRow {
	rows := make([]unitRow, 0, len(units))
	for _, u := range units {
		rows = append(rows, unitRowFromUnit(u))
	}
	return rows
}

// parseUnitRows converts the Wohnungen grid's rows into units, naming the
// offending row (by ID, or its position if that itself is unusable) on the
// first problem found, same as parseMeterRows.
func parseUnitRows(rows []unitRow) ([]masterdata.Unit, string) {
	units := make([]masterdata.Unit, 0, len(rows))
	for i, row := range rows {
		if row.ID == "" && row.Name == "" && row.AreaM2 == "" {
			continue // a blank trailing row from the grid, not a real entry
		}
		label := row.ID
		if label == "" {
			label = fmt.Sprintf("Zeile %d", i+1)
		}
		if row.ID == "" {
			return nil, fmt.Sprintf("Wohnung %s: ID darf nicht leer sein.", label)
		}
		// A decimal comma is what a German keyboard and a German
		// spreadsheet produce; refusing it would be refusing the normal
		// spelling of a number.
		area, err := strconv.ParseFloat(strings.Replace(defaultZero(row.AreaM2), ",", ".", 1), 64)
		if err != nil {
			return nil, fmt.Sprintf("Wohnung %s: ungültige Fläche.", label)
		}
		units = append(units, masterdata.Unit{ID: row.ID, Name: row.Name, AreaM2: area})
	}
	return units, ""
}

func parseKind(s string) (masterdata.Kind, error) {
	switch s {
	case "heating":
		return masterdata.KindHeatCostAllocator, nil
	case "hot_water":
		return masterdata.KindHotWater, nil
	case "cold_water":
		return masterdata.KindColdWater, nil
	case "heat_meter":
		return masterdata.KindHeatMeter, nil
	case "gas":
		return masterdata.KindGas, nil
	default:
		return 0, fmt.Errorf("unbekannte Verbrauchsart %q", s)
	}
}

func kindToString(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeatCostAllocator:
		return "heating"
	case masterdata.KindHotWater:
		return "hot_water"
	case masterdata.KindColdWater:
		return "cold_water"
	case masterdata.KindHeatMeter:
		return "heat_meter"
	case masterdata.KindGas:
		return "gas"
	default:
		return ""
	}
}

// meterRow is one row of the bulk-edit grid: every masterdata.Meter field
// as a plain string, so the browser can send back whatever the operator
// typed or pasted (including something momentarily invalid mid-edit)
// without the JSON encoding itself getting in the way; validation happens
// entirely server-side when the grid is submitted (STAMM-05).
type meterRow struct {
	Number       string `json:"number"`
	MeterPointID string `json:"meter_point_id"`
	AESKey       string `json:"aes_key"`
	InstalledAt  string `json:"installed_at"`
	StartReading string `json:"start_reading"`
	RemovedAt    string `json:"removed_at"`
	EndReading   string `json:"end_reading"`
	KCFactor     string `json:"kc_factor"`
	FreezeMonth  string `json:"freeze_month"`
}

// meterRowFromMeter formats one meter for display — in a grid, a CSV, or
// the JSON export. kind decides how the two readings are written: m³
// with a comma for water, a plain integer otherwise (see
// reading_value.go), matching however the operator would type them back
// in.
func meterRowFromMeter(m masterdata.Meter, kind masterdata.Kind) meterRow {
	row := meterRow{
		Number:       m.Number,
		MeterPointID: m.MeterPointID,
		InstalledAt:  formatGridDay(m.InstalledAt),
		StartReading: formatReadingValue(kind, m.StartReading),
	}
	if m.AESKey != ([16]byte{}) {
		row.AESKey = hex.EncodeToString(m.AESKey[:])
	}
	if m.RemovedAt != nil {
		row.RemovedAt = formatGridDay(*m.RemovedAt)
	}
	if m.EndReading != nil {
		row.EndReading = formatReadingValue(kind, *m.EndReading)
	}
	if m.KCFactor != 0 {
		row.KCFactor = strconv.FormatFloat(m.KCFactor, 'f', -1, 64)
	}
	if m.FreezeMonth != 0 {
		row.FreezeMonth = strconv.Itoa(m.FreezeMonth)
	}
	return row
}

// handleMasterDataSave replaces one table, leaving the other two as they
// are on disk. Each is a whole list: what the grid submits is the new
// contents, so a removed row is a deletion.
//
// Saving them separately works because the dependencies run one way —
// meters need meter points, meter points need units — and the natural
// order of work follows that direction. A meter point whose unit does not
// exist yet is refused with a message that says so, rather than silently
// waiting for the other table to catch up. What still holds is that
// masterdata.Validate sees the whole graph before anything is written:
// one table is edited, all three are checked (STAMM-05).
func (a *App) handleMasterDataSave(w http.ResponseWriter, r *http.Request) {
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

	table := normalizeTab(r.PostFormValue("table"))
	grids := gridsOf(md)
	updated := md
	var parseErr, what string

	switch table {
	case tabUnits:
		if err := json.Unmarshal([]byte(r.PostFormValue("rows")), &grids.Units); err != nil {
			a.renderMasterDataTab(w, sess, md, tabUnits, "Ungültige Eingabedaten in der Tabelle Wohnungen.")
			return
		}
		updated.Units, parseErr = parseUnitRows(grids.Units)
		what = fmt.Sprintf("%d units saved", len(updated.Units))
	case tabMeterPoints:
		if err := json.Unmarshal([]byte(r.PostFormValue("rows")), &grids.MeterPoints); err != nil {
			a.renderMasterDataTab(w, sess, md, tabMeterPoints, "Ungültige Eingabedaten in der Tabelle Zählerplätze.")
			return
		}
		updated.MeterPoints, parseErr = parseMeterPointRows(grids.MeterPoints)
		what = fmt.Sprintf("%d meter points saved", len(updated.MeterPoints))
	case tabMeters:
		if err := json.Unmarshal([]byte(r.PostFormValue("rows")), &grids.Meters); err != nil {
			a.renderMasterDataTab(w, sess, md, tabMeters, "Ungültige Eingabedaten in der Tabelle Zähler.")
			return
		}
		updated.Meters, parseErr = parseMeterTableRows(grids.Meters, md.MeterPoints)
		what = fmt.Sprintf("%d meters saved", len(updated.Meters))
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	fail := func(errMsg string) {
		a.renderMasterDataPage(w, sess, md, grids, table, nil, nil, errMsg, "")
	}
	if parseErr != "" {
		fail(parseErr)
		return
	}
	if msg := orphanedByRename(md, updated, table); msg != "" {
		fail(msg)
		return
	}
	if d := masterdata.Validate(updated); !d.OK() {
		fail("Prüfung fehlgeschlagen: " + joinErrors(d.Errors))
		return
	}
	if err := a.Vault.Save(a.MasterDataPath, updated); err != nil {
		fail("Speichern fehlgeschlagen: " + err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), what)
	http.Redirect(w, r, "/operator/masterdata?tab="+table, http.StatusSeeOther)
}

// orphanedByRename catches the one way a full-table save can look like a
// small edit and be a large one: In einem Raster, das die ganze Liste
// ersetzt, ist eine geänderte ID nicht von „Zeile gelöscht, andere neu
// angelegt" zu unterscheiden. Alles, was auf die alte ID zeigte, hinge
// danach in der Luft.
//
// masterdata.Validate fängt das ohnehin ab, aber mit einer Meldung über
// den ganzen Graphen. Hier steht, was der Betreiber gerade getan hat und
// wo das Umbenennen hingehört: in den Reiter Gesamtansicht, dessen Dialoge
// alles Abhängige mitziehen.
func orphanedByRename(before, after masterdata.MasterData, table string) string {
	switch table {
	case tabUnits:
		known := make(map[string]bool, len(after.Units))
		for _, u := range after.Units {
			known[u.ID] = true
		}
		for _, mp := range before.MeterPoints {
			if mp.UnitID != "" && !known[mp.UnitID] {
				return fmt.Sprintf("Die Wohnung %s wird noch von Zählerplatz %s benutzt. "+
					"Zum Umbenennen den Reiter Gesamtansicht verwenden — dort ziehen Zählerplätze und Zugänge mit.", mp.UnitID, mp.ID)
			}
		}
		for _, ac := range before.Accesses {
			if !known[ac.UnitID] {
				return fmt.Sprintf("Für die Wohnung %s gibt es noch einen Zugang. "+
					"Zum Umbenennen den Reiter Gesamtansicht verwenden — dort ziehen Zählerplätze und Zugänge mit.", ac.UnitID)
			}
		}
	case tabMeterPoints:
		known := make(map[string]bool, len(after.MeterPoints))
		for _, mp := range after.MeterPoints {
			known[mp.ID] = true
		}
		for _, m := range before.Meters {
			if !known[m.MeterPointID] {
				return fmt.Sprintf("Der Zählerplatz %s wird noch von Zähler %s benutzt. "+
					"Zum Umbenennen den Reiter Gesamtansicht verwenden — dort ziehen die Zähler mit.", m.MeterPointID, m.Number)
			}
		}
	}
	return ""
}

func defaultZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e
	}
	return out
}
