package webapp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

// operatorClient logs in as the operator and returns a cookie-carrying
// client alongside the running server.
func operatorClient(t *testing.T, app *App) (*http.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login returned %d", resp.StatusCode)
	}
	return client, srv
}

// sessionIDFrom returns the session cookie's value — which is exactly the
// server-side session ID, and therefore the credential no log may carry.
func sessionIDFrom(t *testing.T, client *http.Client, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing server URL: %v", err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == sessionCookieName {
			return c.Value
		}
	}
	t.Fatal("no session cookie was set")
	return ""
}

func getAuditBody(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, body: %s", url, resp.StatusCode, body)
	}
	return string(body)
}

func TestAuditPageShowsRecordedEvents(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)

	// The login above is itself an auditable event, so the page has
	// something real to show without seeding anything.
	body := getAuditBody(t, client, srv.URL+"/operator/audit")

	if !strings.Contains(body, "Sicherheitsprotokoll") {
		t.Error("audit page should carry its own heading")
	}
	if !strings.Contains(body, "Anmeldung erfolgreich") {
		t.Errorf("the operator's own login should appear, got: %s", body)
	}
}

// TestAuditPageNeverShowsTheSessionID is the rendered-page half of
// access.TestAuditActorNeverLeaksSessionID: the session ID is the session
// cookie, so a log page displaying it verbatim would put a live
// credential on screen and into every backup of audit.db (ZUGANG-08).
func TestAuditPageNeverShowsTheSessionID(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	sessionID := sessionIDFrom(t, client, srv)

	// Take another auditable action, so more than the login is on record.
	getAuditBody(t, client, srv.URL+"/operator/masterdata/export")
	body := getAuditBody(t, client, srv.URL+"/operator/audit")

	if strings.Contains(body, sessionID) {
		t.Error("the audit page rendered the live session ID")
	}
	if !strings.Contains(body, "operator/") {
		t.Errorf("expected a pseudonymous operator actor on the page, got: %s", body)
	}
}

func TestAuditPageFiltersByType(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)

	if err := app.Audit.Record(access.Event{
		Type:   access.EventArchiveDeleted,
		At:     time.Now(),
		Actor:  "operator/deadbeef",
		Detail: "7 entries, 2025-01-01 to 2025-01-31",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	all := getAuditBody(t, client, srv.URL+"/operator/audit")
	if !strings.Contains(all, "7 entries") {
		t.Error("the unfiltered page should show the seeded deletion")
	}
	unfilteredRows := auditRowCount(all)
	if unfilteredRows < 2 {
		t.Fatalf("unfiltered page has %d rows, want at least the login and the deletion", unfilteredRows)
	}

	filtered := getAuditBody(t, client, srv.URL+"/operator/audit?type=archive_deleted")
	if !strings.Contains(filtered, "7 entries") {
		t.Error("the filtered page should still show the matching event")
	}
	// Row-counting rather than label-matching: every type's label also
	// appears in the filter dropdown, so the labels alone prove nothing
	// about what the table below actually holds.
	if got := auditRowCount(filtered); got != 1 {
		t.Errorf("filtered page has %d rows, want exactly the 1 matching event", got)
	}
}

// auditRowCount counts rendered log rows via the one cell every row has
// exactly once (the actor column).
func auditRowCount(body string) int {
	return strings.Count(body, `<td class="token-cell">`)
}

// TestAuditPageRejectsTenant: the log spans every unit and every login
// attempt in the installation, so it is squarely outside what a tenant may
// see (SZ-4).
func TestAuditPageRejectsTenant(t *testing.T) {
	app, mdPath := newTestApp(t)

	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Wohnung A", AreaM2: 50}},
		Accesses: []masterdata.Access{
			{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(srv.URL + "/operator/audit")
	if err != nil {
		t.Fatalf("GET /operator/audit: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if strings.Contains(string(body), "Sicherheitsprotokoll") {
		t.Errorf("a tenant reached the security log, got: %s", body)
	}
}

// TestOverviewShowsVersion covers the reason the version is surfaced at
// all: the update path is "replace the binary and restart", so an
// operator needs somewhere to confirm which build is actually running.
func TestOverviewShowsVersion(t *testing.T) {
	app, _ := newTestApp(t)
	app.Version = "devel-abc1234"
	client, srv := operatorClient(t, app)

	body := getAuditBody(t, client, srv.URL+"/operator")
	if !strings.Contains(body, "devel-abc1234") {
		t.Errorf("the overview should show the running version, got: %s", body)
	}
}

// tenantClientFor logs in with a tenant access token and returns a
// cookie-carrying client plus the server's base URL.
func tenantClientFor(t *testing.T, app *App, token string) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {token}})
	if err != nil {
		t.Fatalf("tenant login: %v", err)
	}
	resp.Body.Close()
	return client, srv.URL
}

// publicServer starts the app and returns its base URL, for the pages
// that are deliberately reachable without a session.
func publicServer(t *testing.T, app *App) string {
	t.Helper()
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)
	return srv.URL
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func postForm(t *testing.T, client *http.Client, target string, fields map[string]string) {
	t.Helper()
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	resp, err := client.PostForm(target, values)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	resp.Body.Close()
}

// lookupSessionFromClient is lookupSession for callers that already have
// a server rather than a bare URL string.
func lookupSessionFromClient(t *testing.T, app *App, client *http.Client, srv *httptest.Server) *access.Session {
	t.Helper()
	return lookupSession(t, app, client.Jar, srv.URL)
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
