package access

import (
	"sync"
	"time"
)

// sweepThreshold is how many distinct keys accumulate before Allow does a
// cleanup pass — so a normal call stays O(1) (or O(one key's own recent
// attempts)), and the O(n) sweep only runs once growth has made one worth
// doing, rather than on every single call.
const sweepThreshold = 1000

// maxTrackedKeys bounds how many distinct keys a Limiter holds onto at
// once, even under a sustained flood of keys that are all still genuinely
// live (so a sweep alone can't remove any of them) — e.g. an attacker
// varying their source address on every attempt, cheap across an IPv6
// /64. Without a hard cap, that would grow the map without bound (a
// memory-exhaustion DoS) while simultaneously defeating the rate limit
// itself, since every never-before-seen key starts back at zero.
const maxTrackedKeys = 20000

// Limiter is a sliding-window rate limiter keyed by an arbitrary string
// (e.g. "unlock:203.0.113.5") — ZUGANG-06 requires login, unlock, and data
// retrieval to be protected against automated attempts, with unlock held
// to a stricter limit since it targets the installation's most critical
// secret.
type Limiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts map[string][]time.Time
	now      func() time.Time
}

// NewLimiter creates a Limiter allowing at most max attempts per key
// within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{
		max:      max,
		window:   window,
		attempts: make(map[string][]time.Time),
		now:      time.Now,
	}
}

// Allow reports whether another attempt for key is currently permitted. It
// always records the attempt, so calling it repeatedly correctly exhausts
// (and, after the window passes, replenishes) the budget — callers must
// call it once per real attempt, not just once per eventual success.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	if len(l.attempts) >= sweepThreshold {
		l.sweep(cutoff)
	}

	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.attempts[key] = kept
		return false
	}

	if _, exists := l.attempts[key]; !exists && len(l.attempts) >= maxTrackedKeys {
		l.evictOne()
	}
	l.attempts[key] = append(kept, now)
	return true
}

// sweep drops every key whose attempts are all older than cutoff — a key
// with nothing left to say "no" about, at the cost of forgetting it
// existed rather than remembering it forever.
func (l *Limiter) sweep(cutoff time.Time) {
	for k, times := range l.attempts {
		stillLive := false
		for _, t := range times {
			if t.After(cutoff) {
				stillLive = true
				break
			}
		}
		if !stillLive {
			delete(l.attempts, k)
		}
	}
}

// evictOne drops one arbitrary tracked key to make room for a new one,
// once maxTrackedKeys is reached without sweep having found anything
// removable (a sustained flood of keys that are all still genuinely
// live). Go's randomized map iteration order makes this an effectively
// random choice, not a predictable one an attacker could aim at.
func (l *Limiter) evictOne() {
	for k := range l.attempts {
		delete(l.attempts, k)
		return
	}
}
