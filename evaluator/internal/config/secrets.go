package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Secrets holds everything BETRIEB-02 forbids keeping in the functional
// configuration file: credentials, and explicitly "Übertragungsgeheimnisse"
// (transfer secrets) like PushSecret. It is meant to live in its own file
// with tighter filesystem permissions, separate from Config.
type Secrets struct {
	SMTP SMTPCredentials `json:"smtp"`

	// PushSecret authenticates the collector's live-view push to the
	// evaluator (ARCH-04): generated on the evaluator side, copied here
	// manually on both ends. The same field name is used in both the
	// collector's and the evaluator's secrets file; only its value needs
	// to match.
	PushSecret string `json:"push_secret"`
}

// SMTPCredentials configures outgoing mail universally (BENACHR-04): any
// host, rather than one provider wired into the source.
type SMTPCredentials struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Encryption string `json:"encryption"` // "none", "starttls", or "tls"
	Username   string `json:"username"`
	Password   string `json:"password"`
	From       string `json:"from"`
}

var validEncryption = map[string]bool{"none": true, "starttls": true, "tls": true}

// LoadSecrets reads and validates the secrets file at path.
func LoadSecrets(path string) (Secrets, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Secrets{}, err
	}
	var s Secrets
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secrets{}, fmt.Errorf("config: %s: %w", path, err)
	}

	if s.SMTP.Encryption == "" {
		s.SMTP.Encryption = "starttls"
	} else if !validEncryption[s.SMTP.Encryption] {
		return Secrets{}, fmt.Errorf("config: %s: smtp.encryption %q is not one of none/starttls/tls", path, s.SMTP.Encryption)
	}
	// A secrets file may exist only for PushSecret (the collector has no
	// use for SMTP at all), so an entirely absent smtp block is fine; a
	// partially filled one almost certainly is not.
	smtpConfigured := s.SMTP.Host != "" || s.SMTP.Port != 0 || s.SMTP.From != ""
	if smtpConfigured && (s.SMTP.Host == "" || s.SMTP.Port == 0 || s.SMTP.From == "") {
		return Secrets{}, fmt.Errorf("config: %s: smtp.host, smtp.port, and smtp.from must all be set together, or all left out", path)
	}

	return s, nil
}

// RequireSMTP returns an error if s has no usable SMTP configuration.
// Callers that are about to send mail (rather than just, say, use
// PushSecret) should call this after LoadSecrets to get a clear startup
// error instead of a confusing failure the first time a message is sent.
func (s Secrets) RequireSMTP() error {
	if s.SMTP.Host == "" || s.SMTP.Port == 0 || s.SMTP.From == "" {
		return fmt.Errorf("config: notify.enabled is true but no smtp configuration was found in the secrets file")
	}
	return nil
}
