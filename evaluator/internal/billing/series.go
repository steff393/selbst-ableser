package billing

import (
	"fmt"

	"selbst-ableser/internal/telegram"
)

// SwapLookup resolves the documented end/start readings for a meter
// replacement, keyed by the outgoing and incoming meter numbers. It comes
// from master data (Meter.EndReading / Meter.StartReading), not from a
// telegram.
type SwapLookup func(outgoingMeter, incomingMeter string) (SwapCorrection, bool)

// KCFactorLookup resolves a meter's kc-factor (FACH-07) as of a given day,
// so a re-recalibrated meter (same physical Number, a new master-data
// record with a different KCFactor from a certain day on) resolves to the
// factor that was actually active for that reading, not just whichever
// record happens to come first. It comes from master data
// (MasterData.MeterByNumber + Meter.EffectiveKCFactor).
type KCFactorLookup func(meter string, day telegram.Day) float64

// FreezeMonthLookup resolves the month whose last day is a meter's
// Stichtag (FACH-03) as of a given day, the same way KCFactorLookup
// resolves a kc-factor. It comes from master data (MasterData.MeterByNumber
// + Meter.EffectiveFreezeMonth).
type FreezeMonthLookup func(meter string, day telegram.Day) int

// CalculateSeries computes month-by-month consumption for one meter point
// across a chronological sequence of found monthly readings. FACH-01's
// backward search must already have been applied by the caller: a month
// with no value at all is simply absent from readings, not represented as
// a zero.
//
// A gap between two consecutive present readings is not treated specially
// — the consumption spanning the missing month(s) is carried into the next
// available month without being marked as such (FACH-08, Variante A: a
// deliberate simplification, not an oversight).
//
// resetsAnnually should be true for heat-cost-allocator meter points and
// false for water meter points (FACH-03: only heat-cost allocators reset;
// water meters count continuously). freezeMonth resolves which calendar
// month each meter's Stichtag falls in; the new period then begins in the
// month after it.
func CalculateSeries(readings []MonthlyValue, resetsAnnually bool, freezeMonth FreezeMonthLookup, kcFactor KCFactorLookup, swap SwapLookup) ([]MonthResult, error) {
	var results []MonthResult
	for i := 1; i < len(readings); i++ {
		prev, cur := readings[i-1], readings[i]

		var swapCorrection *SwapCorrection
		if cur.Meter != prev.Meter {
			sc, ok := swap(prev.Meter, cur.Meter)
			if !ok {
				return results, fmt.Errorf("billing: meter changed from %s to %s but no swap correction is available", prev.Meter, cur.Meter)
			}
			swapCorrection = &sc
		}

		// A new billing period begins in the month *after* the one the
		// device freezes in: it froze on the last day of freezeMonth and
		// has counted from zero ever since.
		billingReset := resetsAnnually && cur.Day.Month() == monthAfter(freezeMonth(cur.Meter, cur.Day))
		month := string(cur.Day)[:7] // "YYYY-MM"

		// A swap in the first month of a billing period is computable
		// (see CalculateMonth) only while both devices share that period:
		// the outgoing meter's end reading counts from zero because it
		// reset with the same period. Two devices with different reset
		// months break that, and there is no reading that would tell us
		// what the outgoing one had counted since its own last reset.
		if swapCorrection != nil && billingReset && freezeMonth(prev.Meter, prev.Day) != freezeMonth(cur.Meter, cur.Day) {
			return results, fmt.Errorf(
				"billing: month %s: meters %s and %s were swapped in the first month of a billing period but their Stichtag months differ",
				month, prev.Meter, cur.Meter)
		}

		res, err := CalculateMonth(month, prev, cur, swapCorrection, billingReset, kcFactor(cur.Meter, cur.Day))
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

// monthAfter is the calendar month following m, wrapping December to
// January.
func monthAfter(m int) int {
	if m == 12 {
		return 1
	}
	return m + 1
}
