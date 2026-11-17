package archive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/telegram"
)

// exportFixture is an archive with one entry before, two inside and one
// after the range 2025-01-01 … 2025-01-31.
func exportFixture(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	at := time.Date(2025, 1, 15, 23, 55, 0, 0, telegram.Local)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2024-12-31"), ReceivedAt: at, RSSI: -70, RawHex: "a0"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-15"), ReceivedAt: at, RSSI: -71, RawHex: "a1"},
		{MeterID: "m2", Day: mustDay(t, "2025-01-31"), ReceivedAt: at, RSSI: -72, RawHex: "b1"},
		{MeterID: "m2", Day: mustDay(t, "2025-02-01"), ReceivedAt: at, RSSI: -73, RawHex: "b2"},
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func assertJanuaryOnly(t *testing.T, dest *Store, report MigrationReport) {
	t.Helper()
	if report.EntriesInserted != 2 || len(report.Issues) != 0 {
		t.Errorf("report = %+v, want 2 inserted and no issues", report)
	}
	all, err := dest.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("imported = %+v, want the two January entries", all)
	}
	for _, e := range all {
		if e.Day < "2025-01-01" || e.Day > "2025-01-31" {
			t.Errorf("entry from %s came along", e.Day)
		}
		if e.RSSI > -71 || e.ReceivedAt.IsZero() {
			t.Errorf("entry %+v lost its values on the way", e)
		}
	}
}

// TestExportRangeRoundTripsThroughImport: the .db download is a file the
// import takes back as it is, with exactly the range in it.
func TestExportRangeRoundTripsThroughImport(t *testing.T) {
	source := exportFixture(t)
	path := filepath.Join(t.TempDir(), "excerpt.db")
	n, err := source.ExportRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"), path)
	if err != nil || n != 2 {
		t.Fatalf("ExportRange = %d, %v; want 2 entries", n, err)
	}
	for _, sidecar := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + sidecar); err == nil {
			t.Errorf("%s%s left behind: the download must be one self-contained file", path, sidecar)
		}
	}

	dest := openTestStore(t)
	report, err := ImportFile(dest, path)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	assertJanuaryOnly(t, dest, report)
}

func TestExportRangeRefusesAnExistingFile(t *testing.T) {
	source := exportFixture(t)
	path := filepath.Join(t.TempDir(), "excerpt.db")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExportRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"), path); err == nil {
		t.Error("ExportRange over an existing file: expected an error")
	}
}

// TestJSONRoundTripsThroughImport: the .json download, kept as text, can
// be brought back too.
func TestJSONRoundTripsThroughImport(t *testing.T) {
	source := exportFixture(t)
	entries, err := source.Range(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, entries); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "excerpt.json")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := openTestStore(t)
	report, err := ImportFile(dest, path)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	assertJanuaryOnly(t, dest, report)

	// The same file again changes nothing: same rules as for a database.
	again, err := ImportFile(dest, path)
	if err != nil || again.EntriesInserted != 0 || again.EntriesUnchanged != 2 {
		t.Errorf("second import = %+v, %v; want 2 unchanged", again, err)
	}
}

// TestJSONImportReportsMalformedEntries: a hand-edited text file may lose
// a field; that entry is named in the report, the rest still goes in.
func TestJSONImportReportsMalformedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edited.json")
	content := `[
  {"meter_id": "m1", "day": "2025-01-15", "received_at": "2025-01-15T23:55:00+01:00", "rssi": -70, "raw_hex": "aa"},
  {"meter_id": "m2", "day": "15.01.2025", "received_at": "2025-01-15T23:55:00+01:00", "rssi": -70, "raw_hex": "bb"},
  {"meter_id": "m3", "day": "2025-01-15", "received_at": "2025-01-15T23:55:00+01:00", "rssi": -70, "raw_hex": "zz"}
]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := openTestStore(t)
	report, err := ImportFile(dest, path)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if report.EntriesInserted != 1 || len(report.Issues) != 2 {
		t.Errorf("report = %+v, want 1 inserted and 2 issues", report)
	}
}

func TestImportFileRejectsUnknownFormat(t *testing.T) {
	for name, content := range map[string]string{
		"text.txt":    "this is neither",
		"object.json": `{"meter_id": "m1"}`,
		"broken.json": `[{"meter_id": `,
	} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportFile(openTestStore(t), path); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%s: err = %v, want ErrUnknownFormat", name, err)
		}
	}
}
