package webapp

import (
	"fmt"
	"strconv"
	"strings"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// This file is the machinery behind the Zählerplätze page: one view of
// "which device sits where, and which ones sat there before", plus the two
// bulk operations an operator actually performs on a real installation.
//
// Why a second page next to Stammdaten at all: the master-data grid is one
// flat table of meter *rows*, so a meter point's history is spread over
// several lines and a swap of forty heat-cost allocators is forty manual
// edits. Both are fine for correcting a detail and wrong for the job that
// actually occurs — a fitter hands over a mounting protocol as a list, and
// the key supplier mails a table. So the input here is that list, pasted
// as it arrives, and the page's job is to say what it would change before
// anything is written.
//
// Everything below is deliberately free of HTTP and of the vault: it takes
// master data and text, and returns a plan. That makes the interesting part
// — matching pasted lines to meter points, and refusing the lines that
// don't add up — testable without a browser.

// ---------- Eingefügte Tabellen ----------

// parsePastedTable turns pasted text into rows of trimmed cells. It
// accepts what actually arrives: a block copied out of a spreadsheet (tab
// separated), a German CSV export (semicolon), or a comma-separated list.
//
// The separator is chosen for the whole block rather than per line, and
// tabs and semicolons win over commas — a comma is a decimal separator in
// this country far more often than it is a column separator, and guessing
// per line would split one row differently from the next.
func parsePastedTable(text string) [][]string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	sep := ""
	switch {
	case strings.Contains(text, "\t"):
		sep = "\t"
	case strings.Contains(text, ";"):
		sep = ";"
	case strings.Contains(text, ","):
		sep = ","
	}

	var table [][]string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var cells []string
		if sep == "" {
			cells = strings.Fields(line) // whitespace-separated, e.g. pasted from a mail body
		} else {
			cells = strings.Split(line, sep)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		table = append(table, cells)
	}
	return table
}

// looksLikeHeader reports whether the first pasted row names its columns
// rather than carrying data. A header is optional: a protocol pasted
// without one is read by position instead (see swapColumns).
func looksLikeHeader(row []string) bool {
	for _, cell := range row {
		lower := strings.ToLower(cell)
		for _, word := range []string{"nummer", "zähler", "zaehler", "stand", "platz", "alt", "neu", "schlüssel", "schluessel", "key"} {
			if strings.Contains(lower, word) {
				return true
			}
		}
	}
	return false
}

// swapColumns says which pasted column holds what, counted from 1 so that
// the zero value means "this column is absent" rather than "column 0" — a
// forgotten field then reads nothing instead of silently reading the first
// column, which is a mistake that costs an afternoon to find.
type swapColumns struct {
	Point        int
	OldNumber    int
	EndReading   int
	NewNumber    int
	StartReading int

	// KCFactor and FreezeMonth are optional. Both belong to the *device*,
	// not to the place it sits at (see docs/04-fachlogik.md: the billing
	// code resolves them per meter and per day precisely so a replacement
	// can bring its own), so a swap to a different model may change them.
	// Left out, they are inherited from the outgoing meter, which is what
	// a protocol that does not mention them means.
	KCFactor    int
	FreezeMonth int
}

// defaultSwapColumns is the order a protocol without a header is read in.
// It is the order the fitter's list normally has, and it is printed above
// the paste box so nobody has to guess. The two optional columns have no
// place in it: without a header there is no way to tell a kc factor from
// a reading.
var defaultSwapColumns = swapColumns{OldNumber: 1, EndReading: 2, NewNumber: 3, StartReading: 4}

// detectSwapColumns maps a header row to column positions. Matching is
// deliberately loose — "Zählernummer alt", "alte Nr.", "ALT" all mean the
// same thing on a printed protocol — and anything it cannot place stays
// -1 rather than being guessed at.
func detectSwapColumns(header []string) swapColumns {
	var cols swapColumns
	for i, cell := range header {
		lower := strings.ToLower(cell)
		isOld := strings.Contains(lower, "alt") || strings.Contains(lower, "ausbau")
		isNew := strings.Contains(lower, "neu") || strings.Contains(lower, "einbau")
		isReading := strings.Contains(lower, "stand")
		switch {
		// The two optional columns come first: "Bewertungsfaktor" contains
		// no keyword of the others, but "Stichtag" would otherwise never be
		// reached if a later case matched it by accident.
		case strings.Contains(lower, "kc"), strings.Contains(lower, "faktor"), strings.Contains(lower, "bewertung"):
			cols.KCFactor = i + 1
		case strings.Contains(lower, "stichtag"), strings.Contains(lower, "reset"):
			cols.FreezeMonth = i + 1
		case strings.Contains(lower, "platz"):
			cols.Point = i + 1
		case isReading && isOld, strings.Contains(lower, "endstand"):
			cols.EndReading = i + 1
		case isReading && isNew, strings.Contains(lower, "anfangsstand"), strings.Contains(lower, "startstand"):
			cols.StartReading = i + 1
		case isOld:
			cols.OldNumber = i + 1
		case isNew:
			cols.NewNumber = i + 1
		}
	}
	return cols
}

// cell reads a 1-based column, or "" when the column is absent (0) or the
// line is shorter than the header promised.
func cell(row []string, idx int) string {
	if idx <= 0 || idx > len(row) {
		return ""
	}
	return row[idx-1]
}

// ---------- Zählerwechsel ----------

// swapPlan is what one pasted line would do. A plan with a Problem is
// shown but not applied; a Note or a Warning is worth reading and applied
// anyway — the difference between them is how loudly the page says so.
type swapPlan struct {
	Line int

	PointID    string
	PointLabel string          // "Wohnung 3 · WOH · Heizung", for the preview table
	Kind       masterdata.Kind // the meter point's kind, for formatting readings

	OldNumber  string
	EndReading int64

	NewNumber    string
	StartReading int64

	// KCFactor and FreezeMonth as the incoming device will get them, either
	// from the pasted line or inherited from the outgoing one — Inherited
	// says which, because a silently carried-over evaluation factor is
	// exactly the kind of thing that must not go unnoticed on a swap to a
	// different model.
	KCFactor    float64
	FreezeMonth int
	Inherited   bool

	Problem string
	Note    string
	Warning string
}

func (p swapPlan) OK() bool { return p.Problem == "" }

// DeviceValuesText renders the two device properties for the preview.
// Heating meters show both; a water meter has neither (it is never scaled
// and never resets), so it shows nothing rather than a meaningless 1.
func (p swapPlan) DeviceValuesText() string {
	if p.KCFactor == 0 && p.FreezeMonth == 0 {
		return "—"
	}
	parts := make([]string, 0, 2)
	if p.KCFactor != 0 {
		parts = append(parts, "kc "+strconv.FormatFloat(p.KCFactor, 'g', -1, 64))
	}
	if p.FreezeMonth != 0 {
		parts = append(parts, "Stichtag "+strconv.Itoa(p.FreezeMonth))
	}
	return strings.Join(parts, " · ")
}

// EndReadingText and StartReadingText keep the template free of formatting
// decisions for the two values a plan may not have.
func (p swapPlan) EndReadingText() string {
	if p.OldNumber == "" {
		return "—"
	}
	return formatReadingValue(p.Kind, p.EndReading)
}

func (p swapPlan) StartReadingText() string { return formatReadingValue(p.Kind, p.StartReading) }

// planSwaps matches every pasted line to a meter point and says what it
// would do, without changing anything.
//
// A line is matched by the *outgoing* meter's number, because that is the
// one identifier a mounting protocol always carries and the operator never
// has to look up. A line may instead name the meter point directly, which
// is how a first installation is entered: no outgoing meter exists yet.
//
// day is the swap date, entered once for the whole batch rather than per
// line: a fitter works through a building in an afternoon, and the model
// requires the outgoing meter's removal and the incoming one's
// installation to fall on the same day anyway (masterdata.Validate refuses
// both a gap and an overlap).
func planSwaps(md masterdata.MasterData, table [][]string, cols swapColumns, day telegram.Day) []swapPlan {
	pointByID := make(map[string]masterdata.MeterPoint, len(md.MeterPoints))
	for _, mp := range md.MeterPoints {
		pointByID[mp.ID] = mp
	}
	unitNames := make(map[string]string, len(md.Units))
	for _, u := range md.Units {
		unitNames[u.ID] = u.Name
	}

	// The device a number belongs to, and whether it is still installed.
	activeByNumber := make(map[string]masterdata.Meter, len(md.Meters))
	knownNumbers := make(map[string]bool, len(md.Meters))
	activeAtPoint := make(map[string]masterdata.Meter, len(md.MeterPoints))
	for _, m := range md.Meters {
		knownNumbers[m.Number] = true
		if m.RemovedAt == nil {
			activeByNumber[m.Number] = m
			activeAtPoint[m.MeterPointID] = m
		}
	}

	label := func(mp masterdata.MeterPoint) string {
		unit := unitNames[mp.UnitID]
		if unit == "" {
			unit = mp.UnitID
		}
		return fmt.Sprintf("%s · %s · %s", unit, mp.Room, kindLabel(mp.Kind))
	}

	seenOld := make(map[string]int, len(table))
	seenNew := make(map[string]int, len(table))
	plans := make([]swapPlan, 0, len(table))

	for i, row := range table {
		plan := swapPlan{
			Line:      i + 1,
			OldNumber: normalizeNumber(cell(row, cols.OldNumber)),
			NewNumber: normalizeNumber(cell(row, cols.NewNumber)),
			PointID:   strings.TrimSpace(cell(row, cols.Point)),
		}

		// Which meter point does this line concern?
		var mp masterdata.MeterPoint
		switch {
		case plan.OldNumber != "":
			outgoing, ok := activeByNumber[plan.OldNumber]
			if !ok {
				if knownNumbers[plan.OldNumber] {
					plan.Problem = fmt.Sprintf("Zähler %s ist bereits ausgebaut", plan.OldNumber)
				} else {
					plan.Problem = fmt.Sprintf("Zähler %s ist nicht bekannt", plan.OldNumber)
				}
				plans = append(plans, plan)
				continue
			}
			mp = pointByID[outgoing.MeterPointID]
			plan.PointID = mp.ID
		case plan.PointID != "":
			var ok bool
			mp, ok = pointByID[plan.PointID]
			if !ok {
				plan.Problem = fmt.Sprintf("Zählerplatz %s ist nicht bekannt", plan.PointID)
				plans = append(plans, plan)
				continue
			}
			if current, busy := activeAtPoint[mp.ID]; busy {
				plan.Problem = fmt.Sprintf("An Platz %s ist Zähler %s eingebaut — bitte dessen Nummer als alte Nummer angeben", mp.ID, current.Number)
				plans = append(plans, plan)
				continue
			}
		default:
			plan.Problem = "Weder alte Zählernummer noch Zählerplatz angegeben"
			plans = append(plans, plan)
			continue
		}
		plan.PointLabel = label(mp)
		plan.Kind = mp.Kind

		if prev, dup := seenOld[plan.OldNumber]; dup && plan.OldNumber != "" {
			plan.Problem = fmt.Sprintf("Zähler %s kommt schon in Zeile %d vor", plan.OldNumber, prev)
			plans = append(plans, plan)
			continue
		}

		// The incoming device.
		if plan.NewNumber == "" {
			plan.Problem = "Neue Zählernummer fehlt"
			plans = append(plans, plan)
			continue
		}
		if !isMeterNumber(plan.NewNumber) {
			plan.Problem = fmt.Sprintf("Neue Zählernummer %q ist nicht achtstellig", plan.NewNumber)
			plans = append(plans, plan)
			continue
		}
		if prev, dup := seenNew[plan.NewNumber]; dup {
			plan.Problem = fmt.Sprintf("Neue Nummer %s kommt schon in Zeile %d vor", plan.NewNumber, prev)
			plans = append(plans, plan)
			continue
		}
		if knownNumbers[plan.NewNumber] {
			if existing, active := activeByNumber[plan.NewNumber]; !active || existing.MeterPointID != mp.ID {
				plan.Problem = fmt.Sprintf("Zählernummer %s ist bereits vergeben", plan.NewNumber)
				plans = append(plans, plan)
				continue
			}
		}

		// The two readings.
		if plan.OldNumber != "" {
			outgoing := activeByNumber[plan.OldNumber]
			endText := strings.TrimSpace(cell(row, cols.EndReading))
			if endText == "" {
				plan.Problem = "Endstand fehlt"
				plans = append(plans, plan)
				continue
			}
			end, msg := parseReadingValue(mp.Kind, "Endstand", endText)
			if msg != "" {
				plan.Problem = msg
				plans = append(plans, plan)
				continue
			}
			plan.EndReading = end
			if end < outgoing.StartReading {
				plan.Note = fmt.Sprintf("Endstand %s liegt unter dem Anfangsstand %s dieses Zählers",
					formatReadingValue(mp.Kind, end), formatReadingValue(mp.Kind, outgoing.StartReading))
			}
			if day.Before(outgoing.InstalledAt) {
				plan.Problem = fmt.Sprintf("Wechseldatum liegt vor dem Einbau von Zähler %s (%s)", outgoing.Number, outgoing.InstalledAt)
				plans = append(plans, plan)
				continue
			}
		}

		// A fresh device usually starts at zero, and a protocol that
		// leaves the column empty means exactly that (parseReadingValue
		// returns 0 for an empty cell without complaint).
		startText := strings.TrimSpace(cell(row, cols.StartReading))
		start, msg := parseReadingValue(mp.Kind, "Anfangsstand", startText)
		if msg != "" {
			plan.Problem = msg
			plans = append(plans, plan)
			continue
		}
		if startText == "" && plan.Note == "" {
			plan.Note = "Anfangsstand nicht angegeben, wird als 0 übernommen"
		}
		plan.StartReading = start

		// The two device properties. Given in the line they win; left out
		// they carry over from the outgoing device, which is what a
		// protocol that does not mention them means.
		plan.Inherited = true
		if plan.OldNumber != "" {
			outgoing := activeByNumber[plan.OldNumber]
			plan.KCFactor = outgoing.KCFactor
			plan.FreezeMonth = outgoing.FreezeMonth
		}
		if s := strings.TrimSpace(cell(row, cols.KCFactor)); s != "" {
			kc, err := parseKCFactor(s)
			if err != nil {
				plan.Problem = "kc-Faktor: " + err.Error()
				plans = append(plans, plan)
				continue
			}
			plan.KCFactor = kc
			plan.Inherited = false
		}
		if s := strings.TrimSpace(cell(row, cols.FreezeMonth)); s != "" {
			month, err := parseFreezeMonth(s)
			if err != nil {
				plan.Problem = "Stichtag: " + err.Error()
				plans = append(plans, plan)
				continue
			}
			plan.FreezeMonth = month
			plan.Inherited = false
		}
		if mp.Kind == masterdata.KindHeatCostAllocator && plan.KCFactor == 0 && plan.OldNumber == "" && plan.Note == "" {
			plan.Note = "Kein kc-Faktor angegeben — gerechnet wird mit 1"
		}

		// A swap in the first month of a billing period is computable since
		// the period's closing value comes from the device itself (see
		// billing/billing_reset.go) — but only while both devices share
		// that period. Differing Stichtag months are still refused by the
		// billing code, and this is the only place the operator can be told
		// before a month-end fails.
		//
		// The month a new period *begins* in is the one after the Stichtag
		// month, which is what the billing compares against (see
		// CalculateSeries).
		incoming := effectiveFreezeMonth(plan.FreezeMonth)
		if mp.Kind == masterdata.KindHeatCostAllocator && int(day.Month()) == monthAfterFreeze(incoming) {
			outgoing := incoming
			if plan.OldNumber != "" {
				outgoing = effectiveFreezeMonth(activeByNumber[plan.OldNumber].FreezeMonth)
			}
			if outgoing != incoming {
				plan.Warning = fmt.Sprintf(
					"Wechsel im ersten Monat der neuen Abrechnungsperiode, aber alter und neuer Zähler haben verschiedene Stichtage (Monat %d bzw. %d): Für diesen Monat lässt sich der Verbrauch nicht berechnen. Wenn möglich einen Wechseltag in einem anderen Monat eintragen.",
					outgoing, incoming)
			}
		}

		if plan.OldNumber != "" {
			seenOld[plan.OldNumber] = plan.Line
		}
		seenNew[plan.NewNumber] = plan.Line
		plans = append(plans, plan)
	}
	return plans
}

// effectiveFreezeMonth mirrors masterdata.Meter.EffectiveFreezeMonth for a
// value that is not on a Meter yet: 0 means unset, and unset means December
// — the 31 December Stichtag (FACH-03).
func effectiveFreezeMonth(m int) int {
	if m == 0 {
		return 12
	}
	return m
}

// monthAfterFreeze is the month a new billing period begins in: the one
// after the Stichtag month, wrapping December to January.
func monthAfterFreeze(freezeMonth int) int {
	if freezeMonth == 12 {
		return 1
	}
	return freezeMonth + 1
}

// parseKCFactor reads an evaluation factor. Unlike a meter reading this one
// is genuinely decimal (1.4, 0.85), and a German protocol writes it with a
// comma — which is unambiguous here, because a kc factor has no thousands
// to confuse it with.
func parseKCFactor(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return 0, fmt.Errorf("%q ist keine Zahl", s)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%q muss größer als 0 sein", s)
	}
	return v, nil
}

// parseFreezeMonth reads the month the Stichtag falls in, 1 to 12.
func parseFreezeMonth(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q ist keine Monatszahl", s)
	}
	if v < 1 || v > 12 {
		return 0, fmt.Errorf("%q liegt nicht zwischen 1 und 12", s)
	}
	return v, nil
}

// applySwaps writes the applicable plans into a copy of the master data:
// every outgoing meter is closed off on day, and its replacement starts
// the same day at the same point.
//
// The incoming meter's KCFactor and FreezeMonth come from the plan, which
// took them from the pasted line if it named them and from the outgoing
// device otherwise. Inheriting is the right default and not more than
// that: both are properties of the device (docs/04-fachlogik.md resolves
// them per meter and per day for exactly this reason), so a swap to a
// different model can legitimately change either.
func applySwaps(md masterdata.MasterData, plans []swapPlan, day telegram.Day) (masterdata.MasterData, int) {
	byNumber := make(map[string]int, len(md.Meters))
	for i, m := range md.Meters {
		if m.RemovedAt == nil {
			byNumber[m.Number] = i
		}
	}

	meters := make([]masterdata.Meter, len(md.Meters))
	copy(meters, md.Meters)

	applied := 0
	for _, plan := range plans {
		if !plan.OK() {
			continue
		}
		incoming := masterdata.Meter{
			Number:       plan.NewNumber,
			MeterPointID: plan.PointID,
			InstalledAt:  day,
			StartReading: plan.StartReading,
			KCFactor:     plan.KCFactor,
			FreezeMonth:  plan.FreezeMonth,
		}
		if plan.OldNumber != "" {
			idx, ok := byNumber[plan.OldNumber]
			if !ok {
				continue // the data moved under us since the preview
			}
			removedAt := day
			endReading := plan.EndReading
			meters[idx].RemovedAt = &removedAt
			meters[idx].EndReading = &endReading
		}
		meters = append(meters, incoming)
		applied++
	}

	md.Meters = meters
	return md, applied
}

// ---------- gemeinsame Kleinigkeiten ----------

// normalizeNumber strips what a copied cell carries around a device
// number: spaces, an Excel apostrophe, thousands dots.
func normalizeNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "'")
	replacer := strings.NewReplacer(" ", "", ".", "", " ", "")
	return replacer.Replace(s)
}

// normalizeKey lowercases a pasted key and removes the grouping a supplier
// may print it with (spaces, dashes, a 0x prefix).
func normalizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	replacer := strings.NewReplacer(" ", "", "-", "", ":", "", " ", "")
	return replacer.Replace(s)
}

func isMeterNumber(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
