package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"selbst-ableser/internal/telegram"
)

// Entry is one archived telegram: a meter's last-received-that-day
// telegram, still in whatever encryption state it arrived in.
type Entry struct {
	MeterID    string       `json:"meter_id"`
	Day        telegram.Day `json:"day"`
	ReceivedAt time.Time    `json:"received_at"`
	RSSI       int          `json:"rssi"`
	RawHex     string       `json:"raw_hex"`
}

// ErrConflict is returned by InsertHistorical when the archive already
// holds a different entry for the same meter and day.
var ErrConflict = errors.New("archive: existing entry differs from the one being migrated")

// Store is the append-only telegram archive. A Store is safe for
// concurrent use.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if necessary) the SQLite-backed archive at
// path. WAL mode is enabled with a relaxed synchronous setting: on the
// target hardware (an SD card with no other local storage) minimizing
// write/fsync frequency matters more than surviving an OS crash
// mid-transaction, and the archive is expected to be backed up regularly
// regardless.
func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("archive: creating %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite connections are not safe for concurrent use

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("archive: %s: %w", pragma, err)
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
		return nil, fmt.Errorf("archive: creating schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// InsertHistorical records a telegram from a bulk import of previously
// recorded data (see the migration tool), independent of what "today" is.
// It is idempotent: re-importing an entry identical to what is already
// archived is a silent no-op (changed=false); importing an entry that
// conflicts with a different, already-archived entry for the same meter
// and day is rejected with ErrConflict rather than silently overwriting
// closed-period data.
func (s *Store) InsertHistorical(e Entry) (changed bool, err error) {
	existing, found, err := s.Get(e.MeterID, e.Day)
	if err != nil {
		return false, err
	}
	if found {
		if entriesEqual(existing, e) {
			return false, nil
		}
		return false, fmt.Errorf("%w: meter %s, day %s", ErrConflict, e.MeterID, e.Day)
	}
	if err := s.put(e); err != nil {
		return false, err
	}
	return true, nil
}

// Correct replaces an archived entry unconditionally, regardless of its
// day or whether one already exists. It is the sole exception to the
// archive's immutability, reserved for correcting a demonstrably wrong
// entry; callers are responsible for restricting it to an authenticated,
// deliberately confirmed operator action, kept separate from the
// automated collector/transfer path (see docs/architektur.md, security model).
func (s *Store) Correct(e Entry) error {
	return s.put(e)
}

func entriesEqual(a, b Entry) bool {
	return a.MeterID == b.MeterID &&
		a.Day == b.Day &&
		a.ReceivedAt.Equal(b.ReceivedAt) &&
		a.RSSI == b.RSSI &&
		a.RawHex == b.RawHex
}

func (s *Store) put(e Entry) error {
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

// Get returns the archived entry for a meter and day, if any.
func (s *Store) Get(meterID string, day telegram.Day) (Entry, bool, error) {
	const stmt = `SELECT received_at, rssi, raw_hex FROM telegrams WHERE meter_id = ? AND day = ?;`
	row := s.db.QueryRow(stmt, meterID, string(day))
	var receivedAt string
	var rssi int
	var rawHex string
	switch err := row.Scan(&receivedAt, &rssi, &rawHex); err {
	case nil:
		ts, perr := parseTimestamp(receivedAt)
		if perr != nil {
			return Entry{}, false, perr
		}
		return Entry{MeterID: meterID, Day: day, ReceivedAt: ts, RSSI: rssi, RawHex: rawHex}, true, nil
	case sql.ErrNoRows:
		return Entry{}, false, nil
	default:
		return Entry{}, false, err
	}
}

// LastDayAtOrBefore returns the most recent day a meter has an archived
// entry for, no later than notAfter — used to find a sensible default UVI
// month (UI-01) when "today" itself has no data yet, without pulling back
// every archived entry just to find the last one (see All's own doc
// comment on why that would be wasteful for this).
func (s *Store) LastDayAtOrBefore(meterID string, notAfter telegram.Day) (telegram.Day, bool, error) {
	const stmt = `SELECT day FROM telegrams WHERE meter_id = ? AND day <= ? ORDER BY day DESC LIMIT 1;`
	var day string
	switch err := s.db.QueryRow(stmt, meterID, string(notAfter)).Scan(&day); err {
	case nil:
		return telegram.Day(day), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

func (s *Store) firstDayAtOrAfter(meterID string, notBefore telegram.Day) (telegram.Day, bool, error) {
	const stmt = `SELECT day FROM telegrams WHERE meter_id = ? AND day >= ? ORDER BY day ASC LIMIT 1;`
	var day string
	switch err := s.db.QueryRow(stmt, meterID, string(notBefore)).Scan(&day); err {
	case nil:
		return telegram.Day(day), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

// NearestDay returns the archived day closest to target for meterID —
// used by internal/correction to find a template telegram when target
// itself has no archived entry (a gap being backfilled, not a wrong value
// being fixed). A tie between an earlier and a later candidate prefers the
// earlier one. found is false only if the meter has no archived entries
// at all.
func (s *Store) NearestDay(meterID string, target telegram.Day) (telegram.Day, bool, error) {
	before, foundBefore, err := s.LastDayAtOrBefore(meterID, target)
	if err != nil {
		return "", false, err
	}
	if foundBefore && before == target {
		return before, true, nil
	}
	after, foundAfter, err := s.firstDayAtOrAfter(meterID, target)
	if err != nil {
		return "", false, err
	}

	switch {
	case foundBefore && foundAfter:
		if target.DaysUntil(after) < -target.DaysUntil(before) {
			return after, true, nil
		}
		return before, true, nil
	case foundBefore:
		return before, true, nil
	case foundAfter:
		return after, true, nil
	default:
		return "", false, nil
	}
}

// FirstDay returns the earliest day a meter has an archived entry for.
// It is LastDayAtOrBefore's counterpart, used to bound how far back the
// UVI's month navigation may go (UI-01) so paging cannot run off the
// start of the data.
func (s *Store) FirstDay(meterID string) (telegram.Day, bool, error) {
	const stmt = `SELECT day FROM telegrams WHERE meter_id = ? ORDER BY day ASC LIMIT 1;`
	var day string
	switch err := s.db.QueryRow(stmt, meterID).Scan(&day); err {
	case nil:
		return telegram.Day(day), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

// All returns every archived entry for a meter, ordered by day. It exists
// for verification (migration checks, tests) rather than for the billing
// path, which will need range queries this type does not yet provide.
func (s *Store) All(meterID string) ([]Entry, error) {
	const stmt = `SELECT day, received_at, rssi, raw_hex FROM telegrams WHERE meter_id = ? ORDER BY day;`
	rows, err := s.db.Query(stmt, meterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var day, receivedAt, rawHex string
		var rssi int
		if err := rows.Scan(&day, &receivedAt, &rssi, &rawHex); err != nil {
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

const timestampLayout = "2006-01-02T15:04:05"

func formatTimestamp(t time.Time) string {
	return t.In(telegram.Local).Format(timestampLayout)
}

func parseTimestamp(s string) (time.Time, error) {
	return time.ParseInLocation(timestampLayout, s, telegram.Local)
}
