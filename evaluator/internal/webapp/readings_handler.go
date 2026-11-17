package webapp

import (
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/billing"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// readingsDefaultRangeDays is the "Zählerstände" page's default lookback
// when no explicit Von/Bis is given — a full multi-year archive should
// never be decrypted and rendered unfiltered by default.
const readingsDefaultRangeDays = 180

// readingRow is one archived telegram, resolved against current master
// data and decrypted where a matching meter and key are on file. Meter
// point/unit stay blank for a telegram from a meter nothing has been
// assigned to yet (STAMM-06 onboarding is discovered here, not hidden).
// It is sent to the browser as JSON: the meter list, its search and type
// filter, and the selected meter's charts and telegram table all happen
// client-side (web/static/readings.js) so they react instantly, without a
// round trip per click — only Von/Bis (which change what is even fetched
// from the archive) reload the page.
//
// The page shows only what is in this row — the decoded current value and
// the reception data — never the telegram's other records: reading those
// would need per-manufacturer interpretation, and the wmbusmeters link
// (DecodeURL) already covers a look at the full content.
type readingRow struct {
	MeterID    string `json:"meter_id"`
	Day        string `json:"day"`
	ReceivedAt string `json:"received_at"`
	RSSI       int    `json:"rssi"`

	UnitID       string `json:"unit_id"`
	UnitName     string `json:"unit_name"`
	MeterPointID string `json:"meter_point_id"`
	Room         string `json:"room"`

	// DeviceType and Manufacturer are read straight from the telegram's
	// cleartext header (EN 13757-3 medium byte, M-field), independent of
	// master data or decryption — available even for a telegram nothing
	// has decrypted or been assigned to yet.
	DeviceType     string `json:"device_type"`      // short code, e.g. "HKV" — "" if unrecognized
	DeviceTypeName string `json:"device_type_name"` // German term, for a tooltip

	// Manufacturer is likewise read straight from the cleartext header (the
	// M-field, bytes 2-3), independent of master data or decryption.
	Manufacturer     string `json:"manufacturer"`      // three-letter code, e.g. "TCH"
	ManufacturerName string `json:"manufacturer_name"` // registered full name, for a tooltip — "" if not in the registry

	// FreezeMonth is the master-data Stichtag month of the meter this
	// telegram belongs to (masterdata.Meter.FreezeMonth): 0 for a meter that
	// counts straight through or one with no master data. readings.js needs
	// it because a reset is not always visible in the values themselves — a
	// meter can count past its pre-reset level again before the first
	// telegram after the Stichtag is archived.
	FreezeMonth int `json:"freeze_month"`

	Evaluable bool   `json:"evaluable"`
	Value     int64  `json:"value"`
	ValueUnit string `json:"value_unit"`

	DecodeURL string `json:"decode_url"` // wmbusmeters.org cross-check link (SZ-10's documented exception)
}

// meterSummary is one distinct meter's current master-data context — one
// entry of the client-side meter list, one per meter rather than per
// telegram.
type meterSummary struct {
	MeterID          string `json:"meter_id"`
	UnitID           string `json:"unit_id"`
	UnitName         string `json:"unit_name"`
	MeterPointID     string `json:"meter_point_id"`
	Room             string `json:"room"`
	DeviceType       string `json:"device_type"`
	DeviceTypeName   string `json:"device_type_name"`
	Manufacturer     string `json:"manufacturer"`
	ManufacturerName string `json:"manufacturer_name"`
	FreezeMonth      int    `json:"freeze_month"`
}

// readingsFilter only carries what genuinely has to be resolved
// server-side: the date range (it decides what gets fetched and decrypted
// at all) and, for the CSV export, an optional explicit meter allow-list —
// the meter list's search and type filter are client-side-only state in
// readings.js, with no server-side equivalent to keep in sync.
type readingsFilter struct {
	From     telegram.Day
	To       telegram.Day
	MeterIDs []string // nil/empty means "every meter in range" (no restriction)
}

// QueryString rebuilds the filter as a URL query string for the CSV
// export link. readings.js builds the same shape of query itself, passing
// the exact set of meters currently in its list (after search and type
// filter) — or none at all if nothing is restricting the list, so the
// export matches what's on screen without ever sending every meter ID for
// the common unfiltered case. Which meter is selected for the detail view
// does not narrow the export.
func (f readingsFilter) QueryString() string {
	v := url.Values{}
	for _, id := range f.MeterIDs {
		v.Add("meter", id)
	}
	v.Set("from", string(f.From))
	v.Set("to", string(f.To))
	return v.Encode()
}

// readingsPreset is one quick-pick range button above the meter list.
type readingsPreset struct {
	Label    string
	From, To telegram.Day
	Active   bool // the page currently shows exactly this range
}

// readingsPresets are the quick picks: the last 30/90/365 days up to
// today, the previous calendar year — which puts Bis on 31.12., the usual
// Stichtag for the key-day export — and, if the archive holds anything,
// its whole extent.
func readingsPresets(today telegram.Day, stats archive.Stats, current readingsFilter) []readingsPreset {
	year, _ := strconv.Atoi(string(today)[:4])
	year--
	presets := []readingsPreset{
		{Label: "30 Tage", From: today.AddDays(-29), To: today},
		{Label: "90 Tage", From: today.AddDays(-89), To: today},
		{Label: "1 Jahr", From: today.AddDays(-364), To: today},
		{Label: fmt.Sprintf("Jahr %d", year), From: telegram.Day(fmt.Sprintf("%04d-01-01", year)), To: telegram.Day(fmt.Sprintf("%04d-12-31", year))},
	}
	if stats.EarliestDay != "" {
		presets = append(presets, readingsPreset{Label: "Alles", From: stats.EarliestDay, To: stats.LatestDay})
	}
	for i := range presets {
		presets[i].Active = presets[i].From == current.From && presets[i].To == current.To
	}
	return presets
}

type readingsPageData struct {
	Base
	Filter           readingsFilter
	Presets          []readingsPreset
	KeyDayLabel      string // Bis as dd.mm.yyyy: the key-day export takes its Stichtag from Bis
	LookbackDays     int
	RowsJSON         template.JS
	MeterSummary     []meterSummary
	MeterSummaryJSON template.JS
	UnitOrderJSON    template.JS // unit IDs in master-data order, for grouping the meter list
	Error            string
}

// handleReadings is the archive's "Zählerstände" view: every archived
// telegram in a time range, decrypted where a meter and key are known —
// the evaluator-side counterpart to the Archiv page's still-encrypted
// download, and (since an unrecognized meter still appears, in its own
// group) the way an operator discovers a meter that hasn't been onboarded
// into the Stammdaten yet. Only Von/Bis are handled server-side (they
// decide what gets fetched and decrypted at all); the meter list and the
// selected meter's value and RSSI charts all happen client-side against
// the full in-range result, so they react immediately — see
// web/static/readings.js.
func (a *App) handleReadings(w http.ResponseWriter, r *http.Request) {
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

	unitOrder := make([]string, 0, len(md.Units))
	for _, u := range md.Units {
		unitOrder = append(unitOrder, u.ID)
	}
	unitOrderJSON, _ := json.Marshal(unitOrder)

	today := a.today()
	stats, err := a.Store.Stats(today)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	filter, errMsg := parseReadingsFilter(r, today)
	presets := readingsPresets(today, stats, filter)
	if errMsg != "" {
		a.render(w, "readings.html", readingsPageData{
			Base: a.base("Zählerstände", sess), Filter: filter, Error: errMsg,
			Presets: presets, KeyDayLabel: formatDay(filter.To), LookbackDays: a.lookbackDays(),
			// valid empty JSON, not the zero value "" — these are embedded directly into a <script> block
			RowsJSON: "[]", MeterSummaryJSON: "[]", UnitOrderJSON: template.JS(unitOrderJSON),
		})
		return
	}

	rows, err := a.buildReadingRows(md, readingsFilter{From: filter.From, To: filter.To})
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	summaries := summarizeMeters(rows)

	rowsJSON, _ := json.Marshal(rows)
	summaryJSON, _ := json.Marshal(summaries)

	a.render(w, "readings.html", readingsPageData{
		Base:             a.base("Zählerstände", sess),
		Filter:           filter,
		Presets:          presets,
		KeyDayLabel:      formatDay(filter.To),
		LookbackDays:     a.lookbackDays(),
		RowsJSON:         template.JS(rowsJSON),
		MeterSummary:     summaries,
		MeterSummaryJSON: template.JS(summaryJSON),
		UnitOrderJSON:    template.JS(unitOrderJSON),
	})
}

// keyDayRow is one line of the key-day export: one meter that was
// installed on the Stichtag, with the reading FACH-01's lookup found for
// it — or none.
type keyDayRow struct {
	UnitName, MeterPointID, Room, Kind, MeterID string
	Found                                       bool
	Value, Unit                                 string
	ReadingDay                                  telegram.Day
	Note                                        string
}

// buildKeyDayRows lists every meter active on day, in master-data order
// (unit, then meter point), each with its reading on day as FACH-01 finds
// it: the telegram of that exact day, or — if none was archived or it is
// not evaluable — the closest earlier one within the lookback window. A
// meter with no reading in that window still gets its row, with an empty
// value and a note, so a gap is visible in the file instead of the meter
// silently missing from it. On a swap day both the outgoing and the
// incoming meter are active and both appear.
func (a *App) buildKeyDayRows(md masterdata.MasterData, day telegram.Day) ([]keyDayRow, error) {
	unitIndex := make(map[string]int, len(md.Units))
	unitNameByID := make(map[string]string, len(md.Units))
	for i, u := range md.Units {
		unitIndex[u.ID] = i
		unitNameByID[u.ID] = u.Name
	}
	pointIndex := make(map[string]int, len(md.MeterPoints))
	pointByID := make(map[string]masterdata.MeterPoint, len(md.MeterPoints))
	for i, mp := range md.MeterPoints {
		pointIndex[mp.ID] = i
		pointByID[mp.ID] = mp
	}
	// A house meter (no unit) sorts after every unit.
	unitRank := func(unitID string) int {
		if i, ok := unitIndex[unitID]; ok {
			return i
		}
		return len(md.Units)
	}

	var meters []masterdata.Meter
	for _, m := range md.Meters {
		if m.Active(day) {
			meters = append(meters, m)
		}
	}
	sort.SliceStable(meters, func(i, j int) bool {
		pi, pj := pointByID[meters[i].MeterPointID], pointByID[meters[j].MeterPointID]
		if ri, rj := unitRank(pi.UnitID), unitRank(pj.UnitID); ri != rj {
			return ri < rj
		}
		return pointIndex[pi.ID] < pointIndex[pj.ID]
	})

	lookback := a.lookbackDays()
	rows := make([]keyDayRow, 0, len(meters))
	for _, m := range meters {
		mp := pointByID[m.MeterPointID]
		row := keyDayRow{
			UnitName: unitLabel(unitNameByID, mp.UnitID), MeterPointID: mp.ID, Room: mp.Room,
			Kind: kindLabel(mp.Kind), MeterID: m.Number, Unit: kindValueUnit(mp.Kind),
		}
		reading, found, err := billing.FindReading(a.Store, m.Number, m.AESKey, day, lookback)
		if err != nil {
			return nil, err
		}
		switch {
		case !found:
			row.Note = fmt.Sprintf("kein auswertbarer Wert in den %d Tagen bis zum Stichtag", lookback)
		default:
			row.Found = true
			row.Value = formatReadingValue(mp.Kind, reading.Value)
			row.ReadingDay = reading.Day
			if reading.Day != day {
				row.Note = fmt.Sprintf("Wert vom %s, %d Tag(e) vor dem Stichtag", formatDay(reading.Day), reading.Day.DaysUntil(day))
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// handleReadingsKeyDay streams buildKeyDayRows as CSV: the readings of all
// meters on one Stichtag, for handing over to a billing program. Unlike the
// plain export below it is per meter, not per telegram, and values follow
// the display convention (water in m³ with a decimal comma), the same one
// the Stammdaten import reads.
func (a *App) handleReadingsKeyDay(w http.ResponseWriter, r *http.Request) {
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
	day, err := telegram.ParseDay(r.URL.Query().Get("day"))
	if err != nil {
		http.Error(w, "Ungültiger Stichtag.", http.StatusBadRequest)
		return
	}
	rows, err := a.buildKeyDayRows(md, day)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=zaehlerstaende-stichtag-%s.csv", day))
	// UTF-8 BOM, as in the Stammdaten CSV export: without it Excel reads the
	// file as ANSI and garbles every umlaut.
	io.WriteString(w, "\uFEFF")
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"Wohnung", "Zählerplatz", "Raum", "Art", "Zähler", "Stichtag", "Zählerstand", "Einheit", "Telegramm vom", "Hinweis"})
	for _, row := range rows {
		readingDay := ""
		if row.Found {
			readingDay = formatDay(row.ReadingDay)
		}
		cw.Write([]string{
			row.UnitName, row.MeterPointID, row.Room, row.Kind, row.MeterID, formatDay(day),
			row.Value, row.Unit, readingDay, row.Note,
		})
	}
	cw.Flush()

	a.audit(access.EventDataIngested, sess.AuditActor(), fmt.Sprintf("key-day export %d meters, %s", len(rows), day))
}

// handleReadingsExport streams the same filtered, decrypted rows as CSV —
// the generalized, filterable counterpart to the Stammdaten page's
// single-Stichtag "Zählerstände exportieren". Unlike the page itself,
// this is a plain download navigation, so it applies every filter
// server-side from the query string readings.js keeps up to date.
func (a *App) handleReadingsExport(w http.ResponseWriter, r *http.Request) {
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

	filter, errMsg := parseReadingsFilter(r, a.today())
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}
	rows, err := a.buildReadingRows(md, filter)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=zaehlerstaende-%s-bis-%s.csv", filter.From, filter.To))
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"Wohnung", "Zählerplatz", "Raum", "Typ", "Hersteller", "Zähler", "Tag", "Empfangen", "RSSI", "Wert", "Einheit"})
	for _, row := range rows {
		value, unit := "", ""
		if row.Evaluable {
			value, unit = fmt.Sprintf("%d", row.Value), row.ValueUnit
		}
		cw.Write([]string{
			row.UnitName, row.MeterPointID, row.Room, row.DeviceType, row.Manufacturer, row.MeterID,
			row.Day, row.ReceivedAt, fmt.Sprintf("%d", row.RSSI), value, unit,
		})
	}
	cw.Flush()

	a.audit(access.EventDataIngested, sess.AuditActor(), fmt.Sprintf("readings export %d rows, %s to %s", len(rows), filter.From, filter.To))
}

func parseReadingsFilter(r *http.Request, today telegram.Day) (readingsFilter, string) {
	q := r.URL.Query()
	f := readingsFilter{
		MeterIDs: q["meter"],
		To:       today,
	}
	if s := q.Get("to"); s != "" {
		d, err := telegram.ParseDay(s)
		if err != nil {
			return f, "Ungültiges Bis-Datum."
		}
		f.To = d
	}
	f.From = f.To.AddDays(-readingsDefaultRangeDays)
	if s := q.Get("from"); s != "" {
		d, err := telegram.ParseDay(s)
		if err != nil {
			return f, "Ungültiges Von-Datum."
		}
		f.From = d
	}
	return f, ""
}

// buildReadingRows resolves every archive entry in f's date range against
// md and, if f carries an explicit MeterIDs allow-list, restricts the
// result to those meters — used as-is by the CSV export, where it mirrors
// exactly the meters readings.js currently has visible. The main page
// instead calls this with only From/To set, so the browser gets the full
// in-range result and does its own column filtering/row selection on top
// of it (see handleReadings).
func (a *App) buildReadingRows(md masterdata.MasterData, f readingsFilter) ([]readingRow, error) {
	entries, err := a.Store.Range(f.From, f.To)
	if err != nil {
		return nil, err
	}

	var meterIDFilter map[string]bool
	if len(f.MeterIDs) > 0 {
		meterIDFilter = make(map[string]bool, len(f.MeterIDs))
		for _, id := range f.MeterIDs {
			meterIDFilter[id] = true
		}
	}

	meterPointByID := make(map[string]masterdata.MeterPoint, len(md.MeterPoints))
	for _, mp := range md.MeterPoints {
		meterPointByID[mp.ID] = mp
	}
	unitNameByID := make(map[string]string, len(md.Units))
	for _, u := range md.Units {
		unitNameByID[u.ID] = u.Name
	}

	rows := make([]readingRow, 0, len(entries))
	for _, e := range entries {
		if meterIDFilter != nil && !meterIDFilter[e.MeterID] {
			continue
		}

		row := readingRow{MeterID: e.MeterID, Day: string(e.Day), ReceivedAt: e.ReceivedAt.Format("2006-01-02 15:04:05"), RSSI: e.RSSI}
		// The medium and manufacturer fields sit in the telegram's
		// cleartext header (offsets 9 and 2-3), ahead of any encryption,
		// so this works even for a telegram this instance cannot decrypt
		// or has no master-data match for.
		raw, rawErr := hex.DecodeString(e.RawHex)
		haveHeader := rawErr == nil && len(raw) > 9
		var key [16]byte
		if meter, found := md.MeterByNumber(e.MeterID, e.Day); found {
			row.MeterPointID = meter.MeterPointID
			row.FreezeMonth = meter.FreezeMonth
			key = meter.AESKey
			if mp, ok := meterPointByID[meter.MeterPointID]; ok {
				row.Room = mp.Room
				row.UnitID = mp.UnitID
				row.UnitName = unitLabel(unitNameByID, mp.UnitID)
			}
		}
		// Identified only once the key is known: deviceIdentity looks past
		// the link layer into the transport header and, where it can, the
		// application layer — and for an encrypted telegram that needs
		// the key.
		if haveHeader {
			manufacturer, medium := deviceIdentity(raw, key)
			row.Manufacturer = manufacturer
			row.ManufacturerName, _ = telegram.ManufacturerName(row.Manufacturer)
			if dt, ok := telegram.IdentifyDeviceType(medium); ok {
				row.DeviceType = dt.Abbr
				row.DeviceTypeName = dt.Name
			}
		}

		if reading, found, err := billing.ReadValue(e, key); err == nil && found {
			row.Evaluable = true
			row.Value = reading.Value
			row.ValueUnit = reading.Unit.String()
		}
		row.DecodeURL = wmbusmetersURL(e.RawHex, key)

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Day != rows[j].Day {
			return rows[i].Day < rows[j].Day
		}
		return rows[i].MeterID < rows[j].MeterID
	})
	return rows, nil
}

// wmbusmetersURL builds a direct link to wmbusmeters.org's own decoder,
// pre-filled with this telegram (and its key, if one is on file). This is
// SZ-10's one documented exception to "no outgoing connections to third
// parties": a deliberate, operator-triggered cross-check of this
// system's own decoding against an external reference decoder. Only ever
// reached by an explicit operator click, never loaded automatically.
func wmbusmetersURL(rawHex string, key [16]byte) string {
	if key == ([16]byte{}) {
		return "https://wmbusmeters.org/analyze/" + rawHex
	}
	return "https://wmbusmeters.org/analyze/" + rawHex + ":auto:" + hex.EncodeToString(key[:])
}

// summarizeMeters collects one row per distinct meter number, carrying
// whatever master-data context its most recent telegram resolved to — the
// client-side meter list's data source.
func summarizeMeters(rows []readingRow) []meterSummary {
	byMeter := make(map[string]meterSummary)
	var order []string
	for _, row := range rows {
		if _, seen := byMeter[row.MeterID]; !seen {
			order = append(order, row.MeterID)
		}
		byMeter[row.MeterID] = meterSummary{
			MeterID: row.MeterID, UnitID: row.UnitID, UnitName: row.UnitName,
			MeterPointID: row.MeterPointID, Room: row.Room,
			DeviceType: row.DeviceType, DeviceTypeName: row.DeviceTypeName,
			Manufacturer: row.Manufacturer, ManufacturerName: row.ManufacturerName,
			FreezeMonth: row.FreezeMonth,
		}
	}
	sort.Strings(order)
	out := make([]meterSummary, 0, len(order))
	for _, m := range order {
		out = append(out, byMeter[m])
	}
	return out
}
