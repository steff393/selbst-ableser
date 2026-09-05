// Package settings fetches the collector's operating parameters from the
// evaluator (report interval, telegram filter rules) instead of reading
// them from a locally maintained configuration file. The evaluator is the
// single place an operator manages these, even across several collectors;
// see Fetch for the one exception every collector still needs a fixed
// answer to before it can even ask: where the evaluator is and how to
// authenticate to it.
//
// A Cache keeps the last successfully fetched Settings on disk so a
// collector that starts (or briefly loses contact) while the evaluator is
// unreachable keeps operating on the last known values instead of
// stalling — this is a cache the collector maintains for itself, not a
// configuration file an operator is meant to edit.
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FilterRule discards telegrams for a meter (or, with MeterID "*", every
// meter) whose raw hex begins with one of BlockedPrefixes.
type FilterRule struct {
	MeterID         string   `json:"meter_id"`
	BlockedPrefixes []string `json:"blocked_prefixes"`
}

// Settings is everything the evaluator hands back to a collector that
// authenticates to it.
type Settings struct {
	// ReportIntervalSeconds governs the frequent live-view push. <= 0
	// means the live view is switched off entirely — no push happens at
	// all, not even an infrequent one (the evaluator computes this value
	// from its own separate enabled/interval pair; see its collector
	// settings page for why those are kept apart there).
	ReportIntervalSeconds int `json:"report_interval_seconds"`

	// DailyPushHour is the hour (0-23) the once-a-day durable push runs
	// at. <= 0 or > 23 falls back to the built-in default (3).
	DailyPushHour int `json:"daily_push_hour"`

	// IdleReconnectSeconds is how long the receiver connection may go
	// without receiving anything before it is force-reconnected (see
	// internal/receiver's watchConnection). <= 0 falls back to the
	// built-in default (120s).
	IdleReconnectSeconds int `json:"idle_reconnect_seconds"`

	// ConfigPollSeconds is how often this Settings value itself is
	// re-fetched — independent of ReportIntervalSeconds above, and also
	// how promptly TriggerPush (below) is noticed. <= 0 falls back to the
	// built-in default (60s).
	ConfigPollSeconds int `json:"config_poll_seconds"`

	// TriggerPush asks for one immediate, full push (durable commit plus
	// backup, exactly like the daily push) — "Push jetzt auslösen" in the
	// evaluator's UI. The evaluator resets this to false itself the
	// moment it serves it once, so it is a one-shot signal even though
	// this collector only notices it on its next poll.
	TriggerPush bool `json:"trigger_push"`

	FilterRules []FilterRule `json:"filter_rules"`
}

// LiveViewActive reports whether the frequent live-view push should run
// at all.
func (s Settings) LiveViewActive() bool {
	return s.ReportIntervalSeconds > 0
}

// ReportInterval is only meaningful while LiveViewActive is true; callers
// should check that first rather than rely on this returning zero.
func (s Settings) ReportInterval() time.Duration {
	if s.ReportIntervalSeconds <= 0 {
		return time.Second
	}
	return time.Duration(s.ReportIntervalSeconds) * time.Second
}

// DailyHour is the configured daily-push hour, or the built-in default
// (3) for an unset or out-of-range value.
func (s Settings) DailyHour() int {
	// <= 0 rather than < 0: an unconfigured field is indistinguishable
	// from an explicit "00:00", so midnight itself cannot be chosen —
	// not a real limitation in practice, since deliverDue (cmd/saCollector)
	// never sends a day until it is fully over regardless of which hour
	// this is; the hour only controls how soon after midnight that
	// happens. 3 leaves a comfortable margin for the last telegram of a
	// day to be processed while still delivering well before the next
	// working day.
	if s.DailyPushHour <= 0 || s.DailyPushHour > 23 {
		return 3
	}
	return s.DailyPushHour
}

// IdleReconnect is the configured idle-reconnect threshold, or the
// built-in default (120s) for an unset or non-positive value.
func (s Settings) IdleReconnect() time.Duration {
	if s.IdleReconnectSeconds <= 0 {
		return 120 * time.Second
	}
	return time.Duration(s.IdleReconnectSeconds) * time.Second
}

// ConfigPollInterval is the configured settings-poll interval, or the
// built-in default (60s) for an unset or non-positive value.
func (s Settings) ConfigPollInterval() time.Duration {
	if s.ConfigPollSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(s.ConfigPollSeconds) * time.Second
}

// Report is what a collector says about itself on every poll — see
// cmd/saCollector's collectorStatus, which assembles it, and the
// evaluator's Collector page, which is the only consumer.
type Report struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`

	Receiver     ReceiverStatus     `json:"receiver"`
	BackupMedium BackupMediumStatus `json:"backup_medium"`
}

// ReceiverStatus says whether a wM-Bus receiver is attached right now.
// Port is its device path (`/dev/ttyUSB0`, `COM3`), Since when the
// current unbroken connection began; both are zero while disconnected.
type ReceiverStatus struct {
	Connected bool      `json:"connected"`
	Port      string    `json:"port,omitempty"`
	Since     time.Time `json:"since,omitempty"`
}

// BackupMediumStatus says whether the daily backup would find somewhere
// to write (DATEN-06). Path is the mount point or drive letter it was
// detected at.
type BackupMediumStatus struct {
	Connected bool   `json:"connected"`
	Path      string `json:"path,omitempty"`
}

// Fetch asks the evaluator at baseURL for the current Settings, over
// POST /collector/config, sending report as the request body. secret is
// sent the same way as every other collector-to-evaluator call (ARCH-04's
// shared transfer secret); an empty secret is only accepted at all if the
// evaluator also has none configured and the request arrives over
// loopback — the evaluator decides that, not this client.
//
// POST rather than GET because this call carries a body now. It stays one
// call rather than two: the poll already runs every 60 seconds whether or
// not telegrams flow, which is exactly the cadence a status display wants,
// and a second endpoint would need its own authentication, its own
// failure handling and its own reason to exist.
func Fetch(ctx context.Context, client *http.Client, baseURL, secret string, report Report) (Settings, error) {
	body, err := json.Marshal(report)
	if err != nil {
		return Settings{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/collector/config", bytes.NewReader(body))
	if err != nil {
		return Settings{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Settings{}, fmt.Errorf("settings: fetching config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Settings{}, fmt.Errorf("settings: evaluator returned %s", resp.Status)
	}

	var s Settings
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("settings: decoding config: %w", err)
	}
	return s, nil
}

// Cache persists the last known-good Settings to a local file, so a
// failed Fetch has something to fall back on.
type Cache struct {
	path string
}

func NewCache(path string) *Cache {
	return &Cache{path: path}
}

// Load reads the cached Settings, if any. found is false the first time a
// collector ever runs (nothing cached yet), not an error. TriggerPush is
// always forced false — see Save for why it must never survive a
// restart, and this guards against a cache file written by an older
// build that did not yet strip it.
func (c *Cache) Load() (s Settings, found bool, err error) {
	data, err := os.ReadFile(c.path)
	if os.IsNotExist(err) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, false, err
	}
	s.TriggerPush = false
	return s, true, nil
}

// Save writes s to the cache file atomically (temp file, then rename), so
// a concurrent reader never observes a half-written file.
//
// TriggerPush is always written as false, regardless of s: it is a
// one-shot pulse ("push jetzt auslösen" newly requested), not state, and
// the evaluator already resets its own copy the moment it serves it once
// (see collector_settings_handlers.go). Caching it as true would let a
// stale true survive on disk across a restart or a lost connection; the
// rising-edge check in cmd/saCollector compares a freshly fetched value
// against this cached one, so a stale true would make a genuinely new
// request right after reconnecting look like "no change" and get
// silently dropped.
func (c *Cache) Save(s Settings) error {
	s.TriggerPush = false
	if dir := filepath.Dir(c.path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("settings: creating %s: %w", dir, err)
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
