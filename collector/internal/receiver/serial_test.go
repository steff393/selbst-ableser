package receiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

// scriptedPort is a fake Port whose Read is driven entirely by the test: it
// delivers whatever byte slices are pushed onto reads, or fails with
// whatever error is pushed onto errs, and unblocks a pending Read as soon
// as Close is called (mirroring watchForCancellation's expectation that
// closing a port interrupts a blocked read).
type scriptedPort struct {
	reads chan []byte
	errs  chan error
	done  chan struct{}
	once  sync.Once
}

func newScriptedPort() *scriptedPort {
	return &scriptedPort{
		reads: make(chan []byte, 16),
		errs:  make(chan error, 4),
		done:  make(chan struct{}),
	}
}

func (p *scriptedPort) Write(b []byte) (int, error) { return len(b), nil }

func (p *scriptedPort) Read(b []byte) (int, error) {
	select {
	case chunk := <-p.reads:
		return copy(b, chunk), nil
	case err := <-p.errs:
		return 0, err
	case <-p.done:
		return 0, io.ErrClosedPipe
	}
}

func (p *scriptedPort) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

// buildResponseFrame builds a valid SLIP-framed, checksummed handshake
// response starting with prefix — matching exactly what sendAndExpect
// checks for, so a scriptedPort loaded with these satisfies initReceiver.
func buildResponseFrame(prefix []byte) []byte {
	crc := telegram.CRC16(prefix)
	content := append(append([]byte{}, prefix...), byte(crc), byte(crc>>8))
	return telegram.EncodeFrame(content)
}

func queueHandshakeOK(p *scriptedPort) {
	p.reads <- buildResponseFrame(deviceInfoPrefix)
	p.reads <- buildResponseFrame(configureC1T1Prefix)
}

// buildResponseFrameWithPayload is buildResponseFrame plus bytes past the
// fixed prefix — like a real device-info response would carry (firmware
// version, serial number, ...), which sendAndExpect's own prefix check
// never looks at.
func buildResponseFrameWithPayload(prefix, payload []byte) []byte {
	full := append(append([]byte{}, prefix...), payload...)
	crc := telegram.CRC16(full)
	content := append(full, byte(crc), byte(crc>>8))
	return telegram.EncodeFrame(content)
}

// TestSendAndExpectRetriesTransientBadResponse covers a real hardware
// report: a fresh connection's first handshake step sometimes gets one
// garbled or unrelated response immediately followed by a clean one.
// sendAndExpect must recover from that itself, without the caller
// tearing the whole connection down and reconnecting from scratch.
func TestSendAndExpectRetriesTransientBadResponse(t *testing.T) {
	orig := handshakeStepRetryDelay
	handshakeStepRetryDelay = time.Millisecond
	defer func() { handshakeStepRetryDelay = orig }()

	p := newScriptedPort()
	p.reads <- buildResponseFrame([]byte{0x00, 0x00, 0x00}) // valid frame, wrong prefix
	p.reads <- buildResponseFrame(deviceInfoPrefix)

	var lastDataAt atomic.Int64
	reader := newFrameReader(p, &lastDataAt)

	got, err := sendAndExpect(context.Background(), reader, deviceInfoRequest, deviceInfoPrefix)
	if err != nil {
		t.Fatalf("sendAndExpect() error = %v, want recovery on retry", err)
	}
	if !bytes.Equal(got, deviceInfoPrefix) {
		t.Errorf("sendAndExpect() = % X, want % X", got, deviceInfoPrefix)
	}
}

// TestSendAndExpectGivesUpAfterRetriesExhausted is the sibling check: a
// persistently wrong response must still fail, not retry forever.
func TestSendAndExpectGivesUpAfterRetriesExhausted(t *testing.T) {
	orig := handshakeStepRetryDelay
	handshakeStepRetryDelay = time.Millisecond
	defer func() { handshakeStepRetryDelay = orig }()

	p := newScriptedPort()
	for i := 0; i < handshakeStepRetries; i++ {
		p.reads <- buildResponseFrame([]byte{0x00, 0x00, 0x00})
	}

	var lastDataAt atomic.Int64
	reader := newFrameReader(p, &lastDataAt)

	if _, err := sendAndExpect(context.Background(), reader, deviceInfoRequest, deviceInfoPrefix); err == nil {
		t.Fatal("sendAndExpect() succeeded, want failure after exhausting retries on a persistently wrong response")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// TestSerialSourceReconnectFiresCallbacks drives a connect, a simulated
// unplug (a read error), and an automatic reconnect, and checks that
// OnConnect/OnDisconnect fire exactly where an operator-facing log message
// should appear: once per successful (re)connect, once per lost
// connection — never on every backoff retry while nothing is present yet.
func TestSerialSourceReconnectFiresCallbacks(t *testing.T) {
	var mu sync.Mutex
	var ports []*scriptedPort

	open := func() (Port, string, error) {
		p := newScriptedPort()
		queueHandshakeOK(p)
		mu.Lock()
		ports = append(ports, p)
		mu.Unlock()
		return p, "test-port", nil
	}

	src := NewSerialSource(open)
	src.backoff = time.Millisecond
	src.maxBackoff = 5 * time.Millisecond

	var connects, disconnects int32
	src.OnConnect = func(string) { atomic.AddInt32(&connects, 1) }
	src.OnDisconnect = func(err error) { atomic.AddInt32(&disconnects, 1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := src.Next(ctx); err != nil {
				return
			}
		}
	}()

	waitFor(t, "first connect", func() bool { return atomic.LoadInt32(&connects) == 1 })
	if disconnects := atomic.LoadInt32(&disconnects); disconnects != 0 {
		t.Errorf("disconnects = %d before any failure, want 0", disconnects)
	}

	mu.Lock()
	first := ports[0]
	mu.Unlock()
	first.errs <- io.EOF // simulate the receiver being unplugged

	waitFor(t, "disconnect callback", func() bool { return atomic.LoadInt32(&disconnects) == 1 })
	waitFor(t, "reconnect callback", func() bool { return atomic.LoadInt32(&connects) == 2 })

	cancel()
	<-done

	mu.Lock()
	opened := len(ports)
	mu.Unlock()
	if opened != 2 {
		t.Errorf("open() was called %d times, want exactly 2 (initial connect + one reconnect)", opened)
	}
}

// TestSerialSourceReportsDeviceInfoOnConnect checks that OnDeviceInfo
// receives each handshake step's *full* response, past sendAndExpect's own
// fixed-prefix check — the whole point being troubleshooting value
// (firmware/hardware identification) that a bare pass/fail never gives.
func TestSerialSourceReportsDeviceInfoOnConnect(t *testing.T) {
	firmwarePayload := []byte{0x01, 0x02, 0x03, 0x04}
	configPayload := []byte{0xAA}

	p := newScriptedPort()
	p.reads <- buildResponseFrameWithPayload(deviceInfoPrefix, firmwarePayload)
	p.reads <- buildResponseFrameWithPayload(configureC1T1Prefix, configPayload)
	open := func() (Port, string, error) { return p, "test-port", nil }

	src := NewSerialSource(open)

	type call struct {
		step string
		raw  []byte
	}
	var mu sync.Mutex
	var calls []call
	src.OnDeviceInfo = func(step string, raw []byte) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{step, append([]byte{}, raw...)})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { src.Next(ctx) }() //nolint:errcheck // only the callback firing matters here

	waitFor(t, "two device-info callbacks", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	})

	mu.Lock()
	defer mu.Unlock()
	wantInfo := append(append([]byte{}, deviceInfoPrefix...), firmwarePayload...)
	if calls[0].step != "device info" || !bytes.Equal(calls[0].raw, wantInfo) {
		t.Errorf("first call = %+v, want step %q raw % X", calls[0], "device info", wantInfo)
	}
	wantCfg := append(append([]byte{}, configureC1T1Prefix...), configPayload...)
	if calls[1].step != "C1/T1 configuration" || !bytes.Equal(calls[1].raw, wantCfg) {
		t.Errorf("second call = %+v, want step %q raw % X", calls[1], "C1/T1 configuration", wantCfg)
	}
}

// TestSerialSourceConnectErrorReportedOncePerFailureStreak covers the case
// this change was actually about: nothing plugged in at all. open() keeps
// failing identically on every backoff retry; OnConnectError must fire
// exactly once for that streak, not once per retry, and again only if the
// failure reason changes or a connect has since succeeded.
func TestSerialSourceConnectErrorReportedOncePerFailureStreak(t *testing.T) {
	var mu sync.Mutex
	var failWith error = errNoReceiver
	attempts := 0

	open := func() (Port, string, error) {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if failWith != nil {
			return nil, "", failWith
		}
		p := newScriptedPort()
		queueHandshakeOK(p)
		return p, "test-port", nil
	}

	src := NewSerialSource(open)
	src.backoff = time.Millisecond
	src.maxBackoff = 2 * time.Millisecond

	var connectErrors, connects int32
	src.OnConnectError = func(err error) { atomic.AddInt32(&connectErrors, 1) }
	src.OnConnect = func(string) { atomic.AddInt32(&connects, 1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := src.Next(ctx); err != nil {
				return
			}
		}
	}()

	// Several retries against the same failure: exactly one report, no
	// matter how many attempts it took.
	waitFor(t, "at least 5 failed attempts", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 5
	})
	if got := atomic.LoadInt32(&connectErrors); got != 1 {
		t.Errorf("connectErrors = %d after repeated identical failures, want exactly 1", got)
	}

	// A different failure reason should be reported again.
	mu.Lock()
	failWith = errWrongDevice
	mu.Unlock()
	waitFor(t, "second distinct failure reported", func() bool { return atomic.LoadInt32(&connectErrors) == 2 })

	// Once it starts succeeding, a later new failure should be reported
	// again too, not assumed already known.
	mu.Lock()
	failWith = nil
	mu.Unlock()
	waitFor(t, "connect succeeds", func() bool { return atomic.LoadInt32(&connects) == 1 })

	cancel()
	<-done
}

var (
	errNoReceiver  = errors.New("receiver: no IMST iU891A-XL (or compatible) receiver currently connected")
	errWrongDevice = errors.New("receiver: unexpected response")
)

func TestSetIdleTimeoutAndDerivedCheckInterval(t *testing.T) {
	orig := currentIdleTimeout()
	defer SetIdleTimeout(orig)

	SetIdleTimeout(4 * time.Second)
	if got := currentIdleTimeout(); got != 4*time.Second {
		t.Errorf("currentIdleTimeout() = %v, want 4s", got)
	}
	if got := idleCheckInterval(); got != time.Second {
		t.Errorf("idleCheckInterval() = %v, want 1s (a quarter of 4s)", got)
	}

	SetIdleTimeout(0) // ignored — must not disable the watchdog
	if got := currentIdleTimeout(); got != 4*time.Second {
		t.Errorf("currentIdleTimeout() after SetIdleTimeout(0) = %v, want unchanged 4s", got)
	}
	SetIdleTimeout(-time.Second) // likewise ignored
	if got := currentIdleTimeout(); got != 4*time.Second {
		t.Errorf("currentIdleTimeout() after SetIdleTimeout(negative) = %v, want unchanged 4s", got)
	}
}

// withShortIdleTimers temporarily overrides the idle-watchdog threshold
// to a test-friendly duration via the real SetIdleTimeout API, restoring
// it on cleanup. Real values (minutes) would make every test using them
// impossibly slow; idleCheckInterval() derives automatically as a quarter
// of whatever this is set to.
func withShortIdleTimers(t *testing.T) {
	t.Helper()
	orig := currentIdleTimeout()
	SetIdleTimeout(20 * time.Millisecond)
	t.Cleanup(func() { SetIdleTimeout(orig) })
}

// TestWatchConnectionClosesPortWhenIdleTooLong covers the actual bug
// report this exists for: on Windows in particular, a Read pending when
// the USB device is physically removed (and replugged) can stay pending
// forever — no error, no timeout — even though the device never even
// disappears from the OS's own device list. watchConnection does not
// wait on Read or on any "is it still there" signal at all; it
// force-closes the port from outside once nothing has been read for
// idleTimeout, which is what actually unblocks a Read stuck like that.
func TestWatchConnectionClosesPortWhenIdleTooLong(t *testing.T) {
	withShortIdleTimers(t)
	p := newScriptedPort()
	// No frames, no error, ever queued: this stands in for a Read that
	// would otherwise block forever on a surprise-removed device.
	var lastDataAt atomic.Int64
	lastDataAt.Store(time.Now().UnixNano())

	done := make(chan struct{})
	go watchConnection(context.Background(), p, done, &lastDataAt)

	select {
	case <-p.done:
		// port.Close() was called, as intended
	case <-time.After(2 * time.Second):
		t.Fatal("watchConnection did not close the port after going idle")
	}
}

// TestWatchConnectionLeavesPortOpenWhileDataKeepsArriving is the sibling
// check: it must not close a connection that keeps proving itself alive.
func TestWatchConnectionLeavesPortOpenWhileDataKeepsArriving(t *testing.T) {
	withShortIdleTimers(t)
	p := newScriptedPort()
	var lastDataAt atomic.Int64
	lastDataAt.Store(time.Now().UnixNano())

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(idleCheckInterval() / 2)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				lastDataAt.Store(time.Now().UnixNano())
			}
		}
	}()

	watchDone := make(chan struct{})
	go watchConnection(context.Background(), p, watchDone, &lastDataAt)

	select {
	case <-p.done:
		t.Fatal("watchConnection closed the port even though data kept arriving")
	case <-time.After(10 * currentIdleTimeout()):
		// still open, as expected — stop the watcher cleanly
	}
	close(watchDone)
}

// TestSerialSourceRecoversWhenReadNeverReturns is the end-to-end version,
// through Next() itself, of the exact scenario reported: a receiver whose
// Read call simply never comes back — not even an error — once the
// device is gone, indistinguishable from the OS's own point of view
// (COM14 stayed listed the whole time in this project's real report).
// Only the idle watchdog can recover from that; nothing else here ever
// gets control back to notice.
func TestSerialSourceRecoversWhenReadNeverReturns(t *testing.T) {
	withShortIdleTimers(t)

	var mu sync.Mutex
	var ports []*scriptedPort
	open := func() (Port, string, error) {
		p := newScriptedPort()
		queueHandshakeOK(p)
		mu.Lock()
		ports = append(ports, p)
		mu.Unlock()
		return p, "test-port", nil
		// Deliberately nothing further queued on this port: after the
		// handshake, its Read blocks forever, exactly like a stuck
		// overlapped read on a surprise-removed Windows device.
	}

	src := NewSerialSource(open)
	src.backoff = time.Millisecond
	src.maxBackoff = 2 * time.Millisecond

	var connects int32
	src.OnConnect = func(string) { atomic.AddInt32(&connects, 1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := src.Next(ctx); err != nil {
				return
			}
		}
	}()

	waitFor(t, "first connect", func() bool { return atomic.LoadInt32(&connects) == 1 })
	// From here, the port never delivers anything again — only the idle
	// watchdog can move things forward.
	waitFor(t, "idle watchdog forces a reconnect", func() bool { return atomic.LoadInt32(&connects) == 2 })

	mu.Lock()
	opened := len(ports)
	mu.Unlock()
	if opened != 2 {
		t.Errorf("open() was called %d times, want exactly 2", opened)
	}

	cancel()
	<-done
}
