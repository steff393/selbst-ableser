package webapp

import (
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/config"
)

// TestNotifySettingsDefaultToGMX: the common case should be one field to
// fill in (the password), not five.
func TestNotifySettingsDefaultToGMX(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)

	body := getAuditBody(t, client, srv.URL+"/operator/notify")
	for _, want := range []string{"mail.gmx.net", "587", "STARTTLS"} {
		if !strings.Contains(body, want) {
			t.Errorf("the form should be pre-filled with %q, got: %s", want, body)
		}
	}
}

// TestNotifySettingsSaveSplitsConfigAndSecrets: what to send belongs in
// the config file, how to reach the server in the secrets file — one form
// for the operator, two files on disk (BETRIEB-02).
func TestNotifySettingsSaveSplitsConfigAndSecrets(t *testing.T) {
	app, _ := newTestApp(t)
	dir := t.TempDir()
	app.ConfigPath = filepath.Join(dir, "config.json")
	app.SecretsPath = filepath.Join(dir, "secrets.json")

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	postForm(t, client, srv.URL+"/operator/notify", map[string]string{
		"csrf_token":     sess.CSRFToken,
		"enabled":        "on",
		"operator_email": "betreiber@example.org",
		"base_url":       "https://example.org",
		"host":           "mail.gmx.net",
		"port":           "587",
		"encryption":     "starttls",
		"username":       "konto@gmx.de",
		"password":       "geheim",
		"from":           "konto@gmx.de",
	})

	cfg, err := config.LoadOrEmpty(app.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if !cfg.Notify.Enabled || cfg.Notify.OperatorEmail != "betreiber@example.org" {
		t.Errorf("notify settings not stored in the config file: %+v", cfg.Notify)
	}
	// Credentials must not be in the functional config file.
	raw, err := readFileString(app.ConfigPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if strings.Contains(raw, "geheim") {
		t.Error("the SMTP password must not be written to the config file")
	}

	secrets, err := config.LoadSecretsOrEmpty(app.SecretsPath)
	if err != nil {
		t.Fatalf("LoadSecretsOrEmpty: %v", err)
	}
	if secrets.SMTP.Host != "mail.gmx.net" || secrets.SMTP.Password != "geheim" {
		t.Errorf("SMTP credentials not stored in the secrets file: %+v", secrets.SMTP)
	}
}

// An empty password field must leave the stored one alone, so editing an
// unrelated setting does not silently clear it.
func TestNotifySettingsKeepPasswordWhenBlank(t *testing.T) {
	app, _ := newTestApp(t)
	app.SMTP = config.SMTPCredentials{Host: "mail.gmx.net", Port: 587, Encryption: "starttls", Password: "bleibt"}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	postForm(t, client, srv.URL+"/operator/notify", map[string]string{
		"csrf_token":     sess.CSRFToken,
		"operator_email": "betreiber@example.org",
		"host":           "mail.gmx.net",
		"port":           "587",
		"encryption":     "starttls",
		"password":       "",
	})

	if app.SMTP.Password != "bleibt" {
		t.Errorf("Password = %q, want the previously stored one", app.SMTP.Password)
	}
}

func TestNotifySettingsRejectEnablingWithoutAnAddress(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	postForm(t, client, srv.URL+"/operator/notify", map[string]string{
		"csrf_token": sess.CSRFToken,
		"enabled":    "on",
		"encryption": "starttls",
	})

	if app.NotifyConfig.Enabled {
		t.Error("enabling the send without an operator address should be rejected")
	}
}
