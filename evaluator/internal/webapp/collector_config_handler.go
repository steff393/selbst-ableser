package webapp

import (
	"encoding/json"
	"net/http"
	"time"
)

type collectorFilterRuleResponse struct {
	MeterID         string   `json:"meter_id"`
	BlockedPrefixes []string `json:"blocked_prefixes"`
}

// collectorReport mirrors settings.Report in the collector module. The two
// cannot share a type — the modules are separate by construction (ARCH-01)
// — so the JSON tags are the contract, written out in
// [07-collector.md §7.7](../../../docs/07-collector.md).
type collectorReport struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`

	Receiver struct {
		Connected bool      `json:"connected"`
		Port      string    `json:"port"`
		Since     time.Time `json:"since"`
	} `json:"receiver"`

	BackupMedium struct {
		Connected bool   `json:"connected"`
		Path      string `json:"path"`
	} `json:"backup_medium"`
}

// maxCollectorReport caps the status body. It describes one machine in a
// handful of short strings; anything larger is a caller doing something
// other than reporting status.
const maxCollectorReport = 8 << 10

// handleCollectorConfig answers a saCollector's "what should I be doing"
// question: report interval, daily-push hour, idle-reconnect threshold,
// how often to ask again, telegram filter rules, and whether a manual
// push was just requested — operator-managed here rather than in a file
// saCollector reads locally. Authenticated the same way as every other
// collector-facing endpoint (validCollectorAuth), including the
// no-secret loopback default a freshly started saCollector with no flags
// at all relies on.
func (a *App) handleCollectorConfig(w http.ResponseWriter, r *http.Request) {
	if !validCollectorAuth(r.Header.Get("Authorization"), a.PushSecret, r.RemoteAddr, a.TrustProxy) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	// The body is the collector's own status (see settings.Report in the
	// collector module). A malformed or absent one is not worth refusing
	// the settings over — a collector that cannot describe itself should
	// still learn its filter rules — so it is recorded as far as it
	// parsed, under whatever name came through.
	var report collectorReport
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectorReport)).Decode(&report)
	}
	a.markCollectorSeen(report)

	rules := make([]collectorFilterRuleResponse, 0, len(a.CollectorConfig.FilterRules))
	for _, r := range a.CollectorConfig.FilterRules {
		rules = append(rules, collectorFilterRuleResponse{MeterID: r.MeterID, BlockedPrefixes: r.BlockedPrefixes})
	}

	// Whether the live view should push at all is its own flag now,
	// evaluated fresh on every poll so it expires on its own without
	// anything having to run on a timer — ReportIntervalSeconds always
	// carries the actually configured interval alongside it, active or
	// not, instead of doubling as the on/off signal by being zeroed
	// while inactive. That conflation made a collector briefly out of
	// contact hard to tell apart from one genuinely told to stop: both
	// looked like "0". See config.Collector's own comment for why the
	// interval and the window are kept apart internally in the first
	// place.
	liveViewActive, _ := a.CollectorConfig.LiveViewActiveAt(a.now())

	triggerPush := a.CollectorConfig.TriggerPush
	a.CollectorConfig.TriggerPush = false // one-shot: consumed by being served once

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		ReportIntervalSeconds int                           `json:"report_interval_seconds"`
		LiveViewActive        bool                          `json:"live_view_active"`
		DailyPushHour         int                           `json:"daily_push_hour"`
		IdleReconnectSeconds  int                           `json:"idle_reconnect_seconds"`
		ConfigPollSeconds     int                           `json:"config_poll_seconds"`
		TriggerPush           bool                          `json:"trigger_push"`
		FilterRules           []collectorFilterRuleResponse `json:"filter_rules"`
	}{
		ReportIntervalSeconds: a.CollectorConfig.ReportIntervalSeconds,
		LiveViewActive:        liveViewActive,
		DailyPushHour:         a.CollectorConfig.DailyPushHour,
		IdleReconnectSeconds:  a.CollectorConfig.IdleReconnectSeconds,
		ConfigPollSeconds:     a.CollectorConfig.ConfigPollSeconds,
		TriggerPush:           triggerPush,
		FilterRules:           rules,
	})
}
