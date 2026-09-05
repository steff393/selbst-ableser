package receiver

import (
	"context"
	"time"
)

// Telegram is one received radio telegram together with its reception
// metadata. The payload is still in whatever encryption state it arrived
// in; the collector never decrypts it.
type Telegram struct {
	MeterID    string
	ReceivedAt time.Time
	RSSI       int
	Raw        []byte
}

// Source produces telegrams as they are received. Implementations must
// never require key material, master data, or access credentials — the
// collector module cannot even import a package that provides them (see
// internal/telegram/doc.go).
type Source interface {
	// Next blocks until a telegram is available or ctx is canceled.
	Next(ctx context.Context) (Telegram, error)
	Close() error
}
