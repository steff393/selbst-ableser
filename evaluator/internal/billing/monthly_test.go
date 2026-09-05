package billing

import "testing"

func TestMonthEnds(t *testing.T) {
	got := MonthEnds(mustDay(t, "2024-12-15"), mustDay(t, "2025-03-01"))
	want := []string{"2024-12-31", "2025-01-31", "2025-02-28", "2025-03-31"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if string(got[i]) != w {
			t.Errorf("got[%d] = %s, want %s", i, got[i], w)
		}
	}
}

func TestMonthEndsSingleMonth(t *testing.T) {
	got := MonthEnds(mustDay(t, "2025-02-01"), mustDay(t, "2025-02-28"))
	if len(got) != 1 || string(got[0]) != "2025-02-28" {
		t.Errorf("got %v, want [2025-02-28]", got)
	}
}

func TestMonthEndsLeapYear(t *testing.T) {
	got := MonthEnds(mustDay(t, "2024-02-01"), mustDay(t, "2024-02-01"))
	if len(got) != 1 || string(got[0]) != "2024-02-29" {
		t.Errorf("got %v, want [2024-02-29]", got)
	}
}
