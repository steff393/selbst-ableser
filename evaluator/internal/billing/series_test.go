package billing

import (
	"testing"

	"selbst-ableser/internal/telegram"
)

// TestCalculateSeries_AcceptanceScenario1 reproduces the accepted example
// calculation for a heat-cost-allocator meter point running continuously
// for 14 months across a billing-period reset at the turn of the year
// (D.3's acceptance criterion for the whole calculation chain).
func TestCalculateSeries_AcceptanceScenario1(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "90000001", Day: mustDay(t, "2024-12-31"), Value: 380},
		{Meter: "90000001", Day: mustDay(t, "2025-01-31"), Value: 45},
		{Meter: "90000001", Day: mustDay(t, "2025-02-28"), Value: 130},
		{Meter: "90000001", Day: mustDay(t, "2025-03-31"), Value: 210},
		{Meter: "90000001", Day: mustDay(t, "2025-04-30"), Value: 245},
		{Meter: "90000001", Day: mustDay(t, "2025-05-31"), Value: 255},
		{Meter: "90000001", Day: mustDay(t, "2025-06-30"), Value: 258},
		{Meter: "90000001", Day: mustDay(t, "2025-07-31"), Value: 260},
		{Meter: "90000001", Day: mustDay(t, "2025-08-31"), Value: 262},
		{Meter: "90000001", Day: mustDay(t, "2025-09-30"), Value: 268},
		{Meter: "90000001", Day: mustDay(t, "2025-10-31"), Value: 300},
		{Meter: "90000001", Day: mustDay(t, "2025-11-30"), Value: 360},
		{Meter: "90000001", Day: mustDay(t, "2025-12-31"), Value: 430},
		{Meter: "90000001", Day: mustDay(t, "2026-01-31"), Value: 50},
	}
	wantConsumption := []float64{45, 85, 80, 35, 10, 3, 2, 2, 6, 32, 60, 70, 50}
	wantResets := map[string]bool{"2025-01": true, "2026-01": true}

	results, err := CalculateSeries(readings, true, constantResetMonth(1), constantKC(1), noSwaps)
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	if len(results) != len(wantConsumption) {
		t.Fatalf("got %d results, want %d", len(results), len(wantConsumption))
	}
	for i, res := range results {
		if res.Consumption != wantConsumption[i] {
			t.Errorf("month %s: Consumption = %v, want %v", res.Month, res.Consumption, wantConsumption[i])
		}
		if res.BillingReset != wantResets[res.Month] {
			t.Errorf("month %s: BillingReset = %v, want %v", res.Month, res.BillingReset, wantResets[res.Month])
		}
	}

	// Control sum from the spec: consumption for Jan-Dec 2025 must equal
	// the December reading exactly, since the year started at zero.
	var sum2025 float64
	for i, res := range results {
		if res.Month >= "2025-01" && res.Month <= "2025-12" {
			sum2025 += res.Consumption
			_ = i
		}
	}
	if sum2025 != 430 {
		t.Errorf("sum of 2025 consumption = %v, want 430", sum2025)
	}
}

// TestCalculateSeries_AcceptanceScenario2 reproduces the accepted example
// for a meter replacement within a billing month.
func TestCalculateSeries_AcceptanceScenario2(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "90000010", Day: mustDay(t, "2025-01-31"), Value: 120},
		{Meter: "90000011", Day: mustDay(t, "2025-02-28"), Value: 8},
		{Meter: "90000011", Day: mustDay(t, "2025-03-31"), Value: 45},
	}
	swap := SwapCorrection{OutgoingMeter: "90000010", EndReading: 150, IncomingMeter: "90000011", StartReading: 0}
	swapLookup := func(outgoing, incoming string) (SwapCorrection, bool) {
		if outgoing == swap.OutgoingMeter && incoming == swap.IncomingMeter {
			return swap, true
		}
		return SwapCorrection{}, false
	}

	results, err := CalculateSeries(readings, false, constantResetMonth(1), constantKC(1), swapLookup)
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Month != "2025-02" || results[0].Consumption != 38 {
		t.Errorf("February: %+v, want consumption 38", results[0])
	}
	if results[0].Swap == nil {
		t.Error("February result should record the swap")
	}
	if results[1].Month != "2025-03" || results[1].Consumption != 37 {
		t.Errorf("March: %+v, want consumption 37", results[1])
	}
	if results[1].Swap != nil {
		t.Error("March result should not record a swap")
	}
}

// TestCalculateSeries_ConfigurableResetMonth checks that the billing-period
// reset (FACH-03) fires in whatever month resetMonth resolves to for the
// meter, not just January — a per-meter reset date (Stichtag) rather than a
// fixed one.
func TestCalculateSeries_ConfigurableResetMonth(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "M", Day: mustDay(t, "2025-05-31"), Value: 400},
		{Meter: "M", Day: mustDay(t, "2025-06-30"), Value: 30},
		{Meter: "M", Day: mustDay(t, "2025-07-31"), Value: 50},
	}

	results, err := CalculateSeries(readings, true, constantResetMonth(6), constantKC(1), noSwaps)
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Month != "2025-06" || !results[0].BillingReset || results[0].Consumption != 30 {
		t.Errorf("June: %+v, want a reset with consumption 30", results[0])
	}
	if results[1].Month != "2025-07" || results[1].BillingReset || results[1].Consumption != 20 {
		t.Errorf("July: %+v, want no reset, consumption 20", results[1])
	}
}

func TestCalculateSeries_GapCarriesForwardWithoutMarking(t *testing.T) {
	// February is missing entirely (FACH-08, Variante A): the March
	// result simply spans January through March, with nothing to say a
	// month was skipped.
	readings := []MonthlyValue{
		{Meter: "M", Day: mustDay(t, "2025-01-31"), Value: 10},
		{Meter: "M", Day: mustDay(t, "2025-03-31"), Value: 25},
	}
	results, err := CalculateSeries(readings, false, constantResetMonth(1), constantKC(1), noSwaps)
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Consumption != 15 {
		t.Errorf("Consumption = %v, want 15 (spanning the missing February)", results[0].Consumption)
	}
}

func TestCalculateSeries_MissingSwapCorrectionIsAnError(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "A", Day: mustDay(t, "2025-01-31"), Value: 10},
		{Meter: "B", Day: mustDay(t, "2025-02-28"), Value: 5},
	}
	if _, err := CalculateSeries(readings, false, constantResetMonth(1), constantKC(1), noSwaps); err == nil {
		t.Fatal("expected an error when the meter changed but no swap correction is available")
	}
}

func constantKC(v float64) KCFactorLookup {
	return func(string, telegram.Day) float64 { return v }
}

func constantResetMonth(v int) ResetMonthLookup {
	return func(string, telegram.Day) int { return v }
}

func noSwaps(string, string) (SwapCorrection, bool) { return SwapCorrection{}, false }
