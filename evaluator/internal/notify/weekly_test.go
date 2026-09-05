package notify

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

// TestWeeklyStatusSendsEvenWithNothingWrong is the whole point of the
// weekly mail as opposed to the immediate alert: a quiet week must
// produce a message saying so, otherwise silence is ambiguous between
// "fine" and "the system is off".
func TestWeeklyStatusSendsEvenWithNothingWrong(t *testing.T) {
	dir := t.TempDir()
	store, err := archive.OpenStore(filepath.Join(dir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	audit, err := access.OpenAuditLog(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer audit.Close()

	mdPath := filepath.Join(dir, "masterdata.enc")
	if err := masterdata.Save(mdPath, masterdata.MasterData{}, "a long enough password"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var vault masterdata.Vault
	if err := vault.Unlock(mdPath, "a long enough password"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	sender := &fakeSender{}
	monday := time.Date(2026, 8, 24, 10, 0, 0, 0, time.Local)

	sent, err := SendWeeklyStatus(sender, audit, store, &vault, monday, 5, "betreiber@example.org", false)
	if err != nil {
		t.Fatalf("SendWeeklyStatus: %v", err)
	}
	if !sent {
		t.Fatal("the weekly status should go out even with nothing to report")
	}
	if len(sender.calls) != 1 || !strings.Contains(sender.calls[0].body, "0 Zähler: In Ordnung") {
		t.Errorf("unexpected message: %+v", sender.calls)
	}

	// Later the same day, past the sending hour: the window has already
	// closed for this week (WeeklyStatusDue is now only true within that
	// one hour — see schedule.go), so nothing goes out. That is a
	// different reason than the old "already sent this week" dedup, but
	// the same outward result.
	sent, err = SendWeeklyStatus(sender, audit, store, &vault, monday.Add(26*time.Hour), 5, "betreiber@example.org", false)
	if err != nil {
		t.Fatalf("second SendWeeklyStatus: %v", err)
	}
	if sent || len(sender.calls) != 1 {
		t.Errorf("outside the sending hour, nothing should go out: %+v", sender.calls)
	}

	// The next week is a different period and sends again.
	sent, err = SendWeeklyStatus(sender, audit, store, &vault, monday.AddDate(0, 0, 7), 5, "betreiber@example.org", false)
	if err != nil {
		t.Fatalf("next week: %v", err)
	}
	if !sent || len(sender.calls) != 2 {
		t.Errorf("the next week should send again, got %d messages", len(sender.calls))
	}
}

// TestWeeklyStatusHasNoDedupWithinItsOwnHour documents a deliberate
// consequence of no longer reading the audit log to decide whether to
// send (see schedule.go's note): calling this twice within the same
// sending hour really does send twice. Nothing here protects against
// that — what does, under normal operation, is that the hourly check
// loop (cmd/saEvaluator) only calls this once per hour in the first
// place.
func TestWeeklyStatusHasNoDedupWithinItsOwnHour(t *testing.T) {
	dir := t.TempDir()
	store, err := archive.OpenStore(filepath.Join(dir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	audit, err := access.OpenAuditLog(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer audit.Close()

	mdPath := filepath.Join(dir, "masterdata.enc")
	if err := masterdata.Save(mdPath, masterdata.MasterData{}, "a long enough password"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var vault masterdata.Vault
	if err := vault.Unlock(mdPath, "a long enough password"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	sender := &fakeSender{}
	monday := time.Date(2026, 8, 24, 10, 0, 0, 0, time.Local)

	for i := 0; i < 2; i++ {
		sent, err := SendWeeklyStatus(sender, audit, store, &vault, monday.Add(time.Duration(i)*time.Minute), 5, "betreiber@example.org", false)
		if err != nil {
			t.Fatalf("SendWeeklyStatus #%d: %v", i, err)
		}
		if !sent {
			t.Fatalf("SendWeeklyStatus #%d should send: still within the same sending hour", i)
		}
	}
	if len(sender.calls) != 2 {
		t.Errorf("expected two emails (no dedup within the function itself), got %d", len(sender.calls))
	}
}

func TestWeeklyStatusStaysQuietOutsideItsHour(t *testing.T) {
	sender := &fakeSender{}
	cases := []time.Time{
		time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local),  // Monday, before the hour
		time.Date(2026, 8, 24, 11, 0, 0, 0, time.Local), // Monday, after the hour
		time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local), // Tuesday, right hour but wrong day
	}
	for _, when := range cases {
		sent, err := SendWeeklyStatus(sender, nil, nil, nil, when, 5, "betreiber@example.org", false)
		if err != nil {
			t.Fatalf("SendWeeklyStatus(%s): %v", when, err)
		}
		if sent || len(sender.calls) != 0 {
			t.Errorf("nothing should be sent at %s, outside the sending hour", when)
		}
	}
}

// TestWeeklyStatusForce covers the operator's manual "send now" action: it
// bypasses WeeklyStatusDue and sends regardless of the day or hour.
func TestWeeklyStatusForce(t *testing.T) {
	dir := t.TempDir()
	store, err := archive.OpenStore(filepath.Join(dir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	audit, err := access.OpenAuditLog(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer audit.Close()

	mdPath := filepath.Join(dir, "masterdata.enc")
	if err := masterdata.Save(mdPath, masterdata.MasterData{}, "a long enough password"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var vault masterdata.Vault
	if err := vault.Unlock(mdPath, "a long enough password"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	sender := &fakeSender{}
	tooEarly := time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local) // Monday, before SendHour

	sent, err := SendWeeklyStatus(sender, audit, store, &vault, tooEarly, 5, "betreiber@example.org", true)
	if err != nil {
		t.Fatalf("forced SendWeeklyStatus: %v", err)
	}
	if !sent || len(sender.calls) != 1 {
		t.Fatalf("forced send should go out even before the scheduled hour, got sent=%v calls=%d", sent, len(sender.calls))
	}
}
