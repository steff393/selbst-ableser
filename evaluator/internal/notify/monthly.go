package notify

import (
	"fmt"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// Sender is the one method notify needs from a mailer, so tests can supply
// a fake instead of talking to a real SMTP server.
type Sender interface {
	Send(to, subject, body string) error
}

// MonthlyReminderResult summarizes one run of SendMonthlyReminders.
type MonthlyReminderResult struct {
	Sent   int
	Failed []string // unit IDs whose send failed

	// SentAddresses is a masked (see maskEmail) copy of each successful
	// send's recipient, in send order — for the operator's own Monatslauf
	// summary mail, so a glance confirms the run reached the expected
	// tenants without that summary becoming a full, forwardable mailing
	// list of every tenant's real address.
	SentAddresses []string
}

const reminderSubject = "Unterjährige Verbrauchsinformation"

func reminderBody(baseURL string) string {
	return "Sehr geehrter Mieter,\n\n" +
		"Ihre unterjährige Verbrauchsinformation für den letzten Monat steht Ihnen unter " +
		"folgendem Link zur Verfügung:\n" + baseURL + "\n\n" +
		"Viele Grüße\n" +
		"selbst-ableser – weil deine Daten dir gehören."
}

// SendMonthlyReminders sends the "a new UVI is available" notice
// (BENACHR-01) to every currently valid access grant with an email on
// file, then one summary copy to the operator (BENACHR-03). The message
// body is fixed text plus baseURL — nothing computed from a reading —
// which is what makes BENACHR-02 hold structurally rather than by care at
// each call site.
//
// Every call sends to every current access unconditionally — there is no
// per-unit "already sent this month" check here, deliberately: the
// scheduled call site (cmd/saEvaluator) only calls this within
// MonthlyReminderDue's one-hour window, which under normal operation the
// hourly check loop enters at most once, so nothing here needs to guard
// against a duplicate itself. The operator's manual "Manueller Versand"
// action calls this directly, any time, for exactly the same reason a
// deliberate resend needs no gate.
//
// Recording access.EventNotificationSent after each send is still done,
// but purely as the audit trail's own record — see weekly.go's
// monthlyReminderConfirmed, which reads it back only as a best-effort hint
// for the weekly status mail, never to decide whether to send here.
func SendMonthlyReminders(sender Sender, audit *access.AuditLog, md masterdata.MasterData, month string, today telegram.Day, baseURL, operatorEmail string) (MonthlyReminderResult, error) {
	var result MonthlyReminderResult

	for _, acc := range md.Accesses {
		if acc.Email == "" || !accessCurrentOn(acc, today) {
			continue
		}
		if err := sender.Send(acc.Email, reminderSubject, reminderBody(baseURL)); err != nil {
			result.Failed = append(result.Failed, acc.UnitID)
			continue
		}
		detail := fmt.Sprintf("unit %s month %s", acc.UnitID, month)
		if err := audit.Record(access.Event{Type: access.EventNotificationSent, Detail: detail}); err != nil {
			return result, err
		}
		result.Sent++
		result.SentAddresses = append(result.SentAddresses, maskEmail(acc.Email))
	}

	if operatorEmail != "" && (result.Sent > 0 || len(result.Failed) > 0) {
		summary := fmt.Sprintf(
			"Monatslauf %s: %d versendet, %d fehlgeschlagen%s.",
			month, result.Sent, len(result.Failed), failedSuffix(result.Failed),
		)
		if len(result.SentAddresses) > 0 {
			summary += "\n\nVersendet an: " + strings.Join(result.SentAddresses, ", ")
		}
		// Best-effort: a failed admin copy must not undo the tenant
		// sends already recorded above, and BENACHR-03's own failure
		// alert (see weekly.go) is the backstop if mail is down entirely.
		_ = sender.Send(operatorEmail, "selbst-ableser: Monatslauf "+month, summary)
	}

	return result, nil
}

// maskEmail partially obscures an address for the operator's Monatslauf
// summary: enough of the local part and domain survive to recognize which
// tenant it was at a glance (e.g. "erika.musterfrau@example.com" becomes
// "erikXXX@exXXX.com"), without the summary spelling out every tenant's
// real address in full — a forwarded or archived copy of that mail is then
// far less useful as a ready-made mailing list.
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at+1:]

	dot := strings.IndexByte(domain, '.')
	if dot <= 0 {
		return maskKeepingPrefix(local, 4) + "@" + maskKeepingPrefix(domain, 2)
	}
	return maskKeepingPrefix(local, 4) + "@" + maskKeepingPrefix(domain[:dot], 2) + domain[dot:]
}

// maskKeepingPrefix keeps s's first n bytes (or all of it, if shorter) and
// replaces the rest with a fixed "XXX" — a fixed-length mask rather than
// one scaled to the original length, so the mask itself never leaks how
// long the real value was.
func maskKeepingPrefix(s string, n int) string {
	if len(s) <= n {
		return s + "XXX"
	}
	return s[:n] + "XXX"
}

func failedSuffix(failed []string) string {
	if len(failed) == 0 {
		return ""
	}
	return " (Wohnungen: " + strings.Join(failed, ", ") + ")"
}

func accessCurrentOn(a masterdata.Access, day telegram.Day) bool {
	if day.Before(a.Start) {
		return false
	}
	if a.End != nil && a.End.Before(day) {
		return false
	}
	return true
}
