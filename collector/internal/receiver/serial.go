package receiver

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

// Port is the minimal interface a receiver connection must satisfy. It is
// implemented both by the real serial backend (see serialport.go) and by
// fakes in tests.
type Port interface {
	io.Reader
	io.Writer
	Close() error
}

// PortOpener opens a fresh connection to the receiver. It is called again
// on every (re)connect attempt, so it must perform any device discovery or
// fixed-path opening itself.
type PortOpener func() (Port, string, error)

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

// idleTimeoutNanos governs the fallback recovery path for a connection
// that looks fine but has gone quietly dead: on Windows, a USB-serial
// device that is unplugged and replugged does not necessarily disappear
// from the OS's device list, or reflect anything at all in its (still
// open) file handle's behavior — Read on it may just never return again,
// no error, no timeout. There is no reliable positive signal to detect
// that state from the handle itself, so instead of trying to detect it
// directly, the connection is simply rebuilt from scratch (close, reopen,
// full init sequence) whenever nothing at all — not even a corrupt frame
// — has come in for this long, checked at a quarter of that interval. A
// working connection in a real installation should see some radio
// activity well within the default two minutes (its own meters or simply
// neighboring devices); one that does not is worth refreshing.
//
// Stored as an atomic int64 of time.Duration nanoseconds (not a plain
// var) because SetIdleTimeout can be called at any time, from the
// evaluator-settings poll goroutine, concurrently with watchConnection
// goroutines reading it — see cmd/saCollector, where a fetched setting
// updates this on every settings refresh.
var idleTimeoutNanos atomic.Int64

func init() {
	idleTimeoutNanos.Store(int64(2 * time.Minute))
}

// SetIdleTimeout changes the idle-reconnect threshold new and existing
// connections use from now on. d <= 0 is ignored (keeps whatever was
// configured before) rather than disabling the watchdog entirely — an
// idle collector connection should always eventually self-heal.
func SetIdleTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	idleTimeoutNanos.Store(int64(d))
}

func currentIdleTimeout() time.Duration {
	return time.Duration(idleTimeoutNanos.Load())
}

// idleCheckInterval returns how often watchConnection re-checks — a
// quarter of the current idle timeout, so the actual detection delay is
// never more than 25% later than the configured threshold. Keeping this
// value sane in absolute terms (not, say, sub-second) is the evaluator
// settings page's job when it validates idle_reconnect_seconds, not
// this package's — it just does the mechanical division.
func idleCheckInterval() time.Duration {
	if interval := currentIdleTimeout() / 4; interval > 0 {
		return interval
	}
	return time.Second // currentIdleTimeout() is always positive in practice; defensive only
}

// frameReader incrementally extracts SLIP-delimited frames from a Port. It
// retains any bytes read past the end of one frame for the next call, so
// that multiple frames delivered by a single underlying Read are not lost
// — a real serial port (and a test double) can easily deliver more than
// one frame's worth of bytes at once.
type frameReader struct {
	port Port
	buf  []byte

	// lastDataAt, if set, is stamped (UnixNano) every time Read returns
	// any bytes at all, valid frame or not — watchConnection uses it to
	// notice a connection that has gone idle for too long (see
	// idleTimeout above).
	lastDataAt *atomic.Int64
}

func newFrameReader(port Port, lastDataAt *atomic.Int64) *frameReader {
	return &frameReader{port: port, lastDataAt: lastDataAt}
}

// next returns the next frame's unescaped content (without delimiters,
// including its trailing checksum), blocking on port reads as needed.
//
// An error other than telegram.ErrMalformedEscape means the underlying
// port itself failed (disconnect, I/O error, EOF, or watchConnection
// force-closing it) and must be treated as a connection loss;
// ErrMalformedEscape means only this one frame is corrupt.
func (r *frameReader) next(ctx context.Context) ([]byte, error) {
	for {
		frame, consumed, err := telegram.SplitFrame(r.buf)
		if err != telegram.ErrIncompleteFrame {
			r.buf = r.buf[consumed:]
			return frame, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk := make([]byte, 4096)
		n, rerr := r.port.Read(chunk)
		if n > 0 {
			r.buf = append(r.buf, chunk[:n]...)
			if r.lastDataAt != nil {
				r.lastDataAt.Store(time.Now().UnixNano())
			}
		}
		if rerr != nil {
			return nil, rerr
		}
	}
}

// initReceiver performs the wake-up and configuration handshake, reading
// responses through reader so any bytes it buffers past the handshake
// (e.g. the start of a radio telegram arriving right afterwards) remain
// available to the caller afterwards.
//
// onResponse, if not nil, is called once per handshake step that gets a
// validated response — with the full content (prefix included), not just
// the fact that it matched — so a caller can surface it for
// troubleshooting (firmware/hardware identification from the device-info
// bytes beyond the fixed prefix this package itself checks, or confirming
// what the receiver actually echoed back for the C1/T1 configuration).
// Never called for a step that fails; the returned error already carries
// whatever was received in that case (see sendAndExpect).
func initReceiver(ctx context.Context, reader *frameReader, onResponse func(step string, raw []byte)) error {
	if _, err := reader.port.Write(wakeUpSequence); err != nil {
		return fmt.Errorf("receiver: wake-up write failed: %w", err)
	}
	info, err := sendAndExpect(ctx, reader, deviceInfoRequest, deviceInfoPrefix)
	if err != nil {
		return fmt.Errorf("receiver: device-info handshake failed: %w", err)
	}
	if onResponse != nil {
		onResponse("device info", info)
	}
	cfg, err := sendAndExpect(ctx, reader, configureC1T1Req, configureC1T1Prefix)
	if err != nil {
		return fmt.Errorf("receiver: C1/T1 configuration handshake failed: %w", err)
	}
	if onResponse != nil {
		onResponse("C1/T1 configuration", cfg)
	}
	return nil
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
func sendAndExpect(ctx context.Context, reader *frameReader, request, wantPrefix []byte) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= handshakeStepRetries; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(handshakeStepRetryDelay):
			}
		}
		content, err := trySendAndExpect(ctx, reader, request, wantPrefix)
		if err == nil {
			return content, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func trySendAndExpect(ctx context.Context, reader *frameReader, request, wantPrefix []byte) ([]byte, error) {
	if _, err := reader.port.Write(request); err != nil {
		return nil, err
	}
	frame, err := reader.next(ctx)
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

// SerialSource receives telegrams from a wM-Bus receiver connected over a
// serial link. It reconnects automatically (with backoff) if the
// connection is lost, and discards any frame that fails validation without
// interrupting reception.
type SerialSource struct {
	open       PortOpener
	port       Port
	reader     *frameReader
	watchDone  chan struct{} // closed to stop the goroutine watching ctx for the current connection
	backoff    time.Duration
	maxBackoff time.Duration
	lastDataAt atomic.Int64 // see idleTimeout

	// OnConnect and OnDisconnect, if set, are called after every successful
	// (re)connect and every lost connection respectively — the initial
	// connect and any later reconnect are treated alike (this package has
	// no logging dependency of its own, see internal/telegram/doc.go, so an
	// operator-facing "receiver (re)connected"/"connection lost" message
	// belongs at the call site, driven by these).
	//
	// OnConnect receives the port that was actually opened. With
	// auto-detection that is only known here, inside the opener, and it is
	// what the evaluator shows as the receiver's whereabouts.
	OnConnect    func(port string)
	OnDisconnect func(err error)

	// OnConnectError, if set, is called once when connecting first starts
	// failing (no receiver present yet, or a receiver present but not
	// answering correctly), and again only if the failure reason changes —
	// never once per backoff retry, which for a receiver that is simply
	// not plugged in yet would otherwise repeat forever.
	OnConnectError func(err error)
	lastConnectErr string

	// OnDeviceInfo, if set, is called once per successful (re)connect for
	// each vendor-specific handshake step (device info, then C1/T1
	// configuration) with that step's validated response — for
	// troubleshooting, see initReceiver's doc comment.
	OnDeviceInfo func(step string, raw []byte)
}

// NewSerialSource creates a SerialSource that uses open to (re)connect.
func NewSerialSource(open PortOpener) *SerialSource {
	return &SerialSource{
		open:       open,
		backoff:    time.Second,
		maxBackoff: 30 * time.Second,
	}
}

func (s *SerialSource) Next(ctx context.Context) (Telegram, error) {
	for {
		if err := s.ensureConnected(ctx); err != nil {
			return Telegram{}, err
		}

		frame, err := s.reader.next(ctx)
		if err == nil {
			f, perr := telegram.Parse(frame)
			if perr != nil {
				continue // fails a protocol check: discard, keep receiving
			}
			meterID, _ := f.MeterNumber() // already validated by Parse
			return Telegram{
				MeterID:    meterID,
				ReceivedAt: time.Now(),
				RSSI:       f.Header.RSSI,
				Raw:        f.Raw,
			}, nil
		}
		if errors.Is(err, telegram.ErrMalformedEscape) {
			continue // one corrupt frame: discard, keep receiving
		}

		// The port itself failed. This is also how a canceled ctx can show
		// up here: frameReader.next already checks ctx between reads, but
		// if cancellation happens while a read is in flight, the watcher
		// goroutine started in ensureConnected closes the port to unblock
		// it instead. Tell the two apart before deciding whether to
		// reconnect or to stop.
		s.disconnect()
		if ctx.Err() != nil {
			return Telegram{}, ctx.Err()
		}
		if s.OnDisconnect != nil {
			s.OnDisconnect(err)
		}
		s.backoff = time.Second
	}
}

func (s *SerialSource) ensureConnected(ctx context.Context) error {
	if s.port != nil {
		return nil
	}
	for {
		port, path, err := s.open()
		if err == nil {
			s.lastDataAt.Store(time.Now().UnixNano()) // fresh grace period for this connection
			reader := newFrameReader(port, &s.lastDataAt)
			watchDone := make(chan struct{})
			go watchConnection(ctx, port, watchDone, &s.lastDataAt)

			ierr := initReceiver(ctx, reader, s.OnDeviceInfo)
			if ierr == nil {
				s.port = port
				s.reader = reader
				s.watchDone = watchDone
				s.lastConnectErr = ""
				if s.OnConnect != nil {
					s.OnConnect(path)
				}
				return nil
			}
			close(watchDone)
			port.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.noteConnectError(ierr)
		} else {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.noteConnectError(err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.backoff):
		}
		if s.backoff < s.maxBackoff {
			s.backoff *= 2
			if s.backoff > s.maxBackoff {
				s.backoff = s.maxBackoff
			}
		}
	}
}

// watchConnection closes port as soon as ctx is done, or as soon as
// lastDataAt shows nothing has been read for idleTimeout, so a blocking
// Read on it returns instead of waiting indefinitely for data that may
// never come again (see idleTimeout's doc comment for why this is a
// timeout rather than a positive "is it still there" check). Closing port
// is what actually unblocks a Read stuck like that; frameReader's own
// read loop never gets control back on its own to notice anything is
// wrong. It stops watching once watchDone is closed, which happens when
// the connection is torn down for any other reason.
func watchConnection(ctx context.Context, port Port, watchDone <-chan struct{}, lastDataAt *atomic.Int64) {
	for {
		// Recomputed every iteration, not fixed once via time.NewTicker,
		// so a timeout changed through SetIdleTimeout while this
		// connection is already open takes effect on the very next check
		// instead of only on the next reconnect.
		select {
		case <-ctx.Done():
			port.Close()
			return
		case <-watchDone:
			return
		case <-time.After(idleCheckInterval()):
			last := time.Unix(0, lastDataAt.Load())
			if time.Since(last) >= currentIdleTimeout() {
				port.Close()
				return
			}
		}
	}
}

// noteConnectError calls OnConnectError, but only if err's message differs
// from the last one reported — an unchanging failure (typically: still no
// receiver plugged in) stays silent after its first report instead of
// repeating on every backoff retry.
func (s *SerialSource) noteConnectError(err error) {
	if s.OnConnectError == nil || err.Error() == s.lastConnectErr {
		return
	}
	s.lastConnectErr = err.Error()
	s.OnConnectError(err)
}

// disconnect tears down the current connection, if any, including
// stopping its cancellation watcher.
func (s *SerialSource) disconnect() {
	if s.watchDone != nil {
		close(s.watchDone)
		s.watchDone = nil
	}
	if s.port != nil {
		s.port.Close()
		s.port = nil
	}
	s.reader = nil
}

func (s *SerialSource) Close() error {
	s.disconnect()
	return nil
}
