package webapp

import (
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/masterdata"
)

func csvTestData(t *testing.T) masterdata.MasterData {
	t.Helper()
	removed, err := parseGridDay("15.02.2026")
	if err != nil {
		t.Fatal(err)
	}
	end := int64(412)
	return masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "1", Name: "EG links", AreaM2: 62.4}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "101", UnitID: "1", Room: "WZ", Kind: masterdata.KindHeatCostAllocator},
			{ID: "102", UnitID: "1", Room: "SZ", Kind: masterdata.KindHeatCostAllocator},
			{ID: "H01", Room: "Heizraum", Kind: masterdata.KindHeatMeter},
		},
		Meters: []masterdata.Meter{
			{Number: "10004711", MeterPointID: "101", KCFactor: 1.2, FreezeMonth: 7},
			{Number: "10004690", MeterPointID: "101", RemovedAt: &removed, EndReading: &end},
		},
	}
}

// TestCSVRoundTrip: what the export writes, the import reads back without
// changing anything. A file that came out of this system has to go into
// it again — that is the whole point of offering it.
func TestCSVRoundTrip(t *testing.T) {
	md := csvTestData(t)

	for _, c := range []struct {
		table string
		text  string
	}{
		{"units", csvUnits(md)},
		{"meterpoints", csvMeterPoints(md)},
		{"meters", csvMeters(md)},
	} {
		preview, msg := planImport(md, c.table, c.text)
		if msg != "" {
			t.Fatalf("%s: %s", c.table, msg)
		}
		if len(preview.Unknown) != 0 {
			t.Errorf("%s: the export writes columns its own import does not know: %v", c.table, preview.Unknown)
		}
		for _, row := range preview.Rows {
			if !row.OK() {
				t.Errorf("%s line %d (%s): %s", c.table, row.Line, row.Key, row.Problem)
			}
			if row.Action != "wird geändert" {
				t.Errorf("%s line %d: action = %q, want an unchanged row to be recognized", c.table, row.Line, row.Action)
			}
		}
	}
}

// TestImportOnlyTouchesTheColumnsItCarries is F-1 and F-2 together: a
// supplier's list of numbers and keys is the normal case, and it must
// leave kc factors, dates and everything else exactly as they were.
func TestImportOnlyTouchesTheColumnsItCarries(t *testing.T) {
	md := csvTestData(t)
	csv := "Zählernummer;AES-Schlüssel\n10004711;8B4C71A0E2F35D9C1A70B4E62D8F0315"

	records, unknown, msg := parseCSV(csv)
	if msg != "" || len(unknown) != 0 {
		t.Fatalf("parseCSV: %s %v", msg, unknown)
	}
	updated, rows := applyRecords(md, "meters", records)
	if len(rows) != 1 || !rows[0].OK() {
		t.Fatalf("rows = %+v, want one accepted row", rows)
	}

	var meter masterdata.Meter
	for _, m := range updated.Meters {
		if m.Number == "10004711" {
			meter = m
		}
	}
	if meter.AESKey == ([16]byte{}) {
		t.Error("the key was not taken over")
	}
	if meter.KCFactor != 1.2 || meter.FreezeMonth != 7 {
		t.Errorf("meter = %+v, want kc and Stichtag untouched by a file that does not mention them", meter)
	}
	if len(updated.Meters) != len(md.Meters) {
		t.Errorf("meters = %d, want %d — a row missing from the file is not a deletion", len(updated.Meters), len(md.Meters))
	}
}

// TestImportNeverDeletes is the other half of F-1: the file is not the
// new whole truth. A spreadsheet that was accidentally filtered must not
// take half the installation with it.
func TestImportNeverDeletes(t *testing.T) {
	md := csvTestData(t)
	records, _, _ := parseCSV("Wohnung;Bezeichnung\n1;Erdgeschoss links")
	updated, _ := applyRecords(md, "units", records)

	if len(updated.Units) != 1 || updated.Units[0].Name != "Erdgeschoss links" {
		t.Fatalf("units = %+v, want the one unit renamed", updated.Units)
	}
	if updated.Units[0].AreaM2 != 62.4 {
		t.Errorf("area = %v, want it untouched", updated.Units[0].AreaM2)
	}
	if len(updated.MeterPoints) != 3 || len(updated.Meters) != 2 {
		t.Error("importing into one table must not touch another")
	}
}

// TestImportPlacesAFreeMeterButNeverMovesOne is 6.3: a device may be put
// at a meter point that has none, because that is a first assignment. It
// may not be moved from one to another, because that is a swap — and a
// swap needs the outgoing device's end reading, which no list of values
// carries.
func TestImportPlacesAFreeMeterButNeverMovesOne(t *testing.T) {
	md := csvTestData(t)

	records, _, _ := parseCSV("Zählernummer;Zählerplatz\n10009999;102")
	updated, rows := applyRecords(md, "meters", records)
	if !rows[0].OK() {
		t.Fatalf("placing a new device at a free meter point must work: %s", rows[0].Problem)
	}
	if len(updated.Meters) != 3 {
		t.Errorf("meters = %d, want the new one added", len(updated.Meters))
	}

	_, rows = applyRecords(md, "meters", mustRecords(t, "Zählernummer;Zählerplatz\n10004711;102"))
	if rows[0].OK() || !strings.Contains(rows[0].Problem, "Zählerwechsel") {
		t.Errorf("moving a placed device = %+v, want a refusal that names the swap", rows[0])
	}

	_, rows = applyRecords(md, "meters", mustRecords(t, "Zählernummer;Zählerplatz\n10009999;101"))
	if rows[0].OK() || !strings.Contains(rows[0].Problem, "10004711") {
		t.Errorf("placing onto an occupied meter point = %+v, want a refusal that names the occupant", rows[0])
	}

	_, rows = applyRecords(md, "meters", mustRecords(t, "Zählernummer;kc-Faktor\n10009999;1,4"))
	if rows[0].OK() || !strings.Contains(rows[0].Problem, "Zählerplatz") {
		t.Errorf("a new device without a meter point = %+v, want a refusal that says so", rows[0])
	}
}

func mustRecords(t *testing.T, csv string) []csvRecord {
	t.Helper()
	records, _, msg := parseCSV(csv)
	if msg != "" {
		t.Fatalf("parseCSV: %s", msg)
	}
	return records
}

// TestCSVColumnsAreRecognizedLoosely: a file that travelled through a
// spreadsheet rarely keeps its spelling, and the order of columns is
// nobody's promise.
func TestCSVColumnsAreRecognizedLoosely(t *testing.T) {
	records, unknown, msg := parseCSV("KC;zaehlernummer;AES Schlüssel;Bemerkung\n1,4;10004711;;egal")
	if msg != "" {
		t.Fatal(msg)
	}
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Values["number"] != "10004711" || records[0].Values["kc"] != "1,4" {
		t.Errorf("values = %+v, want the columns recognized whatever their spelling and order", records[0].Values)
	}
	if !records[0].has("aes") {
		t.Error("an empty cell in a column the file carries still means: set this to empty")
	}
	if len(unknown) != 1 || unknown[0] != "Bemerkung" {
		t.Errorf("unknown = %v, want the one column nobody asked for to be named", unknown)
	}
}

// TestImportPreviewWritesNothing: the preview is a preview.
func TestImportPreviewWritesNothing(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	before, _ := app.Vault.Get()
	body := post(t, client, srvURL+"/operator/masterdata/import", url.Values{
		"csrf_token": {csrf},
		"table":      {"meters"},
		"csv":        {"Zählernummer;kc-Faktor\n10004711;1,4"},
	})
	if !strings.Contains(body, "es ist noch nichts geschrieben") {
		t.Errorf("expected the preview, got: %s", firstLines(body))
	}

	after, _ := app.Vault.Get()
	for i := range after.Meters {
		if after.Meters[i].KCFactor != before.Meters[i].KCFactor {
			t.Fatalf("the preview changed meter %s", after.Meters[i].Number)
		}
	}
}

func TestImportApplyWrites(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	post(t, client, srvURL+"/operator/masterdata/import/apply", url.Values{
		"csrf_token": {csrf},
		"table":      {"meters"},
		"csv":        {"Zählernummer;kc-Faktor;Stichtagsmonat\n10004711;1,4;12"},
	})

	md, _ := app.Vault.Get()
	for _, m := range md.Meters {
		if m.Number != "10004711" {
			continue
		}
		if m.KCFactor != 1.4 || m.FreezeMonth != 12 {
			t.Errorf("meter after import = %+v, want kc 1.4 and Stichtag 12", m)
		}
	}
}

// TestRefusedImportRowsLeaveNothingBehind is the defect this test was
// written for: every apply*Record used to write into the master data as
// it went and only afterwards decide that the row was no good. The
// preview showed ✕, and the row landed anyway — a meter point with
// default values, a unit without an area, a meter nobody asked for.
func TestRefusedImportRowsLeaveNothingBehind(t *testing.T) {
	md := csvTestData(t)

	cases := []struct {
		name  string
		table string
		csv   string
	}{
		{"Zählerplatz mit unbekannter Wohnung", "meterpoints",
			"Zählerplatz;Wohnung;Typ\n909;gibtsnicht;HKV"},
		{"neuer Zählerplatz ohne Typ", "meterpoints",
			"Zählerplatz;Raum\n909;Flur"},
		{"neue Wohnung ohne Fläche", "units",
			"Wohnung;Bezeichnung\n9;Dachgeschoss"},
		{"neue Wohnung mit Fläche 0", "units",
			"Wohnung;Bezeichnung;Fläche\n9;Dachgeschoss;0"},
		{"neuer Zähler mit unlesbarem Schlüssel", "meters",
			"Zählernummer;Zählerplatz;AES-Schlüssel\n10009999;102;keinhex"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			updated, rows := applyRecords(md, c.table, mustRecords(t, c.csv))
			if len(rows) != 1 || rows[0].OK() {
				t.Fatalf("rows = %+v, want exactly one refusal", rows)
			}
			if len(updated.Units) != len(md.Units) {
				t.Errorf("units = %+v, want them untouched", updated.Units)
			}
			if len(updated.MeterPoints) != len(md.MeterPoints) {
				t.Errorf("meter points = %+v, want them untouched", updated.MeterPoints)
			}
			if len(updated.Meters) != len(md.Meters) {
				t.Errorf("meters = %+v, want them untouched", updated.Meters)
			}
		})
	}
}

// TestImportKeepsGoingAfterARefusedRow: one bad line does not cost the
// whole file. A supplier's list of forty keys is worth having even if one
// row names a device nobody entered yet.
func TestImportKeepsGoingAfterARefusedRow(t *testing.T) {
	md := csvTestData(t)
	updated, rows := applyRecords(md, "meters", mustRecords(t,
		"Zählernummer;kc-Faktor\n99999999;1,4\n10004711;1,5"))

	if len(rows) != 2 || rows[0].OK() || !rows[1].OK() {
		t.Fatalf("rows = %+v, want the unknown number refused and the known one taken", rows)
	}
	for _, m := range updated.Meters {
		if m.Number == "10004711" && m.KCFactor != 1.5 {
			t.Errorf("kc = %v, want the second line to have been applied", m.KCFactor)
		}
	}
}
