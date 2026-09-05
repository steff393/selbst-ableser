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

// ResetMonthLookup resolves a meter's billing-period reset month
// ("Stichtag", FACH-03) as of a given day, the same way KCFactorLookup
// resolves a kc-factor. It comes from master data (MasterData.MeterByNumber
// + Meter.EffectiveResetMonth).
type ResetMonthLookup func(meter string, day telegram.Day) int

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
// water meters count continuously). resetMonth resolves which calendar
// month that reset falls in, per meter.
func CalculateSeries(readings []MonthlyValue, resetsAnnually bool, resetMonth ResetMonthLookup, kcFactor KCFactorLookup, swap SwapLookup) ([]MonthResult, error) {
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

		billingReset := resetsAnnually && cur.Day.Month() == resetMonth(cur.Meter, cur.Day)
		month := string(cur.Day)[:7] // "YYYY-MM"

		res, err := CalculateMonth(month, prev, cur, swapCorrection, billingReset, kcFactor(cur.Meter, cur.Day))
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}
