package receiver

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

// imstDevice is the IMST iU891A-XL (and USB-ID-compatible devices): SLIP
// framing, a vendor handshake, and a 10-byte receiver envelope carrying
// the RSSI in front of every telegram. This is the receiver the program
// was originally written for and stays its default.
type imstDevice struct{}

func (imstDevice) Name() string  { return "imst" }
func (imstDevice) BaudRate() int { return 115200 }

// Receiver initialization sequence for the IMST iU891A-XL (and compatible
// devices): wake-up padding, a device-info request/response, and a
// request to configure combined C1/T1 reception. These exact byte
// sequences are not documented publicly and were determined by
// measurement; they must be preserved as given.
var (
	wakeUpSequence = bytes.Repeat([]byte{0xC0}, 30)

	deviceInfoRequest   = []byte{0xC0, 0x01, 0x03, 0x04, 0x24, 0xC0}
	deviceInfoPrefix    = []byte{0x01, 0x04, 0x00}
	configureC1T1Req    = []byte{0xC0, 0x09, 0x03, 0x03, 0x0E, 0x00, 0x00, 0x00, 0x32, 0x00, 0xA0, 0xBB, 0x0D, 0x00, 0xAF, 0x61, 0xC0}
	configureC1T1Prefix = []byte{0x09, 0x04, 0x00}
)

// Init performs the wake-up and configuration handshake. Responses are
// read through conn so any bytes buffered past the handshake (e.g. the
// start of a radio telegram arriving right afterwards) remain available
// to the receive loop.
func (imstDevice) Init(ctx context.Context, conn Conn, onResponse func(step string, raw []byte)) error {
	if _, err := conn.Write(wakeUpSequence); err != nil {
		return fmt.Errorf("receiver: wake-up write failed: %w", err)
	}
	info, err := sendAndExpect(ctx, conn, deviceInfoRequest, deviceInfoPrefix)
	if err != nil {
		return fmt.Errorf("receiver: device-info handshake failed: %w", err)
	}
	if onResponse != nil {
		onResponse("device info", info)
	}
	cfg, err := sendAndExpect(ctx, conn, configureC1T1Req, configureC1T1Prefix)
	if err != nil {
		return fmt.Errorf("receiver: C1/T1 configuration handshake failed: %w", err)
	}
	if onResponse != nil {
		onResponse("C1/T1 configuration", cfg)
	}
	return nil
}

// SplitFrame is the SLIP framing, with a malformed escape reported as a
// corrupt frame rather than a broken connection.
func (imstDevice) SplitFrame(buf []byte) ([]byte, int, error) {
	frame, consumed, err := telegram.SplitFrame(buf)
	if errors.Is(err, telegram.ErrMalformedEscape) {
		return nil, consumed, ErrCorruptFrame
	}
	return frame, consumed, err
}

// ParseFrame decodes the receiver envelope (checksum, length, RSSI) and
// the wM-Bus telegram inside it.
func (imstDevice) ParseFrame(frame []byte) (Telegram, error) {
	f, err := telegram.Parse(frame)
	if err != nil {
		return Telegram{}, err
	}
	meterID, err := f.MeterNumber() // already validated by Parse
	if err != nil {
		return Telegram{}, err
	}
	return Telegram{
		MeterID:    meterID,
		ReceivedAt: time.Now(),
		RSSI:       f.Header.RSSI,
		Raw:        f.Raw,
	}, nil
}

// handshakeStepRetries bounds how many times a single handshake step (one
// request/response exchange) is retried after getting a response that
// fails validation, before giving up and letting the caller tear the
// whole connection down and reconnect from scratch. On real hardware, a
// single bad response right after connecting, or right after the
// previous step's response, has turned out to be the receiver still
// settling — its own USB-serial bridge, or the device still finishing
// the previous exchange — far more often than a genuinely broken link,
// and clears up on an immediate retry much faster than a full reconnect
// (with its own port reset and backoff delay) would.
const handshakeStepRetries = 3

// handshakeStepRetryDelay is a var, not a const, so tests can shrink it.
var handshakeStepRetryDelay = 200 * time.Millisecond

// sendAndExpect returns the validated response's full content (prefix
// included) on success, so a caller can look at what follows the fixed
// prefix this function itself checks. It retries the exchange itself
// (see handshakeStepRetries) rather than failing on the first bad
// response — both requests this is used for (device info, C1/T1
// configuration) are plain queries/settings with no side effect that
// resending would double up on.
func sendAndExpect(ctx context.Context, conn Conn, request, wantPrefix []byte) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= handshakeStepRetries; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(handshakeStepRetryDelay):
			}
		}
		content, err := trySendAndExpect(ctx, conn, request, wantPrefix)
		if err == nil {
			return content, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func trySendAndExpect(ctx context.Context, conn Conn, request, wantPrefix []byte) ([]byte, error) {
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	frame, err := conn.NextFrame(ctx)
	if err != nil {
		return nil, err
	}
	if len(frame) < 2 {
		return nil, fmt.Errorf("control frame too short")
	}
	content := frame[:len(frame)-2]
	gotCRC := binary.LittleEndian.Uint16(frame[len(frame)-2:])
	if telegram.CRC16(content) != gotCRC {
		return nil, fmt.Errorf("control frame checksum mismatch")
	}
	if len(content) < len(wantPrefix) || !bytes.Equal(content[:len(wantPrefix)], wantPrefix) {
		return nil, fmt.Errorf("unexpected response % X", content)
	}
	return content, nil
}
