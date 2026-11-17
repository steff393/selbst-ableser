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

	results, err := CalculateSeries(readings, constantFreezeMonth(12), constantFactor(1), noSwaps)
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

	results, err := CalculateSeries(readings, constantFreezeMonth(0), constantFactor(1), swapLookup)
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

// TestCalculateSeries_ConfigurableFreezeMonth checks that the billing-period
// reset (FACH-03) fires in the month after whatever FreezeMonth says for the
// meter, not just January — a per-meter Stichtag rather than a
// fixed one.
func TestCalculateSeries_ConfigurableFreezeMonth(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "M", Day: mustDay(t, "2025-05-31"), Value: 400},
		{Meter: "M", Day: mustDay(t, "2025-06-30"), Value: 30},
		{Meter: "M", Day: mustDay(t, "2025-07-31"), Value: 50},
	}

	results, err := CalculateSeries(readings, constantFreezeMonth(5), constantFactor(1), noSwaps)
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

// Without a Stichtag month a meter counts continuously, whatever its kind:
// January is a difference like every other month, not the whole reading.
func TestCalculateSeries_NoFreezeMonthCountsContinuously(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "M", Day: mustDay(t, "2025-12-31"), Value: 4000},
		{Meter: "M", Day: mustDay(t, "2026-01-31"), Value: 4300},
		{Meter: "M", Day: mustDay(t, "2026-02-28"), Value: 4450},
	}
	results, err := CalculateSeries(readings, constantFreezeMonth(0), constantFactor(1), noSwaps)
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	for i, want := range []float64{300, 150} {
		if results[i].BillingReset || results[i].Consumption != want {
			t.Errorf("%s: %+v, want no reset and consumption %v", results[i].Month, results[i], want)
		}
	}
}

// A swap in the first month of a period depends only on the outgoing
// device: it is the one that was counting when the month began. The
// incoming one was installed later and counts from its start reading
// either way, so the two may have different Stichtag months, or one none.
func TestCalculateSeries_SwapInResetMonthFollowsTheOutgoingDevice(t *testing.T) {
	readings := []MonthlyValue{
		{Meter: "OLD", Day: mustDay(t, "2025-12-31"), Value: 400},
		{Meter: "NEW", Day: mustDay(t, "2026-01-31"), Value: 25},
	}
	swap := func(string, string) (SwapCorrection, bool) {
		return SwapCorrection{OutgoingMeter: "OLD", EndReading: 0, IncomingMeter: "NEW", StartReading: 5}, true
	}
	cases := []struct {
		name       string
		old, new   int
		endReading int64
		wantReset  bool
		wantRaw    float64
	}{
		// Old device reset at the turn of the year and counted 30 until
		// its removal; the new one counted 20 since.
		{"outgoing resets, incoming has none", 12, 0, 30, true, 50},
		{"outgoing resets, incoming differs", 12, 6, 30, true, 50},
		// Old device counted straight through: from 400 to 430.
		{"outgoing has none, incoming resets", 0, 12, 430, false, 50},
		{"neither resets", 0, 0, 430, false, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			freeze := func(meter string, _ telegram.Day) int {
				if meter == "OLD" {
					return c.old
				}
				return c.new
			}
			lookup := func(o, i string) (SwapCorrection, bool) {
				sc, ok := swap(o, i)
				sc.EndReading = c.endReading
				return sc, ok
			}
			results, err := CalculateSeries(readings, freeze, constantFactor(1), lookup)
			if err != nil {
				t.Fatalf("CalculateSeries: %v", err)
			}
			if results[0].BillingReset != c.wantReset || results[0].RawConsumption != c.wantRaw {
				t.Errorf("result = %+v, want reset=%v raw=%v", results[0], c.wantReset, c.wantRaw)
			}
		})
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
	results, err := CalculateSeries(readings, constantFreezeMonth(0), constantFactor(1), noSwaps)
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
	if _, err := CalculateSeries(readings, constantFreezeMonth(0), constantFactor(1), noSwaps); err == nil {
		t.Fatal("expected an error when the meter changed but no swap correction is available")
	}
}

func constantFactor(v float64) FactorLookup {
	return func(string, telegram.Day) float64 { return v }
}

func constantFreezeMonth(v int) FreezeMonthLookup {
	return func(string, telegram.Day) int { return v }
}

func noSwaps(string, string) (SwapCorrection, bool) { return SwapCorrection{}, false }
