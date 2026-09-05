package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"selbst-ableser/collector/internal/settings"
	"selbst-ableser/collector/internal/store"
	"selbst-ableser/collector/internal/telegram"
)

const testDailyHour = 23

// TestLocalFileNames pins the one rule left for this collector's local
// files: both sit in -path under fixed names, with no way to redirect
// either on its own.
func TestLocalFileNames(t *testing.T) {
	dir := filepath.Join("var", "lib", "selbst-ableser-collector")
	if got, want := filepath.Join(dir, settingsCacheFileName), filepath.Join(dir, "settings-cache.json"); got != want {
		t.Errorf("settings cache = %q, want %q", got, want)
	}
	if got, want := filepath.Join(dir, backupFileName), filepath.Join(dir, "backup.db"); got != want {
		t.Errorf("backup = %q, want %q", got, want)
	}
}

func TestNextDailyTriggerSameDayBeforeHour(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	got := nextDailyTrigger(now, testDailyHour)
	want := time.Date(2026, 8, 21, testDailyHour, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextDailyTrigger(%v) = %v, want %v", now, got, want)
	}
}

func TestNextDailyTriggerRollsOverToNextDay(t *testing.T) {
	now := time.Date(2026, 8, 21, testDailyHour, 30, 0, 0, time.UTC)
	got := nextDailyTrigger(now, testDailyHour)
	want := time.Date(2026, 8, 22, testDailyHour, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextDailyTrigger(%v) = %v, want %v (next day)", now, got, want)
	}
}

func TestNextDailyTriggerExactlyAtHourRollsOver(t *testing.T) {
	now := time.Date(2026, 8, 21, testDailyHour, 0, 0, 0, time.UTC)
	got := nextDailyTrigger(now, testDailyHour)
	want := time.Date(2026, 8, 22, testDailyHour, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextDailyTrigger(%v) = %v, want %v (must not fire again at the exact boundary)", now, got, want)
	}
}

func TestNextDailyTriggerUsesConfiguredHour(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	got := nextDailyTrigger(now, 5)
	want := time.Date(2026, 8, 22, 5, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextDailyTrigger(%v, 5) = %v, want %v", now, got, want)
	}
}

var logLineTimestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}  `)

func TestLogLineHasTimestampPrefix(t *testing.T) {
	var buf bytes.Buffer
	old := stdout
	stdout = &buf
	defer func() { stdout = old }()

	logLine("day %s delivered", "2026-08-21")

	got := buf.String()
	if !logLineTimestampPattern.MatchString(got) {
		t.Errorf("logLine output = %q, want it to start with a timestamp", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("day 2026-08-21 delivered")) {
		t.Errorf("logLine output = %q, want it to contain the formatted message", got)
	}
}

func TestErrLineHasTimestampPrefix(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	defer func() { stderr = old }()

	errLine("live report: %v", "connection refused")

	got := buf.String()
	if !logLineTimestampPattern.MatchString(got) {
		t.Errorf("errLine output = %q, want it to start with a timestamp", got)
	}
}

func TestReportErrorTrackerSuppressesRepeats(t *testing.T) {
	tr := &reportErrorTracker{}

	if !tr.note("connection refused") {
		t.Error("first occurrence of an error should be reported")
	}
	if tr.note("connection refused") {
		t.Error("an identical repeat should be suppressed")
	}
	if !tr.note("timeout") {
		t.Error("a different error message should be reported even while the tracker already holds one")
	}

	tr.clear()
	if !tr.note("timeout") {
		t.Error("after clear(), a previously-seen message should be reported again")
	}
}

// fakeConfigServer serves a scripted sequence of /collector/config
// responses, one per request (repeating the last one once exhausted) —
// enough to drive liveSettings.poll through a chosen TriggerPush
// sequence without a real evaluator.
func fakeConfigServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	var i int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&i, 1) - 1
		if int(idx) >= len(bodies) {
			idx = int32(len(bodies) - 1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(bodies[idx]))
	}))
}

// TestLiveSettingsTriggerFiresOnlyOnRisingEdge covers the manual "Push
// jetzt auslösen" wiring: it must fire exactly once when TriggerPush
// newly becomes true, not again while it stays true (the evaluator is
// expected to reset it after serving it once, but a collector poll that
// happens to see "still true" — e.g. a retried request — must not
// re-trigger a second push for the same click).
func TestLiveSettingsTriggerFiresOnlyOnRisingEdge(t *testing.T) {
	srv := fakeConfigServer(t,
		`{"trigger_push": false}`,
		`{"trigger_push": true}`,
		`{"trigger_push": true}`,
		`{"trigger_push": false}`,
		`{"trigger_push": true}`,
	)
	defer srv.Close()

	live := newLiveSettings(filepath.Join(t.TempDir(), "cache.json"), srv.URL, "", srv.Client(), newCollectorStatus("test", "0.0.0-test", time.Now()))
	ctx := context.Background()

	assertNoTrigger := func(step string) {
		t.Helper()
		select {
		case <-live.triggerRequested():
			t.Errorf("%s: trigger fired unexpectedly", step)
		default:
		}
	}
	assertTrigger := func(step string) {
		t.Helper()
		select {
		case <-live.triggerRequested():
		default:
			t.Errorf("%s: expected trigger to fire", step)
		}
	}

	live.poll(ctx) // false
	assertNoTrigger("false")
	live.poll(ctx) // false -> true: rising edge
	assertTrigger("false->true")
	live.poll(ctx) // true -> true: must not re-fire
	assertNoTrigger("true->true")
	live.poll(ctx) // true -> false
	assertNoTrigger("true->false")
	live.poll(ctx) // false -> true: rising edge again
	assertTrigger("false->true again")
}

// TestConfirmedActiveIgnoresStaleCacheUntilPolled ensures a restarted
// collector does not resume frequent live-view pushing purely because a
// previous run's cached settings said it was active — only this run's own
// first successful poll may turn confirmedActive on, so a leftover cache
// from before the evaluator toggled the live view off again cannot cause
// an unwanted burst of pushes before that's found out.
func TestConfirmedActiveIgnoresStaleCacheUntilPolled(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	if err := settings.NewCache(cachePath).Save(settings.Settings{ReportIntervalSeconds: 5}); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	// A server that never answers stands in for "evaluator not reachable
	// yet this run", without needing to wait out a real timeout.
	blocked := make(chan struct{})
	defer close(blocked)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer srv.Close()

	live := newLiveSettings(cachePath, srv.URL, "", srv.Client(), newCollectorStatus("test", "0.0.0-test", time.Now()))
	// Age the collector past its startup live window, which would
	// otherwise keep the push on for its own, unrelated reason (see
	// confirmedActive) and mask what this test is about.
	live.startedAt = time.Now().Add(-startupLiveWindow - time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live.start(ctx)

	if active, _ := live.confirmedActive(); active {
		t.Fatal("confirmedActive should be false before this run's first poll succeeds, even with an active cached value")
	}
	if !live.settings().LiveViewActive() {
		t.Fatal("the cached value itself should still be loaded (e.g. for filter rules), just not trusted for live-view push yet")
	}

	live.mu.Lock()
	live.confirmed = true
	live.mu.Unlock()
	if active, interval := live.confirmedActive(); !active || interval != 5*time.Second {
		t.Errorf("confirmedActive after a successful poll = (%v, %v), want (true, 5s)", active, interval)
	}
}

// TestDeliverDueSendsOnlyDaysFullyOver is the fix for the real report
// this was built for: the once-a-day push had been sending whatever the
// buffer held for "today" — a calendar day that, at any hour before the
// next midnight, still has more telegrams coming. Whatever arrived after
// that early send got shipped the *next* night, one calendar day late,
// and conflicted with what had already been archived for it. A
// scheduled run (includeToday=false) must leave the day still in
// progress alone and only ever send days that are unambiguously over.
func TestDeliverDueSendsOnlyDaysFullyOver(t *testing.T) {
	var mu sync.Mutex
	var gotFinal []bool
	var gotDays []string
	reportSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Final   bool `json:"final"`
			Entries []struct {
				Day string `json:"day"`
			} `json:"entries"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		gotFinal = append(gotFinal, body.Final)
		for _, e := range body.Entries {
			gotDays = append(gotDays, e.Day)
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(struct {
			Accepted  int `json:"accepted"`
			Conflicts int `json:"conflicts"`
		}{Accepted: len(body.Entries)})
	}))
	defer reportSrv.Close()

	buffer, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer buffer.Close()

	today := telegram.DayOf(time.Now())
	yesterday := today.AddDays(-1)
	if err := buffer.Upsert(store.Entry{MeterID: "90000001", Day: yesterday, ReceivedAt: time.Now().Add(-time.Hour), RawHex: "aabb"}); err != nil {
		t.Fatalf("Upsert (yesterday): %v", err)
	}
	if err := buffer.Upsert(store.Entry{MeterID: "90000001", Day: today, ReceivedAt: time.Now(), RawHex: "ccdd"}); err != nil {
		t.Fatalf("Upsert (today): %v", err)
	}

	deliverDue(context.Background(), buffer, reportSrv.URL, "", reportSrv.Client(), filepath.Join(t.TempDir(), "backup.db"), false)

	mu.Lock()
	defer mu.Unlock()
	if len(gotDays) != 1 || gotDays[0] != string(yesterday) {
		t.Errorf("days sent = %v, want exactly [%s]", gotDays, yesterday)
	}
	if len(gotFinal) != 1 || !gotFinal[0] {
		t.Errorf("final flags = %v, want exactly [true]", gotFinal)
	}

	remaining, err := buffer.Days()
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(remaining) != 1 || remaining[0] != today {
		t.Errorf("buffer.Days() after deliverDue = %v, want exactly [%s] (today, still in progress)", remaining, today)
	}
}

// TestDeliverDueManualTriggerIncludesToday covers the other half: a
// manual "Push jetzt auslösen" (includeToday=true) is a deliberate
// request for the current reading right now, so — unlike a scheduled
// run — it must still be able to send today's still-open day, exactly
// as it always could before the fix above.
func TestDeliverDueManualTriggerIncludesToday(t *testing.T) {
	var mu sync.Mutex
	var gotDays []string
	reportSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Entries []struct {
				Day string `json:"day"`
			} `json:"entries"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		for _, e := range body.Entries {
			gotDays = append(gotDays, e.Day)
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(struct {
			Accepted  int `json:"accepted"`
			Conflicts int `json:"conflicts"`
		}{Accepted: len(body.Entries)})
	}))
	defer reportSrv.Close()

	buffer, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer buffer.Close()

	today := telegram.DayOf(time.Now())
	if err := buffer.Upsert(store.Entry{MeterID: "90000001", Day: today, ReceivedAt: time.Now(), RawHex: "ccdd"}); err != nil {
		t.Fatalf("Upsert (today): %v", err)
	}

	deliverDue(context.Background(), buffer, reportSrv.URL, "", reportSrv.Client(), filepath.Join(t.TempDir(), "backup.db"), true)

	mu.Lock()
	defer mu.Unlock()
	if len(gotDays) != 1 || gotDays[0] != string(today) {
		t.Errorf("days sent = %v, want exactly [%s] (today, via manual trigger)", gotDays, today)
	}

	remaining, err := buffer.Days()
	if err != nil {
		t.Fatalf("Days: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("buffer.Days() after a manual delivery = %v, want empty", remaining)
	}
}

// TestRunDailyLoopRecheckDoesNotDeliverSpuriously covers the fix for a
// real report: a collector's daily-push wait, once armed, did not notice
// a later change to the configured hour until it either fired at the
// stale time or the process restarted (see runDailyLoop's doc comment on
// dailyHourRecheckInterval). The fix periodically abandons the current
// wait to re-arm it from the live setting — this checks that doing so,
// on its own, must never itself count as a due delivery: with the
// configured hour always far in the future, several recheck ticks must
// produce zero report POSTs.
func TestRunDailyLoopRecheckDoesNotDeliverSpuriously(t *testing.T) {
	orig := dailyHourRecheckInterval
	dailyHourRecheckInterval = 20 * time.Millisecond
	defer func() { dailyHourRecheckInterval = orig }()

	var posts int32
	reportSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
	}))
	defer reportSrv.Close()

	// Always some hour that will not become due during this test,
	// regardless of when it happens to run.
	farHour := (time.Now().Hour() + 12) % 24
	if farHour == 0 {
		farHour = 12
	}
	configSrv := fakeConfigServer(t, `{"daily_push_hour": `+strconv.Itoa(farHour)+`}`)
	defer configSrv.Close()

	live := newLiveSettings(filepath.Join(t.TempDir(), "cache.json"), configSrv.URL, "", configSrv.Client(), newCollectorStatus("test", "0.0.0-test", time.Now()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live.start(ctx)

	buffer, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer buffer.Close()
	// A real pending entry, so that an incorrect deliverDue call from a
	// recheck tick would actually produce a detectable POST — an empty
	// buffer would silently produce zero POSTs either way and this test
	// would pass without checking anything.
	if err := buffer.Upsert(store.Entry{MeterID: "90000001", Day: "2026-08-21", ReceivedAt: time.Now(), RawHex: "aabb"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runDailyLoop(ctx, live, buffer, reportSrv.URL, "", reportSrv.Client(), filepath.Join(t.TempDir(), "backup.db"))
	}()

	time.Sleep(150 * time.Millisecond) // several recheck ticks at 20ms each
	cancel()
	<-done

	if got := atomic.LoadInt32(&posts); got != 0 {
		t.Errorf("report POSTs from recheck ticks alone = %d, want 0", got)
	}
}

// TestRunReportLoopSendsNothingWhileLiveViewInactive is the end-to-end
// check for the on/off toggle: with the live view off (the default), a
// buffered entry must not be reported at all; turning it on must start
// delivering it.
func TestRunReportLoopSendsNothingWhileLiveViewInactive(t *testing.T) {
	var posts int32
	reportSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		json.NewEncoder(w).Encode(struct {
			Accepted  int `json:"accepted"`
			Conflicts int `json:"conflicts"`
		}{Accepted: 1})
	}))
	defer reportSrv.Close()

	var active atomic.Bool
	configSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seconds := 0
		if active.Load() {
			seconds = 1
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			ReportIntervalSeconds int `json:"report_interval_seconds"`
			ConfigPollSeconds     int `json:"config_poll_seconds"`
		}{ReportIntervalSeconds: seconds, ConfigPollSeconds: 1})
	}))
	defer configSrv.Close()

	buffer, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer buffer.Close()
	if err := buffer.Upsert(store.Entry{MeterID: "90000001", Day: "2026-08-21", ReceivedAt: time.Now(), RawHex: "aabb"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	live := newLiveSettings(filepath.Join(t.TempDir(), "cache.json"), configSrv.URL, "", configSrv.Client(), newCollectorStatus("test", "0.0.0-test", time.Now()))
	// Past the startup live window, so this exercises the evaluator's own
	// on/off decision rather than the window's.
	live.startedAt = time.Now().Add(-startupLiveWindow - time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live.start(ctx)

	lastSent := &lastSentTime{t: time.Time{}} // zero time: everything in the buffer counts as "since"
	reportErr := &reportErrorTracker{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runReportLoop(ctx, live, buffer, reportSrv.URL, "", reportSrv.Client(), lastSent, reportErr)
	}()

	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt32(&posts); got != 0 {
		t.Fatalf("posts while inactive = %d, want 0", got)
	}

	active.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&posts) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&posts); got == 0 {
		t.Fatal("expected at least one report POST after activating the live view")
	}

	cancel()
	<-done
}
