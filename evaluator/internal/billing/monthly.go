package billing

import (
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// MonthEnds returns the last calendar day of each month from the month
// containing from through the month containing to, inclusive.
func MonthEnds(from, to telegram.Day) []telegram.Day {
	var out []telegram.Day
	lastMonth := firstOfMonth(to)
	cursor := firstOfMonth(from)
	for !lastMonth.Before(cursor) {
		end := lastDayOfMonth(cursor)
		out = append(out, end)
		cursor = end.AddDays(1) // the 1st of the following month
	}
	return out
}

func firstOfMonth(d telegram.Day) telegram.Day {
	s := string(d)
	first, err := telegram.ParseDay(s[:8] + "01")
	if err != nil {
		panic(err) // d is already a valid Day, so this cannot happen
	}
	return first
}

func lastDayOfMonth(firstOfMonthDay telegram.Day) telegram.Day {
	// Advance a month at a time is error-prone around month lengths;
	// instead step forward day by day from the first of the month until
	// the month changes, which is simple and unambiguous.
	d := firstOfMonthDay
	for {
		next := d.AddDays(1)
		if next.Month() != d.Month() {
			return d
		}
		d = next
	}
}

// SwapLookupFromMasterData builds a SwapLookup for one meter point from
// its configured meters' documented end/start readings (FACH-02).
func SwapLookupFromMasterData(md masterdata.MasterData, meterPointID string) SwapLookup {
	return func(outgoingMeter, incomingMeter string) (SwapCorrection, bool) {
		var outgoing, incoming *masterdata.Meter
		for i := range md.Meters {
			m := &md.Meters[i]
			if m.MeterPointID != meterPointID {
				continue
			}
			if m.Number == outgoingMeter {
				outgoing = m
			}
			if m.Number == incomingMeter {
				incoming = m
			}
		}
		if outgoing == nil || incoming == nil || outgoing.EndReading == nil {
			return SwapCorrection{}, false
		}
		return SwapCorrection{
			OutgoingMeter: outgoingMeter,
			EndReading:    *outgoing.EndReading,
			IncomingMeter: incomingMeter,
			StartReading:  incoming.StartReading,
		}, true
	}
}

// MonthlyReadingsForMeterPoint collects FACH-01 readings for one meter
// point across a set of month-end evaluation dates, resolving which
// physical meter was active at each date via master data (STAMM-02). A
// month with no active meter, or with no evaluable telegram found within
// the lookback window, is simply absent from the result (FACH-08).
func MonthlyReadingsForMeterPoint(store *archive.Store, md masterdata.MasterData, meterPointID string, monthEnds []telegram.Day, lookbackDays int) ([]MonthlyValue, error) {
	var resetsAnnually bool
	for _, mp := range md.MeterPoints {
		if mp.ID == meterPointID {
			resetsAnnually = mp.Kind.ResetsAnnually()
			break
		}
	}

	var out []MonthlyValue
	for _, day := range monthEnds {
		meter, ok := md.ActiveMeter(meterPointID, day)
		if !ok {
			continue
		}
		reading, found, err := FindReading(store, meter.Number, meter.AESKey, day, lookbackDays)
		if err != nil {
			return nil, err
		}

		// The last month of a billing period closes on the value the device
		// itself froze, not on whatever telegram arrived last before the
		// period ended — see billing_reset.go for why that difference
		// cannot be made up later. It is also the one case where a month
		// with no telegram of its own still yields a reading, because the
		// frozen value is carried in the telegrams that follow it.
		if resetsAnnually && day.Month() == meter.EffectiveFreezeMonth() {
			frozen, ok, err := FindBillingReset(store, meter.Number, meter.AESKey, day, 0)
			if err != nil {
				return nil, err
			}
			if ok && (!found || frozen.Value >= reading.Value) {
				reading = MonthlyValue{Meter: meter.Number, Day: day, Value: frozen.Value}
				found = true
			}
		}

		if !found {
			continue
		}
		out = append(out, reading)
	}
	return out, nil
}
