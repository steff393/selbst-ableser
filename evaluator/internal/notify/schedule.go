package notify

import (
	"fmt"
	"time"
)

// SendHour is the time of day both scheduled mails go out at: late enough
// that a device switched on in the morning has reported, early enough to
// still be the same working day.
const SendHour = 10

// Both schedules are due only within the one hour they name — not "from
// that moment onwards" the way an earlier version of this file had it.
// That earlier version caught up a run missed at the exact moment by
// treating "due" as open-ended and relying on the audit log (via
// access.AuditLog.HasEvent) to stop a caught-up mail from also being a
// duplicate one. audit.db is meant to stay a freely deletable record
// (BETRIEB-01/BETRIEB-07) rather than something correctness depends on, so
// nothing here reads it to decide whether to send anymore — see monthly.go
// and weekly.go. The real cost of the narrower window: a run genuinely
// missed during its one hour (device off, crash loop) is not caught up
// automatically. What replaces catching up is monthlyReminderConfirmed's
// own read of the audit log (weekly.go) — a soft, best-effort hint in the
// next weekly status mail that a month looks unconfirmed, which the
// operator can act on by hand from the Benachrichtigungen page's "Manueller
// Versand".

// WeeklyStatusKey names the calendar week t falls in, e.g. "2026-W34" —
// purely for the audit trail SendWeeklyStatus records under, not read back
// by anything.
func WeeklyStatusKey(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("operator status %04d-W%02d", year, week)
}

// WeeklyStatusDue reports whether this week's operator status mail should
// be sent at t: only within the sending hour on Monday.
func WeeklyStatusDue(t time.Time) bool {
	return t.Weekday() == time.Monday && t.Hour() == SendHour
}

// MonthlyReminderDue reports whether this month's tenant reminder should
// be sent at t: only within the sending hour on the first of the month.
func MonthlyReminderDue(t time.Time) bool {
	return t.Day() == 1 && t.Hour() == SendHour
}
