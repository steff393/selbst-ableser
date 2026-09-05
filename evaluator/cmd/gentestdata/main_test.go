package main

import (
	"testing"
	"time"

	"selbst-ableser/internal/telegram"
)

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, telegram.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLastDayOfNextMonth(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-08-31", "2026-09-30"},
		{"2026-02-15", "2026-03-31"},
		{"2026-12-31", "2027-01-31"}, // year rollover
		{"2024-01-31", "2024-02-29"}, // leap year
		{"2025-01-31", "2025-02-28"}, // non-leap year
	}
	for _, c := range cases {
		got := lastDayOfNextMonth(day(c.in))
		if got.Format("2006-01-02") != c.want {
			t.Errorf("lastDayOfNextMonth(%s) = %s, want %s", c.in, got.Format("2006-01-02"), c.want)
		}
	}
}

func mv(d string, v int64) monthValue { return monthValue{day: day(d), value: v} }

func TestNextHKVValueResetsInJanuary(t *testing.T) {
	months := []monthValue{
		mv("2025-01-31", 50),
		mv("2025-12-31", 450), // a completed Jan->Dec cycle, total 400
	}
	for i := 0; i < 20; i++ {
		got := nextHKVValue(months, day("2026-01-31"))
		if got < 10 {
			t.Errorf("reset value %d below the floor of 10", got)
		}
		// 5-20% of the 400 cycle total, i.e. roughly 20-80.
		if got > 100 {
			t.Errorf("reset value %d implausibly high for a 400-unit cycle", got)
		}
	}
}

func TestNextHKVValueGrowsInWinterMoreThanSummer(t *testing.T) {
	// A steady mid-cycle history: the implied total is derived from the
	// most recent real increment and that month's own seasonal share.
	months := []monthValue{
		mv("2026-01-31", 40),
		mv("2026-07-31", 45), // July's own increment (5) implies a total around 5/0.013 ≈ 385
	}

	var augustSum, decemberSum int64
	const trials = 200
	for i := 0; i < trials; i++ {
		augustSum += nextHKVValue(months, day("2026-08-31")) - months[len(months)-1].value
		decemberSum += nextHKVValue(months, day("2026-12-31")) - months[len(months)-1].value
	}
	augustAvg := augustSum / trials
	decemberAvg := decemberSum / trials
	if decemberAvg <= augustAvg {
		t.Errorf("December's average increment (%d) should exceed August's (%d) — winter should add more than summer", decemberAvg, augustAvg)
	}
}

func TestNextHKVValueNeverDecreases(t *testing.T) {
	months := []monthValue{mv("2026-01-31", 40), mv("2026-06-30", 41)}
	for i := 0; i < 50; i++ {
		got := nextHKVValue(months, day("2026-07-31"))
		if got < months[len(months)-1].value {
			t.Errorf("nextHKVValue = %d, must not be below the last value %d", got, months[len(months)-1].value)
		}
	}
}

func TestNextWaterValueContinuesItsOwnAverageRate(t *testing.T) {
	// 12 months, +1000 per month on average.
	months := []monthValue{mv("2026-01-31", 10000), mv("2026-12-31", 21000)}
	for i := 0; i < 50; i++ {
		got := nextWaterValue(months)
		inc := got - months[len(months)-1].value
		if inc < 800 || inc > 1200 {
			t.Errorf("nextWaterValue increment = %d, want roughly 800-1200 (+/-20%% of 1000)", inc)
		}
	}
}

func TestNextWaterValueNeverDecreases(t *testing.T) {
	months := []monthValue{mv("2026-01-31", 5000)} // a single reading, no rate yet
	for i := 0; i < 50; i++ {
		got := nextWaterValue(months)
		if got <= months[len(months)-1].value {
			t.Errorf("nextWaterValue = %d, must be strictly above the last value %d", got, months[len(months)-1].value)
		}
	}
}
