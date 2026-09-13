package billing

import (
	"encoding/hex"
	"fmt"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

// The billing-period reset ("Stichtag"), read out of the device rather
// than inferred from telegram timing.
//
// A heat-cost allocator freezes its reading on the reset day and carries
// that frozen value in every telegram of the following period, alongside
// the day it belongs to. Using it as the closing reading of the period
// removes an error that could not be corrected any other way:
//
// The month a period ends in was previously closed off with whatever
// telegram arrived last before the month's end (FindReading, searching
// backward up to lookbackDays). In an ordinary month a late or missing
// telegram only shifts consumption into the next month — both readings sit
// on the same continuous counter, so the next difference picks it up
// again. Across a reset there is no next difference: the new period starts
// from zero, so whatever happened between the last telegram and the reset
// day is lost for good. Always in the same direction (too little), and for
// the common 31 December reset always in the middle of the heating season.
//
// The frozen value has no such gap: it is what the device itself recorded
// at the reset moment, and it stays readable for the rest of the year.

// ResetLookaheadDays is how far past a reset day a telegram carrying the
// frozen value is looked for.
//
// The frozen value is not a momentary thing: the device carries it in
// every telegram of the whole new period. So this window is not about
// finding the value in time, it is only about how long a silent device is
// waited for — and it costs one archive lookup per day searched, all of
// them point lookups on the primary key. Six weeks covers the entire
// following month plus slack, which is far more silence than a device
// sending every few minutes ever shows, while staying nowhere near the
// next period's reset.
const ResetLookaheadDays = 45

// BillingResetValue is one device's frozen reading and, when the telegram
// carries one, the day it belongs to.
type BillingResetValue struct {
	Meter   string
	Value   int64
	Date    decode.Date
	HasDate bool
}

// ReadBillingReset decodes the frozen value from one archived entry,
// whichever telegram family it belongs to: a standard record set carries
// it under storage number 1, a manufacturer-specific frame wherever that
// manufacturer puts it. ok is false, without an error, for a telegram that
// simply does not carry one.
func ReadBillingReset(entry archive.Entry, key [16]byte) (BillingResetValue, bool, error) {
	raw, err := hex.DecodeString(entry.RawHex)
	if err != nil {
		return BillingResetValue{}, false, fmt.Errorf("invalid archived hex: %w", err)
	}
	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return BillingResetValue{}, false, fmt.Errorf("invalid archived telegram: %w", err)
	}
	result := crypto.Decrypt(frame, key)
	if !result.Outcome.Evaluable() {
		return BillingResetValue{}, false, nil
	}

	records, ok, walkErr := decode.Standard(frame.CI, result.Payload)
	if !ok {
		reset, ok, err := decode.ManufacturerBillingReset(telegram.Manufacturer(frame.M), raw)
		if err != nil || !ok {
			return BillingResetValue{}, false, err
		}
		return BillingResetValue{
			Value:   reset.Reading.Value,
			Date:    reset.Date,
			HasDate: reset.HasDate,
		}, true, nil
	}

	// Looked up before walkErr is judged: a telegram whose tail is
	// unreadable can still carry a perfectly readable reset value ahead
	// of it (see decode.Standard).
	reading, found, err := decode.BillingResetValue(records)
	if err != nil {
		return BillingResetValue{}, false, err
	}
	if !found {
		return BillingResetValue{}, false, walkErr
	}
	out := BillingResetValue{Value: reading.Value}
	if date, ok, err := decode.BillingResetDate(records); err == nil && ok {
		out.Date, out.HasDate = date, true
	}
	return out, true, nil
}

// FindBillingReset looks for the value a meter froze on resetDay, in the
// telegrams of the days that follow it. Searching forward rather than
// backward is the whole point: the frozen value is only present *after*
// the reset, and it stays present, so a device that was silent over the
// turn of the period still yields an exact closing reading days later.
//
// A frozen value whose own date disagrees with resetDay is refused rather
// than used: it then belongs to a different period (a device whose first
// due date has not been reached yet, or one configured for another month),
// and the caller must fall back to what it did before.
func FindBillingReset(store *archive.Store, meterNumber string, key [16]byte, resetDay telegram.Day, lookaheadDays int) (BillingResetValue, bool, error) {
	if lookaheadDays <= 0 {
		lookaheadDays = ResetLookaheadDays
	}
	for offset := 1; offset <= lookaheadDays; offset++ {
		day := resetDay.AddDays(offset)
		entry, found, err := store.Get(meterNumber, day)
		if err != nil {
			return BillingResetValue{}, false, err
		}
		if !found {
			continue
		}
		reset, ok, err := ReadBillingReset(entry, key)
		if err != nil {
			return BillingResetValue{}, false, fmt.Errorf("billing: meter %s, day %s: %w", meterNumber, day, err)
		}
		if !ok {
			continue // present but carries no frozen value: keep looking
		}
		if reset.HasDate && !sameDay(reset.Date, resetDay) {
			// The device froze a different day than this period ends on.
			// Reporting nothing found leaves the caller with its previous
			// behaviour, which is the safe direction: a wrong closing
			// reading would be invisible in the result.
			return BillingResetValue{}, false, nil
		}
		reset.Meter = meterNumber
		return reset, true, nil
	}
	return BillingResetValue{}, false, nil
}

// sameDay compares a decoded telegram date with an archive day.
func sameDay(d decode.Date, day telegram.Day) bool {
	return telegram.Day(fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)) == day
}
