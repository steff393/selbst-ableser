package webapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/livepush"
)

func TestLiveViewMarksUnknownMeters(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	app.LiveBuffer.Add(livepush.Telegram{MeterID: "99999999", RawHex: "3844934458057440360" + "87ae11820252f2f0b6e4500004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132"})

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(srv.URL + "/operator/live")
	if err != nil {
		t.Fatalf("GET /operator/live: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "99999999") {
		t.Errorf("expected the pushed meter ID in the page: %s", body)
	}
	if !strings.Contains(body, "nein") {
		t.Error("expected the unknown-meter marker ('nein') since no master data meter matches")
	}
}

func TestLiveViewShowsOnlyOneRowPerMeterAndLength(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	// Two pushes for the same meter, same length (3 bytes each), distinguishable
	// by RSSI; the buffer is newest-last, so -70 (added second) is the one that
	// should survive.
	app.LiveBuffer.Add(
		livepush.Telegram{MeterID: "99999999", RSSI: -90, RawHex: "aabbcc"},
		livepush.Telegram{MeterID: "99999999", RSSI: -70, RawHex: "ddeeff"},
	)

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(srv.URL + "/operator/live")
	if err != nil {
		t.Fatalf("GET /operator/live: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if strings.Count(body, "99999999") != 1 {
		t.Errorf("expected the meter to appear exactly once, got %d occurrences: %s", strings.Count(body, "99999999"), body)
	}
	if !strings.Contains(body, "-70 dBm") {
		t.Error("expected the most recently pushed telegram's RSSI (-70), not the older one")
	}
	if strings.Contains(body, "-90 dBm") {
		t.Error("the older push's RSSI (-90) should not appear")
	}
}

func TestMeterIntervalsGapBetweenTwoMostRecent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := []livepush.Telegram{
		// Newest-first, as Buffer.Recent returns them.
		{MeterID: "m1", ReceivedAt: now},
		{MeterID: "m1", ReceivedAt: now.Add(-90 * time.Second)},
		{MeterID: "m1", ReceivedAt: now.Add(-5 * time.Minute)}, // older still: ignored
		{MeterID: "m2", ReceivedAt: now},                       // only one entry: no interval
	}

	got := meterIntervals(entries)
	if d, ok := got["m1"]; !ok || d != 90*time.Second {
		t.Errorf("meterIntervals()[m1] = %v, ok=%v, want 90s", d, ok)
	}
	if _, ok := got["m2"]; ok {
		t.Errorf("meterIntervals()[m2] present, want absent (only one entry)")
	}
}

func TestLiveViewShowsIntervalSinceLastTelegram(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	now := time.Now()
	// Two different lengths for the same meter — the interval must still
	// be computed across both, not reset by the length change.
	app.LiveBuffer.Add(
		livepush.Telegram{MeterID: "99999999", ReceivedAt: now.Add(-2 * time.Minute), RawHex: "aabbcc"},
		livepush.Telegram{MeterID: "99999999", ReceivedAt: now, RawHex: "ddeeff00"},
	)

	client, srv := operatorClient(t, app)
	resp, err := client.Get(srv.URL + "/operator/live")
	if err != nil {
		t.Fatalf("GET /operator/live: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "2m0s") {
		t.Errorf("expected the 2-minute interval to appear: %s", body)
	}
}

func TestLiveViewShowsOneRowPerLengthVariant(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	// Same meter, two different telegram lengths (3 and 4 bytes) — a meter
	// alternating between formats, which is exactly the case the operator
	// needs to see both variants of to write a filter rule.
	app.LiveBuffer.Add(
		livepush.Telegram{MeterID: "99999999", RSSI: -90, RawHex: "aabbcc"},
		livepush.Telegram{MeterID: "99999999", RSSI: -70, RawHex: "ddeeff00"},
	)

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(srv.URL + "/operator/live")
	if err != nil {
		t.Fatalf("GET /operator/live: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if strings.Count(body, "99999999") != 2 {
		t.Errorf("expected the meter to appear twice (once per length), got %d occurrences: %s", strings.Count(body, "99999999"), body)
	}
	if !strings.Contains(body, "-70 dBm") || !strings.Contains(body, "-90 dBm") {
		t.Error("expected both length variants' RSSI to appear, not just the most recent")
	}
}

func TestArchiveDownloadReturnsOnlyRequestedRange(t *testing.T) {
	app, _ := newTestApp(t)
	if _, err := app.Store.InsertHistorical(archiveEntryFor(t, "m1", "2025-01-15", "aabb")); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}
	if _, err := app.Store.InsertHistorical(archiveEntryFor(t, "m1", "2025-03-01", "ccdd")); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {testPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(srv.URL + "/operator/archive/download?from=2025-01-01&to=2025-01-31")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(entries) != 1 || entries[0]["raw_hex"] != "aabb" {
		t.Errorf("entries = %+v, want exactly the January entry", entries)
	}
}

// TestLiveDeleteTodayClearsConflictWithDailyPush reproduces the situation
// this button exists to recover from: an entry has already been written
// durably for today, and a later push for the same day with a differing
// reading is rejected as a conflict — see archive.Store.InsertHistorical.
// Deleting today's entries clears the way for that later push to succeed.
//
// In practice this same-day case now needs a second manual push (see
// handleCollectorTriggerPush): the collector's own scheduled push never
// resends a day it still considers in progress (see deliverDue in
// cmd/saCollector), so the conflict it can cause typically only surfaces
// on a later day — a case this button, scoped to app.today(), does not
// reach; see handleLiveDeleteToday's doc comment.
func TestLiveDeleteTodayClearsConflictWithDailyPush(t *testing.T) {
	app, _ := newTestApp(t)
	today := app.today()
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "m1", Day: today, RawHex: "aabb"}); err != nil {
		t.Fatalf("seeding today's entry: %v", err)
	}

	// Confirms the conflict this feature exists to clear: a differing
	// entry for the same meter and day is rejected, not overwritten.
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "m1", Day: today, RawHex: "ccdd"}); !errors.Is(err, archive.ErrConflict) {
		t.Fatalf("expected ErrConflict before cleanup, got %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	postForm(t, client, srv.URL+"/operator/live/delete-today", map[string]string{
		"csrf_token": sess.CSRFToken,
	})

	if _, found, err := app.Store.Get("m1", today); err != nil || found {
		t.Fatalf("Get after delete: found=%v err=%v, want not found", found, err)
	}
	// The same later push that conflicted above must now succeed.
	if changed, err := app.Store.InsertHistorical(archive.Entry{MeterID: "m1", Day: today, RawHex: "ccdd"}); err != nil || !changed {
		t.Errorf("InsertHistorical after cleanup: changed=%v err=%v, want (true, nil)", changed, err)
	}
}

func TestLiveDeleteTodayRequiresCSRF(t *testing.T) {
	app, _ := newTestApp(t)
	today := app.today()
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "m1", Day: today, RawHex: "aabb"}); err != nil {
		t.Fatalf("seeding today's entry: %v", err)
	}

	client, srv := operatorClient(t, app)
	postForm(t, client, srv.URL+"/operator/live/delete-today", map[string]string{})

	if _, found, err := app.Store.Get("m1", today); err != nil || !found {
		t.Error("a request without a CSRF token must not delete anything")
	}
}

func TestLiveBufferClearEmptiesBuffer(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	app.LiveBuffer.Add(livepush.Telegram{MeterID: "99999999", RawHex: "aabbcc"})

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)
	postForm(t, client, srv.URL+"/operator/live/clear-buffer", map[string]string{
		"csrf_token": sess.CSRFToken,
	})

	if got := app.LiveBuffer.Recent(10); len(got) != 0 {
		t.Errorf("buffer after clear = %+v, want empty", got)
	}
}

func TestLiveBufferClearRequiresCSRF(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	app.LiveBuffer.Add(livepush.Telegram{MeterID: "99999999", RawHex: "aabbcc"})

	client, srv := operatorClient(t, app)
	postForm(t, client, srv.URL+"/operator/live/clear-buffer", map[string]string{})

	if got := app.LiveBuffer.Recent(10); len(got) != 1 {
		t.Error("a request without a CSRF token must not clear the buffer")
	}
}

func archiveEntryFor(t *testing.T, meterID, day, rawHex string) archive.Entry {
	t.Helper()
	return archive.Entry{MeterID: meterID, Day: mustDayT(t, day), RawHex: rawHex}
}
