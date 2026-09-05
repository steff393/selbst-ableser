package webapp

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/telegram"
)

func buildImportUpload(t *testing.T, csrfToken string, dbPath string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf_token", csrfToken); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "backup.db")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

func TestArchiveImportInsertsEntriesFromUpload(t *testing.T) {
	app, _ := newTestApp(t)

	sourcePath := filepath.Join(t.TempDir(), "backup.db")
	source, err := archive.OpenStore(sourcePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	day, err := telegram.ParseDay("2025-03-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.InsertHistorical(archive.Entry{MeterID: "90000099", Day: day, RawHex: "aabbcc"}); err != nil {
		t.Fatal(err)
	}
	source.Close()

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	body, contentType := buildImportUpload(t, sess.CSRFToken, sourcePath)
	resp, err := client.Post(srv.URL+"/operator/archive/import", contentType, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	respBody := string(raw)
	if !strings.Contains(respBody, "1 neu eingefügt") {
		t.Errorf("expected the import report to show 1 inserted entry, got: %s", respBody)
	}

	entry, found, err := app.Store.Get("90000099", day)
	if err != nil || !found {
		t.Fatalf("imported entry not found in the live archive: found=%v err=%v", found, err)
	}
	if entry.RawHex != "aabbcc" {
		t.Errorf("RawHex = %q, want aabbcc", entry.RawHex)
	}
}

func TestArchiveImportReportsConflictsWithoutOverwriting(t *testing.T) {
	app, _ := newTestApp(t)

	day, err := telegram.ParseDay("2025-03-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000099", Day: day, RawHex: "original"}); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(t.TempDir(), "backup.db")
	source, err := archive.OpenStore(sourcePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := source.InsertHistorical(archive.Entry{MeterID: "90000099", Day: day, RawHex: "conflicting"}); err != nil {
		t.Fatal(err)
	}
	source.Close()

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	body, contentType := buildImportUpload(t, sess.CSRFToken, sourcePath)
	resp, err := client.Post(srv.URL+"/operator/archive/import", contentType, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	respBody := string(raw)
	if !strings.Contains(respBody, "Konflikt") {
		t.Errorf("expected the conflict to be reported, got: %s", respBody)
	}

	entry, _, err := app.Store.Get("90000099", day)
	if err != nil {
		t.Fatal(err)
	}
	if entry.RawHex != "original" {
		t.Errorf("existing entry was overwritten: RawHex = %q, want %q", entry.RawHex, "original")
	}
}

func TestArchiveImportRejectsBadFileWithoutCrashing(t *testing.T) {
	app, _ := newTestApp(t)

	badPath := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(badPath, []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	body, contentType := buildImportUpload(t, sess.CSRFToken, badPath)
	resp, err := client.Post(srv.URL+"/operator/archive/import", contentType, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 with an inline error message", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "keine gültige Archiv-Datenbank") {
		t.Errorf("expected the invalid-file error message, got: %s", string(raw))
	}
}

func TestArchiveImportRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/operator/archive/import", "multipart/form-data", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}
