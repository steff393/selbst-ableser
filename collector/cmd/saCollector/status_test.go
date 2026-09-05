package main

import (
	"os"
	"testing"
	"time"
)

func TestCollectorStatusReportsIdentityAndReceiver(t *testing.T) {
	started := time.Date(2026, 3, 4, 11, 30, 0, 0, time.UTC)
	status := newCollectorStatus("dachboden", "0.4.2", started)

	report := status.snapshot()
	if report.Name != "dachboden" || report.Version != "0.4.2" || !report.StartedAt.Equal(started) {
		t.Fatalf("report = %+v, want the configured identity", report)
	}
	if report.Receiver.Connected {
		t.Error("receiver must not be reported as connected before it ever connected")
	}

	status.receiverConnected("/dev/ttyUSB0")
	report = status.snapshot()
	if !report.Receiver.Connected || report.Receiver.Port != "/dev/ttyUSB0" {
		t.Fatalf("receiver = %+v, want connected on /dev/ttyUSB0", report.Receiver)
	}
	since := report.Receiver.Since
	if since.IsZero() {
		t.Fatal("a connected receiver must carry the time it connected")
	}

	// A repeated connect on an unbroken run must not restart the clock:
	// "connected since" answers how long reception has been stable.
	status.receiverConnected("/dev/ttyUSB0")
	if got := status.snapshot().Receiver.Since; !got.Equal(since) {
		t.Errorf("Since = %v after a repeated connect, want the original %v", got, since)
	}

	status.receiverLost()
	report = status.snapshot()
	if report.Receiver.Connected || report.Receiver.Port != "" || !report.Receiver.Since.IsZero() {
		t.Errorf("receiver = %+v, want a cleared state after a loss", report.Receiver)
	}
}

// TestCollectorStatusNameFallback covers the no--name case: the hostname
// is the useful default, and the constant only has to hold where even
// that is unavailable.
func TestCollectorStatusNameFallback(t *testing.T) {
	status := newCollectorStatus("", "0.4.2", time.Now())
	want, err := os.Hostname()
	if err != nil || want == "" {
		want = "saCollector"
	}
	if status.name != want {
		t.Errorf("name = %q, want %q", status.name, want)
	}
	if status.name == "" {
		t.Error("a collector must always report some name")
	}
}
