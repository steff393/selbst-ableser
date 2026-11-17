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

// FactorLookup resolves a meter's factor (FACH-07) as of a given day,
// so a re-recalibrated meter (same physical Number, a new master-data
// record with a different Factor from a certain day on) resolves to the
// factor that was actually active for that reading, not just whichever
// record happens to come first. It comes from master data
// (MasterData.MeterByNumber + Meter.EffectiveFactor).
type FactorLookup func(meter string, day telegram.Day) float64

// FreezeMonthLookup resolves the month whose last day is a meter's
// Stichtag (FACH-03) as of a given day, the same way FactorLookup
// resolves a factor, or 0 for a meter without a Stichtag, which counts
// continuously. It comes from master data (MasterData.MeterByNumber +
// Meter.FreezeMonth).
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
// freezeMonth resolves which calendar month each meter's Stichtag falls
// in; the new period then begins in the month after it. A meter without a
// Stichtag (0) never resets, whatever its kind (FACH-03).
func CalculateSeries(readings []MonthlyValue, freezeMonth FreezeMonthLookup, factor FactorLookup, swap SwapLookup) ([]MonthResult, error) {
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
		//
		// What matters is the device the previous reading came from, the
		// only one that was counting when this month began. Without a swap
		// that is the same device as now. With one, it is the outgoing
		// device; the incoming one was installed during this month, after
		// any reset at its start, and counts from its documented start
		// reading either way. So a swap in the first month of a period is
		// computable whether the two devices share a Stichtag, differ, or
		// one of them has none (see CalculateMonth).
		billingReset := resetsInMonth(freezeMonth(prev.Meter, prev.Day), cur.Day.Month())
		month := string(cur.Day)[:7] // "YYYY-MM"

		res, err := CalculateMonth(month, prev, cur, swapCorrection, billingReset, factor(cur.Meter, cur.Day))
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

// resetsInMonth reports whether a device with the given Stichtag month (0:
// none) starts counting from zero at the beginning of month: the one after
// the Stichtag month, so a 31 December Stichtag starts the new period in
// January.
func resetsInMonth(freezeMonth, month int) bool {
	return freezeMonth != 0 && month == freezeMonth%12+1
}
