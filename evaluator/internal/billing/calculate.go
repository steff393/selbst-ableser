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

	Factor         float64
	RawConsumption float64 // before factor scaling
	Consumption    float64 // after factor scaling (FACH-07)
}

// CalculateMonth computes one month's consumption for a meter point
// (FACH-02, FACH-03, FACH-07).
//
// swap is non-nil when the meter was replaced during this month; current
// must then be a reading from swap.IncomingMeter and previous a reading
// from swap.OutgoingMeter. billingReset means the device previous came
// from reset to zero at the start of this month (FACH-03): the meter
// itself without a swap, the outgoing one with a swap. The incoming one
// was installed after the month began, so its own Stichtag plays no part
// here.
//
// A swap in a reset month is well defined because the period's closing
// value comes from the device itself (see billing_reset.go): the previous
// month ends on the frozen value, the outgoing device begins this month
// at zero, and its end reading is simply what it counted before being
// taken out.
func CalculateMonth(month string, previous, current MonthlyValue, swap *SwapCorrection, billingReset bool, factor float64) (MonthResult, error) {
	var raw float64
	switch {
	case swap != nil && billingReset:
		if current.Meter != swap.IncomingMeter || previous.Meter != swap.OutgoingMeter {
			return MonthResult{}, fmt.Errorf("billing: month %s: swap correction does not match the given readings' meters", month)
		}
		// The outgoing device counted from zero this month, so it
		// contributes everything it counted up to its removal; the
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

	if factor == 0 {
		factor = 1
	}

	return MonthResult{
		Month:          month,
		Previous:       previous,
		Current:        current,
		Swap:           swap,
		BillingReset:   billingReset,
		Factor:         factor,
		RawConsumption: raw,
		Consumption:    raw * factor,
	}, nil
}
