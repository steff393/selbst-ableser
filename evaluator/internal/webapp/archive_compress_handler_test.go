package webapp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/archive"
)

func TestArchiveCompressKeepsOnlyTheMonthEndReading(t *testing.T) {
	app, _ := newTestApp(t)
	for _, e := range []archive.Entry{
		{MeterID: "90000001", Day: mustDayT(t, "2025-01-05"), RawHex: "aa"},
		{MeterID: "90000001", Day: mustDayT(t, "2025-01-29"), RawHex: "bb"},
		{MeterID: "90000001", Day: mustDayT(t, "2025-02-01"), RawHex: "cc"},
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

	resp, err := client.PostForm(srv.URL+"/operator/archive/compress", url.Values{
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
	if !strings.Contains(body, "1 Einträge komprimiert") {
		t.Errorf("expected the compression count in the response, got: %s", body)
	}

	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-01-05")); err != nil || found {
		t.Errorf("2025-01-05 should have been deleted: found=%v err=%v", found, err)
	}
	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-01-29")); err != nil || !found {
		t.Errorf("2025-01-29 (the month-end reading) must survive: found=%v err=%v", found, err)
	}
	if _, found, err := app.Store.Get("90000001", mustDayT(t, "2025-02-01")); err != nil || !found {
		t.Errorf("February entry outside the range must be untouched: found=%v err=%v", found, err)
	}
}

func TestArchiveCompressRejectsInvalidRange(t *testing.T) {
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

	resp, err := client.PostForm(srv.URL+"/operator/archive/compress", url.Values{
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

func TestArchiveCompressRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/operator/archive/compress", url.Values{"from": {"2025-01-01"}, "to": {"2025-01-31"}})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}
