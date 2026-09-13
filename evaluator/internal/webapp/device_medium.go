package webapp

import (
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

// deviceMedium reports the medium byte that says what the device behind a
// telegram actually measures — the value every device-type label and
// meter-kind suggestion in the UI should be read from, rather than byte 9
// directly.
//
// Byte 9 is the link layer's own medium, describing whatever transmits;
// where the telegram also carries an enhanced identification record
// naming the metering device, that one wins (see decode.EnhancedMedium).
// A Qundis heat meter transmits as a radio converter, and only the record
// says "heat".
//
// key may be the zero key. The record lives in the application layer, so
// an encrypted telegram whose key is not at hand simply keeps the
// link-layer answer — the one shown before this function existed. This
// never guesses: every path either finds the record or falls back.
//
// raw is the wM-Bus telegram as archived. Callers already guard against
// one too short to hold a header; a short one returns 0x00 rather than
// panicking, which classifies as "Anderes".
func deviceMedium(raw []byte, key [16]byte) byte {
	if len(raw) < 10 {
		return 0x00
	}
	medium := raw[9]

	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return medium
	}
	result := crypto.Decrypt(frame, key)
	if !result.Outcome.Evaluable() {
		return medium // encrypted and not ours to read: the header is all there is
	}
	// A walk error is deliberately ignored in favour of the records it
	// managed to read: the enhanced identification is the first record in
	// every telegram seen to carry one, so a malformed record further
	// along must not cost the answer.
	records, ok, _ := decode.Standard(frame.CI, result.Payload)
	if !ok {
		return medium
	}
	if m, found := decode.EnhancedMedium(records); found {
		return m
	}
	return medium
}
