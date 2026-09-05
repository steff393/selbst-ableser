package access

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"selbst-ableser/internal/telegram"
)

// EventType is one kind of security-relevant event ZUGANG-08 requires to
// be logged.
type EventType string

const (
	EventLoginSuccess      EventType = "login_success"
	EventLoginFailure      EventType = "login_failure"
	EventLogout            EventType = "logout"
	EventUnlockAttempt     EventType = "unlock_attempt"
	EventMasterDataChange  EventType = "masterdata_change"
	EventAccessChange      EventType = "access_change"
	EventDataIngested      EventType = "data_ingested"
	EventArchiveDeleted    EventType = "archive_deleted"
	EventArchiveCorrected  EventType = "archive_corrected"
	EventArchiveCompressed EventType = "archive_compressed"
	EventNotificationSent  EventType = "notification_sent"
	EventServiceRestart    EventType = "service_restart"
)

// Event is one audit log entry. Detail is free text for context (e.g.
// "unit u3" or "meter 10000001") and must never contain a secret —
// no password, no full token, no AES key, no decrypted reading (ZUGANG-08).
// Use RedactToken when a token needs to be identifiable in a log message.
type Event struct {
	Type EventType
	At   time.Time
	// Actor is who acted, as a pseudonym — see Session.AuditActor, which
	// is what every authenticated call site passes. Never the session ID
	// itself (that is the session cookie), never a password or token.
	// "unauthenticated" and fixed machine names like "collector-report"
	// are the unauthenticated/non-session cases.
	Actor  string
	Detail string
}

// AuditLog is an append-only, SQLite-backed security event log, kept in
// its own file separate from the telegram archive and the master data
// (DATEN-07's separation-by-protection-need principle, applied one level
// further: this file needs neither the archive's total openness nor the
// master data's key-grade protection).
type AuditLog struct {
	db *sql.DB
}

// OpenAuditLog opens (creating if necessary) the audit log at path.
func OpenAuditLog(path string) (*AuditLog, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("access: creating %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("access: %s: %w", pragma, err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	type   TEXT    NOT NULL,
	at     TEXT    NOT NULL,
	actor  TEXT    NOT NULL,
	detail TEXT    NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("access: creating schema: %w", err)
	}

	return &AuditLog{db: db}, nil
}

func (a *AuditLog) Close() error { return a.db.Close() }

// Record appends one event. Failures to write the audit log are returned
// to the caller rather than swallowed — a security-relevant action whose
// logging silently failed is exactly the kind of gap ZUGANG-08 exists to
// prevent.
func (a *AuditLog) Record(e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	const stmt = `INSERT INTO events (type, at, actor, detail) VALUES (?, ?, ?, ?);`
	_, err := a.db.Exec(stmt, string(e.Type), e.At.In(telegram.Local).Format(time.RFC3339), e.Actor, e.Detail)
	return err
}

// DefaultQueryLimit bounds an unspecified Query: the log is meant to be
// read a screenful at a time, not rendered whole.
const DefaultQueryLimit = 200

// Query narrows what an audit-log query returns. The zero value means
// "the most recent events, of every type, over the whole log".
type Query struct {
	Types []EventType  // empty: every type
	From  telegram.Day // "": unbounded
	To    telegram.Day // "": unbounded
	Limit int          // <= 0: DefaultQueryLimit
}

// where renders q's conditions as a SQL fragment plus its arguments.
// Timestamps are stored as local-time RFC3339, whose UTC offset shifts
// with daylight saving, so a day range is matched on the leading date
// component rather than by comparing whole timestamps lexically.
func (q Query) where() (string, []any) {
	var conds []string
	var args []any

	if len(q.Types) > 0 {
		placeholders := make([]string, len(q.Types))
		for i, t := range q.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conds = append(conds, "type IN ("+strings.Join(placeholders, ", ")+")")
	}
	if q.From != "" {
		conds = append(conds, "substr(at, 1, 10) >= ?")
		args = append(args, string(q.From))
	}
	if q.To != "" {
		conds = append(conds, "substr(at, 1, 10) <= ?")
		args = append(args, string(q.To))
	}

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Events returns the events matching q, newest first, along with the total
// number of matches ignoring q.Limit — so a caller can say "showing 200 of
// 1543" without a second round trip.
func (a *AuditLog) Events(q Query) (events []Event, total int, err error) {
	where, args := q.where()

	if err := a.db.QueryRow(`SELECT COUNT(*) FROM events`+where+`;`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	rows, err := a.db.Query(`SELECT type, at, actor, detail FROM events`+where+` ORDER BY id DESC LIMIT ?;`, append(args, limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var typ, at, actor, detail string
		if err := rows.Scan(&typ, &at, &actor, &detail); err != nil {
			return nil, 0, err
		}
		ts, err := time.ParseInLocation(time.RFC3339, at, telegram.Local)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, Event{Type: EventType(typ), At: ts, Actor: actor, Detail: detail})
	}
	return events, total, rows.Err()
}

// BackupTo writes a consistent snapshot of the audit log to destPath,
// safe to call while the log is in normal use — see the identical note on
// archive.Store.BackupTo.
func (a *AuditLog) BackupTo(destPath string) error {
	_, err := a.db.Exec(`VACUUM INTO ?;`, destPath)
	return err
}

// HasEvent reports whether an event of the given type and exact detail
// has ever been recorded. It is what makes the monthly reminder
// (BENACHR-01) idempotent and catch-up friendly: a caller records an
// EventNotificationSent with a detail identifying "this unit, this month"
// after sending, and checks HasEvent before sending again — so a run that
// was missed (device off, network down) is simply retried the next time
// something checks, rather than that month being skipped for good. A
// check tied to one fixed time of day could not offer this: whether the
// reminder ever goes out would depend on the process happening to be
// alive at that moment.
func (a *AuditLog) HasEvent(t EventType, detail string) (bool, error) {
	const stmt = `SELECT EXISTS(SELECT 1 FROM events WHERE type = ? AND detail = ?);`
	var exists bool
	err := a.db.QueryRow(stmt, string(t), detail).Scan(&exists)
	return exists, err
}
