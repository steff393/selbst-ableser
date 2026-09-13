package receiver

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Device is everything that differs between receiver models. The rest of
// this package — reconnecting with backoff, the idle watchdog, discarding
// a bad frame without dropping the connection — is the same regardless of
// which one is plugged in, and lives in SerialSource.
//
// Three things vary in practice, and they are exactly the three methods
// below plus the port speed: how the device is brought up (a vendor
// handshake, or nothing at all), where one frame ends and the next
// begins, and what wraps the wM-Bus telegram inside a frame.
type Device interface {
	// Name is how the device is referred to in log lines, errors, and
	// the -receiver flag.
	Name() string

	// BaudRate is the port speed this device's UART runs at.
	BaudRate() int

	// Init performs whatever handshake the device needs before it
	// delivers telegrams. A device that simply starts sending on
	// power-up implements this as a no-op.
	//
	// onResponse, if not nil, is called once per handshake step that
	// gets a validated response, with the full response — for
	// troubleshooting (firmware identification, confirming what the
	// device echoed back). A device with no handshake never calls it.
	Init(ctx context.Context, conn Conn, onResponse func(step string, raw []byte)) error

	// SplitFrame extracts the first complete frame from buf and reports
	// how many bytes of buf it consumed — including anything skipped
	// ahead of the frame.
	//
	// It returns telegram.ErrIncompleteFrame when buf does not hold a
	// full frame yet (the caller reads more and asks again),
	// ErrCorruptFrame for bytes that are structurally broken but do not
	// put the connection itself in doubt (the caller discards them and
	// carries on), and any other error to mean the stream can no longer
	// be trusted.
	SplitFrame(buf []byte) (frame []byte, consumed int, err error)

	// ParseFrame turns one frame from SplitFrame into a Telegram. An
	// error means this one frame is unusable — the caller discards it
	// and keeps receiving, so a single garbled reception never
	// interrupts the others.
	ParseFrame(frame []byte) (Telegram, error)
}

// Conn is the narrow view of the connection a Device's Init gets: write a
// request, read the next frame back. Reading goes through the same
// buffered reader the receive loop uses afterwards, so bytes that arrive
// during the handshake — the start of a telegram, say — are not lost
// between the two phases.
type Conn interface {
	Write(p []byte) (n int, err error)
	NextFrame(ctx context.Context) ([]byte, error)
}

// ErrCorruptFrame marks bytes that could not be read as a frame while the
// connection itself still looks healthy. It is deliberately distinct from
// a port error: one corrupt frame is normal on a radio link and must not
// cost a reconnect.
var ErrCorruptFrame = errors.New("receiver: corrupt frame")

// devices is every supported receiver, by the name the -receiver flag
// uses. Adding a model means adding it here and nowhere else.
var devices = map[string]Device{
	imstDevice{}.Name():    imstDevice{},
	innotasDevice{}.Name(): innotasDevice{},
}

// DefaultDeviceName is what an installation gets that never says
// otherwise — the receiver this program was originally written for, so
// that an existing setup keeps working untouched.
const DefaultDeviceName = "imst"

// DeviceByName looks up a receiver model for the -receiver flag.
func DeviceByName(name string) (Device, error) {
	if d, ok := devices[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("receiver: unknown receiver %q, known: %v", name, DeviceNames())
}

// DeviceNames lists the supported receivers, sorted, for flag help and
// error messages.
func DeviceNames() []string {
	names := make([]string, 0, len(devices))
	for name := range devices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
