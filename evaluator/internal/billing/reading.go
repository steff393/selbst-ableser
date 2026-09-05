package billing

import (
	"encoding/hex"
	"fmt"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

// DefaultLookbackDays is how far FindReading searches backward from a
// target day if no telegram was archived for that exact day (FACH-01).
// Configurable per installation; five days covers a meter that goes
// quiet for a few days without reaching back so far that a reading from
// a different billing month gets picked up.
const DefaultLookbackDays = 5

// FindReading implements FACH-01's monthly reading lookup: the archived
// entry for meterNumber on target, or — if missing — the closest one found
// searching backward up to lookbackDays days (0 means DefaultLookbackDays).
// found is false if no evaluable telegram exists anywhere in that window,
// which the caller treats as a gap (FACH-08).
func FindReading(store *archive.Store, meterNumber string, key [16]byte, target telegram.Day, lookbackDays int) (MonthlyValue, bool, error) {
	if lookbackDays <= 0 {
		lookbackDays = DefaultLookbackDays
	}
	for offset := 0; offset <= lookbackDays; offset++ {
		day := target.AddDays(-offset)
		entry, found, err := store.Get(meterNumber, day)
		if err != nil {
			return MonthlyValue{}, false, err
		}
		if !found {
			continue
		}
		reading, ok, err := ReadValue(entry, key)
		if err != nil {
			return MonthlyValue{}, false, fmt.Errorf("billing: meter %s, day %s: %w", meterNumber, day, err)
		}
		if !ok {
			continue // present but not evaluable (wrong key, unsupported mode, ...): keep searching
		}
		return MonthlyValue{Meter: meterNumber, Day: day, Value: reading.Value}, true, nil
	}
	return MonthlyValue{}, false, nil
}

// ReadValue decrypts and decodes one archived entry's current value. ok is
// false with no error for a telegram that is present but not evaluable —
// wrong key, unsupported encryption mode, or no recognized current-value
// record — which FindReading treats the same as a missing day.
func ReadValue(entry archive.Entry, key [16]byte) (decode.Reading, bool, error) {
	raw, err := hex.DecodeString(entry.RawHex)
	if err != nil {
		return decode.Reading{}, false, fmt.Errorf("invalid archived hex: %w", err)
	}
	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return decode.Reading{}, false, fmt.Errorf("invalid archived telegram: %w", err)
	}

	result := crypto.Decrypt(frame, key)
	if !result.Outcome.Evaluable() {
		return decode.Reading{}, false, nil
	}

	records, ok, err := decode.Standard(frame.CI, result.Payload)
	if err != nil {
		return decode.Reading{}, false, err
	}
	if !ok {
		// Manufacturer-specific (CI 0xA0-0xB7): no self-describing records
		// to walk, so fall back to whatever decoder is registered for this
		// telegram's manufacturer code, if any (decode.RegisterManufacturerDecoder).
		return decode.ManufacturerSpecific(telegram.Manufacturer(frame.M), result.Payload)
	}
	reading, found, err := decode.CurrentValue(records)
	if err != nil || !found {
		return decode.Reading{}, false, err
	}
	return reading, true, nil
}
