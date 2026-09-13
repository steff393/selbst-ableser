package webapp

import (
	"strings"
	"testing"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

func day(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

// swapTestData is a miniature installation: one unit, three meter points,
// one of them still empty and one with a device that was already replaced
// once, so the history and the "already removed" case are both covered.
func swapTestData(t *testing.T) masterdata.MasterData {
	t.Helper()
	removedAt := day(t, "2025-06-01")
	endReading := int64(400)
	return masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "1", Name: "EG Links", AreaM2: 80}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "101", UnitID: "1", Room: "WOH", Kind: masterdata.KindHeatCostAllocator},
			{ID: "102", UnitID: "1", Room: "BAD", Kind: masterdata.KindHeatCostAllocator},
			{ID: "103", UnitID: "1", Room: "SCH", Kind: masterdata.KindHeatCostAllocator},
			{ID: "121", UnitID: "1", Room: "BAD", Kind: masterdata.KindHotWater},
		},
		Meters: []masterdata.Meter{
			{Number: "10000001", MeterPointID: "101", InstalledAt: day(t, "2024-01-01"), StartReading: 0, KCFactor: 1.4, FreezeMonth: 6},
			{Number: "10000002", MeterPointID: "102", InstalledAt: day(t, "2024-01-01"), StartReading: 0,
				RemovedAt: &removedAt, EndReading: &endReading},
			{Number: "10000003", MeterPointID: "102", InstalledAt: removedAt, StartReading: 0},
			{Number: "10000004", MeterPointID: "103", InstalledAt: day(t, "2024-01-01"), StartReading: 0},
			// 121 stays empty: no device has ever been mounted there.
		},
	}
}

func TestParsePastedTableAcceptsWhatOperatorsActuallyPaste(t *testing.T) {
	cases := map[string]string{
		"Excel (Tabulator)": "10000001\t120\t20000001\t0",
		"CSV (Semikolon)":   "10000001;120;20000001;0",
		"Textblock":         "10000001  120  20000001  0",
	}
	for name, text := range cases {
		got := parsePastedTable(text)
		if len(got) != 1 || len(got[0]) != 4 || got[0][0] != "10000001" || got[0][2] != "20000001" {
			t.Errorf("%s: parsePastedTable = %v", name, got)
		}
	}

	// Blank lines in the middle of a pasted block are normal and must not
	// shift the columns of what follows.
	got := parsePastedTable("10000001\t120\t20000001\t0\n\n10000003\t80\t20000003\t0\n")
	if len(got) != 2 {
		t.Errorf("rows = %d, want 2 (blank line skipped)", len(got))
	}
}

func TestPlanSwapsMatchesByOutgoingNumber(t *testing.T) {
	md := swapTestData(t)
	table := parsePastedTable("10000001\t520\t20000001\t0")

	plans := planSwaps(md, table, defaultSwapColumns, day(t, "2026-03-01"))
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	p := plans[0]
	if !p.OK() {
		t.Fatalf("plan refused: %s", p.Problem)
	}
	if p.PointID != "101" {
		t.Errorf("PointID = %q, want 101 — the point the outgoing meter sits at", p.PointID)
	}
	if p.EndReading != 520 || p.StartReading != 0 || p.NewNumber != "20000001" {
		t.Errorf("plan = %+v", p)
	}
	if !strings.Contains(p.PointLabel, "EG Links") || !strings.Contains(p.PointLabel, "Heizung") {
		t.Errorf("PointLabel = %q, want the unit, room and kind", p.PointLabel)
	}
}

func TestPlanSwapsRefusesWhatCannotBeApplied(t *testing.T) {
	md := swapTestData(t)
	table := parsePastedTable(strings.Join([]string{
		"99999999\t100\t20000001\t0",  // unknown outgoing meter
		"10000002\t500\t20000002\t0",  // already removed in 2025
		"10000001\t520\t2000001\t0",   // new number is seven digits
		"10000003\t520\t10000001\t0",  // new number belongs to another point
		"10000004\t12,5\t20000004\t0", // reading with a separator
		"\t\t\t",                      // an empty line: dropped, not refused
	}, "\n"))

	plans := planSwaps(md, table, defaultSwapColumns, day(t, "2026-03-01"))
	if len(plans) != 5 {
		t.Fatalf("plans = %d, want 5 - the blank line is dropped while reading, not reported", len(plans))
	}
	for i, p := range plans {
		if p.OK() {
			t.Errorf("line %d should have been refused, plan = %+v", i+1, p)
		}
	}
	if !strings.Contains(plans[0].Problem, "nicht bekannt") {
		t.Errorf("unknown meter: %q", plans[0].Problem)
	}
	if !strings.Contains(plans[1].Problem, "bereits ausgebaut") {
		t.Errorf("already removed: %q", plans[1].Problem)
	}
	if !strings.Contains(plans[4].Problem, "Komma") {
		t.Errorf("separator in reading: %q", plans[4].Problem)
	}
}

// TestPlanSwapsRefusesTheSameMeterTwice guards the case a copied protocol
// makes easy: one device listed on two lines would otherwise be removed
// twice and leave two successors at one point.
func TestPlanSwapsRefusesTheSameMeterTwice(t *testing.T) {
	md := swapTestData(t)
	table := parsePastedTable("10000001\t520\t20000001\t0\n10000001\t530\t20000009\t0")

	plans := planSwaps(md, table, defaultSwapColumns, day(t, "2026-03-01"))
	if !plans[0].OK() {
		t.Fatalf("first line should hold: %s", plans[0].Problem)
	}
	if plans[1].OK() || !strings.Contains(plans[1].Problem, "Zeile 1") {
		t.Errorf("second line = %+v, want a refusal naming line 1", plans[1])
	}
}

// TestPlanSwapsFirstInstallation covers the empty meter point: no outgoing
// device exists, so the line names the point instead. Meter point 121 is
// a water meter, so its Anfangsstand is read as m³ and stored in liters
// (see reading_value.go) — "1500" means 1500 m³, i.e. 1,500,000 l.
func TestPlanSwapsFirstInstallation(t *testing.T) {
	md := swapTestData(t)
	cols := swapColumns{Point: 1, NewNumber: 2, StartReading: 3}

	plans := planSwaps(md, parsePastedTable("121\t20000121\t1500"), cols, day(t, "2026-03-01"))
	if len(plans) != 1 || !plans[0].OK() {
		t.Fatalf("plan = %+v", plans)
	}
	if plans[0].PointID != "121" || plans[0].StartReading != 1500000 {
		t.Errorf("plan = %+v", plans[0])
	}
	if plans[0].StartReadingText() != "1500,000" {
		t.Errorf("StartReadingText() = %q, want the m³ value displayed with a comma", plans[0].StartReadingText())
	}

	// The same line for an occupied point must be refused, naming the
	// device that is in the way.
	plans = planSwaps(md, parsePastedTable("101\t20000101\t0"), cols, day(t, "2026-03-01"))
	if plans[0].OK() || !strings.Contains(plans[0].Problem, "10000001") {
		t.Errorf("plan = %+v, want a refusal naming the installed meter", plans[0])
	}
}

func TestDetectSwapColumnsFromHeader(t *testing.T) {
	header := parsePastedTable("Zählernummer alt;Endstand;Zählernummer neu;Anfangsstand")[0]
	if !looksLikeHeader(header) {
		t.Fatal("header row not recognised")
	}
	cols := detectSwapColumns(header)
	want := swapColumns{OldNumber: 1, EndReading: 2, NewNumber: 3, StartReading: 4}
	if cols != want {
		t.Errorf("detectSwapColumns = %+v, want %+v", cols, want)
	}

	// A protocol whose columns come in a different order must still land
	// correctly — that is the whole point of reading the header.
	cols = detectSwapColumns(parsePastedTable("Platz;neue Nr.;alte Nr.;Ausbaustand;Einbaustand")[0])
	if cols.Point != 1 || cols.NewNumber != 2 || cols.OldNumber != 3 || cols.EndReading != 4 || cols.StartReading != 5 {
		t.Errorf("detectSwapColumns = %+v", cols)
	}
}

// TestApplySwapsProducesValidMasterData is the important one: what the
// page writes has to satisfy the same rules the Stammdaten page is held
// to — no gap and no overlap between a device and its successor.
func TestApplySwapsProducesValidMasterData(t *testing.T) {
	md := swapTestData(t)
	swapDay := day(t, "2026-03-01")
	plans := planSwaps(md, parsePastedTable("10000001\t520\t20000001\t0"), defaultSwapColumns, swapDay)

	updated, applied := applySwaps(md, plans, swapDay)
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if d := masterdata.Validate(updated); !d.OK() {
		t.Fatalf("validation failed: %v", d.Errors)
	}

	var outgoing, incoming masterdata.Meter
	for _, m := range updated.Meters {
		switch m.Number {
		case "10000001":
			outgoing = m
		case "20000001":
			incoming = m
		}
	}
	if outgoing.RemovedAt == nil || outgoing.RemovedAt.String() != "2026-03-01" || outgoing.EndReading == nil || *outgoing.EndReading != 520 {
		t.Errorf("outgoing meter = %+v, want closed off on the swap day at 520", outgoing)
	}
	if incoming.InstalledAt.String() != "2026-03-01" || incoming.MeterPointID != "101" {
		t.Errorf("incoming meter = %+v", incoming)
	}
	// The radiator's scaling factor and the billing date belong to the
	// place, not to the device that was swapped out.
	if incoming.KCFactor != 1.4 || incoming.FreezeMonth != 6 {
		t.Errorf("incoming meter = %+v, want KCFactor and FreezeMonth inherited", incoming)
	}
	// The old device stays in the data, which is the whole reason for the
	// history: nothing about the swap erases what was measured before it.
	if outgoing.MeterPointID != "101" {
		t.Errorf("outgoing meter lost its meter point: %+v", outgoing)
	}
}

func TestApplySwapsSkipsRefusedLines(t *testing.T) {
	md := swapTestData(t)
	swapDay := day(t, "2026-03-01")
	plans := planSwaps(md, parsePastedTable("10000001\t520\t20000001\t0\n99999999\t1\t20000009\t0"), defaultSwapColumns, swapDay)

	updated, applied := applySwaps(md, plans, swapDay)
	if applied != 1 {
		t.Errorf("applied = %d, want 1 — the refused line must not be written", applied)
	}
	for _, m := range updated.Meters {
		if m.Number == "20000009" {
			t.Error("a refused line's meter was written anyway")
		}
	}
}

// TestPlanSwapsWarnsAboutDifferingFreezeMonths covers what the billing code
// still refuses: a swap in the first month of a new billing period between
// two devices whose periods start in *different* months. The swap in that
// month as such is fine now — the period closes on the device's own frozen
// value — so only the mismatch is worth a warning.
func TestPlanSwapsWarnsAboutDifferingFreezeMonths(t *testing.T) {
	md := swapTestData(t)
	// Meter 10000001 sits at a heating point and starts its period in June.
	cols := detectSwapColumns(parsePastedTable("alt;Endstand;neu;Anfangsstand;Stichtag")[0])

	// Both devices freeze at the end of June, so their new period starts in
	// July — and July is the swap month: computable, so no warning.
	plans := planSwaps(md, parsePastedTable("10000001;520;20000001;0;6"), cols, day(t, "2026-07-15"))
	if !plans[0].OK() {
		t.Fatalf("the swap must be applicable: %s", plans[0].Problem)
	}
	if plans[0].Warning != "" {
		t.Errorf("Warning = %q, want none when both devices share the period", plans[0].Warning)
	}

	// The outgoing device (10000004, Stichtag 31 December by default) and
	// the replacement (30 June) disagree — that is what the billing refuses.
	plans = planSwaps(md, parsePastedTable("10000004;120;20000004;0;6"), cols, day(t, "2026-07-15"))
	if !plans[0].OK() {
		t.Fatalf("the swap must still be applicable: %s", plans[0].Problem)
	}
	if !strings.Contains(plans[0].Warning, "verschiedene Stichtage") {
		t.Errorf("Warning = %q, want a warning about the differing periods", plans[0].Warning)
	}

	// Outside the incoming device's first period month nothing is
	// remarkable either way.
	plans = planSwaps(md, parsePastedTable("10000004;120;20000004;0;6"), cols, day(t, "2026-03-01"))
	if plans[0].Warning != "" {
		t.Errorf("Warning = %q, want none outside the first month of a period", plans[0].Warning)
	}

	// A water meter never resets, so the month is irrelevant there.
	pointCols := swapColumns{Point: 1, NewNumber: 2, StartReading: 3}
	plans = planSwaps(md, parsePastedTable("121\t20000121\t0"), pointCols, day(t, "2026-01-15"))
	if plans[0].Warning != "" {
		t.Errorf("Warning = %q, want none for a water meter", plans[0].Warning)
	}
}

// TestPlanSwapsTakesDeviceValuesFromTheList is the other half: a swap to a
// different model brings its own evaluation factor, and the pasted line is
// where that arrives.
func TestPlanSwapsTakesDeviceValuesFromTheList(t *testing.T) {
	md := swapTestData(t)
	table := parsePastedTable(strings.Join([]string{
		"alte Nr.;Endstand;neue Nr.;Anfangsstand;kc;Stichtag",
		"10000001;520;20000001;0;0,85;9",
	}, "\n"))
	cols := detectSwapColumns(table[0])
	if cols.KCFactor != 5 || cols.FreezeMonth != 6 {
		t.Fatalf("detectSwapColumns = %+v, want the two optional columns found", cols)
	}

	plans := planSwaps(md, table[1:], cols, day(t, "2026-03-01"))
	if !plans[0].OK() {
		t.Fatalf("plan refused: %s", plans[0].Problem)
	}
	if plans[0].KCFactor != 0.85 || plans[0].FreezeMonth != 9 {
		t.Errorf("plan = %+v, want the values from the line (comma decimal accepted)", plans[0])
	}
	if plans[0].Inherited {
		t.Error("Inherited should be false when the line names the values")
	}

	updated, _ := applySwaps(md, plans, day(t, "2026-03-01"))
	for _, m := range updated.Meters {
		if m.Number == "20000001" && (m.KCFactor != 0.85 || m.FreezeMonth != 9) {
			t.Errorf("incoming meter = %+v, want the pasted device values", m)
		}
	}
}

// TestPlanSwapsInheritsDeviceValuesWithoutColumns pins the default: a
// protocol that says nothing about them carries them over unchanged, and
// the preview says that it did.
func TestPlanSwapsInheritsDeviceValuesWithoutColumns(t *testing.T) {
	md := swapTestData(t)
	plans := planSwaps(md, parsePastedTable("10000001\t520\t20000001\t0"), defaultSwapColumns, day(t, "2026-03-01"))
	if plans[0].KCFactor != 1.4 || plans[0].FreezeMonth != 6 || !plans[0].Inherited {
		t.Errorf("plan = %+v, want the outgoing meter's values, marked as inherited", plans[0])
	}
}

func TestPlanSwapsRefusesUnusableDeviceValues(t *testing.T) {
	md := swapTestData(t)
	cols := detectSwapColumns(parsePastedTable("alt;Endstand;neu;Anfangsstand;kc;Stichtag")[0])

	plans := planSwaps(md, parsePastedTable("10000001;520;20000001;0;0;6"), cols, day(t, "2026-03-01"))
	if plans[0].OK() || !strings.Contains(plans[0].Problem, "kc-Faktor") {
		t.Errorf("plan = %+v, want a refusal: a kc factor of 0 would silently mean 1", plans[0])
	}

	plans = planSwaps(md, parsePastedTable("10000001;520;20000001;0;1,2;13"), cols, day(t, "2026-03-01"))
	if plans[0].OK() || !strings.Contains(plans[0].Problem, "Stichtag") {
		t.Errorf("plan = %+v, want a refusal: month 13 does not exist", plans[0])
	}
}

// TestMasterDataTablesRoundTrip pins what the three tables have to
// preserve between them: everything in the master data comes back out
// unchanged, the meter point that has no meter included. That point used
// to vanish, because the grid was built from the meters — and since a
// save replaces every meter point with what the grids submit, the next
// save deleted a point no row had mentioned.
func TestMasterDataTablesRoundTrip(t *testing.T) {
	md := swapTestData(t)

	points, errMsg := parseMeterPointRows(meterPointRowsFrom(md.MeterPoints))
	if errMsg != "" {
		t.Fatalf("parseMeterPointRows: %s", errMsg)
	}
	meters, errMsg := parseMeterTableRows(meterRowsInTableOrder(md.MeterPoints, md.Meters), md.MeterPoints)
	if errMsg != "" {
		t.Fatalf("parseMeterTableRows: %s", errMsg)
	}

	if len(points) != len(md.MeterPoints) {
		t.Errorf("points after a round trip = %d, want %d", len(points), len(md.MeterPoints))
	}
	if len(meters) != len(md.Meters) {
		t.Errorf("meters after a round trip = %d, want %d", len(meters), len(md.Meters))
	}

	empty := false
	for _, mp := range points {
		if mp.ID != "121" {
			continue
		}
		empty = true
		if mp.UnitID != "1" || mp.Kind != masterdata.KindHotWater {
			t.Errorf("meter point 121 = %+v, want its own fields intact", mp)
		}
	}
	if !empty {
		t.Error("the meter point without a meter did not survive the round trip")
	}

	before := make(map[string]string, len(md.Meters))
	for _, m := range md.Meters {
		before[m.Number] = m.MeterPointID
	}
	for _, m := range meters {
		if before[m.Number] != m.MeterPointID {
			t.Errorf("meter %s came back at %q, want %q", m.Number, m.MeterPointID, before[m.Number])
		}
	}
}

// TestMeterTableGroupsHistoryByPoint keeps a point's devices together and
// in installation order, so one place's history reads as one block rather
// than being scattered through the table.
func TestMeterTableGroupsHistoryByPoint(t *testing.T) {
	md := swapTestData(t)

	var seq []string
	for _, row := range meterRowsInTableOrder(md.MeterPoints, md.Meters) {
		if row.MeterPointID == "102" {
			seq = append(seq, row.Number)
		}
	}
	if len(seq) != 2 || seq[0] != "10000002" || seq[1] != "10000003" {
		t.Errorf("meter point 102's rows = %v, want the older meter first", seq)
	}
}
