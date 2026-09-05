package webapp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

// TestChangePasswordKeepsTheSessionAlive: the form asks only for the new
// password, since the caller already holds an authenticated operator
// session and the unlocked vault already knows the current one. The
// operator's identity did not change, so the session survives and the
// vault is re-keyed in place rather than left holding a stale password.
func TestChangePasswordKeepsTheSessionAlive(t *testing.T) {
	app, mdPath := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/access/password", url.Values{
		"csrf_token":   {sess.CSRFToken},
		"new_password": {"a-brand-new-password-123"},
	})
	if err != nil {
		t.Fatalf("POST password change: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Passwort ge") {
		t.Errorf("expected a confirmation on the Zugänge page, got: %s", raw)
	}

	if _, err := masterdata.Load(mdPath, testPassword); err == nil {
		t.Error("old password should no longer open the file")
	}
	if _, err := masterdata.Load(mdPath, "a-brand-new-password-123"); err != nil {
		t.Errorf("Load with the new password: %v", err)
	}

	// Still logged in, and the vault still usable — a further save must
	// not fail on a stale cached password.
	resp2, err := client.Get(srv.URL + "/operator")
	if err != nil {
		t.Fatalf("GET /operator: %v", err)
	}
	defer resp2.Body.Close()
	if strings.Contains(resp2.Request.URL.Path, "login") {
		t.Error("the session should survive its own password change")
	}
	if app.Vault.Locked() {
		t.Error("the vault should still be unlocked after re-keying itself")
	}
	// The vault must still be able to write, which it can only do with the
	// new password — proving it adopted the one it just re-encrypted with.
	md, _ := app.Vault.Get()
	if err := app.Vault.Save(mdPath, md); err != nil {
		t.Errorf("saving after the password change: %v", err)
	}
	if _, err := masterdata.Load(mdPath, "a-brand-new-password-123"); err != nil {
		t.Errorf("the file should still open with the new password after that save: %v", err)
	}
}

// TestChangePasswordRevokesOtherSessionsButKeepsTheActingOne answers
// Prüfpunkt 5.F: a password change happens exactly when a compromise is
// suspected, so every *other* session — a possible eavesdropper's —
// should end immediately, while the operator who just proved they know
// the new password by setting it stays logged in.
func TestChangePasswordRevokesOtherSessionsButKeepsTheActingOne(t *testing.T) {
	app, mdPath := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	actingJar, _ := cookiejar.New(nil)
	acting := &http.Client{Jar: actingJar}
	loginAsOperator(t, acting, srv.URL)

	otherJar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: otherJar}
	loginAsOperator(t, other, srv.URL)

	// Both sessions work before the change.
	if resp, err := other.Get(srv.URL + "/operator"); err != nil || strings.Contains(resp.Request.URL.Path, "login") {
		t.Fatalf("other session should be logged in before the change: err=%v", err)
	}

	sess := lookupSession(t, app, actingJar, srv.URL)
	resp, err := acting.PostForm(srv.URL+"/operator/access/password", url.Values{
		"csrf_token":   {sess.CSRFToken},
		"new_password": {"a-brand-new-password-123"},
	})
	if err != nil {
		t.Fatalf("POST password change: %v", err)
	}
	resp.Body.Close()

	// The acting session survives.
	resp2, err := acting.Get(srv.URL + "/operator")
	if err != nil {
		t.Fatalf("GET /operator (acting): %v", err)
	}
	resp2.Body.Close()
	if strings.Contains(resp2.Request.URL.Path, "login") {
		t.Error("the acting session should survive its own password change")
	}

	// The other session is gone.
	resp3, err := other.Get(srv.URL + "/operator")
	if err != nil {
		t.Fatalf("GET /operator (other): %v", err)
	}
	resp3.Body.Close()
	if !strings.Contains(resp3.Request.URL.Path, "login") {
		t.Error("the other session should have been revoked by the password change")
	}

	if _, err := masterdata.Load(mdPath, "a-brand-new-password-123"); err != nil {
		t.Errorf("Load with the new password: %v", err)
	}
}

// TestChangePasswordRejectsTokenShapedPassword mirrors
// access.TestBootstrapRejectsTokenShapedPassword for the other entry point
// a password can be set through: a password LooksLikeAccessToken would
// classify as a tenant token would make the login form's credential-shape
// dispatch permanently route it to the wrong check.
func TestChangePasswordRejectsTokenShapedPassword(t *testing.T) {
	app, mdPath := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	tokenShaped := "23456789ABCD" // 12 chars, all in the token alphabet
	if !access.LooksLikeAccessToken(tokenShaped) {
		t.Fatalf("test fixture %q does not actually look like a token — fix the fixture", tokenShaped)
	}

	resp, err := client.PostForm(srv.URL+"/operator/access/password", url.Values{
		"csrf_token":   {sess.CSRFToken},
		"new_password": {tokenShaped},
	})
	if err != nil {
		t.Fatalf("POST password change: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Zugangscodes") {
		t.Errorf("expected an explanatory rejection, got: %s", raw)
	}

	if _, err := masterdata.Load(mdPath, testPassword); err != nil {
		t.Errorf("the original password must still work after a rejected change: %v", err)
	}
}

func TestChangePasswordRejectsTooShort(t *testing.T) {
	app, mdPath := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/access/password", url.Values{
		"csrf_token":   {sess.CSRFToken},
		"new_password": {"kurz"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if _, err := masterdata.Load(mdPath, testPassword); err != nil {
		t.Errorf("the original password must still work after a rejected change: %v", err)
	}
}

// TestDeleteBackupsRemovesDatedBackupsButKeepsTheCurrentFile: exercises the
// "Lösche alte Stammdaten-Backups" button, the operator-facing answer to
// backup()'s documented gap (a password change alone leaves old .bak files
// readable with the old password).
func TestDeleteBackupsRemovesDatedBackupsButKeepsTheCurrentFile(t *testing.T) {
	app, mdPath := newTestApp(t)
	md, _ := app.Vault.Get()
	// backup()'s timestamp suffix has one-second resolution, so a second
	// real Save right after Bootstrap's could collide on the same
	// filename; a synthetic older one sidesteps that instead of sleeping
	// past a second boundary in a test.
	if err := os.WriteFile(mdPath+".20200101-000000.bak", []byte("old"), 0o600); err != nil {
		t.Fatalf("seeding a synthetic old backup: %v", err)
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, _ := filepath.Glob(mdPath + ".*.bak")
	if len(before) != 2 {
		t.Fatalf("expected 2 dated backups before deletion, got %d: %v", len(before), before)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/access/delete-backups", url.Values{
		"csrf_token": {sess.CSRFToken},
	})
	if err != nil {
		t.Fatalf("POST delete-backups: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "2 alte Stammdaten-Backups gelöscht") {
		t.Errorf("expected a confirmation naming the count removed, got: %s", raw)
	}

	after, _ := filepath.Glob(mdPath + ".*.bak")
	if len(after) != 0 {
		t.Errorf("expected all dated backups gone, still found: %v", after)
	}
	if _, err := masterdata.Load(mdPath, testPassword); err != nil {
		t.Errorf("the current master data file must survive: %v", err)
	}
}

// lookupSession recovers the *access.Session behind a client's session
// cookie, so a test can read its CSRF token without scraping HTML.
func lookupSession(t *testing.T, app *App, jar http.CookieJar, srvURL string) *access.Session {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", srvURL, err)
	}
	for _, c := range jar.Cookies(u) {
		if c.Name != sessionCookieName {
			continue
		}
		sess, ok := app.Sessions.Lookup(c.Value)
		if !ok {
			t.Fatalf("session cookie present but not found in the store")
		}
		return sess
	}
	t.Fatal("no session cookie found; did the login not set one?")
	return nil
}
