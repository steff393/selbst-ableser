package webapp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/livepush"
)

func TestCollectorConfigServesConfiguredValues(t *testing.T) {
	app, _ := newTestApp(t)
	app.PushSecret = "s3cr3t"
	app.CollectorConfig = config.Collector{
		LiveViewUntil:         "2099-01-01T00:00:00Z",
		ReportIntervalSeconds: 15,
		FilterRules:           []config.FilterRuleConfig{{MeterID: "90000001", BlockedPrefixes: []string{"aa"}}},
	}
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/collector/config", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got struct {
		ReportIntervalSeconds int `json:"report_interval_seconds"`
		FilterRules           []struct {
			MeterID string `json:"meter_id"`
		} `json:"filter_rules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ReportIntervalSeconds != 15 {
		t.Errorf("ReportIntervalSeconds = %d, want 15", got.ReportIntervalSeconds)
	}
	if len(got.FilterRules) != 1 || got.FilterRules[0].MeterID != "90000001" {
		t.Errorf("FilterRules = %+v", got.FilterRules)
	}
}

func TestCollectorConfigRejectsWrongSecret(t *testing.T) {
	app, _ := newTestApp(t)
	app.PushSecret = "s3cr3t"
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/collector/config", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCollectorConfigLoopbackTrustWithNoSecretConfigured(t *testing.T) {
	app, _ := newTestApp(t)
	// app.PushSecret left empty on purpose: the no-flags saCollector default.
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	// httptest.NewServer listens on 127.0.0.1, so a plain client request
	// already originates from loopback — exactly the case this is for.
	resp, err := http.Post(srv.URL+"/collector/config", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (loopback trust with no secret configured)", resp.StatusCode)
	}
}

func TestCollectorConfigLoopbackTrustDoesNotApplyWithTrustProxy(t *testing.T) {
	app, _ := newTestApp(t)
	app.TrustProxy = true
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/collector/config", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (loopback trust must not apply once TrustProxy is on)", resp.StatusCode)
	}
}

func postReport(t *testing.T, srv *httptest.Server, final bool, entries []archive.Entry) *http.Response {
	t.Helper()
	body, err := json.Marshal(struct {
		Final   bool            `json:"final"`
		Entries []archive.Entry `json:"entries"`
	}{Final: final, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/collector/report", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /collector/report: %v", err)
	}
	return resp
}

func TestCollectorReportTodayNotFinalGoesOnlyToLiveBuffer(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	// No secret configured: relies on loopback trust, like the config test.
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	today := app.today()
	entries := []archive.Entry{{MeterID: "90000001", Day: today, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}}

	resp := postReport(t, srv, false, entries)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got := app.LiveBuffer.Recent(10); len(got) != 1 || got[0].MeterID != "90000001" {
		t.Errorf("LiveBuffer.Recent = %+v, want the one entry", got)
	}
	if _, found, err := app.Store.Get("90000001", today); err != nil {
		t.Fatal(err)
	} else if found {
		t.Error("a non-final, still-open-day entry must not be committed to the durable archive")
	}
}

func TestCollectorReportPastDayIsCommittedEvenWithoutFinal(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	yesterday := app.today().AddDays(-1)
	entries := []archive.Entry{{MeterID: "90000001", Day: yesterday, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}}

	resp := postReport(t, srv, false, entries)
	defer resp.Body.Close()

	var result struct {
		Accepted int `json:"accepted"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Accepted != 1 {
		t.Errorf("accepted = %d, want 1", result.Accepted)
	}
	if _, found, err := app.Store.Get("90000001", yesterday); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Error("a genuinely past day should be committed durably even without the final flag")
	}
}

func TestCollectorReportTodayWithFinalIsCommitted(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	today := app.today()
	entries := []archive.Entry{{MeterID: "90000001", Day: today, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}}

	resp := postReport(t, srv, true, entries)
	defer resp.Body.Close()

	if _, found, err := app.Store.Get("90000001", today); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Error("a final report for today should be committed durably (the manual-push case — the scheduled push itself never marks today final, see deliverDue in cmd/saCollector)")
	}
}

func TestCollectorReportConflictIsReportedNotSilentlyOverwritten(t *testing.T) {
	app, _ := newTestApp(t)
	app.LiveBuffer = livepush.NewBuffer(10)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	yesterday := app.today().AddDays(-1)
	first := archive.Entry{MeterID: "90000001", Day: yesterday, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}
	if _, err := app.Store.InsertHistorical(first); err != nil {
		t.Fatal(err)
	}

	conflicting := archive.Entry{MeterID: "90000001", Day: yesterday, ReceivedAt: time.Now(), RSSI: -90, RawHex: "ccdd"}
	resp := postReport(t, srv, false, []archive.Entry{conflicting})
	defer resp.Body.Close()

	var result struct {
		Conflicts int `json:"conflicts"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Conflicts != 1 {
		t.Errorf("conflicts = %d, want 1", result.Conflicts)
	}
	got, _, err := app.Store.Get("90000001", yesterday)
	if err != nil {
		t.Fatal(err)
	}
	if got.RawHex != "aabb" {
		t.Error("a conflicting entry must not silently overwrite the original")
	}
}
