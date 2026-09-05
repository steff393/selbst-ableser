// Package config loads the system's functional configuration and its
// secrets from two separate files (BETRIEB-02): a mistake in either is
// reported clearly at startup rather than silently replaced with a
// default, and installation-specific quantities (conversion factors,
// billing-period reset date, floor areas, thresholds) deliberately do not
// live here — those are master data (see internal/masterdata), not
// program configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is the functional configuration: addresses, paths, toggles.
// Nothing in here is a secret (see Secrets).
type Config struct {
	Evaluator Evaluator `json:"evaluator"`
	Collector Collector `json:"collector"`
	Notify    Notify    `json:"notify"`
	Legal     Legal     `json:"legal"`
	Logging   Logging   `json:"logging"`
}

type Evaluator struct {
	// Addr is the address to listen on; empty means DefaultAddr. The
	// -addr flag overrides it for a single run without changing the file.
	Addr string `json:"addr,omitempty"`

	// LookbackDays is FACH-01's backward-search window; 0 means the
	// billing package's own default.
	LookbackDays int `json:"lookback_days,omitempty"`

	// TrustProxy MUST be set explicitly before X-Forwarded-For is used to
	// identify a caller for rate limiting and logging (BETRIEB-06: the
	// distinction between "local" and "publicly reachable" must never be
	// implicit). Enabling it without an actual reverse proxy in front of
	// the evaluator lets any caller forge their own rate-limit identity.
	TrustProxy bool `json:"trust_proxy"`

	// AllowedHosts restricts which Host header values the evaluator
	// answers at all (BETRIEB-06); empty means unrestricted. Set this
	// once the evaluator is reachable from the internet.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
}

// DefaultAddr is what an installation listens on unless its config.json
// or the -addr flag says otherwise.
const DefaultAddr = ":8226"

// An installation is one directory holding these five files under these
// exact names — there is no way to rename one or move it out on its own,
// deliberately: they are only meaningful as a set (a backup restores all
// five together, and master data without its archive evaluates nothing),
// and every document describing this system names them literally. Which
// directory it is, is the one thing a caller chooses; everything below
// follows from it.
const (
	ConfigFileName     = "config.json"
	SecretsFileName    = "secrets.json"
	ArchiveFileName    = "archive.db"
	MasterDataFileName = "masterdata.enc"
	AuditFileName      = "audit.db"
)

// ConfigPath, SecretsPath, ArchivePath, MasterDataPath, and AuditPath
// name the five files of the installation in dir.
func ConfigPath(dir string) string  { return filepath.Join(dir, ConfigFileName) }
func SecretsPath(dir string) string { return filepath.Join(dir, SecretsFileName) }
func ArchivePath(dir string) string { return filepath.Join(dir, ArchiveFileName) }
func MasterDataPath(dir string) string {
	return filepath.Join(dir, MasterDataFileName)
}
func AuditPath(dir string) string { return filepath.Join(dir, AuditFileName) }

// Collector holds the operating parameters a saCollector fetches from
// this evaluator (GET /collector/config) rather than reading from its
// own local file — see the webapp package's collector-settings handlers
// for where an operator changes these, and internal/collector's absence
// from this module entirely for why nothing receiver- or transport-
// specific lives here anymore: that is saCollector's own, separate
// module now, and it is never told anything beyond what's listed here.
type Collector struct {
	// LiveViewUntil is when the frequent live-view push stops again,
	// as RFC3339. Empty means off, which is the default: a collector
	// otherwise reports once a day.
	//
	// An expiry rather than a plain on/off flag, because "on" is a
	// diagnostic state, not an operating one — it exists while somebody
	// is watching. Left as a flag, an installation switched on once
	// would push every few seconds for years, which is exactly the
	// constant background traffic and SD-card writing BETRIEB-09 asks to
	// avoid. Forgetting to switch it off is the normal case, so it
	// switches itself off (see LiveViewWindow).
	//
	// Kept separate from ReportIntervalSeconds (rather than using 0 there
	// as "off") so the configured interval survives being switched off
	// and back on.
	LiveViewUntil string `json:"live_view_until,omitempty"`

	// ReportIntervalSeconds is how often the live-view push happens while
	// the live view is on; irrelevant while it is off. 0 lets a collector
	// fall back to its own built-in default.
	ReportIntervalSeconds int `json:"report_interval_seconds,omitempty"`

	// DailyPushHour is the hour (0-23) the once-a-day durable push runs
	// at. 0 is indistinguishable from "unset" and falls back to the
	// built-in default (3) — an accepted simplification that means
	// midnight itself cannot be chosen explicitly (not a real
	// limitation: the collector never sends a day until it is fully
	// over regardless of which hour this is, see
	// collector/internal/settings.Settings.DailyHour).
	DailyPushHour int `json:"daily_push_hour,omitempty"`

	// IdleReconnectSeconds is how long a collector's receiver connection
	// may go without receiving anything before it is force-reconnected.
	// 0 lets a collector fall back to its own built-in default (120s).
	IdleReconnectSeconds int `json:"idle_reconnect_seconds,omitempty"`

	// ConfigPollSeconds is how often a collector re-fetches this
	// configuration. 0 lets a collector fall back to its own built-in
	// default (60s).
	ConfigPollSeconds int `json:"config_poll_seconds,omitempty"`

	// FilterRules discards telegrams before a collector ever buffers,
	// displays, or reports them (FUNK-05): some meters
	// alternate between telegram formats where only one is evaluable, and
	// the unwanted one would otherwise overwrite the useful one in the
	// day's compacted entry.
	FilterRules []FilterRuleConfig `json:"filter_rules,omitempty"`

	// TriggerPush asks a collector to perform one immediate, full push
	// (durable commit plus backup, exactly like the daily push) the next
	// time it fetches this configuration — set by "Push jetzt auslösen"
	// in the UI, reset to false by handleCollectorConfig itself the
	// moment it is served once, so it is a one-shot signal even though a
	// collector only notices it on its next poll. Never persisted to
	// disk: a restart clearing it is fine, nobody is waiting on a signal
	// across a restart.
	TriggerPush bool `json:"-"`
}

// FilterRuleConfig is one telegram-filtering rule: block a meter (or,
// with MeterID "*", every meter) whose raw hex begins with one of
// BlockedPrefixes.
type FilterRuleConfig struct {
	MeterID         string   `json:"meter_id"`
	BlockedPrefixes []string `json:"blocked_prefixes"`
}

type Notify struct {
	Enabled bool `json:"enabled"`

	// StartupNotification sends a short "the system is running again"
	// notice after a start. On by default, but switchable (BENACHR-05):
	// a supervisor restarting a crash-looping process would otherwise
	// generate unbounded mail.
	StartupNotification bool `json:"startup_notification"`

	// OperatorEmail receives the admin copy of every notification run
	// and failure alerts (BENACHR-03).
	OperatorEmail string `json:"operator_email"`

	// BaseURL is the link a monthly reminder points to (BENACHR-02: the
	// message itself never carries consumption data).
	BaseURL string `json:"base_url"`
}

// Legal is UI-12's two public notices (Impressum, Datenschutzerklärung).
// They live here, not in the encrypted master data, because they are, by
// construction, not secret — an Impressum exists to be shown to any
// visitor without logging in — and must be readable before an operator
// has ever unlocked anything, in particular right after every restart,
// which the vault (STAMM-04) never survives unlocked. Config.json is
// loaded unconditionally at startup, so these are available from the
// first request on, the same as the rest of this struct.
type Legal struct {
	ImprintText       string `json:"imprint_text,omitempty"`
	PrivacyPolicyText string `json:"privacy_policy_text,omitempty"`
}

type Logging struct {
	// Level is one of "debug", "info", "warn", "error". Default: "info" —
	// deliberately not "debug", since BETRIEB-09 asks for a sparse
	// default given the SD-card write cost of an ever-growing log.
	Level string `json:"level,omitempty"`
}

var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Load reads and validates the functional configuration at path. A
// present-but-invalid value is always an error; only an absent optional
// field gets its documented default.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	} else if !validLogLevels[c.Logging.Level] {
		return Config{}, fmt.Errorf("config: %s: logging.level %q is not one of debug/info/warn/error", path, c.Logging.Level)
	}

	// Every field has a documented default, so an absent evaluator section
	// — or an absent file altogether — is a valid, fully working
	// installation rather than something to complain about. Only a value
	// that is present and impossible is an error.
	if c.Evaluator.LookbackDays < 0 {
		return Config{}, fmt.Errorf("config: %s: evaluator.lookback_days must not be negative", path)
	}

	if c.Collector.ReportIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: %s: collector.report_interval_seconds must not be negative", path)
	}
	if c.Collector.DailyPushHour < 0 || c.Collector.DailyPushHour > 23 {
		return Config{}, fmt.Errorf("config: %s: collector.daily_push_hour must be between 0 and 23", path)
	}
	if c.Collector.IdleReconnectSeconds < 0 {
		return Config{}, fmt.Errorf("config: %s: collector.idle_reconnect_seconds must not be negative", path)
	}
	if c.Collector.ConfigPollSeconds < 0 {
		return Config{}, fmt.Errorf("config: %s: collector.config_poll_seconds must not be negative", path)
	}

	if c.Notify.Enabled && c.Notify.OperatorEmail == "" {
		return Config{}, fmt.Errorf("config: %s: notify.operator_email is required when notify.enabled is true (BENACHR-03)", path)
	}

	return c, nil
}

// LoadOrEmpty is Load, except a file that does not exist yet is not an
// error — it yields a zero-value Config instead. For callers that treat
// the config file as something they may create lazily on first write
// (the collector's local control page; the default-path template both
// subcommands write out on first start), not something that must already
// be provisioned.
func LoadOrEmpty(path string) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return cfg, nil
}

// LoadSecretsOrEmpty is LoadOrEmpty's counterpart for the secrets file.
func LoadSecretsOrEmpty(path string) (Secrets, error) {
	s, err := LoadSecrets(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Secrets{}, nil
		}
		return Secrets{}, err
	}
	return s, nil
}

// Save writes c to path as JSON, atomically (write a temp file, then
// rename it into place), so a concurrent reader of path never observes a
// partially written file. Used by the collector's local settings page to
// persist push configuration across restarts.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	return writeFileAtomic(path, data)
}

// SaveSecrets writes s to path as JSON, atomically. The directory holding
// path is expected to already have tight permissions (BETRIEB-02); this
// only guarantees the file itself is written 0600.
func SaveSecrets(path string, s Secrets) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("config: creating %s: %w", dir, err)
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}

// NotificationCheckInterval is how often a long-running process should
// check whether it is time to send the monthly reminder — deliberately
// much finer than "once a day at a fixed time", which would tie whether
// the reminder goes out at all to the process being alive at that one
// moment (BENACHR-01 requires a missed run to be caught up instead; see
// access.AuditLog.HasEvent for how that is made idempotent).
const NotificationCheckInterval = time.Hour

// LiveViewWindow is how long the live view stays on once switched on.
// Long enough to cover a working session at the installation, short
// enough that forgetting to switch it off costs one afternoon of extra
// traffic rather than years of it.
const LiveViewWindow = 12 * time.Hour

// LiveViewActiveAt reports whether the live-view push should be running
// at t, and until when. An unparseable value reads as off: a corrupted
// timestamp must not leave the push stuck on forever.
func (c Collector) LiveViewActiveAt(t time.Time) (active bool, until time.Time) {
	if c.LiveViewUntil == "" {
		return false, time.Time{}
	}
	deadline, err := time.Parse(time.RFC3339, c.LiveViewUntil)
	if err != nil {
		return false, time.Time{}
	}
	return t.Before(deadline), deadline
}

// WithLiveViewUntil returns c with the live view switched on until t, or
// off when t is the zero time.
func (c Collector) WithLiveViewUntil(t time.Time) Collector {
	if t.IsZero() {
		c.LiveViewUntil = ""
		return c
	}
	c.LiveViewUntil = t.Format(time.RFC3339)
	return c
}
