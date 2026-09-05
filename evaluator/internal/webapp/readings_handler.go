package webapp

import (
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"

	"selbst-ableser/internal/access"
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
// It is sent to the browser as JSON: the "Zähler" table's Excel-style
// column filters, row selection, and the chart all happen client-side
// (web/static/readings.js) so they react instantly, without a round trip
// per click — only Von/Bis (which change what is even fetched from the
// archive) reload the page.
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

	Evaluable bool   `json:"evaluable"`
	Value     int64  `json:"value"`
	ValueUnit string `json:"value_unit"`

	DecodeURL string `json:"decode_url"` // wmbusmeters.org cross-check link (SZ-10's documented exception)
}

// meterSummary is one distinct meter's current master-data context — the
// row shape of the client-side "selection table" a click on toggles into
// the chart, one row per meter rather than per telegram.
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
}

// readingsFilter now only carries what genuinely has to be resolved
// server-side: the date range (it decides what gets fetched and decrypted
// at all) and, for the CSV export, an optional explicit meter allow-list —
// everything else (Wohnung/Zählerplatz/Zählernummer/Typ/Hersteller column
// filters, row selection) is client-side-only state in readings.js, with
// no server-side equivalent to keep in sync.
type readingsFilter struct {
	From     telegram.Day
	To       telegram.Day
	MeterIDs []string // nil/empty means "every meter in range" (no restriction)
}

// QueryString rebuilds the filter as a URL query string for the CSV
// export link. readings.js calls this indirectly by building the same
// shape of query itself, passing the exact set of meters currently visible
// (after its column filters and row selection) — or none at all if nothing
// is currently restricting the view, so the export matches what's on
// screen without ever sending every meter ID for the common unfiltered case.
func (f readingsFilter) QueryString() string {
	v := url.Values{}
	for _, id := range f.MeterIDs {
		v.Add("meter", id)
	}
	v.Set("from", string(f.From))
	v.Set("to", string(f.To))
	return v.Encode()
}

type readingsPageData struct {
	Base
	Filter           readingsFilter
	RowCount         int
	RowsJSON         template.JS
	MeterSummary     []meterSummary
	MeterSummaryJSON template.JS
	Error            string
}

// handleReadings is the archive's "Zählerstände" view: every archived
// telegram in a time range, decrypted where a meter and key are known —
// the evaluator-side counterpart to the Archiv page's still-encrypted
// download, and (since an unrecognized meter still appears, just with a
// blank Wohnung/Zählerplatz) the way an operator discovers a meter that
// hasn't been onboarded into the Stammdaten yet. Only Von/Bis are handled
// server-side (they decide what gets fetched and decrypted at all); the
// "Zähler" table's Excel-style column filters, row selection, and the
// raw-value chart all happen client-side against the full in-range
// result, so they react immediately — see web/static/readings.js.
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

	filter, errMsg := parseReadingsFilter(r, a.today())
	if errMsg != "" {
		a.render(w, "readings.html", readingsPageData{
			Base: a.base("Zählerstände", sess), Filter: filter, Error: errMsg,
			RowsJSON: "[]", MeterSummaryJSON: "[]", // valid empty JSON, not the zero value "" — this is embedded directly into a <script> block
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
		RowCount:         len(rows),
		RowsJSON:         template.JS(rowsJSON),
		MeterSummary:     summaries,
		MeterSummaryJSON: template.JS(summaryJSON),
	})
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
		if raw, err := hex.DecodeString(e.RawHex); err == nil && len(raw) > 9 {
			if dt, ok := telegram.IdentifyDeviceType(raw[9]); ok {
				row.DeviceType = dt.Abbr
				row.DeviceTypeName = dt.Name
			}
			row.Manufacturer = telegram.Manufacturer(binary.LittleEndian.Uint16(raw[2:4]))
			row.ManufacturerName, _ = telegram.ManufacturerName(row.Manufacturer)
		}
		var key [16]byte
		if meter, found := md.MeterByNumber(e.MeterID, e.Day); found {
			row.MeterPointID = meter.MeterPointID
			key = meter.AESKey
			if mp, ok := meterPointByID[meter.MeterPointID]; ok {
				row.Room = mp.Room
				row.UnitID = mp.UnitID
				row.UnitName = unitNameByID[mp.UnitID]
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
// client-side selection table's data source.
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
		}
	}
	sort.Strings(order)
	out := make([]meterSummary, 0, len(order))
	for _, m := range order {
		out = append(out, byMeter[m])
	}
	return out
}
