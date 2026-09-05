package webapp

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/billing"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/livepush"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

type liveRow struct {
	MeterID          string
	Length           int // raw telegram length in bytes
	ReceivedAt       string
	RSSI             int
	RSSIQuality      string
	DeviceType       string
	DeviceTypeName   string
	Manufacturer     string
	ManufacturerName string
	AESIcon          string
	AESTitle         string
	KnownMeter       bool
	Evaluable        bool
	Value            string
	DecodeURL        string

	// Interval is the gap between this meter's two most recently received
	// telegrams in the current buffer, formatted for display — empty if
	// only one has been seen (nothing to compare against yet). See
	// meterIntervals.
	Interval string
}

type livePageData struct {
	Base
	Rows []liveRow

	// LiveActive/LiveUntil describe the push this page depends on, and
	// the control below them switches it. Without it the page is a
	// permanently empty table with no hint that anything has to be turned
	// on somewhere else first — the switch belongs where its effect is.
	LiveActive bool
	LiveUntil  string
	WindowText string
}

// handleLiveView is UI-06's evaluator half: decrypted telegrams pushed by
// the collector (see push_handler.go), Betreiber-only per ZUGANG-05.
// Decryption happens fresh on every request, from
// the still-encrypted buffer, and the result goes straight into this
// response — never persisted (FACH-12/SZ-3). One row per meter *and*
// telegram length (the most recently pushed one of each), not one per
// push — the buffer holds every individual push, but the operator wants a
// status overview, not a log. Keying on length too, rather than on the
// meter alone, means a meter that alternates between telegram formats
// shows every length variant side by side instead of only the most
// recent one hiding the rest — the case collector filter rules
// (BlockedPrefixes) exist to handle, and are easiest to define having
// seen every variant at once. Each row also carries how long ago this
// meter's previous telegram arrived (see meterIntervals) — a rough,
// window-scoped read on transmission cadence without needing anything
// from the collector, which never learns per-meter timing on its own.
func (a *App) handleLiveView(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var entries []livepush.Telegram
	if a.LiveBuffer != nil {
		entries = a.LiveBuffer.Recent(200)
	}
	liveActive, liveUntil := a.liveViewState()
	md, unlocked := a.Vault.Get()
	intervals := meterIntervals(entries)

	type seenKey struct {
		meterID string
		length  int
	}
	seen := make(map[seenKey]bool, len(entries))
	rows := make([]liveRow, 0, len(entries))
	for _, e := range entries {
		raw, _ := hex.DecodeString(e.RawHex) // length 0 for undecodable hex, its own distinct variant
		key := seenKey{meterID: e.MeterID, length: len(raw)}
		if seen[key] {
			continue // entries are newest-first; the first occurrence per meter+length is the one to show
		}
		seen[key] = true
		row := a.buildLiveRow(e, raw, md, unlocked)
		if d, ok := intervals[e.MeterID]; ok {
			row.Interval = d.Round(time.Second).String()
		}
		rows = append(rows, row)
	}

	a.render(w, "live.html", livePageData{
		Base:       a.base("Live-Ansicht", sess),
		Rows:       rows,
		LiveActive: liveActive,
		LiveUntil:  liveUntil,
		WindowText: strconv.Itoa(int(config.LiveViewWindow.Hours())),
	})
}

// meterIntervals returns, for every meter with at least two entries in
// entries (already newest-first, as Buffer.Recent returns them), the gap
// between its two most recently received telegrams — across all telegram
// lengths, not just matching ones, since this is about how often the
// physical meter transmits at all, not which format variant it used each
// time. A meter with only one entry in the current buffer has nothing to
// compare against and is simply absent from the result — this is a
// snapshot of the current live-view window, not a tracked long-term
// average, so one telegram is not "no interval", it is "not known yet".
func meterIntervals(entries []livepush.Telegram) map[string]time.Duration {
	newest := make(map[string]time.Time, len(entries))
	out := make(map[string]time.Duration, len(entries))
	for _, e := range entries {
		prev, seen := newest[e.MeterID]
		if !seen {
			newest[e.MeterID] = e.ReceivedAt
			continue
		}
		if _, already := out[e.MeterID]; !already {
			out[e.MeterID] = prev.Sub(e.ReceivedAt)
		}
	}
	return out
}

// buildLiveRow renders one entry given its already hex-decoded raw bytes
// (see handleLiveView, which needs that same decode for its own
// per-length grouping and passes it through rather than decoding twice).
func (a *App) buildLiveRow(e livepush.Telegram, raw []byte, md masterdata.MasterData, unlocked bool) liveRow {
	row := liveRow{
		MeterID:     e.MeterID,
		Length:      len(raw),
		ReceivedAt:  e.ReceivedAt.Format("2006-01-02 15:04:05"),
		RSSI:        e.RSSI,
		RSSIQuality: telegram.ClassifyRSSI(e.RSSI).String(),
		Value:       "n.a.",
		DecodeURL:   wmbusmetersURL(e.RawHex, [16]byte{}),
	}
	if len(raw) < 11 {
		return row
	}

	row.Manufacturer = telegram.Manufacturer(binary.LittleEndian.Uint16(raw[2:4]))
	row.ManufacturerName, _ = telegram.ManufacturerName(row.Manufacturer)
	if dt, ok := telegram.IdentifyDeviceType(raw[9]); ok {
		row.DeviceType = dt.Abbr
		row.DeviceTypeName = dt.Name
	}
	row.AESIcon, row.AESTitle = aesLockIcon(raw[10])

	var key [16]byte
	if unlocked {
		if meter, found := md.MeterByNumber(e.MeterID, telegram.DayOf(e.ReceivedAt)); found {
			row.KnownMeter = true
			key = meter.AESKey
		}
	}
	row.DecodeURL = wmbusmetersURL(e.RawHex, key)

	if reading, found, err := billing.ReadValue(archive.Entry{RawHex: e.RawHex}, key); err == nil && found {
		row.Evaluable = true
		row.Value = fmt.Sprintf("%d", reading.Value)
	}
	return row
}

// aesLockIcon summarizes a telegram's transport-level encryption state as a
// compact padlock, from the CI byte alone — no key needed, so it shows
// correctly even for a meter with no configured AES key (UI-06).
func aesLockIcon(ci byte) (icon, title string) {
	label := telegram.EncryptionStatusLabel(ci)
	switch {
	case label == "verschlüsselt":
		return "🔒", label
	case label == "unbekannt":
		return "?", label
	default: // "unverschlüsselt" or "unverschlüsselt (herstellerspezifisch)"
		return "🔓", label
	}
}

// handleLiveDeleteToday is the counterpart to "Push jetzt auslösen": a
// manual push writes today's reading durably even though the day itself
// isn't over yet (see TriggerPush's doc comment), and
// archive.Store.InsertHistorical then rejects any later push for the same
// day whose reading has moved on since as a conflict. Undoing that
// requires clearing today's archived entries so the next push — manual or
// scheduled — has a clean slate to write into; this is the day-range
// delete already used on the Archiv page (DATEN-06), scoped to just today
// and reachable from where the mistake was made.
//
// That scoping is also this button's limit: it always targets today() at
// click time. The collector's own scheduled push only ever resends a day
// once it considers that day over (see deliverDue in cmd/saCollector), so
// the conflict this button is meant to clear typically isn't discovered
// until a later day — by which point this button clears the wrong
// (now-empty) range. Use the Archiv page's ranged delete with the actual
// affected day instead in that case.
func (a *App) handleLiveDeleteToday(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	today := a.today()
	deleted, err := a.Store.DeleteRange(today, today)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.audit(access.EventArchiveDeleted, sess.AuditActor(), fmt.Sprintf("%d entries, %s (live-view cleanup)", deleted, today))
	http.Redirect(w, r, "/operator/live", http.StatusSeeOther)
}

// handleLiveBufferClear empties the live-view buffer on demand — useful
// after testing a filter change or a newly connected receiver, so old
// entries don't sit mixed in with what's actually arriving now. Purely a
// display convenience (see livepush.Buffer's own doc comment: it is a
// ring buffer, not an archive), so unlike handleLiveDeleteToday this
// touches nothing durable and is not audited, the same reasoning already
// applied to handleCollectorTriggerPush.
func (a *App) handleLiveBufferClear(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if a.LiveBuffer != nil {
		a.LiveBuffer.Clear()
	}
	http.Redirect(w, r, "/operator/live", http.StatusSeeOther)
}

// handleLiveViewToggle switches the collector's live push from the
// Live-Ansicht page itself. Same operation as the Collector settings
// page's toggle (see setLiveView) — the control simply also lives where
// its effect is visible, since a page that stays empty until something
// is enabled elsewhere gives no hint of that.
func (a *App) handleLiveViewToggle(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.setLiveView(r.PostFormValue("enabled") != ""); err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	http.Redirect(w, r, "/operator/live", http.StatusSeeOther)
}
