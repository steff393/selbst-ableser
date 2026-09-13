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

// TestCalculateMonthSwapAfterAFreeze covers what used to be refused as
// undefined. In the first month of a billing period both devices count
// from zero, so the month is the outgoing meter's whole reading plus what
// the incoming one has counted since it was installed — the previous
// month's value plays no part, which is exactly why it used to be
// ambiguous and no longer is.
func TestCalculateMonthSwapAfterAFreeze(t *testing.T) {
	prev := MonthlyValue{Meter: "A", Day: mustDay(t, "2025-12-31"), Value: 4180}
	cur := MonthlyValue{Meter: "B", Day: mustDay(t, "2026-01-31"), Value: 30}
	swap := &SwapCorrection{OutgoingMeter: "A", EndReading: 12, IncomingMeter: "B", StartReading: 0}

	res, err := CalculateMonth("2026-01", prev, cur, swap, true, 1)
	if err != nil {
		t.Fatalf("CalculateMonth: %v", err)
	}
	if res.RawConsumption != 42 { // 12 on the old device, 30 on the new one
		t.Errorf("RawConsumption = %v, want 42", res.RawConsumption)
	}
	if !res.BillingReset || res.Swap == nil {
		t.Errorf("result should record both the reset and the swap: %+v", res)
	}

	// The outgoing meter's own start reading is not subtracted: it began
	// the month at zero like every device in a new period.
	if res.RawConsumption == float64(cur.Value-swap.StartReading)+float64(swap.EndReading-prev.Value) {
		t.Error("the ordinary swap formula was used, ignoring the reset")
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
