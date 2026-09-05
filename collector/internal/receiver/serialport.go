package receiver

import (
	"fmt"
	"time"

	"go.bug.st/serial"
)

// receiverReadTimeout bounds how long a single Read on the receiver port
// can block. Without it, an idle receiver leaves Read blocked
// indefinitely, and canceling a pending read by closing the port from
// another goroutine (see SerialSource) is not reliably prompt on every
// platform (on Windows in particular, closing a handle mid-read can take
// several seconds to be noticed). With the timeout, Read instead returns
// (0, nil) periodically even with nothing to deliver, giving frameReader a
// chance to notice a canceled context well within one interval.
const receiverReadTimeout = 500 * time.Millisecond

// OpenSerialPort connects to a receiver at a fixed device path (e.g.
// "/dev/ttyUSB0" or "COM3") at the fixed 115200 8N1 configuration the
// iU891A-XL and compatible devices use.
func OpenSerialPort(path string) PortOpener {
	return func() (Port, string, error) {
		mode := &serial.Mode{
			BaudRate: 115200,
			DataBits: 8,
			Parity:   serial.NoParity,
			StopBits: serial.OneStopBit,
			// go.bug.st/serial only asserts DTR/RTS on open if told to —
			// and its two platform backends disagree on what "not told"
			// means: Windows enables both anyway when this is left nil,
			// Linux leaves the lines exactly as they already are. Left
			// implicit, that made connecting behave differently per
			// platform: many USB-serial bridges (this receiver's included,
			// apparently) treat a DTR/RTS transition as a reset, so
			// Windows always got a receiver in a clean state on connect
			// while Linux could reopen the port with a telegram already
			// mid-flight from before — read as the handshake's expected
			// reply and rejected as "unexpected response". Setting this
			// explicitly makes every platform behave like Windows already
			// did by accident.
			InitialStatusBits: &serial.ModemOutputBits{DTR: true, RTS: true},
		}
		p, err := serial.Open(path, mode)
		if err != nil {
			return nil, "", err
		}
		// Discards anything the OS driver already buffered before this
		// connection existed — a previous run's unread bytes, or noise
		// from the device settling right as the port opened. Without
		// this, the handshake's very first read can pick up stale bytes
		// that were never meant as a reply to anything we sent, and fail
		// validation even though the receiver itself is fine.
		if err := p.ResetInputBuffer(); err != nil {
			p.Close()
			return nil, "", fmt.Errorf("receiver: resetting input buffer: %w", err)
		}
		if err := p.SetReadTimeout(receiverReadTimeout); err != nil {
			p.Close()
			return nil, "", fmt.Errorf("receiver: setting read timeout: %w", err)
		}
		return p, path, nil
	}
}
