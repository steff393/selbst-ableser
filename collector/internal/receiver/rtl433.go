package receiver

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"selbst-ableser/collector/internal/telegram"
)

// RTL433SourceName selects this source on the command line, in the same
// place a serial receiver model is named.
const RTL433SourceName = "rtl433"

// DefaultRTL433Command is looked up in PATH when no explicit path is given.
const DefaultRTL433Command = "rtl_433"

// rtl433Model is the value rtl_433 puts in every wM-Bus telegram's "model"
// field. Its other decoders stay on (their protocol numbers shift between
// releases, so switching them off by number would silently select the
// wrong ones after an update); their lines are simply skipped here.
const rtl433Model = "Wireless-MBus"

// rtl433Args tunes rtl_433 to wM-Bus modes C and T at 868.95 MHz, the ones
// German heat and water meters use, and asks for one JSON object per
// telegram. Not configurable on purpose: a receiver that needs different
// radio settings is a different device, and guessing at half-supported
// combinations is what -receiver exists to avoid.
//
// Deliberately without -M level: rtl_433's RSSI is a relative figure from
// the SDR, not the calibrated dBm a wM-Bus module reports (captured values
// run above 0), so nothing downstream could use it.
func rtl433Args() []string {
	return []string{"-f", "868.95M", "-s", "1200k", "-F", "json"}
}

// RTL433Source receives through rtl_433, which listens with an RTL-SDR
// dongle and prints one JSON object per decoded telegram.
//
// It is a Source, not a Device: an SDR does not deliver framed telegrams
// over a serial port, it delivers raw radio, and the demodulation happens
// in a separate program. That program is started here and its output read
// line by line, which keeps the collector a single static binary with no C
// dependency on librtlsdr — and rtl_433 (GPL-2.0) a program the operator
// installs, not one shipped with this one.
//
// rtl_433 checks every telegram's CRC itself and prints only the ones that
// pass, so — unlike a raw demodulator — nothing here has to sort broken
// frames out.
type RTL433Source struct {
	start      starter
	backoff    time.Duration
	maxBackoff time.Duration

	// running says whether a decoder is up, kept here rather than read off
	// cmd: a test substitutes the starter and has no process handle at
	// all, and inferring it from cmd would then restart the decoder
	// before every single telegram.
	running bool
	cmd     *exec.Cmd
	lines   chan []byte
	fail    chan error

	// OnConnect, OnDisconnect and OnConnectError mirror SerialSource's
	// callbacks, for the same reason: this package has no logging
	// dependency of its own. OnConnect receives the command that was
	// started, which is what the evaluator shows as the receiver's
	// whereabouts.
	OnConnect      func(command string)
	OnDisconnect   func(err error)
	OnConnectError func(err error)

	// OnOutput, if set, receives rtl_433's own stderr lines. They carry
	// the messages that explain a silent receiver — no device found, a
	// driver still claiming the dongle, or which tuner it did find.
	OnOutput func(line string)
}

// starter launches the decoder and returns its two output streams. A test
// substitutes it to run a stand-in without an SDR attached.
type starter func(ctx context.Context) (cmd *exec.Cmd, stdout, stderr io.ReadCloser, err error)

// NewRTL433Source receives through the rtl_433 binary at path (empty means
// DefaultRTL433Command, looked up in PATH).
func NewRTL433Source(path string) *RTL433Source {
	if path == "" {
		path = DefaultRTL433Command
	}
	return newRTL433Source(func(ctx context.Context) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, path, rtl433Args()...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("receiver: starting %s: %w", path, err)
		}
		return cmd, stdout, stderr, nil
	})
}

func newRTL433Source(start starter) *RTL433Source {
	return &RTL433Source{start: start, backoff: time.Second, maxBackoff: 30 * time.Second}
}

var errRTL433Idle = errors.New("receiver: no telegram for the idle timeout")

// Next returns the next telegram, starting rtl_433 if it is not running
// and restarting it if it stops or falls silent.
func (s *RTL433Source) Next(ctx context.Context) (Telegram, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Telegram{}, err
		}
		if !s.running {
			if err := s.launch(ctx); err != nil {
				if s.OnConnectError != nil {
					s.OnConnectError(err)
				}
				if err := s.wait(ctx); err != nil {
					return Telegram{}, err
				}
				continue
			}
		}

		line, err := s.nextLine(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return Telegram{}, ctx.Err()
			}
			s.stop()
			if s.OnDisconnect != nil {
				s.OnDisconnect(err)
			}
			if err := s.wait(ctx); err != nil {
				return Telegram{}, err
			}
			continue
		}

		tel, ok := parseRTL433Line(line)
		if !ok {
			continue // another decoder's device, or a telegram we cannot read
		}
		return tel, nil
	}
}

// Close stops rtl_433 if it is running.
func (s *RTL433Source) Close() error {
	s.stop()
	return nil
}

func (s *RTL433Source) launch(ctx context.Context) error {
	cmd, stdout, stderr, err := s.start(ctx)
	if err != nil {
		return err
	}
	s.cmd = cmd
	s.running = true
	s.lines = make(chan []byte, 16)
	s.fail = make(chan error, 1)

	lines, fail := s.lines, s.fail
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		err := sc.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case fail <- fmt.Errorf("receiver: rtl_433 stopped: %w", err):
		default:
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if s.OnOutput != nil {
				s.OnOutput(sc.Text())
			}
		}
	}()

	s.backoff = time.Second
	if s.OnConnect != nil {
		s.OnConnect(commandLine(cmd))
	}
	return nil
}

// nextLine waits for the next line of output, for the decoder to stop, or
// for the idle timeout to run out — the same watchdog a serial connection
// has (see idleTimeoutNanos), for the same reason: a receiver that has
// stopped delivering looks exactly like one with nothing to deliver, and
// rebuilding it costs a few seconds.
func (s *RTL433Source) nextLine(ctx context.Context) ([]byte, error) {
	// Lines already read come first, even when the decoder has stopped in
	// the meantime: its output and its exit arrive on two channels, and a
	// plain select over both would pick either one. Telegrams that were
	// received before the exit would then be dropped in favour of
	// reporting it, which is exactly the wrong way round.
	select {
	case line := <-s.lines:
		return line, nil
	default:
	}

	idle := time.NewTimer(currentIdleTimeout())
	defer idle.Stop()
	select {
	case line := <-s.lines:
		return line, nil
	case err := <-s.fail:
		return nil, err
	case <-idle.C:
		return nil, errRTL433Idle
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *RTL433Source) stop() {
	if !s.running {
		return
	}
	s.running = false
	if s.cmd == nil {
		return // a substituted starter: nothing of our own to kill
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	s.cmd = nil
}

// wait pauses before the next attempt, lengthening the pause while
// attempts keep failing.
func (s *RTL433Source) wait(ctx context.Context) error {
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
	return nil
}

func commandLine(cmd *exec.Cmd) string {
	if cmd == nil {
		return RTL433SourceName
	}
	return cmd.Path
}

// rtl433Telegram is the part of rtl_433's JSON this needs. Its output
// carries many more fields — including values it decoded itself, which are
// meaningless for manufacturer-specific or encrypted payloads and are
// deliberately ignored: a telegram is read by this system's own decoders
// or not at all.
type rtl433Telegram struct {
	Model string `json:"model"`
	Data  string `json:"data"`
}

// parseRTL433Line turns one line of rtl_433 JSON into a telegram. A line
// that is not a wM-Bus telegram, or one this system cannot parse as such,
// reports false rather than an error: the output is a stream of whatever
// is on the air, and a single unusable line must not take the receiver
// down.
func parseRTL433Line(line []byte) (Telegram, bool) {
	var msg rtl433Telegram
	if err := json.Unmarshal(line, &msg); err != nil || msg.Model != rtl433Model || msg.Data == "" {
		return Telegram{}, false
	}
	raw, err := hex.DecodeString(msg.Data)
	if err != nil {
		return Telegram{}, false
	}
	f, err := telegram.ParseWMBus(raw)
	if err != nil {
		return Telegram{}, false
	}
	meterID, err := f.MeterNumber()
	if err != nil {
		return Telegram{}, false
	}
	// RSSI stays zero — rtl_433 reports a relative figure, not the dBm a
	// wM-Bus module measures, and everything downstream reads zero as
	// "not reported" (see telegram.ClassifyRSSI).
	return Telegram{MeterID: meterID, ReceivedAt: time.Now(), Raw: f.Raw}, true
}
