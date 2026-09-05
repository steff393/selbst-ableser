package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeTestFile(t, `{
		"evaluator": {"addr": ":8080", "allowed_hosts": ["app.example"]}
	}`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Logging.Level != "info" {
		t.Errorf("Logging.Level default = %q, want info", c.Logging.Level)
	}
	if c.Evaluator.TrustProxy {
		t.Error("TrustProxy should default to false")
	}
}

// TestLoadEmptyConfigIsFine: every evaluator field has a documented
// default, so an empty — or entirely absent — config file describes a
// working installation rather than an error. The installation's identity
// is its directory, not this file.
func TestLoadEmptyConfigIsFine(t *testing.T) {
	path := writeTestFile(t, `{}`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Evaluator.Addr != "" {
		t.Errorf("Addr = %q, want empty (DefaultAddr applies at startup)", c.Evaluator.Addr)
	}

	missing, err := LoadOrEmpty(filepath.Join(t.TempDir(), "not-written-yet.json"))
	if err != nil {
		t.Fatalf("LoadOrEmpty on a missing file: %v", err)
	}
	if len(missing.Evaluator.AllowedHosts) != 0 {
		t.Error("a missing config file must read as unrestricted, not as a configured empty list")
	}
}

// TestInstallationFileNames pins the layout every document, backup and
// restore relies on: one directory, five fixed names, no way to redirect
// a single file elsewhere.
func TestInstallationFileNames(t *testing.T) {
	dir := filepath.Join("var", "lib", "selbst-ableser")
	cases := []struct {
		got, want string
	}{
		{ConfigPath(dir), filepath.Join(dir, "config.json")},
		{SecretsPath(dir), filepath.Join(dir, "secrets.json")},
		{ArchivePath(dir), filepath.Join(dir, "archive.db")},
		{MasterDataPath(dir), filepath.Join(dir, "masterdata.enc")},
		{AuditPath(dir), filepath.Join(dir, "audit.db")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("path = %q, want %q", c.got, c.want)
		}
	}
}

func TestLoadCollectorOnlyConfigIsFine(t *testing.T) {
	path := writeTestFile(t, `{
		"collector": {"report_interval_seconds": 15}
	}`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Collector.ReportIntervalSeconds != 15 {
		t.Errorf("Collector fields not loaded correctly: %+v", c.Collector)
	}
	if c.Evaluator.Addr != "" {
		t.Errorf("Evaluator.Addr should stay empty for a collector-only config, got %q", c.Evaluator.Addr)
	}
}

func TestLoadRejectsOutOfRangeDailyPushHour(t *testing.T) {
	for _, hour := range []int{-1, 24, 100} {
		path := writeTestFile(t, fmt.Sprintf(`{"collector": {"daily_push_hour": %d}}`, hour))
		if _, err := Load(path); err == nil {
			t.Errorf("Load with daily_push_hour=%d should have been rejected", hour)
		}
	}
}

func TestLoadRejectsNegativeCollectorSeconds(t *testing.T) {
	cases := []string{
		`{"collector": {"idle_reconnect_seconds": -1}}`,
		`{"collector": {"config_poll_seconds": -1}}`,
	}
	for _, body := range cases {
		path := writeTestFile(t, body)
		if _, err := Load(path); err == nil {
			t.Errorf("Load with %s should have been rejected", body)
		}
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Config{Collector: Collector{ReportIntervalSeconds: 20}}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Collector, c.Collector) {
		t.Errorf("round-tripped Collector = %+v, want %+v", got.Collector, c.Collector)
	}
}

func TestSaveThenLoadRoundTripsFilterRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Config{Collector: Collector{
		FilterRules: []FilterRuleConfig{{MeterID: "90000001", BlockedPrefixes: []string{"aa", "bb"}}},
	}}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Collector.FilterRules, c.Collector.FilterRules) {
		t.Errorf("round-tripped FilterRules = %+v, want %+v", got.Collector.FilterRules, c.Collector.FilterRules)
	}
}

func TestSaveSecretsThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s := Secrets{PushSecret: "sh-sh-secret"}
	if err := SaveSecrets(path, s); err != nil {
		t.Fatalf("SaveSecrets: %v", err)
	}
	got, err := LoadSecrets(path)
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	if got.PushSecret != s.PushSecret {
		t.Errorf("round-tripped PushSecret = %q, want %q", got.PushSecret, s.PushSecret)
	}
}

func TestLoadInvalidLogLevelIsRejectedNotDefaulted(t *testing.T) {
	path := writeTestFile(t, `{
		"evaluator": {"addr": ":8080"},
		"logging": {"level": "verbose"}
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an invalid (not just defaulted) log level")
	}
}

func TestLoadNotifyEnabledRequiresOperatorEmail(t *testing.T) {
	path := writeTestFile(t, `{
		"evaluator": {"addr": ":8080"},
		"notify": {"enabled": true}
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error: notify.enabled without an operator_email")
	}
}

func TestLoadSecretsValid(t *testing.T) {
	path := writeTestFile(t, `{"smtp": {"host": "smtp.example.com", "port": 587, "from": "no-reply@example.com"}}`)
	s, err := LoadSecrets(path)
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	if s.SMTP.Encryption != "starttls" {
		t.Errorf("Encryption default = %q, want starttls", s.SMTP.Encryption)
	}
}

func TestLoadSecretsInvalidEncryption(t *testing.T) {
	path := writeTestFile(t, `{"smtp": {"host": "smtp.example.com", "port": 587, "from": "a@b.c", "encryption": "rot13"}}`)
	if _, err := LoadSecrets(path); err == nil {
		t.Fatal("expected an error for an unknown encryption value")
	}
}

func TestLoadSecretsMissingRequired(t *testing.T) {
	path := writeTestFile(t, `{"smtp": {"host": "smtp.example.com"}}`)
	if _, err := LoadSecrets(path); err == nil {
		t.Fatal("expected an error for missing port/from")
	}
}

func TestLoadSecretsPushOnlyIsFine(t *testing.T) {
	// A secrets file used only for the collector's push secret, with no
	// SMTP block at all, must load without complaint — the collector has
	// no use for SMTP.
	path := writeTestFile(t, `{"push_secret": "abc123"}`)
	s, err := LoadSecrets(path)
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	if s.PushSecret != "abc123" {
		t.Errorf("PushSecret = %q, want abc123", s.PushSecret)
	}
	if err := s.RequireSMTP(); err == nil {
		t.Error("RequireSMTP should fail when no smtp block was configured")
	}
}

func TestRequireSMTPPassesWhenConfigured(t *testing.T) {
	path := writeTestFile(t, `{"smtp": {"host": "smtp.example.com", "port": 587, "from": "a@b.c"}}`)
	s, err := LoadSecrets(path)
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	if err := s.RequireSMTP(); err != nil {
		t.Errorf("RequireSMTP: %v", err)
	}
}
