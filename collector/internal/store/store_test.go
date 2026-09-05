package store

import (
	"testing"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

func day(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func TestOpenMemoryIsUsableAcrossCalls(t *testing.T) {
	// Regression guard for the well-known SQLite :memory: pitfall: each
	// new connection to ":memory:" gets its own empty database unless
	// pinned to a single connection (see Open's SetMaxOpenConns(1)).
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	e := Entry{MeterID: "90000001", Day: day(t, "2026-08-21"), ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}
	if err := s.Upsert(e); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.ForDay(e.Day)
	if err != nil {
		t.Fatalf("ForDay: %v", err)
	}
	if len(got) != 1 || got[0].MeterID != "90000001" {
		t.Fatalf("ForDay after Upsert = %+v, want one entry for 90000001", got)
	}
}

func TestUpsertReplacesSameMeterAndDay(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	d := day(t, "2026-08-21")
	first := Entry{MeterID: "90000001", Day: d, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aa"}
	second := Entry{MeterID: "90000001", Day: d, ReceivedAt: time.Now().Add(time.Minute), RSSI: -70, RawHex: "bb"}

	if err := s.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := s.Upsert(second); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	got, err := s.ForDay(d)
	if err != nil {
		t.Fatalf("ForDay: %v", err)
	}
	if len(got) != 1 || got[0].RawHex != "bb" || got[0].RSSI != -70 {
		t.Fatalf("ForDay = %+v, want a single row with the second (latest) values", got)
	}
}

func TestSinceReturnsOnlyEntriesReceivedAtOrAfterCutoff(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := Entry{MeterID: "1", Day: day(t, "2020-01-01"), ReceivedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, telegram.Local), RSSI: -80, RawHex: "aa"}
	if err := s.Upsert(old); err != nil {
		t.Fatalf("Upsert old: %v", err)
	}

	cutoff := time.Date(2020, 1, 1, 12, 0, 0, 0, telegram.Local)

	recent := Entry{MeterID: "2", Day: day(t, "2020-01-02"), ReceivedAt: time.Date(2020, 1, 2, 0, 0, 0, 0, telegram.Local), RSSI: -80, RawHex: "bb"}
	if err := s.Upsert(recent); err != nil {
		t.Fatalf("Upsert recent: %v", err)
	}

	got, err := s.Since(cutoff)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 1 || got[0].MeterID != "2" {
		t.Fatalf("Since(cutoff) = %+v, want only meter 2 (received after cutoff)", got)
	}
}

func TestDeleteDayOnlyRemovesThatDay(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	d1, d2 := day(t, "2026-08-20"), day(t, "2026-08-21")
	if err := s.Upsert(Entry{MeterID: "1", Day: d1, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(Entry{MeterID: "1", Day: d2, ReceivedAt: time.Now(), RSSI: -80, RawHex: "bb"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDay(d1); err != nil {
		t.Fatalf("DeleteDay: %v", err)
	}

	days, err := s.Days()
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(days) != 1 || days[0] != d2 {
		t.Fatalf("Days after deleting %s = %v, want only %s", d1, days, d2)
	}
}
