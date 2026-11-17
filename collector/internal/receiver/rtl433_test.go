package receiver

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Real rtl_433 output, captured 2026-09-14 in a building with Techem
// devices. Line 1 is a heat cost allocator, line 2 a radio converter
// transmitting for a Wehrle water meter (link layer 02664326, meter
// 03543916), line 3 another allocator. The lines are shortened to the
// fields this reads plus a few of rtl_433's own decoded values — which
// are nonsense for these payloads and must be ignored — and keep the
// repeated keys the real output contains.
const (
	rtl433Allocator  = `{"time" : "2026-09-14T20:10:22", "model" : "Wireless-MBus", "mode" : "T", "M" : "TCH", "id" : 10797508, "data" : "32446850087579106980a0119f333201e012be0024092d09000000000000000000050a0c111410141d1f1e1b141711110d0801", "mic" : "CRC", "err_volume_flow_min_0" : "20.584 m3/min", "inst_energy_wh_0" : "0.000 Wh", "inst_energy_wh_0" : "1230001.000 kWh", "rssi" : -5.440}`
	rtl433Converter  = `{"time" : "2026-09-14T20:10:25", "model" : "Wireless-MBus", "mode" : "C", "M" : "TCH", "id" : 2664326, "data" : "4b4468502643660295377216395403a85cfe07b0003005155ed42729bf7f5a37d10b621e0f72adeba4110e779226de018d7453823ebfe86d613ba2d7c223c4926f52463a598d330fc43078b5", "mic" : "CRC", "rssi" : 1.312}`
	rtl433Allocator2 = `{"time" : "2026-09-14T20:11:41", "model" : "Wireless-MBus", "mode" : "T", "M" : "TCH", "id" : 10703982, "data" : "32446850823970106980a0119f331b00e012080032091e09000000000000000000000000000000020402001106040000000000", "mic" : "CRC", "rssi" : 1.046}`

	// Another 868 MHz device rtl_433 decodes, and two lines that are not
	// telegrams at all.
	rtl433Foreign  = `{"time" : "2026-09-14T20:11:00", "model" : "Bresser-3CH", "id" : 42, "temperature_C" : 19.3}`
	rtl433Garbage  = `not json at all`
	rtl433BadHex   = `{"model" : "Wireless-MBus", "data" : "nothex"}`
	rtl433TooShort = `{"model" : "Wireless-MBus", "data" : "3244"}`
)

// pipeStarter feeds the source from strings instead of a real process, one
// launch at a time, so a test can script what each restart produces.
func pipeStarter(t *testing.T, launches ...string) (starter, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	return func(ctx context.Context) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
		i := int(n.Add(1)) - 1
		if i >= len(launches) {
			return nil, nil, nil, errors.New("no rtl_433 to start")
		}
		return nil, io.NopCloser(strings.NewReader(launches[i])), io.NopCloser(strings.NewReader("")), nil
	}, &n
}

func TestRTL433ReadsRealCapturedOutput(t *testing.T) {
	start, _ := pipeStarter(t, strings.Join([]string{
		rtl433Allocator, rtl433Foreign, rtl433Garbage, rtl433BadHex, rtl433TooShort, rtl433Converter, rtl433Allocator2,
	}, "\n")+"\n")
	s := newRTL433Source(start)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The foreign device, the malformed lines and the unparsable telegrams
	// are skipped without interrupting the stream.
	want := []string{"10797508", "03543916", "10703982"}
	for _, wantID := range want {
		tel, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if tel.MeterID != wantID {
			t.Errorf("MeterID = %s, want %s", tel.MeterID, wantID)
		}
		if tel.RSSI != 0 {
			t.Errorf("RSSI = %d, want 0 — rtl_433 reports no calibrated dBm", tel.RSSI)
		}
		if len(tel.Raw) == 0 || int(tel.Raw[0])+1 != len(tel.Raw) {
			t.Errorf("Raw is not a complete telegram: %s", hex.EncodeToString(tel.Raw))
		}
	}
}

// The second meter number is the point of the whole exercise: the line
// says id 2664326, but that is the Techem radio converter. The telegram's
// long transport header names the water meter the data belongs to.
func TestRTL433TakesTheMeterNotTheConverter(t *testing.T) {
	start, _ := pipeStarter(t, rtl433Converter+"\n")
	s := newRTL433Source(start)
	defer s.Close()

	tel, err := s.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tel.MeterID == "02664326" {
		t.Fatal("filed the telegram under the radio converter's own number")
	}
	if tel.MeterID != "03543916" {
		t.Errorf("MeterID = %s, want 03543916", tel.MeterID)
	}
}

// A decoder that stops — the dongle pulled, rtl_433 crashing — is not the
// end of receiving: it is started again, and the operator is told.
func TestRTL433RestartsAfterTheDecoderStops(t *testing.T) {
	start, launches := pipeStarter(t, rtl433Allocator+"\n", rtl433Allocator2+"\n")
	s := newRTL433Source(start)
	s.backoff, s.maxBackoff = time.Millisecond, time.Millisecond
	defer s.Close()

	var connects, disconnects atomic.Int32
	s.OnConnect = func(string) { connects.Add(1) }
	s.OnDisconnect = func(error) { disconnects.Add(1) }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	second, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if first.MeterID == second.MeterID {
		t.Errorf("both telegrams came from the same launch (%s)", first.MeterID)
	}
	if launches.Load() != 2 || connects.Load() != 2 || disconnects.Load() != 1 {
		t.Errorf("launches=%d connects=%d disconnects=%d, want 2/2/1", launches.Load(), connects.Load(), disconnects.Load())
	}
}

// A decoder that runs but delivers nothing is restarted by the same
// watchdog a silent serial connection has.
func TestRTL433RestartsWhenNothingArrives(t *testing.T) {
	withShortIdleTimers(t)

	silent := make(chan struct{})
	t.Cleanup(func() { close(silent) })
	var launches atomic.Int32
	start := func(ctx context.Context) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
		if launches.Add(1) > 1 {
			return nil, io.NopCloser(strings.NewReader(rtl433Allocator + "\n")), io.NopCloser(strings.NewReader("")), nil
		}
		return nil, io.NopCloser(blockingReader{silent}), io.NopCloser(strings.NewReader("")), nil
	}

	s := newRTL433Source(start)
	s.backoff, s.maxBackoff = time.Millisecond, time.Millisecond
	defer s.Close()

	var idleErr error
	s.OnDisconnect = func(err error) { idleErr = err }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !errors.Is(idleErr, errRTL433Idle) {
		t.Errorf("disconnect reason = %v, want the idle timeout", idleErr)
	}
}

// blockingReader never returns data, standing in for a decoder that is
// running but hears nothing.
type blockingReader struct{ until chan struct{} }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.until
	return 0, io.EOF
}

// rtl_433's own messages are the only place a missing dongle or a driver
// still holding it is explained, so they have to reach the log.
func TestRTL433ForwardsDecoderOutput(t *testing.T) {
	start := func(ctx context.Context) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
		out := io.NopCloser(strings.NewReader(rtl433Allocator + "\n"))
		msgs := io.NopCloser(strings.NewReader("Found Rafael Micro R820T tuner\nTuned to 868.950MHz.\n"))
		return nil, out, msgs, nil
	}
	s := newRTL433Source(start)
	defer s.Close()

	got := make(chan string, 4)
	s.OnOutput = func(line string) { got <- line }

	if _, err := s.Next(context.Background()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	select {
	case line := <-got:
		if !strings.Contains(line, "R820T") {
			t.Errorf("first forwarded line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Error("rtl_433's own output never reached the log")
	}
}

// The real starter runs an actual process. This exercises it end to end —
// pipes, scanning, and Close killing what it started — with the test
// binary itself standing in for rtl_433.
func TestRTL433RunsARealProcess(t *testing.T) {
	s := newRTL433Source(func(ctx context.Context) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestRTL433HelperProcess")
		cmd.Env = append(os.Environ(), "SA_RTL433_HELPER=1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		return cmd, stdout, stderr, cmd.Start()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tel, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if tel.MeterID != "10797508" {
		t.Errorf("MeterID = %s, want 10797508", tel.MeterID)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if s.cmd != nil {
		t.Error("the process was not cleaned up")
	}
}

// TestRTL433HelperProcess is not a test: it is the stand-in rtl_433 the
// test above starts. Without the environment variable it does nothing, so
// a normal test run just skips over it.
func TestRTL433HelperProcess(t *testing.T) {
	if os.Getenv("SA_RTL433_HELPER") != "1" {
		return
	}
	os.Stderr.WriteString("Found Rafael Micro R820T tuner\n")
	os.Stdout.WriteString(rtl433Allocator + "\n")
	// Stay alive so the test can prove Close stops a running process.
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
