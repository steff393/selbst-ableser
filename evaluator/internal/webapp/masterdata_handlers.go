package webapp

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"

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
	Building      masterdata.Building
	UnitsJSON     template.JS // pre-serialized for the bulk-edit grid's initial state
	MeterGridJSON template.JS
	Error         string
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
	a.renderMasterData(w, sess, md, "")
}

func (a *App) renderMasterData(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, errMsg string) {
	a.renderMasterDataRows(w, sess, md, unitRowsFrom(md.Units), meterGridRowsFrom(md.MeterPoints, md.Meters), errMsg)
}

// renderMasterDataRows is renderMasterData with the two grids' rows given
// explicitly, so that after a rejected save (bad row, failed validation)
// the operator sees what they actually typed or pasted again, not the
// unchanged old data — losing a bulk edit to one bad row would defeat the
// point of the grid.
func (a *App) renderMasterDataRows(w http.ResponseWriter, sess *access.Session, md masterdata.MasterData, unitRows []unitRow, meterGridRows []meterGridRow, errMsg string) {
	unitsJSON, _ := json.Marshal(unitRows)
	meterGridJSON, _ := json.Marshal(meterGridRows)

	a.render(w, "masterdata.html", masterDataPageData{
		Base:          a.base("Stammdaten", sess),
		Building:      md.Building,
		UnitsJSON:     template.JS(unitsJSON),
		MeterGridJSON: template.JS(meterGridJSON),
		Error:         errMsg,
	})
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
		a.renderMasterData(w, sess, md, "Ungültiger Umrechnungsfaktor Heizung.")
		return
	}
	hotWater, err := strconv.ParseFloat(r.PostFormValue("hot_water_kwh_per_m3"), 64)
	if err != nil || hotWater < 0 {
		a.renderMasterData(w, sess, md, "Ungültiger Umrechnungsfaktor Warmwasser.")
		return
	}

	md.Building.Name = r.PostFormValue("name")
	md.Building.HeatingKWhPerUnit = heating
	md.Building.HotWaterKWhPerM3 = hotWater

	if err := a.Vault.Save(a.MasterDataPath, md); err != nil {
		a.renderMasterData(w, sess, md, "Speichern fehlgeschlagen: "+err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "building settings")
	http.Redirect(w, r, "/operator/masterdata", http.StatusSeeOther)
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
		area, err := strconv.ParseFloat(defaultZero(row.AreaM2), 64)
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
		return masterdata.KindHeating, nil
	case "hot_water":
		return masterdata.KindHotWater, nil
	case "cold_water":
		return masterdata.KindColdWater, nil
	default:
		return 0, fmt.Errorf("unbekannte Verbrauchsart %q", s)
	}
}

func kindToString(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeating:
		return "heating"
	case masterdata.KindHotWater:
		return "hot_water"
	case masterdata.KindColdWater:
		return "cold_water"
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
	ResetMonth   string `json:"reset_month"`
}

func meterRowFromMeter(m masterdata.Meter) meterRow {
	row := meterRow{
		Number:       m.Number,
		MeterPointID: m.MeterPointID,
		InstalledAt:  formatGridDay(m.InstalledAt),
		StartReading: strconv.FormatInt(m.StartReading, 10),
	}
	if m.AESKey != ([16]byte{}) {
		row.AESKey = hex.EncodeToString(m.AESKey[:])
	}
	if m.RemovedAt != nil {
		row.RemovedAt = formatGridDay(*m.RemovedAt)
	}
	if m.EndReading != nil {
		row.EndReading = strconv.FormatInt(*m.EndReading, 10)
	}
	if m.KCFactor != 0 {
		row.KCFactor = strconv.FormatFloat(m.KCFactor, 'f', -1, 64)
	}
	if m.ResetMonth != 0 {
		row.ResetMonth = strconv.Itoa(m.ResetMonth)
	}
	return row
}

// meterGridRow is one row of the combined Zählerplätze+Zähler bulk-edit
// grid: a meter point's fields together with one meter at that point.
// Multiple rows may repeat the same MeterPointID — that point's meter
// history, one row per meter, exactly mirroring how the grid already
// showed one row per meter before meter points had their own fields too.
//
// RemovedAt has no visible grid column (see docs/architektur.md/UI-05 — the
// operator never types an Ausbaudatum): grid.js fills it in automatically,
// as the replacement meter's InstalledAt, only once a "Zählerwechsel" is
// completed with a new meter's data actually entered. It is still part of
// the JSON payload so an already-closed historical row keeps its removal
// date across an unrelated save.
type meterGridRow struct {
	MeterPointID string `json:"meter_point_id"`
	UnitID       string `json:"unit_id"`
	Room         string `json:"room"`
	Kind         string `json:"kind"`
	Number       string `json:"number"`
	AESKey       string `json:"aes_key"`
	InstalledAt  string `json:"installed_at"`
	StartReading string `json:"start_reading"`
	RemovedAt    string `json:"removed_at"`
	EndReading   string `json:"end_reading"`
	KCFactor     string `json:"kc_factor"`
	ResetMonth   string `json:"reset_month"`
}

func meterGridRowsFrom(points []masterdata.MeterPoint, meters []masterdata.Meter) []meterGridRow {
	pointByID := make(map[string]masterdata.MeterPoint, len(points))
	for _, mp := range points {
		pointByID[mp.ID] = mp
	}
	rows := make([]meterGridRow, 0, len(meters))
	for _, m := range meters {
		mp := pointByID[m.MeterPointID]
		row := meterGridRow{
			MeterPointID: m.MeterPointID,
			UnitID:       mp.UnitID,
			Room:         mp.Room,
			Kind:         kindToString(mp.Kind),
			Number:       m.Number,
			InstalledAt:  formatGridDay(m.InstalledAt),
			StartReading: strconv.FormatInt(m.StartReading, 10),
		}
		if m.AESKey != ([16]byte{}) {
			row.AESKey = hex.EncodeToString(m.AESKey[:])
		}
		if m.RemovedAt != nil {
			row.RemovedAt = formatGridDay(*m.RemovedAt)
		}
		if m.EndReading != nil {
			row.EndReading = strconv.FormatInt(*m.EndReading, 10)
		}
		if m.KCFactor != 0 {
			row.KCFactor = strconv.FormatFloat(m.KCFactor, 'f', -1, 64)
		}
		if m.ResetMonth != 0 {
			row.ResetMonth = strconv.Itoa(m.ResetMonth)
		}
		rows = append(rows, row)
	}
	return rows
}

// handleMasterDataSave replaces units and the combined meter
// points/meters together in one request. They cannot be saved
// independently: Validate checks them as one cross-referenced graph (a
// meter point needs at least one meter; a meter needs an existing meter
// point), so saving them through separate requests made it structurally
// impossible to add a brand-new meter point together with its first meter
// — whichever grid was submitted first was always validated against the
// other's not-yet-saved, still-empty counterpart, and rejected wholesale
// (STAMM-05: reject entirely, not partially).
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

	var unitRows []unitRow
	var meterGridRows []meterGridRow
	if err := json.Unmarshal([]byte(r.PostFormValue("units_json")), &unitRows); err != nil {
		a.renderMasterData(w, sess, md, "Ungültige Eingabedaten im Wohnungen-Raster.")
		return
	}
	if err := json.Unmarshal([]byte(r.PostFormValue("meters_json")), &meterGridRows); err != nil {
		a.renderMasterData(w, sess, md, "Ungültige Eingabedaten im Zählerplätze/Zähler-Raster.")
		return
	}

	fail := func(errMsg string) {
		a.renderMasterDataRows(w, sess, md, unitRows, meterGridRows, errMsg)
	}

	units, parseErr := parseUnitRows(unitRows)
	if parseErr != "" {
		fail(parseErr)
		return
	}
	points, meters, parseErr := parseMeterGridRows(meterGridRows)
	if parseErr != "" {
		fail(parseErr)
		return
	}

	md.Units, md.MeterPoints, md.Meters = units, points, meters
	if d := masterdata.Validate(md); !d.OK() {
		fail("Prüfung fehlgeschlagen: " + joinErrors(d.Errors))
		return
	}
	if err := a.Vault.Save(a.MasterDataPath, md); err != nil {
		fail("Speichern fehlgeschlagen: " + err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), fmt.Sprintf("%d units, %d meter points, %d meters saved", len(units), len(points), len(meters)))
	http.Redirect(w, r, "/operator/masterdata", http.StatusSeeOther)
}

// parseMeterGridRows converts the combined grid's rows into meter points
// and meters, naming the offending row (by meter point ID or meter number)
// on the first problem found. A meter point's fields must be identical on
// every row that repeats its ID (its history rows) — a genuine edit to
// them is made by changing every row for that point, which grid.js keeps
// in sync automatically since only the meter-specific fields ever differ
// between history rows for the same point.
func parseMeterGridRows(rows []meterGridRow) ([]masterdata.MeterPoint, []masterdata.Meter, string) {
	var points []masterdata.MeterPoint
	seen := make(map[string]masterdata.MeterPoint, len(rows))
	meters := make([]masterdata.Meter, 0, len(rows))

	for i, row := range rows {
		if row.MeterPointID == "" && row.UnitID == "" && row.Room == "" && row.Number == "" {
			continue // a blank trailing row from the grid, not a real entry
		}
		label := row.MeterPointID
		if label == "" {
			label = fmt.Sprintf("Zeile %d", i+1)
		}
		if row.MeterPointID == "" {
			return nil, nil, fmt.Sprintf("Zeile %d: Zählerplatz-ID fehlt.", i+1)
		}
		kind, err := parseKind(row.Kind)
		if err != nil {
			return nil, nil, fmt.Sprintf("Zählerplatz %s: %v", label, err)
		}
		mp := masterdata.MeterPoint{ID: row.MeterPointID, UnitID: row.UnitID, Room: row.Room, Kind: kind}
		if existing, ok := seen[mp.ID]; ok {
			if existing != mp {
				return nil, nil, fmt.Sprintf("Zählerplatz %s: Wohnung/Raum/Typ stimmen nicht mit einer anderen Zeile für denselben Zählerplatz überein.", label)
			}
		} else {
			seen[mp.ID] = mp
			points = append(points, mp)
		}

		// A row that establishes the meter point but names no meter is
		// kept as a meter point on its own. Laying out an installation's
		// rooms before the devices are mounted (and leaving a point empty
		// between a removal and its replacement) is a normal working
		// state; Validate reports it as a warning, and every evaluating
		// path treats such a point as simply having no readings.
		if row.Number == "" {
			continue
		}

		// AES key empty is a normal, valid value (an unencrypted meter):
		// crypto.Decrypt never even looks at the key once it has decided,
		// from the telegram's own CI byte, that there is nothing to
		// decrypt — a zero-value key is simply never used in that case.
		// When it is used (a meter that does need decrypting but has no
		// key on file yet), it fails the same way any wrong key would:
		// gracefully, as "not evaluable", never a crash.
		var aesKey [16]byte
		if row.AESKey != "" {
			key, err := hex.DecodeString(row.AESKey)
			if err != nil || len(key) != 16 {
				return nil, nil, fmt.Sprintf("Zähler %s: AES-Schlüssel muss leer oder genau 32 Hex-Zeichen sein.", row.Number)
			}
			copy(aesKey[:], key)
		}
		installedAt, err := parseGridDay(row.InstalledAt)
		if err != nil {
			return nil, nil, fmt.Sprintf("Zähler %s: ungültiges Einbaudatum (TT.MM.JJJJ erwartet).", row.Number)
		}
		startReading, err := strconv.ParseInt(defaultZero(row.StartReading), 10, 64)
		if err != nil {
			return nil, nil, fmt.Sprintf("Zähler %s: ungültiger Anfangsstand.", row.Number)
		}

		m := masterdata.Meter{
			Number:       row.Number,
			MeterPointID: row.MeterPointID,
			AESKey:       aesKey,
			InstalledAt:  installedAt,
			StartReading: startReading,
		}

		if row.RemovedAt != "" {
			removedAt, err := parseGridDay(row.RemovedAt)
			if err != nil {
				return nil, nil, fmt.Sprintf("Zähler %s: ungültiges Ausbaudatum.", row.Number)
			}
			m.RemovedAt = &removedAt
		}
		if row.EndReading != "" {
			endReading, err := strconv.ParseInt(row.EndReading, 10, 64)
			if err != nil {
				return nil, nil, fmt.Sprintf("Zähler %s: ungültiger Endstand.", row.Number)
			}
			m.EndReading = &endReading
		}
		if row.KCFactor != "" {
			kc, err := strconv.ParseFloat(row.KCFactor, 64)
			if err != nil {
				return nil, nil, fmt.Sprintf("Zähler %s: ungültiger kc-Faktor.", row.Number)
			}
			m.KCFactor = kc
		}
		if row.ResetMonth != "" {
			rm, err := strconv.Atoi(row.ResetMonth)
			if err != nil || rm < 1 || rm > 12 {
				return nil, nil, fmt.Sprintf("Zähler %s: ungültiger Stichtag-Monat (1-12 erwartet).", row.Number)
			}
			m.ResetMonth = rm
		}

		meters = append(meters, m)
	}
	return points, meters, ""
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
