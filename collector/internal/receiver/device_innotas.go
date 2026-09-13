package receiver

import (
	"context"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

// innotasDevice is the Innotas "Wireless M-Bus Radio-Receiver", an FTDI
// USB-serial bridge in front of a Radiocrafts RC1180-MBUS3 module.
//
// Everything here was established by measurement rather than from
// documentation (the vendor manual states only the module type; the
// module's own datasheet is behind a request form) — see
// device_innotas_test.go, whose fixture is a real capture from such a
// device.
//
// Three ways it differs from the IMST:
//
//   - 19200 baud instead of 115200. Confirmed by elimination: at every
//     other rate the module's config-mode prompt came back as the
//     garbage a mis-sampled line produces, at 19200 as a clean '>'.
//   - Frames are 0x68 <L> <L bytes> 0x16 — a start byte, a length, the
//     payload, a stop byte — instead of SLIP delimiters, and they follow
//     each other back to back with nothing in between.
//   - There is no receiver envelope: the payload from the length byte
//     onwards already *is* the wM-Bus telegram, so it needs no unwrapping
//     at all. The module also strips the over-the-air CRC blocks (the
//     length arithmetic works out exactly), which means it checks them
//     itself — there is nothing left here to verify them against.
type innotasDevice struct{}

func (innotasDevice) Name() string  { return "innotas" }
func (innotasDevice) BaudRate() int { return 19200 }

const (
	innotasFrameStart = 0x68
	innotasFrameStop  = 0x16

	// innotasMinLength is the smallest length byte that could describe a
	// wM-Bus telegram at all — telegram.ParseWMBus needs the length byte
	// plus ten more (C, M, A, version, medium, CI). Anything below that
	// means this 0x68 was payload rather than a frame start.
	innotasMinLength = 10
)

// Init sends nothing at all: the module streams frames on its own from
// the moment it has power, which is exactly how the capture this device
// was built from was made (with the diagnostic explicitly told not to
// transmit). Writing a configuration sequence would mean guessing at the
// module's command set from its family conventions, and a wrong guess
// there does not fail loudly — it writes to the module's configuration
// memory. Not worth it for a device that already delivers what is needed.
func (innotasDevice) Init(context.Context, Conn, func(string, []byte)) error {
	return nil
}

// SplitFrame finds the next 0x68 <L> ... 0x16 frame.
//
// A 0x68 also occurs inside payloads, so a start byte alone means
// nothing: a candidate only counts once the length byte points at a stop
// byte in the right place. Where it does not, the scan simply moves on a
// byte — which is also how it recovers from the junk a port sometimes
// delivers before the first whole frame (the capture starts with two
// stray 0x00).
func (innotasDevice) SplitFrame(buf []byte) ([]byte, int, error) {
	for i := 0; i < len(buf); i++ {
		if buf[i] != innotasFrameStart {
			continue
		}
		if len(buf) < i+2 {
			// A start byte with no length byte yet: keep it, drop
			// whatever preceded it, and wait for more.
			return nil, i, telegram.ErrIncompleteFrame
		}
		length := int(buf[i+1])
		if length < innotasMinLength {
			continue // too short to be a telegram: not a frame start
		}
		stopAt := i + 2 + length
		if stopAt >= len(buf) {
			return nil, i, telegram.ErrIncompleteFrame
		}
		if buf[stopAt] != innotasFrameStop {
			continue // length does not point at a stop byte: not a frame start
		}
		// Copied rather than handed out as a slice of the read buffer:
		// the telegram outlives this call by a long way (it is buffered,
		// reported, and archived), and aliasing the buffer the receive
		// loop keeps appending to would make that a question of
		// allocation timing rather than of correctness.
		frame := append([]byte(nil), buf[i+1:stopAt]...)
		return frame, stopAt + 1, nil
	}
	// Nothing that could still become a frame: none of it is worth
	// keeping, so the buffer does not grow without bound on a line that
	// only ever delivers noise.
	return nil, len(buf), telegram.ErrIncompleteFrame
}

// ParseFrame reads the frame as a plain wM-Bus telegram.
//
// RSSI stays zero: the module can be configured to append it to each
// frame, but the one measured here is not, and turning that on means
// writing to its configuration memory (see Init). Everything downstream
// treats an RSSI of zero as "not reported" rather than as a reading.
func (innotasDevice) ParseFrame(frame []byte) (Telegram, error) {
	f, err := telegram.ParseWMBus(frame)
	if err != nil {
		return Telegram{}, err
	}
	meterID, err := f.MeterNumber() // already validated by ParseWMBus
	if err != nil {
		return Telegram{}, err
	}
	return Telegram{
		MeterID:    meterID,
		ReceivedAt: time.Now(),
		Raw:        f.Raw,
	}, nil
}
