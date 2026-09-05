package archive

import (
	"database/sql"

	"selbst-ableser/internal/telegram"
)

// Stats is a summary of the archive's contents, for the operator overview
// (UI-03) — cheap to compute, no decryption involved.
type Stats struct {
	TotalEntries    int
	EarliestDay     telegram.Day // zero value if the archive is empty
	LatestDay       telegram.Day
	MetersYesterday int // distinct meters with an entry for the day before today
}

// Stats summarizes the archive as of today (interpreted in local time).
// MetersYesterday looks at yesterday rather than today because the daily
// push only ever commits a day once it is fully over (see the collector's
// own deliverDue) — an archived entry for today is the exception, not the
// rule, so a today-based count would misleadingly read near zero most of
// the time.
func (s *Store) Stats(today telegram.Day) (Stats, error) {
	var stats Stats
	row := s.db.QueryRow(`SELECT COUNT(*), MIN(day), MAX(day) FROM telegrams;`)
	var earliest, latest sql.NullString
	if err := row.Scan(&stats.TotalEntries, &earliest, &latest); err != nil {
		return Stats{}, err
	}
	if earliest.Valid {
		stats.EarliestDay = telegram.Day(earliest.String)
	}
	if latest.Valid {
		stats.LatestDay = telegram.Day(latest.String)
	}

	row = s.db.QueryRow(`SELECT COUNT(DISTINCT meter_id) FROM telegrams WHERE day = ?;`, string(today.AddDays(-1)))
	if err := row.Scan(&stats.MetersYesterday); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

// Range returns every archived entry, across all meters, with a day in
// [from, to] inclusive, ordered by day then meter — UI-08's "Auswahl und
// Download von Ausschnitten" (DATEN-08). Still-encrypted, like everything
// else the archive holds (A.3): downloading it needs no authorization
// beyond being logged in as operator, which the caller enforces.
func (s *Store) Range(from, to telegram.Day) ([]Entry, error) {
	const stmt = `SELECT meter_id, day, received_at, rssi, raw_hex FROM telegrams
		WHERE day >= ? AND day <= ? ORDER BY day, meter_id;`
	rows, err := s.db.Query(stmt, string(from), string(to))
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

// DeleteRange permanently removes every archived entry, across all
// meters, with a day in [from, to] inclusive, and reports how many rows
// were actually removed. This is DATEN-06's required manual-cleanup
// path: the archive is otherwise kept indefinitely and nothing is ever
// deleted automatically — this is the one, deliberate, operator-only way
// to reduce it, distinct from DATEN-09's per-entry correction.
func (s *Store) DeleteRange(from, to telegram.Day) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM telegrams WHERE day >= ? AND day <= ?;`, string(from), string(to))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CompressRange reduces archive storage for every full calendar month in
// [from, to] (DATEN-06's manual-cleanup path, like DeleteRange — never
// automatic, always an explicit operator action): for each such month and
// each meter, if that meter has an entry within lookbackDays of the
// month's last day — exactly the window FindReading (FACH-01) searches to
// find a month's UVI reading — every other entry that meter has in that
// month is deleted, since FindReading will never need them once the entry
// it actually depends on is confirmed present. A month with no such entry
// for a meter is left completely untouched, so this can never create a
// gap FindReading would otherwise have bridged. A month only partially
// covered by [from, to] is skipped entirely, at either edge.
//
// This only checks presence; it never decrypts anything. The caller is
// responsible for having confirmed the affected months' readings are
// plausible and gap-free first.
func (s *Store) CompressRange(from, to telegram.Day, lookbackDays int) (int64, error) {
	var totalDeleted int64
	cursor := firstOfMonth(from)
	lastMonth := firstOfMonth(to)
	for !lastMonth.Before(cursor) {
		monthStart := cursor
		monthEnd := lastDayOfMonth(cursor)
		cursor = monthEnd.AddDays(1)

		if monthStart.Before(from) || to.Before(monthEnd) {
			continue // partial month at the edge of the requested range: leave it untouched
		}
		deleted, err := s.compressMonth(monthStart, monthEnd, lookbackDays)
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += deleted
	}
	return totalDeleted, nil
}

func (s *Store) compressMonth(monthStart, monthEnd telegram.Day, lookbackDays int) (int64, error) {
	meterIDs, err := s.distinctMeterIDsInRange(monthStart, monthEnd)
	if err != nil {
		return 0, err
	}

	windowStart := monthEnd.AddDays(-lookbackDays)
	if windowStart.Before(monthStart) {
		windowStart = monthStart
	}

	var deleted int64
	for _, meterID := range meterIDs {
		keepDay, found, err := s.lastDayInRange(meterID, windowStart, monthEnd)
		if err != nil {
			return deleted, err
		}
		if !found {
			continue // no month-end reading for this meter: leave the whole month alone
		}
		res, err := s.db.Exec(`DELETE FROM telegrams WHERE meter_id = ? AND day >= ? AND day <= ? AND day <> ?;`,
			meterID, string(monthStart), string(monthEnd), string(keepDay))
		if err != nil {
			return deleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}

func (s *Store) distinctMeterIDsInRange(from, to telegram.Day) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT meter_id FROM telegrams WHERE day >= ? AND day <= ?;`, string(from), string(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) lastDayInRange(meterID string, from, to telegram.Day) (telegram.Day, bool, error) {
	const stmt = `SELECT day FROM telegrams WHERE meter_id = ? AND day >= ? AND day <= ? ORDER BY day DESC LIMIT 1;`
	var day string
	switch err := s.db.QueryRow(stmt, meterID, string(from), string(to)).Scan(&day); err {
	case nil:
		return telegram.Day(day), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

func firstOfMonth(d telegram.Day) telegram.Day {
	s := string(d)
	first, err := telegram.ParseDay(s[:8] + "01")
	if err != nil {
		panic(err) // d is already a valid Day, so this cannot happen
	}
	return first
}

func lastDayOfMonth(firstOfMonthDay telegram.Day) telegram.Day {
	// Advance a month at a time is error-prone around month lengths;
	// instead step forward day by day from the first of the month until
	// the month changes, which is simple and unambiguous.
	d := firstOfMonthDay
	for {
		next := d.AddDays(1)
		if next.Month() != d.Month() {
			return d
		}
		d = next
	}
}

// AllEntries returns every archived entry, across all meters and days,
// ordered by day then meter — used to merge one store's full contents into
// another (see ImportFile), where there is no day range to narrow by.
func (s *Store) AllEntries() ([]Entry, error) {
	const stmt = `SELECT meter_id, day, received_at, rssi, raw_hex FROM telegrams ORDER BY day, meter_id;`
	rows, err := s.db.Query(stmt)
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

// BackupTo writes a consistent snapshot of the archive to destPath, safe
// to call while the store is in normal use (BETRIEB-07, DATEN-06): SQLite's
// VACUUM INTO produces a complete, self-consistent copy even with
// concurrent writers, unlike copying the database file directly, which can
// catch it mid-write under WAL mode.
func (s *Store) BackupTo(destPath string) error {
	_, err := s.db.Exec(`VACUUM INTO ?;`, destPath)
	return err
}

// LastSeen returns the most recent day a meter has an archived entry, and
// false if it has never sent anything (UI-07's "nie empfangen" case).
func (s *Store) LastSeen(meterID string) (telegram.Day, bool, error) {
	row := s.db.QueryRow(`SELECT MAX(day) FROM telegrams WHERE meter_id = ?;`, meterID)
	var day sql.NullString
	if err := row.Scan(&day); err != nil {
		return "", false, err
	}
	if !day.Valid {
		return "", false, nil
	}
	return telegram.Day(day.String), true, nil
}
