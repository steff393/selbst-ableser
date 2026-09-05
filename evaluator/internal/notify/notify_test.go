package notify

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

type sentMail struct{ to, subject, body string }

type fakeSender struct {
	calls   []sentMail
	failFor map[string]bool
}

func (f *fakeSender) Send(to, subject, body string) error {
	if f.failFor[to] {
		return errors.New("simulated send failure")
	}
	f.calls = append(f.calls, sentMail{to, subject, body})
	return nil
}

func mustDay(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func openTestAudit(t *testing.T) *access.AuditLog {
	t.Helper()
	a, err := access.OpenAuditLog(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestSendMonthlyReminders(t *testing.T) {
	audit := openTestAudit(t)
	sender := &fakeSender{}
	today := mustDay(t, "2025-06-15")
	expired := mustDay(t, "2025-01-01")

	md := masterdata.MasterData{
		Accesses: []masterdata.Access{
			{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "u1@example.com"},
			{Token: "b", UnitID: "u2", Start: mustDay(t, "2024-01-01")},                                         // no email: skipped
			{Token: "c", UnitID: "u3", Start: mustDay(t, "2024-01-01"), End: &expired, Email: "u3@example.com"}, // expired: skipped
		},
	}

	result, err := SendMonthlyReminders(sender, audit, md, "2025-06", today, "https://example.com/uvi", "")
	if err != nil {
		t.Fatalf("SendMonthlyReminders: %v", err)
	}
	if result.Sent != 1 {
		t.Errorf("Sent = %d, want 1", result.Sent)
	}
	if len(sender.calls) != 1 || sender.calls[0].to != "u1@example.com" {
		t.Errorf("unexpected calls: %+v", sender.calls)
	}
	if strings.Contains(sender.calls[0].body, "kWh") {
		t.Error("reminder body should never contain anything that looks like a consumption figure")
	}

	has, err := audit.HasEvent(access.EventNotificationSent, "unit u1 month 2025-06")
	if err != nil || !has {
		t.Errorf("expected the send to be recorded: has=%v err=%v", has, err)
	}
}

// TestSendMonthlyRemindersSendsOnEveryCall documents a deliberate
// consequence of dropping the per-unit "already notified this month"
// check (see monthly.go's doc comment on why): calling this twice for the
// same month sends twice. Nothing in this function guards against that —
// what does, under normal operation, is that the caller only invokes it
// within MonthlyReminderDue's one-hour window, which the hourly check
// loop enters at most once a month.
func TestSendMonthlyRemindersSendsOnEveryCall(t *testing.T) {
	audit := openTestAudit(t)
	sender := &fakeSender{}
	today := mustDay(t, "2025-06-15")
	md := masterdata.MasterData{
		Accesses: []masterdata.Access{{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "u1@example.com"}},
	}

	first, err := SendMonthlyReminders(sender, audit, md, "2025-06", today, "https://x", "")
	if err != nil || first.Sent != 1 {
		t.Fatalf("first run: %+v, err=%v", first, err)
	}
	second, err := SendMonthlyReminders(sender, audit, md, "2025-06", today, "https://x", "")
	if err != nil || second.Sent != 1 {
		t.Fatalf("second run: %+v, err=%v", second, err)
	}
	if len(sender.calls) != 2 {
		t.Errorf("expected two emails (no dedup within this function), got %d", len(sender.calls))
	}
}

func TestSendMonthlyReminders_OperatorSummary(t *testing.T) {
	audit := openTestAudit(t)
	sender := &fakeSender{}
	today := mustDay(t, "2025-06-15")
	md := masterdata.MasterData{
		Accesses: []masterdata.Access{{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "u1@example.com"}},
	}

	if _, err := SendMonthlyReminders(sender, audit, md, "2025-06", today, "https://x", "admin@example.com"); err != nil {
		t.Fatalf("SendMonthlyReminders: %v", err)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("expected the tenant email plus one operator summary, got %d calls", len(sender.calls))
	}
	if sender.calls[1].to != "admin@example.com" {
		t.Errorf("second call went to %q, want the operator address", sender.calls[1].to)
	}
}

func TestSendMonthlyReminders_OperatorSummaryListsMaskedAddresses(t *testing.T) {
	audit := openTestAudit(t)
	sender := &fakeSender{}
	today := mustDay(t, "2025-06-15")
	md := masterdata.MasterData{
		Accesses: []masterdata.Access{
			{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "erika.musterfrau@example.com"},
		},
	}

	if _, err := SendMonthlyReminders(sender, audit, md, "2025-06", today, "https://x", "admin@example.com"); err != nil {
		t.Fatalf("SendMonthlyReminders: %v", err)
	}
	summary := sender.calls[len(sender.calls)-1].body
	if strings.Contains(summary, "erika.musterfrau@example.com") {
		t.Errorf("operator summary must not contain the real address, got: %s", summary)
	}
	if !strings.Contains(summary, "erikXXX@exXXX.com") {
		t.Errorf("operator summary should list the masked address, got: %s", summary)
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"erika.musterfrau@example.com", "erikXXX@exXXX.com"},
		{"ab@x.de", "abXXX@xXXX.de"},
		{"a@b", "aXXX@bXXX"},
		{"not-an-email", "***"},
	}
	for _, c := range cases {
		if got := maskEmail(c.in); got != c.want {
			t.Errorf("maskEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSendMonthlyReminders_NoOperatorSummaryWhenNothingHappened(t *testing.T) {
	audit := openTestAudit(t)
	sender := &fakeSender{}
	today := mustDay(t, "2025-06-15")
	// No accesses at all, so nothing is sent and nothing should be
	// reported either (BENACHR-05: no notification without cause).
	if _, err := SendMonthlyReminders(sender, audit, masterdata.MasterData{}, "2025-06", today, "https://x", "admin@example.com"); err != nil {
		t.Fatalf("SendMonthlyReminders: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Errorf("expected no mail at all, got %d calls", len(sender.calls))
	}
}

func TestSendStartupNotification(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		sender := &fakeSender{}
		if err := SendStartupNotification(sender, nil, false, "admin@example.com", "https://x"); err != nil {
			t.Fatalf("SendStartupNotification: %v", err)
		}
		if len(sender.calls) != 0 {
			t.Error("disabled startup notification should not send anything")
		}
	})

	t.Run("enabled", func(t *testing.T) {
		audit := openTestAudit(t)
		sender := &fakeSender{}
		if err := SendStartupNotification(sender, audit, true, "admin@example.com", "https://x"); err != nil {
			t.Fatalf("SendStartupNotification: %v", err)
		}
		if len(sender.calls) != 1 {
			t.Fatal("enabled startup notification should send once")
		}
		if !strings.Contains(sender.calls[0].body, "entsperrt") || !strings.Contains(sender.calls[0].body, "https://x") {
			t.Errorf("expected the unlock instruction and the base URL, got: %s", sender.calls[0].body)
		}
	})
}

// computeWeeklyStatus itself (rather than the removed immediate, unthrottled
// "Störungsmeldung" — see its own doc comment) is what SendWeeklyStatus's
// counts come from, so these cover its detection logic directly.

func TestComputeWeeklyStatus_LockedVault(t *testing.T) {
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	vault := &masterdata.Vault{} // never unlocked
	counts, err := computeWeeklyStatus(store, vault, nil, mustDay(t, "2025-06-15"), 5)
	if err != nil {
		t.Fatalf("computeWeeklyStatus: %v", err)
	}
	if !counts.Locked {
		t.Errorf("expected Locked=true, got %+v", counts)
	}
}

func TestComputeWeeklyStatus_NeverReceivedAndSilentAndOK(t *testing.T) {
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	today := mustDay(t, "2025-06-15")
	// 90000002 last sent long enough ago to count as silent; 90000003 sent
	// today and counts as OK; 90000001 has never sent at all.
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000002", Day: mustDay(t, "2025-06-01"), RawHex: "aa"}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000003", Day: today, RawHex: "aa"}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	mdPath := filepath.Join(t.TempDir(), "masterdata.enc")
	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Kind: masterdata.KindHeating},
			{ID: "mp2", UnitID: "u1", Kind: masterdata.KindColdWater},
			{ID: "mp3", UnitID: "u1", Kind: masterdata.KindHotWater},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", InstalledAt: mustDay(t, "2024-01-01")},
			{Number: "90000002", MeterPointID: "mp2", InstalledAt: mustDay(t, "2024-01-01")},
			{Number: "90000003", MeterPointID: "mp3", InstalledAt: mustDay(t, "2024-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, "a sufficiently long password"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	vault := &masterdata.Vault{}
	if err := vault.Unlock(mdPath, "a sufficiently long password"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	counts, err := computeWeeklyStatus(store, vault, nil, today, 5)
	if err != nil {
		t.Fatalf("computeWeeklyStatus: %v", err)
	}
	want := WeeklyStatusCounts{NeverReceived: 1, Silent: 1, OK: 1, MonthlyReminderConfirmed: true}
	if counts != want {
		t.Errorf("computeWeeklyStatus = %+v, want %+v", counts, want)
	}
}

// TestMonthlyReminderConfirmed_Unconfirmed and
// TestMonthlyReminderConfirmed_Confirmed cover monthlyReminderConfirmed: a
// soft, audit-log-derived hint that this month's tenant reminder may not
// have gone out — never a reason to resend automatically (see
// schedule.go's note on why sending itself does not read the audit log).
func TestMonthlyReminderConfirmed_Unconfirmed(t *testing.T) {
	today := mustDay(t, "2025-06-15")
	md := masterdata.MasterData{
		Accesses: []masterdata.Access{{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "u1@example.com"}},
	}
	audit := openTestAudit(t) // empty: no record of this month's reminder

	if got := monthlyReminderConfirmed(md, audit, today); got {
		t.Error("expected monthlyReminderConfirmed=false with no audit record for this month")
	}
}

func TestMonthlyReminderConfirmed_Confirmed(t *testing.T) {
	today := mustDay(t, "2025-06-15")
	md := masterdata.MasterData{
		Accesses: []masterdata.Access{{Token: "a", UnitID: "u1", Start: mustDay(t, "2024-01-01"), Email: "u1@example.com"}},
	}
	audit := openTestAudit(t)
	if err := audit.Record(access.Event{Type: access.EventNotificationSent, Detail: "unit u1 month 2025-06"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if got := monthlyReminderConfirmed(md, audit, today); !got {
		t.Error("expected monthlyReminderConfirmed=true once the month is recorded sent")
	}
}
