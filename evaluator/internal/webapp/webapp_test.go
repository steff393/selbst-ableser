package webapp

import (
	"bytes"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
	"selbst-ableser/web"
)

const testPassword = "correct horse battery staple"

func newTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "masterdata.enc")
	if err := access.Bootstrap(mdPath, testPassword); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	store, err := archive.OpenStore(filepath.Join(dir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	auditLog, err := access.OpenAuditLog(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	t.Cleanup(func() { auditLog.Close() })

	templates, err := LoadTemplates(web.FS)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		t.Fatalf("static fs: %v", err)
	}

	app := &App{
		Store:          store,
		Vault:          &masterdata.Vault{},
		MasterDataPath: mdPath,
		Sessions:       access.NewSessionStore(time.Hour),
		Audit:          auditLog,
		LoginLimiter:   access.NewLimiter(100, time.Minute),
		UnlockLimiter:  access.NewLimiter(100, time.Minute),
		Templates:      templates,
		StaticFS:       staticFS,
	}
	return app, mdPath
}

// TestTemplatesRenderWithMinimalData is a smoke test: every page must
// parse and execute without error given essentially empty data, catching
// template syntax mistakes (a wrong field name, a bad action) that only
// show up at render time in Go's html/template.
func TestTemplatesRenderWithMinimalData(t *testing.T) {
	templates, err := LoadTemplates(web.FS)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	base := Base{Title: "Test"}
	cases := map[string]any{
		"login.html":             loginPageData{Base: base},
		"state.html":             StateMessage{Base: base, Heading: "H", Message: "M"},
		"uvi.html":               uviPageData{Base: base, Month: "2025-06"},
		"operator_overview.html": overviewPageData{Base: base},
		"access.html":            accessPageData{Base: base},
		"masterdata.html":        masterDataPageData{Base: base, MeterGridJSON: template.JS("[]")},
		"meterstatus.html":       meterStatusPageData{Base: base},
		"legal.html":             legalPageData{Base: base},
		"live.html":              livePageData{Base: base},
		"archive.html":           archivePageData{Base: base},
		"readings.html":          readingsPageData{Base: base, RowsJSON: template.JS("[]"), MeterSummaryJSON: template.JS("[]")},
		"audit.html":             auditPageData{Base: base},
		"uvi_overview.html":      buildingOverviewData{Base: base, Month: "2026-06"},
		"notify.html":            notifySettingsPageData{Base: base, Encryption: "starttls"},
		"security.html":          securitySettingsPageData{Base: base, CurrentHost: "localhost:8226"},
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl, ok := templates[name]
			if !ok {
				t.Fatalf("no template loaded for %s", name)
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
				t.Fatalf("executing %s: %v", name, err)
			}
			if buf.Len() == 0 {
				t.Errorf("%s rendered to an empty document", name)
			}
		})
	}
}

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/operator", "/uvi", "/operator/access", "/operator/masterdata"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("GET %s = %d, want %d (redirect to /login)", path, resp.StatusCode, http.StatusSeeOther)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Errorf("GET %s redirected to %q, want /login", path, loc)
		}
	}
}

func TestOperatorLoginAndOverview(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login landed on status %d, body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Betreiberübersicht") {
		t.Errorf("expected the operator overview page, got: %s", body)
	}
	if app.Vault.Locked() {
		t.Error("logging in with the correct password should have unlocked the vault")
	}
}

func TestOperatorLoginWrongPassword(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/login", url.Values{"credential": {"wrong password entirely"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "fehlgeschlagen") {
		t.Errorf("expected a generic failure message, got: %s", body)
	}
	if !app.Vault.Locked() {
		t.Error("a failed login must not leave the vault unlocked")
	}
}

func TestTenantLoginAndUVI(t *testing.T) {
	app, mdPath := newTestApp(t)

	// Set up a unit, a meter point, and an access grant directly (the
	// bulk-edit HTTP path for meters is exercised separately).
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
		Accesses: []masterdata.Access{
			{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// The vault starts locked: a tenant cannot log in yet.
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}})
	if err != nil {
		t.Fatalf("login while locked: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Gesperrt") {
		t.Errorf("expected the locked-state page while the vault is locked, got: %s", body)
	}

	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	resp, err = client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant login landed on status %d, body: %s", resp.StatusCode, body)
	}
	// No archived readings exist yet. The UVI page itself should say so —
	// on the page, with its month navigation intact — rather than
	// redirecting to a standalone dead end the tenant can only leave with
	// the browser's back button.
	if !strings.Contains(string(body), "keine Werte vor") {
		t.Errorf("expected the empty-month notice on the UVI page, got: %s", body)
	}
	if !strings.Contains(string(body), "uvi-nav") {
		t.Errorf("the empty UVI page must keep its navigation, got: %s", body)
	}
}

func TestLogoutRequiresCSRF(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	// A logout with a missing/wrong CSRF token must not end the session.
	if _, err := client.PostForm(srv.URL+"/logout", url.Values{"csrf_token": {"wrong"}}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp, err := client.Get(srv.URL + "/operator")
	if err != nil {
		t.Fatalf("GET /operator: %v", err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/operator" {
		t.Errorf("session should still be valid after a CSRF-less logout attempt, ended up at %s", resp.Request.URL.Path)
	}
}

func mustDayT(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

// TestSecurityHeadersOnEveryResponse covers M3: nosniff, frame-deny, CSP
// and HSTS must be present on every response, including the login page
// (unauthenticated) — a security header applied only after login would
// leave the one page every visitor sees first unprotected.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer resp.Body.Close()

	cases := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=15552000; includeSubDomains",
	}
	for header, want := range cases {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q, got: %s", directive, csp)
		}
	}
}
