package billing

import (
	"path/filepath"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

// bcdBytesFor is the inverse of decodeBCD: it encodes an integer as
// 3-byte packed BCD, least significant pair first.
func bcdBytesFor(v int64) [3]byte {
	pair := func(n int64) byte { return byte((n/10)<<4 | (n % 10)) }
	return [3]byte{pair(v % 100), pair((v / 100) % 100), pair((v / 10000) % 100)}
}

// TestYearOverYearSeries covers the chart's data source: a fixed
// calendar-year window (not "last 12 months"), correctly aligned
// month-for-month against the year before it, with a FACH-08 gap
// correctly reported as Found=false rather than a misleading 0, and
// months with no data yet (the current year hasn't reached December)
// likewise Found=false.
func TestYearOverYearSeries(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	insert := func(day string, value int64) {
		t.Helper()
		d := mustDay(t, day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(value)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: d, ReceivedAt: dayTime(t, d), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s): %v", day, err)
		}
	}
	// Heating meters reset their own display around each year-end
	// (FACH-03: resetsAnnually), so every January's raw reading *is* that
	// month's consumption directly, not a difference from December — the
	// values below reflect that real device behavior.
	for _, e := range []struct {
		day   string
		value int64
	}{
		{"2024-12-31", 999}, // irrelevant: the Jan 2025 reset ignores it
		{"2025-01-31", 10}, {"2025-02-28", 20}, {"2025-03-31", 30}, {"2025-04-30", 40},
		// May 2025 deliberately skipped (FACH-08 gap): June carries both.
		{"2025-06-30", 60}, {"2025-07-31", 70}, {"2025-08-31", 80}, {"2025-09-30", 90},
		{"2025-10-31", 100}, {"2025-11-30", 110}, {"2025-12-31", 120},
		{"2026-01-31", 15}, {"2026-02-28", 30},
	} {
		insert(e.day, e.value)
	}

	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 60}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
		},
	}

	series, err := YearOverYearSeries(store, md, "u1", masterdata.KindHeating, 2026, DefaultLookbackDays)
	if err != nil {
		t.Fatalf("YearOverYearSeries: %v", err)
	}
	if series.Year != 2026 || series.Kind != masterdata.KindHeating {
		t.Errorf("Year/Kind = %d/%v, want 2026/Heating", series.Year, series.Kind)
	}

	// Current year (2026): January and February have data, the rest doesn't yet.
	if got := series.CurrentYear[0]; !got.Found || got.Value != 15 || got.Month != "2026-01" {
		t.Errorf("CurrentYear[Jan] = %+v, want {2026-01, 15, true}", got)
	}
	if got := series.CurrentYear[1]; !got.Found || got.Value != 15 || got.Month != "2026-02" {
		t.Errorf("CurrentYear[Feb] = %+v, want {2026-02, 15, true}", got)
	}
	for i := 2; i < 12; i++ {
		if series.CurrentYear[i].Found {
			t.Errorf("CurrentYear[%d] (%s) = %+v, want Found=false (no data yet)", i, series.CurrentYear[i].Month, series.CurrentYear[i])
		}
	}

	// Prior year (2025): a full year, except the May gap (carried into June).
	wantPrior := [12]float64{10, 10, 10, 10, 0 /* May: gap */, 20, 10, 10, 10, 10, 10, 10}
	for i, want := range wantPrior {
		got := series.PriorYear[i]
		if i == 4 { // May: must be reported absent, not a misleading 0
			if got.Found {
				t.Errorf("PriorYear[May] = %+v, want Found=false", got)
			}
			continue
		}
		if !got.Found || got.Value != want {
			t.Errorf("PriorYear[%d] (%s) = %+v, want value %v, Found=true", i, got.Month, got, want)
		}
	}
	if series.PriorYear[0].Month != "2025-01" || series.PriorYear[11].Month != "2025-12" {
		t.Errorf("PriorYear month labels = %s..%s, want 2025-01..2025-12", series.PriorYear[0].Month, series.PriorYear[11].Month)
	}
}

// TestBuildingYearOverYearSeriesSumsAcrossUnits covers the one thing that
// actually differs from YearOverYearSeries: summing every unit's meter
// points of a kind into one series, not just one unit's. Two units, one
// heating meter point each, different consumption — the building series
// must be their sum, and neither unit's own YearOverYearSeries may equal
// it (otherwise the test could not tell "summed" from "only unit A" or
// "only unit B").
func TestBuildingYearOverYearSeriesSumsAcrossUnits(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	insert := func(meterID, day string, value int64) {
		t.Helper()
		d := mustDay(t, day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(value)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: meterID, Day: d, ReceivedAt: dayTime(t, d), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s, %s): %v", meterID, day, err)
		}
	}
	// Unit 1: December (irrelevant — the January reset ignores it), then
	// raw readings 10 (January's own consumption, since heating resets at
	// the year boundary) and 25 (February's consumption is the delta from
	// January: 25-10 = 15).
	insert("90000001", "2025-12-31", 999)
	insert("90000001", "2026-01-31", 10)
	insert("90000001", "2026-02-28", 25)
	// Unit 2: same pattern, different values (100, then 250 → February
	// consumption 150), so the building sum is unambiguous evidence both
	// units contributed to *each* month, not just January.
	insert("90000002", "2025-12-31", 999)
	insert("90000002", "2026-01-31", 100)
	insert("90000002", "2026-02-28", 250)

	md := masterdata.MasterData{
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Unit 1", AreaM2: 60},
			{ID: "u2", Name: "Unit 2", AreaM2: 60},
		},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating},
			{ID: "mp2", UnitID: "u2", Room: "Living room", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
			{Number: "90000002", MeterPointID: "mp2", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
		},
	}

	series, err := BuildingYearOverYearSeries(store, md, masterdata.KindHeating, 2026, DefaultLookbackDays)
	if err != nil {
		t.Fatalf("BuildingYearOverYearSeries: %v", err)
	}
	if series.Year != 2026 || series.Kind != masterdata.KindHeating {
		t.Errorf("Year/Kind = %d/%v, want 2026/Heating", series.Year, series.Kind)
	}
	if got := series.CurrentYear[0]; !got.Found || got.Value != 110 { // 10 + 100
		t.Errorf("CurrentYear[Jan] = %+v, want {110, true}", got)
	}
	if got := series.CurrentYear[1]; !got.Found || got.Value != 165 { // (25-10) + (250-100)
		t.Errorf("CurrentYear[Feb] = %+v, want {165, true}", got)
	}

	// Sanity: this really is the sum, not one unit's own series reused.
	u1Series, err := YearOverYearSeries(store, md, "u1", masterdata.KindHeating, 2026, DefaultLookbackDays)
	if err != nil {
		t.Fatalf("YearOverYearSeries(u1): %v", err)
	}
	if u1Series.CurrentYear[0].Value == series.CurrentYear[0].Value {
		t.Error("building series should not equal a single unit's series")
	}
}

// TestSeriesForMeterPointUsesKCFactorActiveOnReadingDay covers FACH-07's
// kc-Faktor lookup for a meter that was re-recalibrated in place: two
// master-data records for the same physical meter Number at the same
// meter point (not a swap — the device never changed), with different
// KCFactor values on either side of the cutover. Each month's consumption
// must be scaled by whichever record was actually active on that reading's
// day, not by whichever of the two records happens to come first in
// md.Meters.
func TestSeriesForMeterPointUsesKCFactorActiveOnReadingDay(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	insert := func(day string, value int64) {
		t.Helper()
		d := mustDay(t, day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(value)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: d, ReceivedAt: dayTime(t, d), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s): %v", day, err)
		}
	}
	for _, e := range []struct {
		day   string
		value int64
	}{
		{"2025-04-30", 100}, {"2025-05-31", 110}, {"2025-06-30", 125},
		{"2025-07-31", 135}, {"2025-08-31", 140},
	} {
		insert(e.day, e.value)
	}

	removedAt := mustDay(t, "2025-06-30")
	endReading := int64(125)
	mp := masterdata.MeterPoint{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating}
	md := masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 60}},
		MeterPoints: []masterdata.MeterPoint{mp},
		Meters: []masterdata.Meter{
			// Recalibrated in place on 2025-07-01: same physical meter
			// Number, no swap, only the kc-Faktor changed.
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2025-01-01"), RemovedAt: &removedAt, EndReading: &endReading, KCFactor: 1},
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2025-07-01"), KCFactor: 2},
		},
	}

	results, err := seriesForMeterPoint(store, md, mp, mustDay(t, "2025-04-01"), mustDay(t, "2025-08-31"), DefaultLookbackDays)
	if err != nil {
		t.Fatalf("seriesForMeterPoint: %v", err)
	}

	want := map[string]float64{"2025-05": 10, "2025-06": 15, "2025-07": 20, "2025-08": 10}
	got := make(map[string]float64, len(results))
	for _, r := range results {
		got[r.Month] = r.Consumption
	}
	for month, wantConsumption := range want {
		if got[month] != wantConsumption {
			t.Errorf("Consumption[%s] = %v, want %v (kc-Faktor active on that reading's day)", month, got[month], wantConsumption)
		}
	}
}

// TestLatestAvailableMonthRespectsNotAfter is the direct unit test for
// LatestAvailableMonth's upper bound — see internal/webapp's UVI handler,
// which feeds it min(today, the tenant's own access end) so a former
// tenant's default landing month can never run past their own move-out,
// even if the meter (now serving a later tenant) kept reporting after
// that. The equivalent end-to-end scenario cannot be exercised through a
// real login, since an already-expired access grant is rejected at
// login time (see internal/access/token.go) before it would ever reach
// this bound.
func TestLatestAvailableMonthRespectsNotAfter(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	for _, day := range []string{"2025-10-31", "2025-11-30", "2025-12-31", "2026-01-31"} {
		d := mustDay(t, day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(100)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: d, ReceivedAt: dayTime(t, d), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s): %v", day, err)
		}
	}

	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 60}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
		},
	}

	// notAfter set to the tenant's own move-out (November), with data on
	// record both before and after it: the result must not cross it.
	got, found, err := LatestAvailableMonth(store, md, "u1", mustDay(t, "2025-11-30"))
	if err != nil {
		t.Fatalf("LatestAvailableMonth: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if got != mustDay(t, "2025-11-30") {
		t.Errorf("LatestAvailableMonth = %s, want 2025-11-30 (must not cross notAfter into December/January)", got)
	}

	// Without a restrictive bound, the true latest day wins.
	got, found, err = LatestAvailableMonth(store, md, "u1", mustDay(t, "2026-08-15"))
	if err != nil {
		t.Fatalf("LatestAvailableMonth: %v", err)
	}
	if !found || got != mustDay(t, "2026-01-31") {
		t.Errorf("LatestAvailableMonth with a distant notAfter = (%s, %v), want (2026-01-31, true)", got, found)
	}

	// A unit with no archived data at all: found is false, not an error.
	got, found, err = LatestAvailableMonth(store, md, "u-nonexistent", mustDay(t, "2026-08-15"))
	if err != nil {
		t.Fatalf("LatestAvailableMonth: %v", err)
	}
	if found {
		t.Errorf("LatestAvailableMonth for an unknown unit = (%s, true), want found=false", got)
	}
}

func TestBuildUnitReport_AcceptanceScenario1(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	values := []struct {
		day   string
		value int64
	}{
		{"2024-12-31", 380}, {"2025-01-31", 45}, {"2025-02-28", 130}, {"2025-03-31", 210},
		{"2025-04-30", 245}, {"2025-05-31", 255}, {"2025-06-30", 258}, {"2025-07-31", 260},
		{"2025-08-31", 262}, {"2025-09-30", 268}, {"2025-10-31", 300}, {"2025-11-30", 360},
		{"2025-12-31", 430}, {"2026-01-31", 50},
	}
	for _, v := range values {
		d := mustDay(t, v.day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(v.value)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: d, ReceivedAt: dayTime(t, d), RSSI: -80, RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s): %v", v.day, err)
		}
	}

	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 60}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
		},
	}
	building := masterdata.Building{HeatingKWhPerUnit: 1.42}

	report, err := BuildUnitReport(store, md, building, "u1", mustDay(t, "2025-06-30"), DefaultLookbackDays)
	if err != nil {
		t.Fatalf("BuildUnitReport: %v", err)
	}
	if len(report.Kinds) != 1 {
		t.Fatalf("got %d kind reports, want 1", len(report.Kinds))
	}
	k := report.Kinds[0]

	if k.CurrentMonth.Consumption != 3 {
		t.Errorf("CurrentMonth.Consumption = %v, want 3", k.CurrentMonth.Consumption)
	}
	if k.PriorMonth == nil || k.PriorMonth.Consumption != 10 {
		t.Errorf("PriorMonth = %+v, want consumption 10 (May)", k.PriorMonth)
	}
	if k.PriorYearMonth != nil {
		t.Errorf("PriorYearMonth = %+v, want nil (no June 2024 data)", k.PriorYearMonth)
	}
	if len(k.History) != 6 {
		t.Errorf("History has %d entries, want 6 (Jan-Jun 2025)", len(k.History))
	} else if k.History[0].Month != "2025-01" || k.History[len(k.History)-1].Month != "2025-06" {
		t.Errorf("History = %+v, want to run from 2025-01 through 2025-06", k.History)
	}
	if !k.KWhApplicable || k.KWh != 3*1.42 {
		t.Errorf("KWh = %v (applicable=%v), want %v", k.KWh, k.KWhApplicable, 3*1.42)
	}
	if len(k.Readings) != 1 || k.Readings[0].Current.Value != 258 || k.Readings[0].Current.Day != mustDay(t, "2025-06-30") {
		t.Errorf("Readings = %+v, want one entry with value 258 on 2025-06-30", k.Readings)
	}

	cmp, ok := report.ComparisonPerM2[masterdata.KindHeating]
	if !ok {
		t.Fatal("expected a heating comparison value")
	}
	if want := 3.0 / 60.0; cmp != want {
		t.Errorf("ComparisonPerM2 = %v, want %v", cmp, want)
	}
	// With a single unit, its own per-m2 consumption always equals the
	// building average, so the deviation must be exactly zero.
	if !k.ComparisonApplicable || k.ComparisonPercent != 0 {
		t.Errorf("ComparisonPercent = %v (applicable=%v), want 0", k.ComparisonPercent, k.ComparisonApplicable)
	}
}

// TestBuildUnitReport_AggregatesMultipleMeterPointsOfSameKind covers
// UI-01's "je Verbrauchsart" requirement: a unit with two heating meter
// points (say, two rooms) must report as one combined Heizung section,
// not two — CurrentMonth/PriorMonth summed across both, one comparison
// percentage computed from the combined figure (not one understated
// percentage per meter, which is what this used to compute before this
// aggregation existed — each meter's own consumption divided by the
// *whole* unit's area), and both meters itemized in Readings.
func TestBuildUnitReport_AggregatesMultipleMeterPointsOfSameKind(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	insert := func(meterID, day string, value int64) {
		t.Helper()
		d := mustDay(t, day)
		rawHex := buildEncryptedTelegramHex(t, key, hcaPayload(bcdBytesFor(value)))
		if _, err := store.InsertHistorical(archive.Entry{MeterID: meterID, Day: d, ReceivedAt: dayTime(t, d), RSSI: -80, RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical(%s, %s): %v", meterID, day, err)
		}
	}
	// u1's two heating meter points: Living room (mp1) reaches 20 then
	// another 20 (April->May->June); Bedroom (mp2) reaches 10 then
	// another 20. Combined: May = 30, June = 40.
	insert("90000001", "2025-04-30", 0)
	insert("90000001", "2025-05-31", 20)
	insert("90000001", "2025-06-30", 40)
	insert("90000002", "2025-04-30", 0)
	insert("90000002", "2025-05-31", 10)
	insert("90000002", "2025-06-30", 30)
	// u2's single heating meter point: June consumption 160, so the
	// building-wide June average per m2 comes out to a clean 1.0 (total
	// consumption 200 over total area 200), letting u1's -60% deviation
	// be checked without it being the degenerate "only unit" case.
	insert("90000003", "2025-05-31", 0)
	insert("90000003", "2025-06-30", 160)

	md := masterdata.MasterData{
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Unit 1", AreaM2: 100},
			{ID: "u2", Name: "Unit 2", AreaM2: 100},
		},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: masterdata.KindHeating},
			{ID: "mp2", UnitID: "u1", Room: "Bedroom", Kind: masterdata.KindHeating},
			{ID: "mp3", UnitID: "u2", Room: "Living room", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
			{Number: "90000002", MeterPointID: "mp2", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
			{Number: "90000003", MeterPointID: "mp3", AESKey: key, InstalledAt: mustDay(t, "2024-01-01")},
		},
	}
	building := masterdata.Building{HeatingKWhPerUnit: 2}

	report, err := BuildUnitReport(store, md, building, "u1", mustDay(t, "2025-06-30"), DefaultLookbackDays)
	if err != nil {
		t.Fatalf("BuildUnitReport: %v", err)
	}
	if len(report.Kinds) != 1 {
		t.Fatalf("got %d kind reports, want 1 (both meter points combine into a single Heizung section)", len(report.Kinds))
	}
	k := report.Kinds[0]

	if k.CurrentMonth.Consumption != 40 {
		t.Errorf("CurrentMonth.Consumption = %v, want 40 (20+20)", k.CurrentMonth.Consumption)
	}
	if k.PriorMonth == nil || k.PriorMonth.Consumption != 30 {
		t.Errorf("PriorMonth = %+v, want consumption 30 (20+10)", k.PriorMonth)
	}
	if k.KWh != 40*2 {
		t.Errorf("KWh = %v, want %v (combined consumption times the building factor)", k.KWh, 40*2.0)
	}

	if len(k.Readings) != 2 {
		t.Fatalf("got %d readings, want 2 (one per meter point)", len(k.Readings))
	}
	rooms := map[string]bool{}
	for _, r := range k.Readings {
		rooms[r.Room] = true
	}
	if !rooms["Living room"] || !rooms["Bedroom"] {
		t.Errorf("Readings rooms = %v, want both Living room and Bedroom itemized", rooms)
	}

	if !k.ComparisonApplicable {
		t.Fatal("expected a comparison value")
	}
	if want, got := -60.0, k.ComparisonPercent; got < want-0.01 || got > want+0.01 {
		t.Errorf("ComparisonPercent = %v, want approximately %v (computed from the combined 40, not either meter alone)", got, want)
	}
}

func TestPercentChange(t *testing.T) {
	cur := MonthResult{Consumption: 120}
	prior := MonthResult{Consumption: 100}
	pct, ok := cur.PercentChange(&prior)
	if !ok || pct != 20 {
		t.Errorf("PercentChange = %v, ok=%v, want 20", pct, ok)
	}
	if _, ok := cur.PercentChange(nil); ok {
		t.Error("PercentChange against nil should report ok=false")
	}
	zero := MonthResult{Consumption: 0}
	if _, ok := cur.PercentChange(&zero); ok {
		t.Error("PercentChange against a zero baseline should report ok=false")
	}
}
