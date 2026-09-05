package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

func unitOnlyMasterData(unitID, unitName string) masterdata.MasterData {
	return masterdata.MasterData{
		Units: []masterdata.Unit{{ID: unitID, Name: unitName, AreaM2: 50}},
	}
}

// gridJSON builds the accesses_json payload the same way the browser's
// SAGrid.create submit handler would.
func gridJSON(t *testing.T, rows []accessGridRow) string {
	t.Helper()
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	return string(b)
}

// TestAccessSaveCreatesGrantWithEmail covers the gap the model's own doc
// comment used to gesture at without an actual UI behind it: BENACHR-01's
// monthly reminder reads Access.Email, but nothing let an operator set it
// until this field existed on the grid.
func TestAccessSaveCreatesGrantWithEmail(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{UnitID: "u1", Start: "01.01.2026", Email: "mieter@example.org"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 1 || md.Accesses[0].Email != "mieter@example.org" || md.Accesses[0].UnitID != "u1" {
		t.Errorf("Accesses = %+v, want one grant with the given email", md.Accesses)
	}
	if md.Accesses[0].Token == "" {
		t.Error("a new row must get a server-generated token")
	}
}

// The email is optional: a grant with none must still be created normally.
func TestAccessSaveCreatesGrantWithoutEmail(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{UnitID: "u1", Start: "01.01.2026"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 1 || md.Accesses[0].Email != "" {
		t.Errorf("Accesses = %+v, want one grant with no email", md.Accesses)
	}
}

// TestAccessSaveShowsTokenInGridNotSeparateBanner: a freshly generated
// token is shown exactly once — but that once is in the grid itself
// (read-only Zugangscode column), not a separate "Neue Zugänge erstellt"
// banner repeating the same information right above it.
func TestAccessSaveShowsTokenInGridNotSeparateBanner(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{UnitID: "u1", Start: "01.01.2026"}}
	values := url.Values{"csrf_token": {sess.CSRFToken}, "accesses_json": {gridJSON(t, rows)}}
	resp, err := client.PostForm(srv.URL+"/operator/access", values)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body := readAll(t, resp)

	md, _ := app.Vault.Get()
	token := md.Accesses[0].Token
	if token == "" {
		t.Fatal("no token was generated")
	}
	if !strings.Contains(body, token) {
		t.Error("the new token should appear in the response (the grid), even without a separate banner")
	}
	if strings.Contains(body, "Neue Zugänge erstellt") {
		t.Error("the separate \"Neue Zugänge erstellt\" banner should be gone — the grid already shows the token")
	}
	if !strings.Contains(body, "Gespeichert.") {
		t.Error("a generic save confirmation should still show, even when new grants were created")
	}
}

func TestAccessSaveRejectsImplausibleEmail(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{UnitID: "u1", Start: "01.01.2026", Email: "not-an-email"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 0 {
		t.Errorf("an implausible email should reject the whole save, got: %+v", md.Accesses)
	}
}

func TestAccessSaveRejectsUnknownUnit(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{UnitID: "does-not-exist", Start: "01.01.2026"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 0 {
		t.Errorf("an unknown unit should reject the whole save, got: %+v", md.Accesses)
	}
}

// TestAccessSaveEditsExistingGrant covers editing an existing grant's
// fields — set, change, and clear the email, and change its End date —
// while its token, looked up from the (read-only in the grid) token cell,
// stays exactly what it already was.
func TestAccessSaveEditsExistingGrant(t *testing.T) {
	app, mdPath := newTestApp(t)
	md := unitOnlyMasterData("u1", "Wohnung A")
	md.Accesses = []masterdata.Access{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")}}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: "01.01.2020", End: "31.12.2026", Email: "mieter@example.org"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	got, _ := app.Vault.Get()
	if len(got.Accesses) != 1 {
		t.Fatalf("Accesses = %+v, want exactly one (edited, not duplicated)", got.Accesses)
	}
	edited := got.Accesses[0]
	if edited.Token != "AAAA-BBBB-CCCC" {
		t.Errorf("Token = %q, want the original token unchanged", edited.Token)
	}
	if edited.Email != "mieter@example.org" {
		t.Errorf("Email = %q after setting", edited.Email)
	}
	if edited.End == nil || string(*edited.End) != "2026-12-31" {
		t.Errorf("End = %v, want 2026-12-31", edited.End)
	}

	// Clearing: an empty value is a legitimate, deliberate "no address".
	rows[0].Email = ""
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})
	got, _ = app.Vault.Get()
	if got.Accesses[0].Email != "" {
		t.Errorf("Email = %q after clearing, want empty", got.Accesses[0].Email)
	}
}

// TestAccessSaveRejectsUnknownToken guards the security invariant this
// whole redesign turns on: a row's token cell is read-only in the grid, so
// the only way a non-blank, non-matching token reaches the server is a
// tampered or stale request — and that must be rejected outright, never
// silently accepted as describing a new grant (which would let a client
// choose its own "random" token).
func TestAccessSaveRejectsUnknownToken(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	rows := []accessGridRow{{Token: "attacker-chosen-token", UnitID: "u1", Start: "01.01.2026"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    sess.CSRFToken,
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 0 {
		t.Errorf("a row with an unrecognized token must not create or store a grant, got: %+v", md.Accesses)
	}
}

func TestAccessSaveRequiresCSRF(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := masterdata.Save(mdPath, unitOnlyMasterData("u1", "Wohnung A"), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	rows := []accessGridRow{{UnitID: "u1", Start: "01.01.2026"}}
	postForm(t, client, srv.URL+"/operator/access", map[string]string{
		"accesses_json": gridJSON(t, rows),
	})

	md, _ := app.Vault.Get()
	if len(md.Accesses) != 0 {
		t.Error("a request without a CSRF token must not create a grant")
	}
}

// TestAccessSaveChecksCSRFBeforeVaultLock locks in the order Prüfpunkt 6.A
// asked for: CSRF immediately after the role check, ahead of any vault
// access. A locked vault plus a missing CSRF token must fail as "bad
// request" (the CSRF path), not render the "Gesperrt" page (which would
// mean Vault.Get() ran first).
func TestAccessSaveChecksCSRFBeforeVaultLock(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	// operatorClient's login unlocks the vault as a side effect (logging
	// in as operator *is* unlocking — see docs/architektur.md); lock it back up
	// to reach the state this test is actually about: an operator session
	// that exists, but a vault that is locked again.
	app.Vault.Lock()

	resp, err := client.PostForm(srv.URL+"/operator/access", url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (bad request from the CSRF check) — got the locked page instead, meaning the vault was checked first", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestAccessSaveRevokesRemovedGrant covers the grid's replacement for the
// old dedicated "Widerrufen" button: a token missing from the submitted
// rows is gone from master data and its session is cut immediately
// (ZUGANG-04), not just refused on the next login.
func TestAccessSaveRevokesRemovedGrant(t *testing.T) {
	app, mdPath := newTestApp(t)
	token, err := access.GenerateAccessToken()
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	md := unitOnlyMasterData("u1", "Wohnung A")
	md.Accesses = []masterdata.Access{{Token: token, UnitID: "u1", Start: mustDayT(t, "2020-01-01")}}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	tenantJar, _ := cookiejar.New(nil)
	tenantClient := &http.Client{Jar: tenantJar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loginResp, err := tenantClient.PostForm(srv.URL+"/login", url.Values{"credential": {token}})
	if err != nil {
		t.Fatalf("tenant login: %v", err)
	}
	loginResp.Body.Close()

	uviResp, err := tenantClient.Get(srv.URL + "/uvi")
	if err != nil {
		t.Fatalf("GET /uvi: %v", err)
	}
	uviResp.Body.Close()
	if uviResp.StatusCode != http.StatusOK {
		t.Fatalf("tenant login did not establish a working session (GET /uvi = %d)", uviResp.StatusCode)
	}

	opJar, _ := cookiejar.New(nil)
	opClient := &http.Client{Jar: opJar}
	opLoginResp, err := opClient.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}})
	if err != nil {
		t.Fatalf("operator login: %v", err)
	}
	opLoginResp.Body.Close()
	opSess := lookupSession(t, app, opJar, srv.URL)

	postForm(t, opClient, srv.URL+"/operator/access", map[string]string{
		"csrf_token":    opSess.CSRFToken,
		"accesses_json": gridJSON(t, nil), // no rows: the only grant is removed
	})

	got, _ := app.Vault.Get()
	if len(got.Accesses) != 0 {
		t.Errorf("Accesses = %+v, want none after removing the only row", got.Accesses)
	}

	afterResp, err := tenantClient.Get(srv.URL + "/uvi")
	if err != nil {
		t.Fatalf("GET /uvi after revoke: %v", err)
	}
	afterResp.Body.Close()
	if afterResp.StatusCode != http.StatusSeeOther {
		t.Errorf("GET /uvi after revoke = %d, want %d (session must be cut immediately)", afterResp.StatusCode, http.StatusSeeOther)
	}
}

func TestLooksLikeEmail(t *testing.T) {
	valid := []string{"a@b.de", "mieter@example.org"}
	invalid := []string{"", "no-at-sign", "@missinglocal", "trailing@", "has space@example.org"}
	for _, s := range valid {
		if !looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = true, want false", s)
		}
	}
}

// The email survives round-tripping through the master data JSON export
// (STAMM-07) alongside everything else — it is genuine master data now
// that there's a UI for it, not a field that only ever exists in memory.
func TestMasterDataExportIncludesAccessEmail(t *testing.T) {
	app, mdPath := newTestApp(t)
	md := unitOnlyMasterData("u1", "Wohnung A")
	md.Accesses = []masterdata.Access{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01"), Email: "mieter@example.org"}}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	body := getAuditBody(t, client, srv.URL+"/operator/masterdata/export")
	if !strings.Contains(body, "mieter@example.org") {
		t.Errorf("expected the access email in the export, got: %s", body)
	}
}
