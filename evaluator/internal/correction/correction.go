// Package correction builds a corrected archive telegram: given a genuine
// telegram previously received from a meter and a new reading value, it
// produces a new raw telegram identical in every respect except the value
// itself — same header, same manufacturer/version/medium, same encryption
// (re-encrypted with the meter's own key where the original was
// encrypted) — for internal/archive.Store.Correct to write in its place
// (DATEN-09).
package correction

import (
	"encoding/hex"
	"fmt"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

// RSSI marks a corrected entry. Real received signal strength is always
// negative dBm on the supported receiver hardware (see
// collector/internal/telegram's signed-byte RSSI decoding) — a maximal
// positive value can never occur naturally, and unlike 0 it can never be
// mistaken for an unset field's zero value either. This is a deliberately
// cheap, schema-free way to flag a correction inline wherever RSSI is
// already displayed (Zählerstände, exports); the audit log separately
// records who made the correction and why.
const RSSI = 127

// IsMarked reports whether e carries the correction RSSI marker.
func IsMarked(e archive.Entry) bool {
	return e.RSSI == RSSI
}

// Build produces a corrected telegram: template's current-value record is
// replaced with newValue (in the same unit template's own record already
// uses — HCA units or liters), re-encrypted with key using template's own
// header fields as the IV wherever the original was encrypted, so every
// byte except the value itself is unchanged. template does not need to be
// the entry for the day being corrected — see archive.Store.NearestDay for
// locating the nearest one when that day has no archived entry of its own
// (a gap being backfilled rather than a wrong value being fixed).
func Build(template archive.Entry, key [16]byte, newValue int64) (rawHex string, oldValue int64, err error) {
	if newValue < 0 {
		return "", 0, fmt.Errorf("correction: value must not be negative")
	}
	raw, err := hex.DecodeString(template.RawHex)
	if err != nil {
		return "", 0, fmt.Errorf("correction: invalid template hex: %w", err)
	}
	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return "", 0, fmt.Errorf("correction: invalid template telegram: %w", err)
	}

	result := crypto.Decrypt(frame, key)
	if !result.Outcome.Evaluable() {
		return "", 0, fmt.Errorf("correction: template telegram is not decryptable with this key")
	}

	oldValue, err = patchCurrentValue(frame, result.Payload, newValue)
	if err != nil {
		return "", 0, err
	}

	if result.Outcome != crypto.OutcomeDecrypted {
		return hex.EncodeToString(result.Payload), oldValue, nil
	}
	reencrypted, err := crypto.Encrypt(frame, result.Payload, key)
	if err != nil {
		return "", 0, fmt.Errorf("correction: re-encrypting: %w", err)
	}
	return hex.EncodeToString(reencrypted), oldValue, nil
}

// patchCurrentValue overwrites payload's current-value field with newValue
// in place — via the standard DIF/VIF path where the telegram has one, or
// a registered manufacturer-specific patcher otherwise — and returns the
// value it replaced.
func patchCurrentValue(frame *telegram.Frame, payload []byte, newValue int64) (oldValue int64, err error) {
	records, ok, err := decode.Standard(frame.CI, payload)
	if err != nil {
		return 0, fmt.Errorf("correction: decoding template: %w", err)
	}
	if ok {
		reading, record, found, err := decode.CurrentValueRecord(records)
		if err != nil {
			return 0, fmt.Errorf("correction: decoding template's current value: %w", err)
		}
		if !found {
			return 0, fmt.Errorf("correction: template telegram has no current-value record")
		}
		encoded, err := decode.EncodeCurrentValue(reading.Unit, newValue, len(record.Data))
		if err != nil {
			return 0, fmt.Errorf("correction: %w", err)
		}
		copy(record.Data, encoded) // aliases payload's backing array
		return reading.Value, nil
	}

	mfr := telegram.Manufacturer(frame.M)
	reading, found, err := decode.ManufacturerSpecific(mfr, payload)
	if err != nil {
		return 0, fmt.Errorf("correction: decoding template's current value: %w", err)
	}
	if !found {
		return 0, fmt.Errorf("correction: no known way to read a %s manufacturer-specific telegram", mfr)
	}
	patched, err := decode.PatchManufacturerSpecific(mfr, payload, newValue)
	if err != nil {
		return 0, fmt.Errorf("correction: %w", err)
	}
	if !patched {
		return 0, fmt.Errorf("correction: no known way to patch a %s manufacturer-specific telegram", mfr)
	}
	return reading.Value, nil
}
