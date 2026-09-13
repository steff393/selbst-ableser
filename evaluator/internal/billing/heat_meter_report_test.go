package billing

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

// qundisHeatTemplate is a real Qundis heat-meter telegram (meter 68817136,
// captured 2026-09-11, 1 kWh). qundisHeatTelegram rewrites its meter number
// and total so a series of month-end readings can be archived for several
// meters without inventing a telegram format.
const qundisHeatTemplate = "5944934436718168483778077936718168934448040DFF5F350082E100005E0107C006FFFF01000000FFFF000000005F38000000000080008000800080008000800080008000800080008000002F02FD170000046D33084B3949"

const (
	qundisHeatTotalAt = 37 // raw offset of the 4-byte BCD total
)

func qundisHeatTelegram(t *testing.T, meter string, kWh int64) string {
	t.Helper()
	raw, err := hex.DecodeString(qundisHeatTemplate)
	if err != nil {
		t.Fatal(err)
	}
	id := bcdLE(t, meter)
	copy(raw[4:8], id)   // link-layer address
	copy(raw[13:17], id) // enhanced identification
	copy(raw[qundisHeatTotalAt:qundisHeatTotalAt+4], bcdLE(t, fmt.Sprintf("%08d", kWh)))
	return hex.EncodeToString(raw)
}

// bcdLE packs an 8-digit decimal string into 4 bytes of BCD, least
// significant pair first.
func bcdLE(t *testing.T, digits string) []byte {
	t.Helper()
	if len(digits) != 8 {
		t.Fatalf("bcdLE wants 8 digits, got %q", digits)
	}
	out := make([]byte, 4)
	for i := 0; i < 4; i++ {
		pair := digits[6-2*i : 8-2*i]
		out[i] = (pair[0]-'0')<<4 | (pair[1] - '0')
	}
	return out
}

func TestReadValueQundisHeatMeter(t *testing.T) {
	reading, found, err := ReadValue(archive.Entry{RawHex: qundisHeatTemplate}, [16]byte{})
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if reading.Value != 1 || reading.Unit.String() != "kWh" {
		t.Errorf("reading = %+v, want 1 kWh", reading)
	}

	again, found, err := ReadValue(archive.Entry{RawHex: qundisHeatTelegram(t, "68817138", 4711)}, [16]byte{})
	if err != nil || !found || again.Value != 4711 {
		t.Errorf("rewritten telegram: %+v found=%v err=%v, want 4711 kWh", again, found, err)
	}
}

// The installation this was built for: three flats, one heat meter each
// in the underfloor heating, no allocators, no radio water meters yet.
// Each flat's heating has to appear in its consumption information in kWh
// — as read, never multiplied by the heating factor that exists for
// allocator units, which is set here precisely so a wrong use would show.
func TestBuildUnitReportHeatMetersPerFlat(t *testing.T) {
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	flats := []struct {
		unit, meter string
		area        float64
		readings    [3]int64 // month ends: 2026-09-30, 2026-10-31, 2026-11-30
	}{
		{"u1", "68817136", 80, [3]int64{1, 121, 401}},  // Oct 120, Nov 280
		{"u2", "68817137", 60, [3]int64{0, 90, 300}},   // Oct 90,  Nov 210
		{"u3", "68817138", 100, [3]int64{2, 152, 552}}, // Oct 150, Nov 400
	}
	days := []string{"2026-09-30", "2026-10-31", "2026-11-30"}

	md := masterdata.MasterData{}
	for i, f := range flats {
		mp := fmt.Sprintf("mp%d", i+1)
		md.Units = append(md.Units, masterdata.Unit{ID: f.unit, Name: f.unit, AreaM2: f.area})
		md.MeterPoints = append(md.MeterPoints, masterdata.MeterPoint{ID: mp, UnitID: f.unit, Room: "Fußbodenheizung", Kind: masterdata.KindHeatMeter})
		md.Meters = append(md.Meters, masterdata.Meter{Number: f.meter, MeterPointID: mp, InstalledAt: mustDay(t, "2026-09-01")})
		for j, day := range days {
			d := mustDay(t, day)
			entry := archive.Entry{MeterID: f.meter, Day: d, ReceivedAt: dayTime(t, d), RawHex: qundisHeatTelegram(t, f.meter, f.readings[j])}
			if _, err := store.InsertHistorical(entry); err != nil {
				t.Fatalf("InsertHistorical: %v", err)
			}
		}
	}
	building := masterdata.Building{HeatingKWhPerUnit: 1.42}

	report, err := BuildUnitReport(store, md, building, "u1", mustDay(t, "2026-11-30"), DefaultLookbackDays)
	if err != nil {
		t.Fatalf("BuildUnitReport: %v", err)
	}
	if len(report.Kinds) != 1 || report.Kinds[0].Kind != masterdata.KindHeatMeter {
		t.Fatalf("kinds = %+v, want exactly the heat meter", report.Kinds)
	}
	k := report.Kinds[0]

	if k.CurrentMonth.Consumption != 280 {
		t.Errorf("November consumption = %v kWh, want 280", k.CurrentMonth.Consumption)
	}
	if k.PriorMonth == nil || k.PriorMonth.Consumption != 120 {
		t.Errorf("October = %+v, want 120 kWh", k.PriorMonth)
	}
	if !k.KWhApplicable || k.KWh != 280 {
		t.Errorf("KWh = %v (applicable=%v), want 280 — the reading itself, not scaled by 1.42", k.KWh, k.KWhApplicable)
	}
	if len(k.Readings) != 1 || k.Readings[0].Current.Value != 401 {
		t.Errorf("Readings = %+v, want the meter at 401 kWh", k.Readings)
	}

	// Building average for November: (280 + 210 + 400) kWh over
	// (80 + 60 + 100) m². Every flat has a heat meter here, so dividing by
	// the whole building's area is right for this installation.
	wantAvg := (280.0 + 210 + 400) / (80 + 60 + 100)
	if got := report.ComparisonPerM2[masterdata.KindHeatMeter]; got != wantAvg {
		t.Errorf("building average = %v kWh/m², want %v", got, wantAvg)
	}
	if !k.ComparisonApplicable {
		t.Fatal("no comparison computed")
	}
	if want := 280.0 / 80; k.ComparisonOwnPerM2 != want {
		t.Errorf("own = %v kWh/m², want %v", k.ComparisonOwnPerM2, want)
	}
	if k.ComparisonAvgPerM2 != wantAvg {
		t.Errorf("average shown = %v kWh/m², want %v — kWh/m², not divided by 1000", k.ComparisonAvgPerM2, wantAvg)
	}
}
