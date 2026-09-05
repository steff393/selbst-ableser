// Package notify sends email notifications to tenants and operators over a
// configurable SMTP connection.
package notify

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"selbst-ableser/internal/config"
)

// Mailer sends plain-text email over SMTP, configured entirely from
// config.SMTPCredentials (BENACHR-04: no hardcoded provider, credentials
// never in source).
type Mailer struct {
	creds config.SMTPCredentials
}

// NewMailer creates a Mailer from SMTP credentials loaded via
// config.LoadSecrets.
func NewMailer(creds config.SMTPCredentials) *Mailer {
	return &Mailer{creds: creds}
}

// Send delivers a plain-text message to one recipient.
func (m *Mailer) Send(to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", m.creds.Host, m.creds.Port)
	msg := buildMessage(m.creds.From, to, subject, body)

	var auth smtp.Auth
	if m.creds.Username != "" {
		auth = smtp.PlainAuth("", m.creds.Username, m.creds.Password, m.creds.Host)
	}

	switch m.creds.Encryption {
	case "tls":
		return sendTLS(addr, m.creds.Host, auth, m.creds.From, to, msg)
	case "none", "starttls", "":
		// smtp.SendMail itself opts into STARTTLS when the server offers
		// it and falls back to plaintext only if the server doesn't —
		// suitable for both "starttls" and a same-host "none" (e.g. a
		// local relay) configuration alike.
		return smtp.SendMail(addr, auth, m.creds.From, []string{to}, msg)
	default:
		return fmt.Errorf("notify: unknown encryption mode %q", m.creds.Encryption)
	}
}

func sendTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
