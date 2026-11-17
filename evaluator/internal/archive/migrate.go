package archive

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"selbst-ableser/internal/telegram"
)

// MigrationIssue names one thing that could not be imported, and why: a
// "meter X, day Y" description of the conflicting entry. An import must
// name a problem rather than silently skip it.
type MigrationIssue struct {
	File string
	Err  error
}

// MigrationReport summarizes one ImportFile run.
type MigrationReport struct {
	FilesProcessed   int
	EntriesInserted  int
	EntriesUnchanged int
	Issues           []MigrationIssue
}

// ErrUnknownFormat is ImportFile's answer to a file that is neither an
// archive-schema SQLite database nor the JSON the archive download writes.
var ErrUnknownFormat = errors.New("archive: neither an archive database nor an archive JSON file")

// sqliteMagic is the fixed first 16 bytes of every SQLite database file.
var sqliteMagic = []byte("SQLite format 3\x00")

// jsonLeadIn is what may precede the opening bracket of a JSON file:
// whitespace, and a byte-order mark some editors add when saving.
var jsonLeadIn = string([]rune{' ', 0x09, 0x0A, 0x0D, 0xFEFF})

// ImportFile imports every entry from the file at sourcePath into dest,
// following the same conflict rules as Store.InsertHistorical: an entry
// already present with different values is recorded as an issue rather
// than overwritten, so importing the same file twice — or two files that
// overlap — is always safe. sourcePath is only ever read, never modified.
//
// Two formats are accepted, told apart by content rather than by file
// name: an archive-schema SQLite database (a collector's backup.db,
// another installation's archive.db, or the archive download's .db), and
// the JSON array the archive download writes (see WriteJSON) — so that an
// excerpt kept as readable text can still be brought back.
func ImportFile(dest *Store, sourcePath string) (MigrationReport, error) {
	var report MigrationReport

	head, err := readHead(sourcePath, len(sqliteMagic))
	if err != nil {
		return report, fmt.Errorf("reading %s: %w", sourcePath, err)
	}

	var entries []Entry
	switch {
	case bytes.Equal(head, sqliteMagic):
		entries, err = entriesFromDatabase(sourcePath)
	case bytes.HasPrefix(bytes.TrimLeft(head, jsonLeadIn), []byte("[")):
		entries, err = entriesFromJSON(sourcePath, &report)
	default:
		return report, ErrUnknownFormat
	}
	if err != nil {
		return report, fmt.Errorf("reading %s: %w", sourcePath, err)
	}
	report.FilesProcessed = 1

	for _, e := range entries {
		changed, ierr := dest.InsertHistorical(e)
		if ierr != nil {
			report.Issues = append(report.Issues, MigrationIssue{
				File: fmt.Sprintf("meter %s, day %s", e.MeterID, e.Day),
				Err:  ierr,
			})
			continue
		}
		if changed {
			report.EntriesInserted++
		} else {
			report.EntriesUnchanged++
		}
	}
	return report, nil
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:read], nil
}

func entriesFromDatabase(path string) ([]Entry, error) {
	source, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return source.AllEntries()
}

// entriesFromJSON reads the download's JSON. Unlike a database, a text
// file may have been edited by hand, so every entry is checked for the
// three things the archive cannot do without — a meter, a valid day, and
// telegram bytes — and one that fails goes into the report instead of
// into the archive.
func entriesFromJSON(path string, report *MigrationReport) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var raw []Entry
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknownFormat, err)
	}
	entries := make([]Entry, 0, len(raw))
	for _, e := range raw {
		_, dayErr := telegram.ParseDay(string(e.Day))
		_, hexErr := hex.DecodeString(e.RawHex)
		if e.MeterID == "" || dayErr != nil || e.RawHex == "" || hexErr != nil {
			report.Issues = append(report.Issues, MigrationIssue{
				File: fmt.Sprintf("meter %q, day %q", e.MeterID, e.Day),
				Err:  errors.New("archive: incomplete or malformed entry"),
			})
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// WriteJSON writes entries as the archive download's JSON: an array of
// Entry, one object per archived telegram — readable without any tool, and
// accepted back by ImportFile.
func WriteJSON(w io.Writer, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// ExportRange writes every entry with a day in [from, to] inclusive into a
// new archive-schema SQLite database at destPath — the same format as the
// archive itself and a collector's backup.db, so ImportFile takes it back
// as it is. destPath must not exist yet. The file is left in rollback-
// journal mode, a single self-contained file with no -wal or -shm beside
// it, since it is meant to be handed over rather than written to.
func (s *Store) ExportRange(from, to telegram.Day, destPath string) (int, error) {
	if _, err := os.Stat(destPath); err == nil {
		return 0, fmt.Errorf("archive: %s already exists", destPath)
	}
	entries, err := s.Range(from, to)
	if err != nil {
		return 0, err
	}

	dest, err := OpenStore(destPath)
	if err != nil {
		return 0, err
	}
	defer dest.Close()

	tx, err := dest.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO telegrams (meter_id, day, received_at, rssi, raw_hex) VALUES (?, ?, ?, ?, ?);`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(e.MeterID, string(e.Day), formatTimestamp(e.ReceivedAt), e.RSSI, e.RawHex); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if _, err := dest.db.Exec(`PRAGMA journal_mode=DELETE;`); err != nil {
		return 0, err
	}
	return len(entries), nil
}
