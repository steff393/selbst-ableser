// Package livepush defines the wire format and a bounded in-memory buffer
// for the collector's "immediate push" to the evaluator's live view
// (UI-06, ZUGANG-05): telegrams stay encrypted in
// transit and in this buffer — the collector that sends them has no key
// material to do otherwise (ARCH-03), and the evaluator that receives them
// only decrypts on demand, in memory, for its own Betreiber-only display
// (FACH-12/SZ-3) — nothing here is ever written to disk.
package livepush

import (
	"sync"
	"time"
)

// Telegram is one pushed telegram, still fully encrypted.
type Telegram struct {
	MeterID    string    `json:"meter_id"`
	ReceivedAt time.Time `json:"received_at"`
	RSSI       int       `json:"rssi"`
	RawHex     string    `json:"raw_hex"`
}

// Buffer holds the most recently pushed telegrams, newest last, capped at
// a fixed size — a ring buffer, not an archive: nothing here is meant to
// survive a restart or to substitute for the real archive.
type Buffer struct {
	mu       sync.Mutex
	capacity int
	items    []Telegram
}

// NewBuffer creates a Buffer holding at most capacity telegrams.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 200
	}
	return &Buffer{capacity: capacity}
}

// Add appends telegrams, discarding the oldest entries beyond capacity.
func (b *Buffer) Add(telegrams ...Telegram) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = append(b.items, telegrams...)
	if over := len(b.items) - b.capacity; over > 0 {
		b.items = b.items[over:]
	}
}

// Clear discards every buffered telegram. Nothing else notices — the next
// poll from a running collector simply starts refilling it, this is not a
// durable state change (see the package doc comment).
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = nil
}

// Recent returns up to n of the most recently added telegrams, newest
// first. n <= 0 means "all of them".
func (b *Buffer) Recent(n int) []Telegram {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.items) {
		n = len(b.items)
	}
	out := make([]Telegram, n)
	for i := 0; i < n; i++ {
		out[i] = b.items[len(b.items)-1-i]
	}
	return out
}
