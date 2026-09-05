package archive

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/telegram"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustDay(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func TestInsertHistoricalIdempotent(t *testing.T) {
	s := openTestStore(t)
	day := mustDay(t, "2024-01-31")
	e := Entry{MeterID: "m", Day: day, ReceivedAt: time.Date(2024, 1, 31, 23, 55, 0, 0, telegram.Local), RSSI: -80, RawHex: "aa"}

	changed, err := s.InsertHistorical(e)
	if err != nil || !changed {
		t.Fatalf("first InsertHistorical: changed=%v err=%v", changed, err)
	}
	changed, err = s.InsertHistorical(e)
	if err != nil || changed {
		t.Fatalf("repeated InsertHistorical of the same entry: changed=%v err=%v, want changed=false err=nil", changed, err)
	}
}

func TestInsertHistoricalConflict(t *testing.T) {
	s := openTestStore(t)
	day := mustDay(t, "2024-01-31")
	e := Entry{MeterID: "m", Day: day, ReceivedAt: time.Date(2024, 1, 31, 23, 55, 0, 0, telegram.Local), RSSI: -80, RawHex: "aa"}
	if _, err := s.InsertHistorical(e); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	conflicting := e
	conflicting.RawHex = "different"
	_, err := s.InsertHistorical(conflicting)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("InsertHistorical of conflicting data: err = %v, want ErrConflict", err)
	}
}

func TestCorrectOverridesArchivedEntry(t *testing.T) {
	s := openTestStore(t)
	pastDay := mustDay(t, "2025-06-10")

	original := Entry{MeterID: "m", Day: pastDay, ReceivedAt: time.Date(2025, 6, 10, 23, 55, 0, 0, telegram.Local), RSSI: -80, RawHex: "wrong"}
	if _, err := s.InsertHistorical(original); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	corrected := original
	corrected.RawHex = "corrected"
	if err := s.Correct(corrected); err != nil {
		t.Fatalf("Correct: %v", err)
	}

	got, _, err := s.Get("m", pastDay)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RawHex != "corrected" {
		t.Errorf("RawHex after Correct = %q, want %q", got.RawHex, "corrected")
	}
}

func TestAllOrdersByDay(t *testing.T) {
	s := openTestStore(t)
	days := []string{"2025-03-01", "2025-01-01", "2025-02-01"}
	for _, d := range days {
		day := mustDay(t, d)
		if _, err := s.InsertHistorical(Entry{MeterID: "m", Day: day, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aa"}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}
	entries, err := s.All("m")
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []telegram.Day{"2025-01-01", "2025-02-01", "2025-03-01"}
	for i, e := range entries {
		if e.Day != want[i] {
			t.Errorf("entries[%d].Day = %s, want %s", i, e.Day, want[i])
		}
	}
}

func TestLastDayAtOrBefore(t *testing.T) {
	s := openTestStore(t)
	for _, d := range []string{"2025-01-01", "2025-02-01", "2025-03-01"} {
		day := mustDay(t, d)
		if _, err := s.InsertHistorical(Entry{MeterID: "m", Day: day, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aa"}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	if got, found, err := s.LastDayAtOrBefore("m", mustDay(t, "2025-12-31")); err != nil || !found || got != mustDay(t, "2025-03-01") {
		t.Errorf("LastDayAtOrBefore(distant future) = (%s, %v, %v), want (2025-03-01, true, nil)", got, found, err)
	}
	if got, found, err := s.LastDayAtOrBefore("m", mustDay(t, "2025-02-15")); err != nil || !found || got != mustDay(t, "2025-02-01") {
		t.Errorf("LastDayAtOrBefore(mid-range) = (%s, %v, %v), want (2025-02-01, true, nil)", got, found, err)
	}
	if _, found, err := s.LastDayAtOrBefore("m", mustDay(t, "2024-12-31")); err != nil || found {
		t.Errorf("LastDayAtOrBefore(before any entry) found=%v err=%v, want found=false", found, err)
	}
	if _, found, err := s.LastDayAtOrBefore("nonexistent", mustDay(t, "2025-12-31")); err != nil || found {
		t.Errorf("LastDayAtOrBefore(unknown meter) found=%v err=%v, want found=false", found, err)
	}
}

func TestNearestDay(t *testing.T) {
	s := openTestStore(t)
	for _, d := range []string{"2025-01-01", "2025-01-10", "2025-01-20"} {
		day := mustDay(t, d)
		if _, err := s.InsertHistorical(Entry{MeterID: "m", Day: day, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aa"}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	if got, found, err := s.NearestDay("m", mustDay(t, "2025-01-10")); err != nil || !found || got != mustDay(t, "2025-01-10") {
		t.Errorf("NearestDay(exact match) = (%s, %v, %v), want (2025-01-10, true, nil)", got, found, err)
	}
	if got, found, err := s.NearestDay("m", mustDay(t, "2025-01-04")); err != nil || !found || got != mustDay(t, "2025-01-01") {
		t.Errorf("NearestDay(closer to earlier) = (%s, %v, %v), want (2025-01-01, true, nil)", got, found, err)
	}
	if got, found, err := s.NearestDay("m", mustDay(t, "2025-01-16")); err != nil || !found || got != mustDay(t, "2025-01-20") {
		t.Errorf("NearestDay(closer to later) = (%s, %v, %v), want (2025-01-20, true, nil)", got, found, err)
	}
	// Exactly between 2025-01-01 and 2025-01-10 (2025-01-05, 5 days from
	// each): ties prefer the earlier candidate.
	if got, found, err := s.NearestDay("m", mustDay(t, "2025-01-05")); err != nil || !found || got != mustDay(t, "2025-01-01") {
		t.Errorf("NearestDay(tie) = (%s, %v, %v), want (2025-01-01, true, nil): ties prefer the earlier day", got, found, err)
	}
	if got, found, err := s.NearestDay("m", mustDay(t, "2025-02-01")); err != nil || !found || got != mustDay(t, "2025-01-20") {
		t.Errorf("NearestDay(after every entry) = (%s, %v, %v), want (2025-01-20, true, nil)", got, found, err)
	}
	if _, found, err := s.NearestDay("nonexistent", mustDay(t, "2025-01-10")); err != nil || found {
		t.Errorf("NearestDay(unknown meter) found=%v err=%v, want found=false", found, err)
	}
}
