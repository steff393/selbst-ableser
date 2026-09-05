package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/config"
)

// TestLiveViewToggleStartsAWindowAndPersists: switching the live view on
// starts a bounded window rather than latching a flag, so an installation
// left switched on stops pushing on its own. Exercised through
// /operator/live/toggle — the Collector tab's own copy of this control
// moved there and was removed.
func TestLiveViewToggleStartsAWindowAndPersists(t *testing.T) {
	app, _ := newTestApp(t)
	app.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	now := mustDayTimeT(t, "2026-03-15")
	app.Now = func() time.Time { return now }

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	if active, _ := app.CollectorConfig.LiveViewActiveAt(now); active {
		t.Fatal("the live view should default to off")
	}

	resp, err := client.PostForm(srv.URL+"/operator/live/toggle", url.Values{
		"csrf_token": {sess.CSRFToken},
		"enabled":    {"1"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	active, until := app.CollectorConfig.LiveViewActiveAt(now)
	if !active {
		t.Error("the live view should be on right after enabling")
	}
	if want := now.Add(config.LiveViewWindow); !until.Equal(want) {
		t.Errorf("window ends at %v, want %v", until, want)
	}
	// And it lapses on its own, with nothing having to run on a timer.
	if stillActive, _ := app.CollectorConfig.LiveViewActiveAt(now.Add(config.LiveViewWindow + time.Minute)); stillActive {
		t.Error("the live view must switch itself off after its window")
	}
	if app.CollectorConfig.ReportIntervalSeconds <= 0 {
		t.Error("the first activation should seed an interval")
	}

	persisted, err := config.LoadOrEmpty(app.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if persistedActive, _ := persisted.Collector.LiveViewActiveAt(now); !persistedActive {
		t.Error("the window should survive a restart")
	}

	// An absent "enabled" field (an unchecked checkbox sends nothing at
	// all for it, not "off") must be treated as disabled.
	resp, err = client.PostForm(srv.URL+"/operator/live/toggle", url.Values{
		"csrf_token": {sess.CSRFToken},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if stillActive, _ := app.CollectorConfig.LiveViewActiveAt(now); stillActive {
		t.Error("the live view should be off after disabling")
	}
}

// TestLiveViewTogglesFromItsOwnPage: the same switch also lives on the
// Live-Ansicht page, where its effect is visible.
func TestLiveViewTogglesFromItsOwnPage(t *testing.T) {
	app, _ := newTestApp(t)
	now := mustDayTimeT(t, "2026-03-15")
	app.Now = func() time.Time { return now }

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/live/toggle", url.Values{
		"csrf_token": {sess.CSRFToken},
		"enabled":    {"1"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if active, _ := app.CollectorConfig.LiveViewActiveAt(now); !active {
		t.Error("the Live-Ansicht page's own control should switch the push on")
	}

	resp, err = client.PostForm(srv.URL+"/operator/live/toggle", url.Values{
		"csrf_token": {sess.CSRFToken},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if active, _ := app.CollectorConfig.LiveViewActiveAt(now); active {
		t.Error("and off again")
	}
}

func TestCollectorAdvancedSaveUpdatesAndPersists(t *testing.T) {
	app, _ := newTestApp(t)
	app.ConfigPath = filepath.Join(t.TempDir(), "config.json")

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/collector/advanced", url.Values{
		"csrf_token":              {sess.CSRFToken},
		"report_interval_seconds": {"30"},
		"daily_push_hour":         {"5"},
		"idle_reconnect_seconds":  {"90"},
		"config_poll_seconds":     {"45"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	got := app.CollectorConfig
	if got.ReportIntervalSeconds != 30 || got.DailyPushHour != 5 || got.IdleReconnectSeconds != 90 || got.ConfigPollSeconds != 45 {
		t.Errorf("CollectorConfig = %+v, want 30/5/90/45", got)
	}

	persisted, err := config.LoadOrEmpty(app.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if persisted.Collector.ReportIntervalSeconds != 30 || persisted.Collector.DailyPushHour != 5 {
		t.Errorf("persisted Collector = %+v, want ReportIntervalSeconds=30, DailyPushHour=5", persisted.Collector)
	}
}

func TestCollectorAdvancedSaveRejectsOutOfRangeValues(t *testing.T) {
	cases := []url.Values{
		{"report_interval_seconds": {"0"}, "daily_push_hour": {"23"}, "idle_reconnect_seconds": {"120"}, "config_poll_seconds": {"60"}},
		{"report_interval_seconds": {"301"}, "daily_push_hour": {"23"}, "idle_reconnect_seconds": {"120"}, "config_poll_seconds": {"60"}},
		{"report_interval_seconds": {"1"}, "daily_push_hour": {"24"}, "idle_reconnect_seconds": {"120"}, "config_poll_seconds": {"60"}},
		{"report_interval_seconds": {"1"}, "daily_push_hour": {"23"}, "idle_reconnect_seconds": {"5"}, "config_poll_seconds": {"60"}},
		{"report_interval_seconds": {"1"}, "daily_push_hour": {"23"}, "idle_reconnect_seconds": {"120"}, "config_poll_seconds": {"1"}},
	}
	for _, form := range cases {
		app, _ := newTestApp(t)
		srv := httptest.NewServer(app.Routes())
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		loginAsOperator(t, client, srv.URL)
		sess := lookupSession(t, app, jar, srv.URL)

		form["csrf_token"] = []string{sess.CSRFToken}
		resp, err := client.PostForm(srv.URL+"/operator/collector/advanced", form)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()

		if app.CollectorConfig.ReportIntervalSeconds != 0 {
			t.Errorf("out-of-range form %v should have been rejected, got CollectorConfig = %+v", form, app.CollectorConfig)
		}
		srv.Close()
	}
}

func TestCollectorTriggerPushSetsFlagAndConfigEndpointConsumesItOnce(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/collector/trigger-push", url.Values{
		"csrf_token": {sess.CSRFToken},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if !app.CollectorConfig.TriggerPush {
		t.Fatal("TriggerPush should be true right after the button is clicked")
	}

	// The collector-facing endpoint must report it exactly once...
	first := fetchCollectorConfigT(t, srv.URL)
	if !first.TriggerPush {
		t.Error("first /collector/config fetch should report trigger_push=true")
	}
	// ...and reset it, so a second fetch does not see it again.
	second := fetchCollectorConfigT(t, srv.URL)
	if second.TriggerPush {
		t.Error("second /collector/config fetch should report trigger_push=false (already consumed)")
	}
}

func fetchCollectorConfigT(t *testing.T, baseURL string) struct {
	ReportIntervalSeconds int  `json:"report_interval_seconds"`
	TriggerPush           bool `json:"trigger_push"`
} {
	t.Helper()
	resp, err := http.Post(baseURL+"/collector/config", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /collector/config: %v", err)
	}
	defer resp.Body.Close()
	var got struct {
		ReportIntervalSeconds int  `json:"report_interval_seconds"`
		TriggerPush           bool `json:"trigger_push"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding /collector/config response: %v", err)
	}
	return got
}

func TestCollectorFilterRuleAddAndRemove(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/collector/filter-rules", url.Values{
		"csrf_token":       {sess.CSRFToken},
		"meter_id":         {"90000001"},
		"blocked_prefixes": {"AA, bb"},
	})
	if err != nil {
		t.Fatalf("POST add: %v", err)
	}
	resp.Body.Close()

	if len(app.CollectorConfig.FilterRules) != 1 {
		t.Fatalf("FilterRules = %+v, want one rule", app.CollectorConfig.FilterRules)
	}
	rule := app.CollectorConfig.FilterRules[0]
	if rule.MeterID != "90000001" || len(rule.BlockedPrefixes) != 2 || rule.BlockedPrefixes[0] != "aa" {
		t.Errorf("rule = %+v, want lowercased prefixes aa/bb", rule)
	}

	resp, err = client.PostForm(srv.URL+"/operator/collector/filter-rules/remove", url.Values{
		"csrf_token": {sess.CSRFToken},
		"index":      {"0"},
	})
	if err != nil {
		t.Fatalf("POST remove: %v", err)
	}
	resp.Body.Close()

	if len(app.CollectorConfig.FilterRules) != 0 {
		t.Errorf("FilterRules after remove = %+v, want none", app.CollectorConfig.FilterRules)
	}
}

func TestCollectorFilterRuleAddDefaultsToAnyMeter(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/collector/filter-rules", url.Values{
		"csrf_token":       {sess.CSRFToken},
		"blocked_prefixes": {"aa"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if len(app.CollectorConfig.FilterRules) != 1 || app.CollectorConfig.FilterRules[0].MeterID != "*" {
		t.Errorf("FilterRules = %+v, want MeterID *", app.CollectorConfig.FilterRules)
	}
}

func TestCollectorSecretSaveAndGenerate(t *testing.T) {
	app, _ := newTestApp(t)
	app.SecretsPath = filepath.Join(t.TempDir(), "secrets.json")

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/collector/secret", url.Values{
		"csrf_token": {sess.CSRFToken},
		"secret":     {"manually-chosen-secret"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if app.PushSecret != "manually-chosen-secret" {
		t.Errorf("PushSecret = %q, want the manually chosen one", app.PushSecret)
	}

	persisted, err := config.LoadSecretsOrEmpty(app.SecretsPath)
	if err != nil {
		t.Fatalf("LoadSecretsOrEmpty: %v", err)
	}
	if persisted.PushSecret != "manually-chosen-secret" {
		t.Errorf("persisted PushSecret = %q, want the manually chosen one", persisted.PushSecret)
	}

	resp, err = client.PostForm(srv.URL+"/operator/collector/secret/generate", url.Values{
		"csrf_token": {sess.CSRFToken},
	})
	if err != nil {
		t.Fatalf("POST generate: %v", err)
	}
	resp.Body.Close()
	if app.PushSecret == "manually-chosen-secret" || app.PushSecret == "" {
		t.Errorf("PushSecret after generate = %q, want a freshly generated value", app.PushSecret)
	}
}

func TestCollectorSettingsViewRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/operator/collector")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login for an unauthenticated request, ended up at %s", resp.Request.URL.Path)
	}
}
