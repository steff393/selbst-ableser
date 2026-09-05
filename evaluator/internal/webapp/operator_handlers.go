package webapp

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

type overviewPageData struct {
	Base
	Stats   archive.Stats
	Error   string
	Version string

	// The installation at a glance (UI-03). Only filled while the vault is
	// unlocked — without master data the archive is just a pile of
	// undecryptable telegrams and none of these counts mean anything.
	HasMasterData bool
	Units         int
	MeterPoints   int
	MetersActive  int
	MetersWithKey int
	AccessesLive  int
	BuildingName  string
	AreaM2        float64

	// MetersYesterdayKnown counts only meters that are actually in the
	// master data, so it can be shown as a fraction of MetersActive. Stats
	// .MetersYesterday cannot: it counts every distinct meter in the
	// archive for yesterday, and the archive deliberately keeps
	// neighbouring devices nobody registered, so that figure can exceed
	// MetersActive outright.
	MetersYesterdayKnown int

	// SilentMeters/NeverSeenMeters mirror the Zählerstatus page's own
	// thresholds, surfaced here so the overview says whether anything
	// needs attention rather than only how much data there is.
	SilentMeters    int
	NeverSeenMeters int
	ThresholdDays   int

	// StorageBytes covers archive, master data and audit log together —
	// the SD-card-wear figure BETRIEB-09 cares about, not one file's size.
	// Kept in sync with the paths renderOperatorOverview actually sums;
	// leaving one out understates exactly the number this exists for.
	StorageBytes int64
	StorageKnown bool

	CollectorSeen   bool
	CollectorLastAt string

	// CollectorsReporting/CollectorsKnown drive the overview's "N von M
	// gemeldet": M is every collector heard from within the last hour, N
	// how many of those are currently on time. With one collector this
	// reads the same as before; with several it is the difference between
	// "something reported" and "all of them did".
	CollectorsReporting int
	CollectorsKnown     int

	// LastEvents is the "Letzte Ereignisse" panel — one row per audit
	// category worth glancing at, newest timestamp or "n.a." if that
	// category has no entry yet. Empty (no panel at all) when there is no
	// audit log to ask (see App.Audit).
	LastEvents []lastEventRow

	// CheckSecuritySettings surfaces the one misconfiguration nothing
	// else reports: a reverse proxy forwarding HTTPS while trust_proxy is
	// off (see proxyLooksUnconfigured). It belongs on this page rather
	// than in the log because the person who can fix it is the one
	// looking at this page after every login, and does not read logs.
	CheckSecuritySettings bool
}

// lastEventRow is one line of the overview's "Letzte Ereignisse" panel.
type lastEventRow struct {
	Label string
	At    string // "" means "never recorded" — the template shows "n.a."
}

// handleOperatorOverview is UI-03: an at-a-glance status page.
func (a *App) handleOperatorOverview(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.renderOperatorOverview(w, r, sess, "")
}

func (a *App) renderOperatorOverview(w http.ResponseWriter, r *http.Request, sess *access.Session, errMsg string) {
	today := a.today()
	stats, err := a.Store.Stats(today)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	reporting, known := a.collectorsReporting()
	data := overviewPageData{
		Base:                  a.base("Betreiberübersicht", sess),
		Stats:                 stats,
		Error:                 errMsg,
		Version:               a.Version,
		ThresholdDays:         silentThresholdDays,
		CheckSecuritySettings: a.proxyLooksUnconfigured(r),
		CollectorsReporting:   reporting,
		CollectorsKnown:       known,
	}

	for _, path := range []string{a.ArchivePath, a.MasterDataPath, a.AuditPath} {
		if path == "" {
			continue
		}
		if fi, err := os.Stat(path); err == nil {
			data.StorageBytes += fi.Size()
			data.StorageKnown = true
		}
	}

	if seen := a.collectorLastSeenAt(); !seen.IsZero() {
		data.CollectorSeen = true
		// Same formatting as the Collector page, so the two never disagree
		// about what "zuletzt" means.
		data.CollectorLastAt = a.formatCollectorTime(seen)
	}

	if a.Audit != nil {
		events, err := a.lastEventRows()
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
		data.LastEvents = events
	}

	if md, unlocked := a.Vault.Get(); unlocked {
		data.HasMasterData = true
		data.BuildingName = md.Building.Name
		data.Units = len(md.Units)
		data.MeterPoints = len(md.MeterPoints)
		for _, u := range md.Units {
			data.AreaM2 += u.AreaM2
		}
		for _, ac := range md.Accesses {
			if ac.Start.Before(today) || ac.Start == today {
				if ac.End == nil || !ac.End.Before(today) {
					data.AccessesLive++
				}
			}
		}
		yesterday := today.AddDays(-1)
		for _, m := range md.Meters {
			if !m.Active(today) {
				continue
			}
			data.MetersActive++
			if m.AESKey != ([16]byte{}) {
				data.MetersWithKey++
			}
			last, found, err := a.Store.LastSeen(m.Number)
			if err != nil {
				a.renderTechnicalError(w, sess, err)
				return
			}
			switch {
			case !found:
				data.NeverSeenMeters++
			case last == yesterday:
				data.MetersYesterdayKnown++
			case last.DaysUntil(today) >= silentThresholdDays:
				data.SilentMeters++
			}
		}
	}

	a.render(w, "operator_overview.html", data)
}

// lastEventRows answers the overview's "Letzte Ereignisse" panel: the most
// recent timestamp per audit category, or "" (rendered as "n.a.") if that
// category has never fired. Only called when a.Audit != nil.
//
// EventNotificationSent covers three different messages (BENACHR-01/02/03)
// distinguished only by Detail, not by their own EventType — see
// notify.SendMonthlyReminders, SendWeeklyStatus and SendStartupNotification
// for where each prefix is written. A single bounded query for that type,
// bucketed here by prefix, is cheaper than a separate round trip per
// prefix and just as correct, since it only has to find the single newest
// match per bucket among the most recent entries.
func (a *App) lastEventRows() ([]lastEventRow, error) {
	rows := []lastEventRow{
		{Label: "Letzte UVI-Benachrichtigung"},
		{Label: "Letzter Wochenstatus"},
		{Label: "Letzte Stammdatenänderung"},
		{Label: "Letzte Zugangsänderung"},
		{Label: "Letzter Push"},
		{Label: "Letzter Neustart"},
	}
	const (
		idxUVIReminder = iota
		idxWeeklyStatus
		idxMasterDataChange
		idxAccessChange
		idxPush
		idxRestart
	)

	single := func(idx int, t access.EventType) error {
		events, _, err := a.Audit.Events(access.Query{Types: []access.EventType{t}, Limit: 1})
		if err != nil {
			return err
		}
		if len(events) > 0 {
			rows[idx].At = events[0].At.Format("2006-01-02 15:04:05")
		}
		return nil
	}
	if err := single(idxMasterDataChange, access.EventMasterDataChange); err != nil {
		return nil, err
	}
	if err := single(idxAccessChange, access.EventAccessChange); err != nil {
		return nil, err
	}
	if err := single(idxPush, access.EventDataIngested); err != nil {
		return nil, err
	}

	// EventServiceRestart is only the manual "Neustart"-button on the
	// Backup page — a crash-recovered or SCP-then-restarted process never
	// touches it. The startup notification fires unconditionally on every
	// process start (gated only by whether it's configured on at all), so
	// it is the more complete signal where available; take whichever of
	// the two is newer.
	var lastRestart time.Time
	restartEvents, _, err := a.Audit.Events(access.Query{Types: []access.EventType{access.EventServiceRestart}, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(restartEvents) > 0 {
		lastRestart = restartEvents[0].At
	}

	notifyEvents, _, err := a.Audit.Events(access.Query{Types: []access.EventType{access.EventNotificationSent}, Limit: access.DefaultQueryLimit})
	if err != nil {
		return nil, err
	}
	for _, e := range notifyEvents {
		switch {
		case rows[idxUVIReminder].At == "" && strings.HasPrefix(e.Detail, "unit "):
			rows[idxUVIReminder].At = e.At.Format("2006-01-02 15:04:05")
		case rows[idxWeeklyStatus].At == "" && strings.HasPrefix(e.Detail, "operator status "):
			rows[idxWeeklyStatus].At = e.At.Format("2006-01-02 15:04:05")
		case e.Detail == "startup" && e.At.After(lastRestart):
			lastRestart = e.At
		}
	}
	if !lastRestart.IsZero() {
		rows[idxRestart].At = lastRestart.Format("2006-01-02 15:04:05")
	}

	return rows, nil
}

// handleUnlock lets an already-logged-in operator unlock the vault again
// after it was locked (STAMM-04) without a full new login.
func (a *App) handleUnlock(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.UnlockLimiter.Allow("unlock:" + a.clientKey(r)) {
		a.audit(access.EventUnlockAttempt, sess.AuditActor(), "rate limited")
		http.Redirect(w, r, "/operator", http.StatusSeeOther)
		return
	}

	err := a.Vault.Unlock(a.MasterDataPath, r.PostFormValue("password"))
	if err != nil {
		a.audit(access.EventUnlockAttempt, sess.AuditActor(), "failed")
	} else {
		a.audit(access.EventUnlockAttempt, sess.AuditActor(), "succeeded")
	}
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

func (a *App) handleLock(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a.Vault.Lock()
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

// handleChangePassword replaces the master-data password (ZUGANG-09: the
// bootstrap-generated password is meant to be swapped for something
// memorable). It lives on the Zugänge page, next to the tenant access
// tokens — every credential of the installation in one place.
//
// It asks only for the new password. Not because the old one is retyped
// somewhere else, but because nothing here ever has it: the vault re-keys
// itself (see masterdata.Vault.ChangePassword) and never hands its
// password out, so there is no code path from an HTTP request to the
// current password at all. The acting session is kept — the operator's
// identity did not change, only the secret behind it — but every *other*
// session, operator or tenant, ends immediately: a password change happens
// exactly when a compromise is suspected, so a possible eavesdropper's
// session should not survive it, even though it was established under the
// old password and nothing here re-checks that.
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if a.Vault.Locked() {
		a.renderLocked(w, sess)
		return
	}

	newPassword := r.PostFormValue("new_password")
	// Same-shaped as a tenant token means the login form's credential-shape
	// dispatch (LooksLikeAccessToken) would route the operator's own
	// password to the token check and never reach the one that could
	// actually verify it — a standing lockout, not a one-off failure. See
	// access.Bootstrap for the same guard on the initial password.
	if access.LooksLikeAccessToken(newPassword) {
		a.renderAccessPage(w, sess, "Dieses Passwort hat genau die Form eines Mieter-Zugangscodes "+
			"(12 Zeichen aus dem eingeschränkten Token-Alphabet) — die Anmeldemaske könnte es dann "+
			"nie mehr vom Passwort unterscheiden. Bitte ein anderes wählen.")
		return
	}
	if err := a.Vault.ChangePassword(a.MasterDataPath, newPassword); err != nil {
		a.audit(access.EventUnlockAttempt, sess.AuditActor(), "password change failed")
		a.renderAccessPage(w, sess, "Passwort konnte nicht geändert werden: "+err.Error())
		return
	}
	a.Sessions.RevokeAllExcept(sess.ID)

	a.audit(access.EventMasterDataChange, sess.AuditActor(), "operator password changed")
	a.renderAccessPageWithFlash(w, sess, "Passwort geändert.")
}

// handleDeleteBackups removes every dated master-data backup
// (masterdata.DeleteDatedBackups). It exists for exactly the situation a
// password change alone does not cover: ChangePassword re-keys only the
// current file, so a .bak written under a possibly-compromised old
// password stays readable with it until someone clears the history by
// hand — this is that hand.
func (a *App) handleDeleteBackups(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	removed, err := masterdata.DeleteDatedBackups(a.MasterDataPath)
	if err != nil {
		a.renderAccessPage(w, sess, "Backups konnten nicht gelöscht werden: "+err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), fmt.Sprintf("deleted %d dated master data backups", removed))
	a.renderAccessPageWithFlash(w, sess, fmt.Sprintf("%d alte Stammdaten-Backups gelöscht.", removed))
}

type meterStatusRow struct {
	MeterPointID string
	Room         string
	UnitName     string
	MeterNumber  string
	LastSeen     string
	DaysSince    int
	NeverSeen    bool
}

type meterStatusPageData struct {
	Base
	ThresholdDays int
	NeverSeen     []meterStatusRow
	Silent        []meterStatusRow
	OK            []meterStatusRow
}

const silentThresholdDays = 5 // matches billing.DefaultLookbackDays, FACH-01's default backward-search window

// handleMeterStatus is UI-07: which configured meters have gone quiet.
func (a *App) handleMeterStatus(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	units := make(map[string]string, len(md.Units))
	for _, u := range md.Units {
		units[u.ID] = u.Name
	}
	meterPoints := make(map[string]meterPointLite, len(md.MeterPoints))
	for _, mp := range md.MeterPoints {
		meterPoints[mp.ID] = meterPointLite{Room: mp.Room, UnitName: units[mp.UnitID]}
	}

	today := a.today()
	data := meterStatusPageData{Base: a.base("Zählerstatus", sess), ThresholdDays: silentThresholdDays}
	for _, m := range md.Meters {
		if m.RemovedAt != nil {
			continue // no longer expected to send
		}
		mp := meterPoints[m.MeterPointID]
		row := meterStatusRow{MeterPointID: m.MeterPointID, Room: mp.Room, UnitName: mp.UnitName, MeterNumber: m.Number}

		last, found, err := a.Store.LastSeen(m.Number)
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
		if !found {
			row.NeverSeen = true
			data.NeverSeen = append(data.NeverSeen, row)
			continue
		}
		row.LastSeen = formatDay(last)
		row.DaysSince = last.DaysUntil(today)
		if row.DaysSince >= silentThresholdDays {
			data.Silent = append(data.Silent, row)
		} else {
			data.OK = append(data.OK, row)
		}
	}

	a.render(w, "meterstatus.html", data)
}

// meterPointLite holds only the two fields the status table needs, so the
// handler doesn't have to carry the full masterdata.MeterPoint around.
type meterPointLite struct {
	Room     string
	UnitName string
}
