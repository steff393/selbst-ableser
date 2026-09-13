package receiver

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"selbst-ableser/collector/internal/telegram"
)

// innotasCaptureHex is 933 bytes recorded from a real Innotas receiver at
// 19200 baud (the diagnostic in cmd/receiverdump produced it, with the
// device explicitly told not to transmit — so this is the module talking
// entirely on its own). It holds twelve frames from six meters, and it
// starts with two stray 0x00 the port delivered before the first whole
// frame, which is exactly the resynchronization case worth keeping.
const innotasCaptureHex = "" +
	"0000685944934436718168483778077936718168934448040dff5f350082590000800007c006ffff00000000ffff0000" +
	"00005f38000000000080008000800080008000800080008000800080008000002f02fd170000046d0608493946166832" +
	"4493447155762735087a9e0000200b6e5100004b6e820000426c3f3ccb086e510000c2086c5f38326cffff046d190849" +
	"39b116685944934438718168483778077938718168934448040dff5f3500826900005e0107c006ffff00000000ffff00" +
	"0000005f38000000000080008000800080008000800080008000800080008000002f02fd170000046d07084939641668" +
	"5444934417215636333778077988005226934433070dff5f350082290000100007c113ffff960802003f3c828701005f" +
	"38200802001c001d001500260016001c001c00170016002100100020002f046d09084939ab1668594493443671816848" +
	"3778077936718168934448040dff5f350082590000110007c006ffff00000000ffff000000005f380000000000800080" +
	"00800080008000800080008000800080008000002f02fd170000046d080849394716684a449344715576273508780dff" +
	"5f3500829e0000600107b06effff510000003f3c820000005f385100000001000b000b000d00bbff0b000d000a000400" +
	"0000000000002f046d1a084939ad1668424493443671816848377236718168934448045a0000200c06000000004c0600" +
	"000000426cffffcc080600000000c2086c5f3802fd170000326cffff046d08084939471668324493442760762735087a" +
	"100000200b6e7101004b6e690100426c3f3ccb086e710100c2086c5f38326cffff046d1a084939ab1668594493443871" +
	"8168483778077938718168934448040dff5f350082690000f00007c006ffff00000000ffff000000005f380000000000" +
	"80008000800080008000800080008000800080008000002f02fd170000046d090849396416682a4468500489106076f0" +
	"a0009f33283500482835008084000209006ba1007cb2008dc3009ed4000fe500aa166859449344367181684837780779" +
	"36718168934448040dff5f3500825a0000610107c006ffff00000000ffff000000005f38000000000080008000800080" +
	"008000800080008000800080008000002f02fd170000046d0a0849394716685444934417215636333778077988005226" +
	"934433070dff5f3500822a0000600107c113ffff960802003f3c828701005f38200802001c001d001500260016001c00" +
	"1c00170016002100100020002f046d0b084939a916"

// splitAll runs the device's splitter over a whole byte stream the way
// frameReader does, returning every frame it yields.
func splitAll(t *testing.T, dev Device, stream []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	buf := stream
	for {
		frame, consumed, err := dev.SplitFrame(buf)
		buf = buf[consumed:]
		if err == telegram.ErrIncompleteFrame {
			return frames
		}
		if err != nil {
			t.Fatalf("SplitFrame: unexpected error %v", err)
		}
		frames = append(frames, frame)
	}
}

// TestInnotasSplitsRealCapture is the test the whole adapter rests on: a
// real recording, split and parsed by the real code, has to yield exactly
// the twelve telegrams that are in it, in order, with the meter numbers
// their headers carry.
func TestInnotasSplitsRealCapture(t *testing.T) {
	stream, err := hex.DecodeString(innotasCaptureHex)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if len(stream) != 933 {
		t.Fatalf("fixture is %d bytes, want the 933 that were captured", len(stream))
	}

	dev := innotasDevice{}
	frames := splitAll(t, dev, stream)

	wantMeters := []string{
		"68817136", "27765571", "68817138", "36562117", "68817136", "27765571",
		"68817136", "27766027", "68817138", "60108904", "68817136", "36562117",
	}
	if len(frames) != len(wantMeters) {
		t.Fatalf("got %d frames, want %d", len(frames), len(wantMeters))
	}
	for i, frame := range frames {
		tele, err := dev.ParseFrame(frame)
		if err != nil {
			t.Errorf("frame %d: ParseFrame: %v", i+1, err)
			continue
		}
		if tele.MeterID != wantMeters[i] {
			t.Errorf("frame %d: MeterID = %s, want %s", i+1, tele.MeterID, wantMeters[i])
		}
		// The frame handed on for archiving must be the telegram itself,
		// length byte first — the same shape the IMST path produces, so
		// everything downstream stays unaware of which receiver it came
		// from.
		if len(tele.Raw) == 0 || int(tele.Raw[0])+1 != len(tele.Raw) {
			t.Errorf("frame %d: Raw does not start with its own length byte: % X", i+1, tele.Raw[:min(8, len(tele.Raw))])
		}
	}
}

// TestInnotasReportsNoRSSI pins down a known limitation rather than
// leaving it to be rediscovered: the module can append a signal strength
// byte but the measured one does not, so every telegram from it reports
// zero — "not measured", not "no signal".
func TestInnotasReportsNoRSSI(t *testing.T) {
	stream, _ := hex.DecodeString(innotasCaptureHex)
	frames := splitAll(t, innotasDevice{}, stream)
	tele, err := innotasDevice{}.ParseFrame(frames[0])
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if tele.RSSI != 0 {
		t.Errorf("RSSI = %d, want 0 — this receiver does not report one", tele.RSSI)
	}
}

// TestInnotasSkipsLeadingJunk: the recording begins with two bytes that
// are not part of any frame, as a port reasonably often does right after
// opening. The first frame still has to be found, and the junk has to be
// consumed rather than kept.
func TestInnotasSkipsLeadingJunk(t *testing.T) {
	stream, _ := hex.DecodeString(innotasCaptureHex)
	frame, consumed, err := innotasDevice{}.SplitFrame(stream)
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	// Two junk bytes, then start byte, length byte, payload, stop byte.
	if want := 2 + 3 + int(stream[3]); consumed != want {
		t.Errorf("consumed = %d, want %d — the two junk bytes plus the whole first frame", consumed, want)
	}
	if frame[0] != stream[3] {
		t.Errorf("frame starts with 0x%02X, want the length byte 0x%02X", frame[0], stream[3])
	}
}

// TestInnotasIgnoresStartByteInsidePayload guards the one real hazard of
// this framing: 0x68 is an ordinary payload byte (it occurs inside meter
// addresses in the real capture). A candidate whose length does not point
// at a stop byte must be stepped over, not accepted.
func TestInnotasIgnoresStartByteInsidePayload(t *testing.T) {
	// A 0x68 followed by a plausible length that points at 0xFF rather
	// than a stop byte — decidably not a frame — and then a genuine one.
	// Its own payload carries a 0x68 too (meter addresses really do), so
	// this covers both halves of the hazard.
	falseStart := []byte{0x68, 0x0A, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xFF}
	real := []byte{0x68, 0x0A, 0x44, 0x93, 0x44, 0x36, 0x71, 0x81, 0x68, 0x35, 0x08, 0x7A, 0x16}
	buf := append(append([]byte{}, falseStart...), real...)

	frame, consumed, err := innotasDevice{}.SplitFrame(buf)
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	if consumed != len(buf) {
		t.Errorf("consumed = %d, want all %d bytes", consumed, len(buf))
	}
	if len(frame) != int(real[1])+1 {
		t.Errorf("frame length = %d, want %d — the false start must be stepped over", len(frame), int(real[1])+1)
	}
}

// TestInnotasDiscardsNoiseInsteadOfBuffering: on a line that only ever
// delivers bytes that cannot begin a frame, nothing may be retained, or
// the read buffer grows without bound.
func TestInnotasDiscardsNoiseInsteadOfBuffering(t *testing.T) {
	noise := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	frame, consumed, err := innotasDevice{}.SplitFrame(noise)
	if err != telegram.ErrIncompleteFrame {
		t.Fatalf("err = %v, want ErrIncompleteFrame", err)
	}
	if frame != nil {
		t.Errorf("frame = % X, want none", frame)
	}
	if consumed != len(noise) {
		t.Errorf("consumed = %d, want all %d noise bytes discarded", consumed, len(noise))
	}
}

// TestInnotasWaitsForTheRestOfAFrame: a frame that has arrived only
// partly must be kept, with everything before it dropped.
func TestInnotasWaitsForTheRestOfAFrame(t *testing.T) {
	partial := []byte{0xAA, 0xBB, 0x68, 0x20, 0x44, 0x93}
	frame, consumed, err := innotasDevice{}.SplitFrame(partial)
	if err != telegram.ErrIncompleteFrame {
		t.Fatalf("err = %v, want ErrIncompleteFrame", err)
	}
	if frame != nil {
		t.Errorf("frame = % X, want none yet", frame)
	}
	if consumed != 2 {
		t.Errorf("consumed = %d, want 2 — only the junk ahead of the start byte", consumed)
	}
}

// TestInnotasNeedsNoHandshake documents the deliberate no-op: the module
// talks on its own, and this adapter must not send it anything (see
// innotasDevice.Init for why guessing its command set would be a bad
// trade).
func TestInnotasNeedsNoHandshake(t *testing.T) {
	var written []byte
	conn := &recordingConn{onWrite: func(p []byte) { written = append(written, p...) }}
	if err := (innotasDevice{}).Init(t.Context(), conn, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Init wrote % X to the device, want nothing at all", written)
	}
}

type recordingConn struct{ onWrite func([]byte) }

func (c *recordingConn) Write(p []byte) (int, error) {
	c.onWrite(p)
	return len(p), nil
}

func (c *recordingConn) NextFrame(ctx context.Context) ([]byte, error) {
	return nil, errors.New("no frames expected during a no-op handshake")
}

// TestDeviceRegistry covers the promise that matters to an existing
// installation: without -receiver, nothing changes.
func TestDeviceRegistry(t *testing.T) {
	if DefaultDeviceName != "imst" {
		t.Errorf("DefaultDeviceName = %q — an installation that never passed -receiver must keep the receiver it always had", DefaultDeviceName)
	}
	def, err := DeviceByName(DefaultDeviceName)
	if err != nil {
		t.Fatalf("DeviceByName(%q): %v", DefaultDeviceName, err)
	}
	if def.BaudRate() != 115200 {
		t.Errorf("default baud rate = %d, want 115200", def.BaudRate())
	}

	inn, err := DeviceByName("innotas")
	if err != nil {
		t.Fatalf("DeviceByName(\"innotas\"): %v", err)
	}
	if inn.BaudRate() != 19200 {
		t.Errorf("innotas baud rate = %d, want 19200", inn.BaudRate())
	}

	if _, err := DeviceByName("nope"); err == nil {
		t.Error("an unknown receiver name should be an error, not a silent default")
	}
}

// TestSerialSourceReceivesFromInnotas is the end-to-end check through the
// real SerialSource: no handshake is sent, the capture is delivered in
// chunks that deliberately cut frames in half (a serial port does not
// respect frame boundaries), and telegrams still come out whole and in
// order.
func TestSerialSourceReceivesFromInnotas(t *testing.T) {
	stream, _ := hex.DecodeString(innotasCaptureHex)

	p := newScriptedPort()
	for off := 0; off < len(stream); off += 100 {
		end := off + 100
		if end > len(stream) {
			end = len(stream)
		}
		p.reads <- append([]byte{}, stream[off:end]...)
	}

	open := func() (Port, string, error) { return p, "test-port", nil }
	src := NewSerialSource(open, innotasDevice{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	want := []string{"68817136", "27765571", "68817138", "36562117"}
	for i, wantMeter := range want {
		tele, err := src.Next(ctx)
		if err != nil {
			t.Fatalf("telegram %d: Next: %v", i+1, err)
		}
		if tele.MeterID != wantMeter {
			t.Errorf("telegram %d: MeterID = %s, want %s", i+1, tele.MeterID, wantMeter)
		}
		if tele.ReceivedAt.IsZero() {
			t.Errorf("telegram %d: ReceivedAt not set", i+1)
		}
	}
	// The property that matters as wired up, not just in isolation:
	// bringing this receiver online sends it nothing at all.
	if wrote := p.written.Load(); wrote != 0 {
		t.Errorf("%d bytes were sent to the receiver, want none", wrote)
	}
}
