package notify

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed
}

// TestWeeklyStatusDue: only within the sending hour on Monday — narrow on
// purpose, since nothing downstream reads the audit log to dedup a
// caught-up send anymore (see schedule.go's package-level note).
func TestWeeklyStatusDue(t *testing.T) {
	cases := []struct {
		when string
		want bool
	}{
		{"2026-08-24 09:59", false}, // Monday, before the hour
		{"2026-08-24 10:00", true},  // Monday, on the hour
		{"2026-08-24 10:59", true},  // Monday, still within the hour
		{"2026-08-24 11:00", false}, // Monday, just after the hour
		{"2026-08-26 10:00", false}, // Wednesday: right hour, wrong day
		{"2026-08-30 10:00", false}, // Sunday: same week, still wrong day
	}
	for _, c := range cases {
		if got := WeeklyStatusDue(at(t, c.when)); got != c.want {
			t.Errorf("WeeklyStatusDue(%s) = %v, want %v", c.when, got, c.want)
		}
	}
}

// The key must change from one week to the next and stay put within one,
// since that is what stops a caught-up run from sending twice.
func TestWeeklyStatusKeyIdentifiesTheWeek(t *testing.T) {
	monday := WeeklyStatusKey(at(t, "2026-08-24 10:00"))
	sunday := WeeklyStatusKey(at(t, "2026-08-30 23:00"))
	nextMonday := WeeklyStatusKey(at(t, "2026-08-31 10:00"))

	if monday != sunday {
		t.Errorf("same week gave different keys: %q vs %q", monday, sunday)
	}
	if monday == nextMonday {
		t.Errorf("consecutive weeks share the key %q", monday)
	}
}

// TestMonthlyReminderDue: only within the sending hour on the first of the
// month — see TestWeeklyStatusDue's note; the same reasoning applies here.
func TestMonthlyReminderDue(t *testing.T) {
	cases := []struct {
		when string
		want bool
	}{
		{"2026-09-01 09:59", false}, // the first, before the hour
		{"2026-09-01 10:00", true},
		{"2026-09-01 10:59", true},
		{"2026-09-01 11:00", false}, // the first, just after the hour
		{"2026-09-02 10:00", false}, // right hour, wrong day
		{"2026-09-30 10:00", false},
	}
	for _, c := range cases {
		if got := MonthlyReminderDue(at(t, c.when)); got != c.want {
			t.Errorf("MonthlyReminderDue(%s) = %v, want %v", c.when, got, c.want)
		}
	}
}
