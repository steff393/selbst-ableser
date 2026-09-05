package billing

import (
	"fmt"

	"selbst-ableser/internal/telegram"
)

// MonthlyValue is one meter's reading at a month-end evaluation date
// (FACH-01): which physical meter it came from, the day the reading
// actually used (which may differ from the exact month-end after a
// backward search), and the value itself.
type MonthlyValue struct {
	Meter string
	Day   telegram.Day
	Value int64
}

// SwapCorrection documents a meter replacement inside a billing month
// (FACH-02): the end reading of the outgoing meter and the start reading
// of the incoming meter, as recorded when the meter was physically
// exchanged. Both come from master data, not from a telegram.
type SwapCorrection struct {
	OutgoingMeter string
	EndReading    int64
	IncomingMeter string
	StartReading  int64
}

// MonthResult is one month's consumption for a meter point, carrying
// enough detail to explain how it was computed (FACH-13): the meter(s)
// and readings involved, whether a swap or billing-period reset was
// applied, and the conversion factor.
type MonthResult struct {
	Month        string // "YYYY-MM"
	Previous     MonthlyValue
	Current      MonthlyValue
	Swap         *SwapCorrection
	BillingReset bool

	KCFactor       float64
	RawConsumption float64 // before kc scaling
	Consumption    float64 // after kc scaling (FACH-07)
}

// CalculateMonth computes one month's consumption for a meter point
// (FACH-02, FACH-03, FACH-07).
//
// swap is non-nil when the meter was replaced during this month; current
// must then be a reading from swap.IncomingMeter and previous a reading
// from swap.OutgoingMeter. billingReset forces the previous reading to be
// treated as zero (FACH-03's fixed rule: the month after a heat-cost
// allocator's periodic reset) and must not be combined with swap — the
// spec leaves a meter swap that coincides with a billing-period reset
// explicitly unspecified (see FACH-07's note on differing kc-factors
// across a swap), so this rejects the combination rather than guess at it.
func CalculateMonth(month string, previous, current MonthlyValue, swap *SwapCorrection, billingReset bool, kcFactor float64) (MonthResult, error) {
	if swap != nil && billingReset {
		return MonthResult{}, fmt.Errorf("billing: month %s: a meter swap combined with a billing-period reset is not defined", month)
	}

	var raw float64
	switch {
	case swap != nil:
		if current.Meter != swap.IncomingMeter || previous.Meter != swap.OutgoingMeter {
			return MonthResult{}, fmt.Errorf("billing: month %s: swap correction does not match the given readings' meters", month)
		}
		raw = float64(current.Value-swap.StartReading) + float64(swap.EndReading-previous.Value)
	case billingReset:
		raw = float64(current.Value)
	default:
		raw = float64(current.Value - previous.Value)
	}

	if kcFactor == 0 {
		kcFactor = 1
	}

	return MonthResult{
		Month:          month,
		Previous:       previous,
		Current:        current,
		Swap:           swap,
		BillingReset:   billingReset,
		KCFactor:       kcFactor,
		RawConsumption: raw,
		Consumption:    raw * kcFactor,
	}, nil
}
