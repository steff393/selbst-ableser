package access

import (
	"strconv"
	"testing"
	"time"
)

func TestLimiterAllowsUpToMax(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("the 4th attempt within the window should be denied")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	if !l.Allow("a") {
		t.Fatal("first attempt for key a should be allowed")
	}
	if !l.Allow("b") {
		t.Fatal("first attempt for a different key should be allowed regardless of key a's state")
	}
}

func TestLimiterResetsAfterWindow(t *testing.T) {
	now := time.Now()
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return now }

	if !l.Allow("k") {
		t.Fatal("first attempt should be allowed")
	}
	if l.Allow("k") {
		t.Fatal("second attempt within the window should be denied")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("k") {
		t.Fatal("attempt after the window has passed should be allowed again")
	}
}

// TestLimiterSweepsStaleKeys covers M2: a key with nothing but expired
// attempts must eventually be forgotten, not held onto forever — the map
// growing without bound as callers vary their key (e.g. across many
// source addresses) is itself the memory-exhaustion DoS this closes.
func TestLimiterSweepsStaleKeys(t *testing.T) {
	now := time.Now()
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return now }

	// Enough distinct, now-stale keys to cross sweepThreshold.
	for i := 0; i < sweepThreshold+10; i++ {
		l.Allow(strconv.Itoa(i))
	}
	if got := len(l.attempts); got != sweepThreshold+10 {
		t.Fatalf("seeded %d keys, want them all present before expiry", got)
	}

	// Age everything out, then make one more call — past sweepThreshold,
	// so this call must trigger a sweep.
	now = now.Add(2 * time.Minute)
	l.Allow("fresh-key")

	if got := len(l.attempts); got != 1 {
		t.Errorf("after a sweep past expiry, len(attempts) = %d, want 1 (only the fresh key)", got)
	}
}

// TestLimiterCapsTrackedKeys covers the other half of M2: even a flood of
// keys that never go stale (an attacker varying their source address on
// every single attempt, always within the window) must not grow the map
// without bound.
func TestLimiterCapsTrackedKeys(t *testing.T) {
	now := time.Now()
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < maxTrackedKeys+500; i++ {
		l.Allow(strconv.Itoa(i))
	}

	if got := len(l.attempts); got > maxTrackedKeys {
		t.Errorf("len(attempts) = %d, want capped at %d", got, maxTrackedKeys)
	}
}

// A sweep must never remove a key that still has a live attempt in it —
// the whole point is forgetting only what's genuinely expired.
func TestLimiterSweepKeepsLiveKeys(t *testing.T) {
	now := time.Now()
	l := NewLimiter(5, time.Minute)
	l.now = func() time.Time { return now }

	l.Allow("still-live")
	for i := 0; i < sweepThreshold+10; i++ {
		l.Allow(strconv.Itoa(i))
	}
	now = now.Add(2 * time.Minute)
	l.Allow("still-live") // refreshes it just before the sweep-triggering call
	l.Allow("trigger-sweep")

	if _, ok := l.attempts["still-live"]; !ok {
		t.Error("a key refreshed just before the sweep must survive it")
	}
}
