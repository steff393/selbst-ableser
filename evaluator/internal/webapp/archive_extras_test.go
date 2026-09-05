package webapp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/backup"
)

func loginAsOperator(t *testing.T, client *http.Client, srvURL string) {
	t.Helper()
	if _, err := client.PostForm(srvURL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}
}

func TestArchiveOverviewShowsStorageAndNoBackupYet(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.ArchivePath = filepath.Join(filepath.Dir(mdPath), "archive.db")

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	resp, err := client.Get(srv.URL + "/operator/archive")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "noch keine") {
		t.Errorf("expected the 'no backup yet' indicator, got: %s", body)
	}
	if !strings.Contains(body, "B") { // some byte-size unit should appear
		t.Errorf("expected a storage size to be shown, got: %s", body)
	}
}

func TestArchiveOverviewShowsLastBackupAfterOne(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := backup.Run(app.Store, app.Audit, mdPath, "", "", t.TempDir()); err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	resp, err := client.Get(srv.URL + "/operator/archive")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if strings.Contains(body, "noch keine") {
		t.Error("expected a recorded backup to be shown, not the 'none yet' state")
	}
}
