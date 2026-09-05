package webapp

import (
	"net/http"
	"strconv"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/telegram"
)

// auditEventOrder is the security log's filter dropdown order: the
// authentication events first (what an operator checks the log for most
// often), then the changes, then the routine machine traffic.
var auditEventOrder = []access.EventType{
	access.EventLoginSuccess,
	access.EventLoginFailure,
	access.EventLogout,
	access.EventUnlockAttempt,
	access.EventMasterDataChange,
	access.EventAccessChange,
	access.EventArchiveDeleted,
	access.EventArchiveCompressed,
	access.EventArchiveCorrected,
	access.EventDataIngested,
	access.EventNotificationSent,
	access.EventServiceRestart,
}

var auditEventLabels = map[access.EventType]string{
	access.EventLoginSuccess:      "Anmeldung erfolgreich",
	access.EventLoginFailure:      "Anmeldung fehlgeschlagen",
	access.EventLogout:            "Abmeldung",
	access.EventUnlockAttempt:     "Entsperrversuch",
	access.EventMasterDataChange:  "Stammdaten geändert",
	access.EventAccessChange:      "Zugang geändert",
	access.EventArchiveDeleted:    "Archiv gelöscht",
	access.EventArchiveCompressed: "Archiv komprimiert",
	access.EventArchiveCorrected:  "Archiveintrag korrigiert",
	access.EventDataIngested:      "Datenübernahme",
	access.EventNotificationSent:  "Mitteilung versendet",
	access.EventServiceRestart:    "Dienst neu gestartet",
}

func auditEventLabel(t access.EventType) string {
	if label, ok := auditEventLabels[t]; ok {
		return label
	}
	return string(t) // an event type added without a label still shows, just untranslated
}

// auditNoteworthy marks the entries an operator is actually scanning the
// log for — a failed or throttled attempt to get in, and the one
// irreversible destructive action the interface offers. Everything else
// is routine and stays visually quiet, so "noteworthy" keeps meaning
// something.
func auditNoteworthy(e access.Event) bool {
	switch e.Type {
	case access.EventLoginFailure, access.EventArchiveDeleted, access.EventArchiveCompressed, access.EventArchiveCorrected, access.EventServiceRestart:
		return true
	case access.EventUnlockAttempt:
		return strings.Contains(e.Detail, "failed") || strings.Contains(e.Detail, "rate limited")
	}
	return false
}

type auditEventView struct {
	At         string
	TypeLabel  string
	Actor      string
	Detail     string
	Noteworthy bool
}

type auditTypeOption struct {
	Value    string
	Label    string
	Selected bool
}

type auditLimitOption struct {
	Value    int
	Selected bool
}

type auditPageData struct {
	Base
	Events []auditEventView

	// Total is every entry matching the filter, Shown how many of them are
	// on this page — the pair is what tells an operator their filter is
	// hiding older matches rather than that none exist.
	Total     int
	Shown     int
	Truncated bool

	From         string
	To           string
	TypeOptions  []auditTypeOption
	LimitOptions []auditLimitOption

	Unavailable bool // no audit log configured at all (see App.Audit)
}

// auditLimitChoices are the page sizes offered. The largest is bounded on
// purpose: the log is read a screenful at a time, and rendering an entire
// multi-year log into one page is never the useful answer.
var auditLimitChoices = []int{100, 200, 500, 1000}

// handleAuditLog is ZUGANG-08's reading end: the security log the system
// has always written, made reviewable by the operator it is kept for.
// Only the operator can reach it — the log spans every unit and every
// login attempt, so it is squarely outside what a tenant may see (SZ-4).
func (a *App) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	data := auditPageData{Base: a.base("Sicherheitsprotokoll", sess)}
	if a.Audit == nil {
		data.Unavailable = true
		a.render(w, "audit.html", data)
		return
	}

	q := access.Query{}

	// An unparseable date is treated as no bound rather than an error: the
	// filter is a convenience over a log that is always safe to show in
	// full, so falling back to "everything" is the harmless direction.
	if from, err := telegram.ParseDay(r.URL.Query().Get("from")); err == nil {
		q.From = from
		data.From = string(from)
	}
	if to, err := telegram.ParseDay(r.URL.Query().Get("to")); err == nil {
		q.To = to
		data.To = string(to)
	}

	selectedType := r.URL.Query().Get("type")
	data.TypeOptions = append(data.TypeOptions, auditTypeOption{Value: "", Label: "alle", Selected: selectedType == ""})
	for _, t := range auditEventOrder {
		selected := selectedType == string(t)
		if selected {
			q.Types = []access.EventType{t}
		}
		data.TypeOptions = append(data.TypeOptions, auditTypeOption{
			Value: string(t), Label: auditEventLabel(t), Selected: selected,
		})
	}

	q.Limit = access.DefaultQueryLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		for _, choice := range auditLimitChoices {
			if n == choice {
				q.Limit = n
			}
		}
	}
	for _, choice := range auditLimitChoices {
		data.LimitOptions = append(data.LimitOptions, auditLimitOption{Value: choice, Selected: choice == q.Limit})
	}

	events, total, err := a.Audit.Events(q)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	data.Total = total
	data.Shown = len(events)
	data.Truncated = total > len(events)
	for _, e := range events {
		actor := e.Actor
		if actor == "" {
			// Everything recorded before a session exists — a failed login
			// above all. Blank would read as missing data rather than as
			// the meaningful "nobody was logged in" that it is.
			actor = "nicht angemeldet"
		}
		data.Events = append(data.Events, auditEventView{
			At:         e.At.Format("2006-01-02 15:04:05"),
			TypeLabel:  auditEventLabel(e.Type),
			Actor:      actor,
			Detail:     e.Detail,
			Noteworthy: auditNoteworthy(e),
		})
	}

	a.render(w, "audit.html", data)
}
