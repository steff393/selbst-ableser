package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportFileCopiesEntriesIntoDest(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "backup.db")
	source, err := OpenStore(sourcePath)
	if err != nil {
		t.Fatalf("OpenStore(source): %v", err)
	}
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"},
		{MeterID: "m2", Day: mustDay(t, "2025-01-02"), RawHex: "bb"},
	} {
		if _, err := source.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}
	source.Close()

	dest := openTestStore(t)
	report, err := ImportFile(dest, sourcePath)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if report.EntriesInserted != 2 {
		t.Errorf("EntriesInserted = %d, want 2", report.EntriesInserted)
	}
	if len(report.Issues) != 0 {
		t.Errorf("unexpected issues: %+v", report.Issues)
	}

	if _, found, err := dest.Get("m1", mustDay(t, "2025-01-01")); err != nil || !found {
		t.Errorf("m1/2025-01-01 not imported: found=%v err=%v", found, err)
	}
	if _, found, err := dest.Get("m2", mustDay(t, "2025-01-02")); err != nil || !found {
		t.Errorf("m2/2025-01-02 not imported: found=%v err=%v", found, err)
	}
}

func TestImportFileIsRepeatable(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "backup.db")
	source, err := OpenStore(sourcePath)
	if err != nil {
		t.Fatalf("OpenStore(source): %v", err)
	}
	if _, err := source.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}
	source.Close()

	dest := openTestStore(t)
	if _, err := ImportFile(dest, sourcePath); err != nil {
		t.Fatalf("first ImportFile: %v", err)
	}
	second, err := ImportFile(dest, sourcePath)
	if err != nil {
		t.Fatalf("second ImportFile: %v", err)
	}
	if second.EntriesInserted != 0 || second.EntriesUnchanged != 1 {
		t.Errorf("second run = %+v, want 0 inserted, 1 unchanged", second)
	}
}

func TestImportFileRecordsConflictsWithoutOverwriting(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "backup.db")
	source, err := OpenStore(sourcePath)
	if err != nil {
		t.Fatalf("OpenStore(source): %v", err)
	}
	if _, err := source.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}
	source.Close()

	dest := openTestStore(t)
	if _, err := dest.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "different"}); err != nil {
		t.Fatal(err)
	}

	report, err := ImportFile(dest, sourcePath)
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if len(report.Issues) != 1 {
		t.Fatalf("got %d issues, want 1 (the conflicting entry)", len(report.Issues))
	}
	if !errors.Is(report.Issues[0].Err, ErrConflict) {
		t.Errorf("issue error = %v, want ErrConflict", report.Issues[0].Err)
	}

	got, _, err := dest.Get("m1", mustDay(t, "2025-01-01"))
	if err != nil {
		t.Fatal(err)
	}
	if got.RawHex != "different" {
		t.Errorf("existing entry was overwritten: RawHex = %q, want %q", got.RawHex, "different")
	}
}

func TestImportFileRejectsNonDatabaseFile(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(badPath, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := openTestStore(t)
	if _, err := ImportFile(dest, badPath); err == nil {
		t.Error("ImportFile on a non-database file: expected an error")
	}
}
