package notify

import (
	"fmt"
	"strings"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// WeeklyStatusCounts is BENACHR-03's weekly summary: how many of the
// installation's meters fall into each of three states, and whether this
// month's UVI reminder looks confirmed sent. A fixed-shape count rather
// than an itemized list of the meters/units involved, so the report reads
// as a small, constant-size dashboard regardless of how many meters or
// tenants are configured.
type WeeklyStatusCounts struct {
	// Locked is true when the vault has not been unlocked since the last
	// restart — the counts below are then meaningless, since without
	// master data there is no way to know which meters even exist.
	Locked bool

	NeverReceived int
	Silent        int
	OK            int

	// MonthlyReminderConfirmed is true if the audit log records this
	// month's UVI reminder having gone out to every currently valid
	// access with an email on file — vacuously true if there is none, or
	// if audit is nil. See monthlyReminderConfirmed's own doc comment for
	// the caveats a soft, best-effort hint like this has.
	MonthlyReminderConfirmed bool
}

// computeWeeklyStatus inspects the archive, master data, and audit log for
// what BENACHR-03 asks the weekly status to report. It works even while
// the vault is locked: in that case it can only report the locked state
// itself, since everything else needs to know which meters and accesses
// are even configured.
//
// Feeds only SendWeeklyStatus. There used to also be an immediate,
// unthrottled "Störungsmeldung" the moment something was found wrong —
// removed deliberately: with no per-day dedup like the weekly/monthly
// mails have, it re-sent every hour for as long as a problem persisted,
// not just once. The locked-state case is already signaled by the startup
// notification (every restart begins locked); a silent meter surfaces,
// throttled, in the weekly status instead.
func computeWeeklyStatus(store *archive.Store, vault *masterdata.Vault, audit *access.AuditLog, today telegram.Day, silentThresholdDays int) (WeeklyStatusCounts, error) {
	md, unlocked := vault.Get()
	if !unlocked {
		return WeeklyStatusCounts{Locked: true}, nil
	}

	counts := WeeklyStatusCounts{MonthlyReminderConfirmed: true}
	for _, m := range md.Meters {
		if m.RemovedAt != nil {
			continue // no longer expected to send
		}
		last, found, err := store.LastSeen(m.Number)
		if err != nil {
			return WeeklyStatusCounts{}, err
		}
		if !found {
			counts.NeverReceived++
			continue
		}
		if days := last.DaysUntil(today); days >= silentThresholdDays {
			counts.Silent++
			continue
		}
		counts.OK++
	}
	if audit != nil {
		counts.MonthlyReminderConfirmed = monthlyReminderConfirmed(md, audit, today)
	}

	return counts, nil
}

// monthlyReminderConfirmed looks for an audit-log record that this month's
// tenant reminder (BENACHR-01, see monthly.go) went out to every currently
// valid access with an email — true if there is no such access at all,
// vacuously. This is a soft, best-effort hint, not a guarantee, and
// deliberately triggers no automatic resend of its own — see schedule.go's
// note on why sending itself no longer reads the audit log at all. Two
// things can make this report false without anything actually being
// wrong: an access that only became current after this month's run, and
// an audit log that was cleared or restored from an older backup
// (BETRIEB-01/BETRIEB-07: audit.db is meant to stay freely deletable).
// Either way, the honest response is a hint the operator can act on by
// hand (Benachrichtigungen page, "Manueller Versand"), not a second
// automatic send that might really be a duplicate.
func monthlyReminderConfirmed(md masterdata.MasterData, audit *access.AuditLog, today telegram.Day) bool {
	month := string(today)[:7]
	for _, acc := range md.Accesses {
		if acc.Email == "" || !accessCurrentOn(acc, today) {
			continue
		}
		detail := fmt.Sprintf("unit %s month %s", acc.UnitID, month)
		found, err := audit.HasEvent(access.EventNotificationSent, detail)
		if err != nil || !found {
			// A read failure is treated the same as "not confirmed" —
			// this is only a hint, so erring toward a false alarm is
			// safer than erring toward silence.
			return false
		}
	}
	return true
}

// weeklyStatusBody renders c as the fixed-shape summary BENACHR-03 asks
// for.
func weeklyStatusBody(c WeeklyStatusCounts) string {
	if c.Locked {
		return "Stammdaten sind gesperrt — seit dem letzten Neustart wurde nicht entsperrt; " +
			"Nutzer sehen bis dahin keine Auswertung.\n"
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%d Zähler: Nie empfangen\n", c.NeverReceived)
	fmt.Fprintf(&body, "%d Zähler: Seit längerem stumm\n", c.Silent)
	fmt.Fprintf(&body, "%d Zähler: In Ordnung\n", c.OK)
	body.WriteString("\n")
	if c.MonthlyReminderConfirmed {
		body.WriteString("Die monatliche Mail (UVI) wurde versendet.\n")
	} else {
		body.WriteString("Die monatliche Mail (UVI) wurde möglicherweise noch nicht versendet. " +
			"Bitte prüfen und ggf. manuell senden.\n")
	}
	return body.String()
}

// SendWeeklyStatus is the regular Monday report (see WeeklyStatusDue): it
// goes out whether or not anything is wrong, so a quiet week reads as
// "the system is fine" rather than "the system is off". Recorded in the
// audit log under the week it covers afterward, purely as that log's own
// evidentiary trail — nothing reads it back to decide whether to send (see
// schedule.go's note on why), so calling this twice within the same hour
// really does send twice; the hourly check loop calling it is what keeps
// that from happening in practice.
//
// force skips the WeeklyStatusDue gate — the operator's manual "send now"
// action (Benachrichtigungen page) always sends when force is true,
// regardless of what day or hour it is.
func SendWeeklyStatus(sender Sender, audit *access.AuditLog, store *archive.Store, vault *masterdata.Vault, now time.Time, silentThresholdDays int, operatorEmail string, force bool) (bool, error) {
	if operatorEmail == "" {
		return false, nil
	}
	if !force && !WeeklyStatusDue(now) {
		return false, nil
	}

	counts, err := computeWeeklyStatus(store, vault, audit, telegram.DayOf(now), silentThresholdDays)
	if err != nil {
		return false, err
	}

	if err := sender.Send(operatorEmail, "selbst-ableser: Wöchentlicher Status", weeklyStatusBody(counts)); err != nil {
		return false, err
	}
	if audit != nil {
		if err := audit.Record(access.Event{Type: access.EventNotificationSent, At: now, Actor: "system", Detail: WeeklyStatusKey(now)}); err != nil {
			return true, err
		}
	}
	return true, nil
}
