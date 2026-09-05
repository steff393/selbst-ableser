package telegram

import "testing"

func TestDayAddDays(t *testing.T) {
	d := mustParseDayForTest(t, "2025-01-31")
	if got := d.AddDays(-1); got != "2025-01-30" {
		t.Errorf("AddDays(-1) = %s, want 2025-01-30", got)
	}
	if got := d.AddDays(1); got != "2025-02-01" {
		t.Errorf("AddDays(1) = %s, want 2025-02-01", got)
	}
}

func TestDayDaysUntil(t *testing.T) {
	from := mustParseDayForTest(t, "2025-01-01")
	to := mustParseDayForTest(t, "2025-01-10")
	if got := from.DaysUntil(to); got != 9 {
		t.Errorf("DaysUntil = %d, want 9", got)
	}
	if got := to.DaysUntil(from); got != -9 {
		t.Errorf("DaysUntil (reverse) = %d, want -9", got)
	}
	if got := from.DaysUntil(from); got != 0 {
		t.Errorf("DaysUntil (same day) = %d, want 0", got)
	}
}

func TestDayMonth(t *testing.T) {
	if got := mustParseDayForTest(t, "2025-01-31").Month(); got != 1 {
		t.Errorf("Month() = %d, want 1", got)
	}
	if got := mustParseDayForTest(t, "2025-12-01").Month(); got != 12 {
		t.Errorf("Month() = %d, want 12", got)
	}
}

func mustParseDayForTest(t *testing.T, s string) Day {
	t.Helper()
	d, err := ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}
