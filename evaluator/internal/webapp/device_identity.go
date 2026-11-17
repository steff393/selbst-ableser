package webapp

import (
	"encoding/binary"

	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

// deviceIdentity reports what the device behind a telegram is —
// manufacturer and medium — for every device-type label and meter-kind
// suggestion in the UI. Reading bytes 2-3 and 9 directly would describe
// whatever transmits, which is not always the meter.
//
// Three sources, in order of how specifically they name the metering
// device:
//
//  1. the link layer, always present;
//  2. a long transport header, which names the meter a radio converter
//     transmits for (see telegram.Frame.Identity) — the meter number
//     shown next to this comes from there too;
//  3. an enhanced identification record in the application layer, which a
//     Qundis heat meter uses to say "heat" where its link layer says
//     "radio converter" (see decode.EnhancedMedium).
//
// key may be the zero key. The record in 3 sits behind the encryption, so
// an encrypted telegram whose key is not at hand simply stops after 2.
// This never guesses: every path either finds a more specific source or
// falls back to a less specific one.
//
// raw is the wM-Bus telegram as archived. Callers already guard against
// one too short to hold a header; a short one returns no manufacturer and
// medium 0x00, which classifies as "Anderes".
func deviceIdentity(raw []byte, key [16]byte) (manufacturer string, medium byte) {
	if len(raw) < 10 {
		return "", 0x00
	}
	medium = raw[9]
	manufacturer = telegram.Manufacturer(binary.LittleEndian.Uint16(raw[2:4]))

	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return manufacturer, medium
	}
	id := frame.Identity()
	medium, manufacturer = id.Med, telegram.Manufacturer(id.M)
	result := crypto.Decrypt(frame, key)
	if !result.Outcome.Evaluable() {
		return manufacturer, medium // encrypted and not ours to read: the headers are all there is
	}
	// A walk error is deliberately ignored in favour of the records it
	// managed to read: the enhanced identification is the first record in
	// every telegram seen to carry one, so a malformed record further
	// along must not cost the answer.
	records, ok, _ := decode.Standard(frame.CI, result.Payload)
	if !ok {
		return manufacturer, medium
	}
	if m, found := decode.EnhancedMedium(records); found {
		return manufacturer, m
	}
	return manufacturer, medium
}
