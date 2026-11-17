package webapp

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/archive"
)

func TestArchiveDeleteRemovesOnlyRequestedRange(t *testing.T) {
	app, _ := newTestApp(t)
	for _, e := range []archive.Entry{
		{MeterID: "90000001", Day: mustDayT(t, "2025-01-15"), RawHex: "aa"},
		{MeterID: "90000001", Day: mustDayT(t, "2025-03-01"), RawHex: "bb"},
	} {
		if _, err := app.Store.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/archive/delete", url.Values{
		"csrf_token": {sess.CSRFToken},
		"from":       {"2025-01-01"},
		"to":         {"2025-01-31"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "1 Einträge gelöscht") {
		t.Errorf("expected the deletion count in the response, got: %s", body)
	}

	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-01-15")); err != nil || found {
		t.Errorf("January entry should have been deleted: found=%v err=%v", found, err)
	}
	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-03-01")); err != nil || !found {
		t.Errorf("March entry should be untouched: found=%v err=%v", found, err)
	}
}

func TestArchiveDeleteRejectsInvalidRange(t *testing.T) {
	app, _ := newTestApp(t)
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: mustDayT(t, "2025-01-15"), RawHex: "aa"}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/archive/delete", url.Values{
		"csrf_token": {sess.CSRFToken},
		"from":       {"not-a-date"},
		"to":         {"2025-01-31"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Ungültiger Zeitraum") {
		t.Errorf("expected the invalid-range error, got: %s", string(raw))
	}

	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-01-15")); err != nil || !found {
		t.Errorf("entry must survive a rejected request: found=%v err=%v", found, err)
	}
}

func TestArchiveDeleteRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/operator/archive/delete", url.Values{"from": {"2025-01-01"}, "to": {"2025-01-31"}})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}

// TestArchivePurgeRemovesOnlyMetersOutsideTheMasterData: within the
// chosen range a neighbour's meter goes, a replaced device of our own
// stays — it is still in the master data with its removal date — and
// outside the range nothing is touched.
func TestArchivePurgeRemovesOnlyMetersOutsideTheMasterData(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app) // 10004690 is the replaced device at 201
	for _, e := range []archive.Entry{
		{MeterID: "10004690", Day: mustDayT(t, "2026-01-15"), RawHex: "aa"},
		{MeterID: "10004711", Day: mustDayT(t, "2026-01-15"), RawHex: "ab"},
		{MeterID: "99999999", Day: mustDayT(t, "2026-01-15"), RawHex: "bb"},
		{MeterID: "99999999", Day: mustDayT(t, "2026-01-16"), RawHex: "bc"},
		{MeterID: "99999999", Day: mustDayT(t, "2026-02-15"), RawHex: "bd"},
	} {
		if _, err := app.Store.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	body := post(t, client, srvURL+"/operator/archive/purge", url.Values{
		"csrf_token": {csrf},
		"from":       {"2026-01-01"},
		"to":         {"2026-01-31"},
	})
	if !strings.Contains(body, "2 Einträge von 1 Zählern entfernt") {
		t.Errorf("expected the purge count in the response, got: %s", firstLines(body))
	}
	for _, c := range []struct {
		meter, day string
		want       bool
	}{
		{"10004690", "2026-01-15", true},
		{"10004711", "2026-01-15", true},
		{"99999999", "2026-01-15", false},
		{"99999999", "2026-02-15", true},
	} {
		if _, found, err := app.Store.Get(c.meter, mustDayT(t, c.day)); err != nil || found != c.want {
			t.Errorf("meter %s on %s: found=%v err=%v, want found=%v", c.meter, c.day, found, err, c.want)
		}
	}
}

func TestArchivePurgeRejectsInvalidRange(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "99999999", Day: mustDayT(t, "2026-01-15"), RawHex: "bb"}); err != nil {
		t.Fatal(err)
	}
	body := post(t, client, srvURL+"/operator/archive/purge", url.Values{"csrf_token": {csrf}})
	if !strings.Contains(body, "Ungültiger Zeitraum") {
		t.Errorf("expected the range refusal, got: %s", firstLines(body))
	}
	if _, found, _ := app.Store.Get("99999999", mustDayT(t, "2026-01-15")); !found {
		t.Error("the entry must survive a refused purge")
	}
}

// TestArchiveDownloadAsDatabaseCanBeImported: format=db hands out an
// archive-schema SQLite file with the range in it — the same file the
// import takes, here through the import page itself.
func TestArchiveDownloadAsDatabaseCanBeImported(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	for _, e := range []archive.Entry{
		archiveEntryFor(t, "m1", "2025-01-15", "aabb"),
		archiveEntryFor(t, "m1", "2025-03-01", "ccdd"),
	} {
		if _, err := app.Store.InsertHistorical(e); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := client.Get(srvURL + "/operator/archive/download?format=db&from=2025-01-01&to=2025-01-31")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(raw), "SQLite format 3\x00") {
		t.Fatalf("download is not a SQLite file (Content-Type %q)", resp.Header.Get("Content-Type"))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "archive-2025-01-01-bis-2025-01-31.db") {
		t.Errorf("Content-Disposition = %q, want a .db file name", cd)
	}

	// Into an empty archive: only the January entry may arrive.
	if _, err := app.Store.DeleteRange(mustDayT(t, "2000-01-01"), mustDayT(t, "2099-12-31")); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf_token", csrf)
	part, _ := mw.CreateFormFile("file", "excerpt.db")
	part.Write(raw)
	mw.Close()
	imp, err := client.Post(srvURL+"/operator/archive/import", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	page := readBody(t, imp)
	imp.Body.Close()
	if !strings.Contains(page, "1 neu eingefügt") {
		t.Errorf("expected one inserted entry, got: %s", firstLines(page))
	}
	if _, found, _ := app.Store.Get("m1", mustDayT(t, "2025-03-01")); found {
		t.Error("the March entry was outside the downloaded range and must not come back")
	}
}
