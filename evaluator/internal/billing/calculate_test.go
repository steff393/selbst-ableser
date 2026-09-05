package billing

import (
	"testing"

	"selbst-ableser/internal/telegram"
)

func mustDay(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func TestCalculateMonthPlainDifference(t *testing.T) {
	prev := MonthlyValue{Meter: "M", Day: mustDay(t, "2025-01-31"), Value: 100}
	cur := MonthlyValue{Meter: "M", Day: mustDay(t, "2025-02-28"), Value: 130}

	res, err := CalculateMonth("2025-02", prev, cur, nil, false, 1)
	if err != nil {
		t.Fatalf("CalculateMonth: %v", err)
	}
	if res.Consumption != 30 {
		t.Errorf("Consumption = %v, want 30", res.Consumption)
	}
}

func TestCalculateMonthBillingReset(t *testing.T) {
	prev := MonthlyValue{Meter: "M", Day: mustDay(t, "2024-12-31"), Value: 380}
	cur := MonthlyValue{Meter: "M", Day: mustDay(t, "2025-01-31"), Value: 45}

	res, err := CalculateMonth("2025-01", prev, cur, nil, true, 1)
	if err != nil {
		t.Fatalf("CalculateMonth: %v", err)
	}
	if res.Consumption != 45 {
		t.Errorf("Consumption = %v, want 45 (previous treated as zero)", res.Consumption)
	}
}

func TestCalculateMonthSwap_SpecWorkedExample(t *testing.T) {
	// From the requirements' own worked example: meter A replaced by B
	// within March. Reading A on 28.02 = 480, documented end of A = 500,
	// documented start of B = 0, reading B on 31.03 = 30. Expected: 50.
	prev := MonthlyValue{Meter: "A", Day: mustDay(t, "2025-02-28"), Value: 480}
	cur := MonthlyValue{Meter: "B", Day: mustDay(t, "2025-03-31"), Value: 30}
	swap := &SwapCorrection{OutgoingMeter: "A", EndReading: 500, IncomingMeter: "B", StartReading: 0}

	res, err := CalculateMonth("2025-03", prev, cur, swap, false, 1)
	if err != nil {
		t.Fatalf("CalculateMonth: %v", err)
	}
	if res.Consumption != 50 {
		t.Errorf("Consumption = %v, want 50", res.Consumption)
	}
}

func TestCalculateMonthRejectsSwapPlusReset(t *testing.T) {
	prev := MonthlyValue{Meter: "A", Day: mustDay(t, "2025-12-31"), Value: 480}
	cur := MonthlyValue{Meter: "B", Day: mustDay(t, "2026-01-31"), Value: 30}
	swap := &SwapCorrection{OutgoingMeter: "A", EndReading: 500, IncomingMeter: "B", StartReading: 0}

	if _, err := CalculateMonth("2026-01", prev, cur, swap, true, 1); err == nil {
		t.Fatal("expected an error for swap combined with a billing-period reset")
	}
}

func TestCalculateMonthRejectsMismatchedSwapMeters(t *testing.T) {
	prev := MonthlyValue{Meter: "A", Day: mustDay(t, "2025-02-28"), Value: 480}
	cur := MonthlyValue{Meter: "B", Day: mustDay(t, "2025-03-31"), Value: 30}
	swap := &SwapCorrection{OutgoingMeter: "X", EndReading: 500, IncomingMeter: "Y", StartReading: 0}

	if _, err := CalculateMonth("2025-03", prev, cur, swap, false, 1); err == nil {
		t.Fatal("expected an error when the swap correction does not match the given readings' meters")
	}
}

func TestCalculateMonthAppliesKCFactor(t *testing.T) {
	prev := MonthlyValue{Meter: "M", Day: mustDay(t, "2025-01-31"), Value: 100}
	cur := MonthlyValue{Meter: "M", Day: mustDay(t, "2025-02-28"), Value: 130}

	res, err := CalculateMonth("2025-02", prev, cur, nil, false, 2.0)
	if err != nil {
		t.Fatalf("CalculateMonth: %v", err)
	}
	if res.RawConsumption != 30 || res.Consumption != 60 {
		t.Errorf("RawConsumption = %v, Consumption = %v, want 30 and 60", res.RawConsumption, res.Consumption)
	}
}
