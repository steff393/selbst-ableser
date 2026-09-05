package main

import (
	"testing"
	"time"

	"selbst-ableser/collector/internal/settings"
)

// TestStartupWindowPushesLiveBeforeAnyEvaluatorContact covers the case the
// window exists for: someone has just plugged in a receiver and wants to
// see whether anything arrives, before (or without) the evaluator having
// the live view switched on.
func TestStartupWindowPushesLiveBeforeAnyEvaluatorContact(t *testing.T) {
	l := &liveSettings{startedAt: time.Now()}

	active, interval := l.confirmedActive()
	if !active {
		t.Fatal("a freshly started collector should push live without being asked")
	}
	if interval != startupLiveInterval {
		t.Errorf("interval = %v, want the startup default %v", interval, startupLiveInterval)
	}
}

// Once the window has passed, the evaluator alone decides.
func TestAfterStartupWindowTheEvaluatorDecides(t *testing.T) {
	l := &liveSettings{startedAt: time.Now().Add(-startupLiveWindow - time.Minute)}

	if active, _ := l.confirmedActive(); active {
		t.Error("past the startup window and with the live view off, nothing should push")
	}

	l.confirmed = true
	l.cur = settings.Settings{ReportIntervalSeconds: 7}
	active, interval := l.confirmedActive()
	if !active || interval != 7*time.Second {
		t.Errorf("with the evaluator asking for it: active=%v interval=%v, want true/7s", active, interval)
	}
}

// Inside the window the evaluator's own interval still wins if it has one,
// so switching the live view on with a chosen interval is not overridden
// by the startup default.
func TestStartupWindowPrefersTheConfiguredInterval(t *testing.T) {
	l := &liveSettings{startedAt: time.Now()}
	l.confirmed = true
	l.cur = settings.Settings{ReportIntervalSeconds: 3}

	_, interval := l.confirmedActive()
	if interval != 3*time.Second {
		t.Errorf("interval = %v, want the configured 3s", interval)
	}
}
