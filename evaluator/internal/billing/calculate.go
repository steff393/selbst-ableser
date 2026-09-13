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
// from swap.OutgoingMeter. billingReset means this month is the first of a
// new billing period, so both devices started it at zero (FACH-03).
//
// The two used to be mutually exclusive: a swap in a reset month was
// rejected as undefined, because it was unclear whether the outgoing
// meter's end reading had been taken before or after its own reset. It is
// not unclear once the period's closing value comes from the device itself
// (see billing_reset.go): the previous month then ends on the frozen
// value, this month begins at zero for both devices, and the outgoing
// meter's end reading is simply what it counted before being taken out.
// CalculateSeries still refuses the one case this reasoning does not cover
// — two devices whose billing periods start in different months.
func CalculateMonth(month string, previous, current MonthlyValue, swap *SwapCorrection, billingReset bool, kcFactor float64) (MonthResult, error) {
	var raw float64
	switch {
	case swap != nil && billingReset:
		if current.Meter != swap.IncomingMeter || previous.Meter != swap.OutgoingMeter {
			return MonthResult{}, fmt.Errorf("billing: month %s: swap correction does not match the given readings' meters", month)
		}
		// Both devices count from zero this month: the outgoing one
		// contributes everything it counted up to its removal, the
		// incoming one everything since it was installed.
		raw = float64(current.Value-swap.StartReading) + float64(swap.EndReading)
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
