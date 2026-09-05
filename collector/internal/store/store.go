// Package store holds the collector's own telegram bookkeeping: the
// in-memory working buffer ("buffer.db") that the receive loop writes to
// and the report/live-view loops read from, and the on-disk backup copy
// ("backup.db", written once daily to a USB stick or, failing that, to a
// fixed local path). Both use the same type and the same table schema as
// the evaluator's own archive.db (selbst-ableser/internal/archive), on
// purpose: the evaluator can read a backup.db back in with the exact same
// code path it already uses for archive-sync and for migration, no new
// import format to maintain. This is a schema *compatibility* choice, not
// a code-sharing one — the collector module cannot import the evaluator's
// internal/archive package (different Go module), and does not need to:
// the table is simple enough to define independently.
//
// Despite the schema similarity, a Store here is never "the archive" in
// the evaluator's sense (DATEN-03: append-only, immutable once a day is
// closed). It is explicitly prunable — see DeleteDay — because it only
// ever holds what has not yet been confirmed delivered.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"selbst-ableser/collector/internal/telegram"
)

// Entry is one meter's latest known telegram for one day, still in
// whatever encryption state it arrived in. Field names and JSON tags
// intentionally mirror the evaluator's archive.Entry, so the same JSON
// body can be decoded on either side without a translation step.
type Entry struct {
	MeterID    string       `json:"meter_id"`
	Day        telegram.Day `json:"day"`
	ReceivedAt time.Time    `json:"received_at"`
	RSSI       int          `json:"rssi"`
	RawHex     string       `json:"raw_hex"`
}

// Store is a small SQLite-backed table of Entry rows, keyed by
// (meter_id, day). Safe only for single-connection use — Open enforces
// that (see modernc.org/sqlite's own concurrency limits).
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) a Store at path. path may be
// ":memory:" for a pure in-memory buffer, or a real file path for a
// backup copy — the schema and behavior are identical either way; SQLite
// itself decides what "durable" means for ":memory:" (nothing survives
// process exit, by design — see buffer.db's own doc comment at the call
// site in cmd/saCollector).
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("store: creating %s: %w", dir, err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Required for correctness, not just performance: an in-memory SQLite
	// database belongs to the connection that created it, not the
	// process — a second connection would see a separate, empty database.
	// Pinning to one connection also matches modernc.org/sqlite's own
	// concurrency expectations for the file-backed case.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: %s: %w", pragma, err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS telegrams (
	meter_id    TEXT    NOT NULL,
	day         TEXT    NOT NULL,
	received_at TEXT    NOT NULL,
	rssi        INTEGER NOT NULL,
	raw_hex     TEXT    NOT NULL,
	PRIMARY KEY (meter_id, day)
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: creating schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Upsert records e, replacing any earlier telegram already held for the
// same meter and day. Unlike the evaluator's archive, there is no
// "day already closed, refuse" rule here — this table only ever holds
// not-yet-delivered data, so overwriting within it is always correct.
func (s *Store) Upsert(e Entry) error {
	const stmt = `
INSERT INTO telegrams (meter_id, day, received_at, rssi, raw_hex)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (meter_id, day) DO UPDATE SET
	received_at = excluded.received_at,
	rssi        = excluded.rssi,
	raw_hex     = excluded.raw_hex;`
	_, err := s.db.Exec(stmt, e.MeterID, string(e.Day), formatTimestamp(e.ReceivedAt), e.RSSI, e.RawHex)
	return err
}

// ForDay returns every entry recorded for day, ordered by meter ID.
func (s *Store) ForDay(day telegram.Day) ([]Entry, error) {
	const stmt = `SELECT meter_id, received_at, rssi, raw_hex FROM telegrams WHERE day = ? ORDER BY meter_id;`
	rows, err := s.db.Query(stmt, string(day))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows, func(meterID string) telegram.Day { return day })
}

// Since returns every entry received at or after t, across all days — the
// live view's "what's new since I last checked" query.
func (s *Store) Since(t time.Time) ([]Entry, error) {
	const stmt = `SELECT meter_id, day, received_at, rssi, raw_hex FROM telegrams WHERE received_at >= ? ORDER BY received_at;`
	rows, err := s.db.Query(stmt, formatTimestamp(t))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var meterID, day, receivedAt, rawHex string
		var rssi int
		if err := rows.Scan(&meterID, &day, &receivedAt, &rssi, &rawHex); err != nil {
			return nil, err
		}
		ts, err := parseTimestamp(receivedAt)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{MeterID: meterID, Day: telegram.Day(day), ReceivedAt: ts, RSSI: rssi, RawHex: rawHex})
	}
	return entries, rows.Err()
}

// DeleteDay removes every entry recorded for day — called once that day's
// data has been confirmed delivered (network accepted it, or it was
// written to a backup.db), so this table only ever grows while delivery
// is failing, never forever.
func (s *Store) DeleteDay(day telegram.Day) error {
	_, err := s.db.Exec(`DELETE FROM telegrams WHERE day = ?;`, string(day))
	return err
}

// Days returns the distinct days currently held, oldest first — used to
// find every not-yet-delivered day, not just the most recent one, after
// an outage spanning more than a single day.
func (s *Store) Days() ([]telegram.Day, error) {
	rows, err := s.db.Query(`SELECT DISTINCT day FROM telegrams ORDER BY day;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var days []telegram.Day
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		days = append(days, telegram.Day(d))
	}
	return days, rows.Err()
}

func scanEntries(rows *sql.Rows, dayOf func(meterID string) telegram.Day) ([]Entry, error) {
	var entries []Entry
	for rows.Next() {
		var meterID, receivedAt, rawHex string
		var rssi int
		if err := rows.Scan(&meterID, &receivedAt, &rssi, &rawHex); err != nil {
			return nil, err
		}
		ts, err := parseTimestamp(receivedAt)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{MeterID: meterID, Day: dayOf(meterID), ReceivedAt: ts, RSSI: rssi, RawHex: rawHex})
	}
	return entries, rows.Err()
}

const timestampLayout = "2006-01-02T15:04:05"

func formatTimestamp(t time.Time) string {
	return t.In(telegram.Local).Format(timestampLayout)
}

func parseTimestamp(s string) (time.Time, error) {
	return time.ParseInLocation(timestampLayout, s, telegram.Local)
}
