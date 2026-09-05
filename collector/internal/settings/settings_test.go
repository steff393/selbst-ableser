package settings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path)

	want := Settings{ReportIntervalSeconds: 15, FilterRules: []FilterRule{{MeterID: "90000001", BlockedPrefixes: []string{"aa"}}}}
	if err := c.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, found, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("Load: found = false after Save")
	}
	if got.ReportIntervalSeconds != want.ReportIntervalSeconds || len(got.FilterRules) != 1 {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

// TestCacheNeverPersistsTriggerPush guards against a stale "true" on disk
// surviving a restart or a lost connection: TriggerPush is a one-shot
// pulse, not durable state, so Save must always write it as false
// regardless of what's asked to be cached, and Load must clear it too,
// in case the file was written by a build predating this fix.
func TestCacheNeverPersistsTriggerPush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path)

	if err := c.Save(Settings{TriggerPush: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("Load: found = false after Save")
	}
	if got.TriggerPush {
		t.Error("TriggerPush should never round-trip through the cache as true")
	}

	// Simulate a cache file written by an older build that didn't strip
	// it yet: Load alone must still clear it.
	if err := os.WriteFile(path, []byte(`{"trigger_push": true}`), 0o600); err != nil {
		t.Fatalf("writing raw cache file: %v", err)
	}
	got, _, err = c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TriggerPush {
		t.Error("Load should clear TriggerPush even from a pre-existing cache file that has it set")
	}
}

func TestCacheLoadMissingFileIsNotFoundNotError(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "does-not-exist.json"))
	_, found, err := c.Load()
	if err != nil {
		t.Fatalf("Load on a missing cache file should not error, got: %v", err)
	}
	if found {
		t.Error("Load on a missing cache file should report found = false")
	}
}

func TestLiveViewInactiveWhenReportIntervalUnset(t *testing.T) {
	if (Settings{}).LiveViewActive() {
		t.Error("LiveViewActive() should be false when ReportIntervalSeconds is unset (0) — the live view is off by default")
	}
	if !(Settings{ReportIntervalSeconds: 1}).LiveViewActive() {
		t.Error("LiveViewActive() should be true once ReportIntervalSeconds is positive")
	}
}

func TestDailyHourDefaultsWhenOutOfRange(t *testing.T) {
	cases := []struct {
		configured int
		want       int
	}{
		{0, 3}, {-1, 3}, {24, 3}, {23, 23}, {5, 5},
	}
	for _, c := range cases {
		if got := (Settings{DailyPushHour: c.configured}).DailyHour(); got != c.want {
			t.Errorf("DailyHour() with DailyPushHour=%d = %d, want %d", c.configured, got, c.want)
		}
	}
}

func TestIdleReconnectAndConfigPollDefaults(t *testing.T) {
	if got := (Settings{}).IdleReconnect(); got.Seconds() != 120 {
		t.Errorf("IdleReconnect() of an unset Settings = %v, want 120s default", got)
	}
	if got := (Settings{IdleReconnectSeconds: 30}).IdleReconnect(); got.Seconds() != 30 {
		t.Errorf("IdleReconnect() = %v, want 30s", got)
	}
	if got := (Settings{}).ConfigPollInterval(); got.Seconds() != 60 {
		t.Errorf("ConfigPollInterval() of an unset Settings = %v, want 60s default", got)
	}
	if got := (Settings{ConfigPollSeconds: 5}).ConfigPollInterval(); got.Seconds() != 5 {
		t.Errorf("ConfigPollInterval() = %v, want 5s", got)
	}
}

func TestFetchDecodesEvaluatorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collector/config" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer s3cr3t" {
			t.Errorf("Authorization header = %q, want Bearer s3cr3t", got)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST — the poll carries the status report", r.Method)
		}
		var sent Report
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decoding the report body: %v", err)
		}
		if sent.Name != "test" || !sent.Receiver.Connected || sent.Receiver.Port != "COM3" {
			t.Errorf("report body = %+v, want the collector's own status", sent)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"report_interval_seconds": 30, "filter_rules": []}`))
	}))
	defer srv.Close()

	got, err := Fetch(t.Context(), srv.Client(), srv.URL, "s3cr3t", Report{
		Name:     "test",
		Receiver: ReceiverStatus{Connected: true, Port: "COM3"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.ReportIntervalSeconds != 30 {
		t.Errorf("ReportIntervalSeconds = %d, want 30", got.ReportIntervalSeconds)
	}
}
