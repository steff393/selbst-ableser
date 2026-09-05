package archive

import (
	"path/filepath"
	"testing"
)

func TestStats(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertHistorical(Entry{MeterID: "m2", Day: mustDay(t, "2025-01-04"), RawHex: "bb"}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.Stats(mustDay(t, "2025-01-05"))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2", stats.TotalEntries)
	}
	if stats.EarliestDay != mustDay(t, "2025-01-01") || stats.LatestDay != mustDay(t, "2025-01-04") {
		t.Errorf("range = [%s, %s], want [2025-01-01, 2025-01-04]", stats.EarliestDay, stats.LatestDay)
	}
	if stats.MetersYesterday != 1 {
		t.Errorf("MetersYesterday = %d, want 1", stats.MetersYesterday)
	}
}

func TestStatsEmptyArchive(t *testing.T) {
	s := openTestStore(t)
	stats, err := s.Stats(mustDay(t, "2025-01-05"))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalEntries != 0 || stats.EarliestDay != "" {
		t.Errorf("expected a zero-valued Stats, got %+v", stats)
	}
}

func TestRange(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"},
		{MeterID: "m2", Day: mustDay(t, "2025-01-15"), RawHex: "bb"},
		{MeterID: "m1", Day: mustDay(t, "2025-02-01"), RawHex: "cc"},
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Range(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"))
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (the January ones only)", len(got))
	}
	for _, e := range got {
		if e.Day > "2025-01-31" || e.Day < "2025-01-01" {
			t.Errorf("entry %+v is outside the requested range", e)
		}
	}
}

func TestDeleteRangeRemovesOnlyEntriesInRangeAcrossAllMeters(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"},
		{MeterID: "m2", Day: mustDay(t, "2025-01-15"), RawHex: "bb"},
		{MeterID: "m1", Day: mustDay(t, "2025-02-01"), RawHex: "cc"},
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.DeleteRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"))
	if err != nil {
		t.Fatalf("DeleteRange: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (both January entries, across both meters)", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Day != mustDay(t, "2025-02-01") {
		t.Fatalf("remaining = %+v, want only the February entry", remaining)
	}
}

func TestDeleteRangeOnEmptyRangeDeletesNothing(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteRange(mustDay(t, "2030-01-01"), mustDay(t, "2030-01-31"))
	if err != nil {
		t.Fatalf("DeleteRange: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("remaining = %+v, want the original entry untouched", remaining)
	}
}

func TestCompressRangeKeepsOnlyTheMonthEndReadingWithinLookback(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-05"), RawHex: "aa"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-15"), RawHex: "bb"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-29"), RawHex: "cc"}, // within 5 days of month-end
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.CompressRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"), 5)
	if err != nil {
		t.Fatalf("CompressRange: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Day != mustDay(t, "2025-01-29") {
		t.Fatalf("remaining = %+v, want only the 2025-01-29 entry", remaining)
	}
}

func TestCompressRangeLeavesAMonthUntouchedWithoutAReadingInTheLookbackWindow(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-05"), RawHex: "aa"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-15"), RawHex: "bb"}, // 16 days before month-end, outside lookback
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.CompressRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"), 5)
	if err != nil {
		t.Fatalf("CompressRange: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0: no entry within the lookback window of month-end", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %+v, want both entries untouched", remaining)
	}
}

func TestCompressRangeSkipsPartialMonthsAtTheEdges(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-10"), RawHex: "aa"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-31"), RawHex: "bb"},
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	// The range starts mid-January, so January is only partially covered
	// and must be left alone even though it has a qualifying month-end entry.
	deleted, err := s.CompressRange(mustDay(t, "2025-01-15"), mustDay(t, "2025-02-28"), 5)
	if err != nil {
		t.Fatalf("CompressRange: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0: January is only partially in range", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %+v, want both January entries untouched", remaining)
	}
}

func TestCompressRangeIsPerMeter(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-10"), RawHex: "aa"},
		{MeterID: "m1", Day: mustDay(t, "2025-01-30"), RawHex: "bb"}, // m1: qualifying month-end entry
		{MeterID: "m2", Day: mustDay(t, "2025-01-10"), RawHex: "cc"}, // m2: no month-end entry at all
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.CompressRange(mustDay(t, "2025-01-01"), mustDay(t, "2025-01-31"), 5)
	if err != nil {
		t.Fatalf("CompressRange: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (m1's 2025-01-10 only)", deleted)
	}

	remaining, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %+v, want m1's 2025-01-30 and m2's untouched 2025-01-10", remaining)
	}
}

func TestAllEntries(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []Entry{
		{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"},
		{MeterID: "m2", Day: mustDay(t, "2025-01-15"), RawHex: "bb"},
		{MeterID: "m1", Day: mustDay(t, "2026-02-01"), RawHex: "cc"},
	} {
		if _, err := s.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (every meter and day, unrestricted)", len(got))
	}
}

func TestBackupTo(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aabb"}); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := s.BackupTo(dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	restored, err := OpenStore(dest)
	if err != nil {
		t.Fatalf("OpenStore(backup): %v", err)
	}
	defer restored.Close()

	got, found, err := restored.Get("m1", mustDay(t, "2025-01-01"))
	if err != nil || !found || got.RawHex != "aabb" {
		t.Errorf("restored entry = %+v, found=%v, err=%v", got, found, err)
	}
}

func TestLastSeen(t *testing.T) {
	s := openTestStore(t)
	if _, ok, _ := s.LastSeen("never"); ok {
		t.Error("a meter that never sent anything should report ok=false")
	}

	if _, err := s.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-01-01"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertHistorical(Entry{MeterID: "m1", Day: mustDay(t, "2025-02-01"), RawHex: "bb"}); err != nil {
		t.Fatal(err)
	}
	day, ok, err := s.LastSeen("m1")
	if err != nil || !ok || day != mustDay(t, "2025-02-01") {
		t.Errorf("LastSeen = %s, ok=%v, err=%v, want 2025-02-01", day, ok, err)
	}
}
