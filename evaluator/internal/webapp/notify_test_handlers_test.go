package webapp

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

// postFormBody is postForm plus the rendered response body, for tests that
// need to check the message shown on the page rather than just a side
// effect.
func postFormBody(t *testing.T, client *http.Client, target, csrfToken string) string {
	t.Helper()
	values := url.Values{}
	if csrfToken != "" {
		values.Set("csrf_token", csrfToken)
	}
	resp, err := client.PostForm(target, values)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(body)
}

// fakeMailer is a notify.Sender that records every call instead of talking
// to a real SMTP server — the App.Mailer override point exists exactly so
// these three "Test" handlers can be exercised without one.
type fakeMailer struct {
	calls []struct{ to, subject, body string }
}

func (f *fakeMailer) Send(to, subject, body string) error {
	f.calls = append(f.calls, struct{ to, subject, body string }{to, subject, body})
	return nil
}

func TestNotifyTestMonthlySendsRealReminderAndBypassesDedup(t *testing.T) {
	app, mdPath := newTestApp(t)
	md := unitOnlyMasterData("u1", "Wohnung A")
	md.Accesses = []masterdata.Access{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01"), Email: "mieter@example.org"}}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	app.NotifyConfig.OperatorEmail = "betreiber@example.org"
	app.NotifyConfig.BaseURL = "https://example.org"
	fixedNow := time.Date(2026, 8, 25, 12, 0, 0, 0, time.Local)
	app.Now = func() time.Time { return fixedNow }

	// Simulate this month's reminder having already gone out for this
	// unit — a normal (non-forced) run would skip it.
	if err := app.Audit.Record(access.Event{Type: access.EventNotificationSent, Detail: "unit u1 month 2026-08"}); err != nil {
		t.Fatalf("seeding audit: %v", err)
	}

	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	postForm(t, client, srv.URL+"/operator/notify/test/monthly", map[string]string{
		"csrf_token": sess.CSRFToken,
	})

	if len(fake.calls) != 2 {
		t.Fatalf("expected the tenant reminder plus the operator summary, got %d calls: %+v", len(fake.calls), fake.calls)
	}
	if fake.calls[0].to != "mieter@example.org" {
		t.Errorf("first call went to %q, want the tenant address (force must bypass the already-notified skip)", fake.calls[0].to)
	}
	if fake.calls[1].to != "betreiber@example.org" {
		t.Errorf("second call went to %q, want the operator summary", fake.calls[1].to)
	}
}

func TestNotifyTestMonthlyRequiresUnlockedVault(t *testing.T) {
	app, _ := newTestApp(t)
	app.Vault.Lock()
	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	body := postFormBody(t, client, srv.URL+"/operator/notify/test/monthly", sess.CSRFToken)

	if !strings.Contains(body, "gesperrt") {
		t.Errorf("expected a clear locked-vault message, got: %s", body)
	}
	if len(fake.calls) != 0 {
		t.Errorf("nothing should be sent while the vault is locked, got %d calls", len(fake.calls))
	}
}

func TestNotifyTestWeeklySendsOutsideItsSchedule(t *testing.T) {
	app, _ := newTestApp(t)
	app.NotifyConfig.OperatorEmail = "betreiber@example.org"
	// A Monday well before the scheduled hour — a non-forced run would
	// stay quiet here (see notify.WeeklyStatusDue).
	app.Now = func() time.Time { return time.Date(2026, 8, 24, 9, 0, 0, 0, time.Local) }

	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	postForm(t, client, srv.URL+"/operator/notify/test/weekly", map[string]string{
		"csrf_token": sess.CSRFToken,
	})

	if len(fake.calls) != 1 || fake.calls[0].to != "betreiber@example.org" {
		t.Errorf("expected exactly one weekly status to the operator despite the early hour, got %+v", fake.calls)
	}
}

func TestNotifyTestWeeklyRequiresOperatorEmail(t *testing.T) {
	app, _ := newTestApp(t)
	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	body := postFormBody(t, client, srv.URL+"/operator/notify/test/weekly", sess.CSRFToken)

	if !strings.Contains(body, "Betreiber-Adresse") {
		t.Errorf("expected a clear missing-address message, got: %s", body)
	}
	if len(fake.calls) != 0 {
		t.Errorf("nothing should be sent without an operator address, got %d calls", len(fake.calls))
	}
}

func TestNotifyTestStartupSendsRegardlessOfToggle(t *testing.T) {
	app, _ := newTestApp(t)
	app.NotifyConfig.OperatorEmail = "betreiber@example.org"
	app.NotifyConfig.StartupNotification = false // toggle off: a real restart would stay quiet
	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	postForm(t, client, srv.URL+"/operator/notify/test/startup", map[string]string{
		"csrf_token": sess.CSRFToken,
	})

	if len(fake.calls) != 1 || fake.calls[0].to != "betreiber@example.org" {
		t.Errorf("the manual test should send despite the toggle being off, got %+v", fake.calls)
	}
}

func TestNotifyTestStartupRequiresOperatorEmail(t *testing.T) {
	app, _ := newTestApp(t)
	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	body := postFormBody(t, client, srv.URL+"/operator/notify/test/startup", sess.CSRFToken)

	if !strings.Contains(body, "Betreiber-Adresse") {
		t.Errorf("expected a clear missing-address message, got: %s", body)
	}
	if len(fake.calls) != 0 {
		t.Errorf("nothing should be sent without an operator address, got %d calls", len(fake.calls))
	}
}

func TestNotifyTestMonthlyRequiresCSRF(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	fake := &fakeMailer{}
	app.Mailer = fake

	client, srv := operatorClient(t, app)
	postForm(t, client, srv.URL+"/operator/notify/test/monthly", map[string]string{})

	if len(fake.calls) != 0 {
		t.Error("a request without a CSRF token must not send anything")
	}
}
