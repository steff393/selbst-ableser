package webapp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/livepush"
)

// handleCollectorReport is saCollector's single reporting endpoint,
// replacing the older, separate live-push and archive-sync channels: one
// call, either a frequent "what's new" batch or the once-daily "this day
// is done" batch, and the evaluator — not the caller — decides what each
// entry is worth. Every entry always refreshes the live view; an entry
// is additionally committed durably (the same idempotent, conflict-
// checked InsertHistorical archive-sync and migration already use) when
// either its day is genuinely in the past by the evaluator's own clock,
// or the request marks the whole batch final — the once-daily case,
// where the collector itself asserts a day is finished slightly before
// midnight (see the collector's own design notes for why).
func (a *App) handleCollectorReport(w http.ResponseWriter, r *http.Request) {
	if !validCollectorAuth(r.Header.Get("Authorization"), a.PushSecret, r.RemoteAddr, a.TrustProxy) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var body struct {
		Final   bool            `json:"final"`
		Entries []archive.Entry `json:"entries"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if a.LiveBuffer != nil {
		telegrams := make([]livepush.Telegram, 0, len(body.Entries))
		for _, e := range body.Entries {
			telegrams = append(telegrams, livepush.Telegram{
				MeterID: e.MeterID, ReceivedAt: e.ReceivedAt, RSSI: e.RSSI, RawHex: e.RawHex,
			})
		}
		a.LiveBuffer.Add(telegrams...)
	}

	today := a.today()
	accepted, conflicts := 0, 0
	for _, e := range body.Entries {
		if !body.Final && !e.Day.Before(today) {
			continue // still today by our own clock and not asserted final: live view only
		}
		changed, err := a.Store.InsertHistorical(e)
		switch {
		case err != nil:
			conflicts++
			a.audit(access.EventDataIngested, "collector-report", fmt.Sprintf("conflict: meter %s day %s", e.MeterID, e.Day))
		case changed:
			accepted++
		}
	}
	if accepted > 0 {
		a.audit(access.EventDataIngested, "collector-report", fmt.Sprintf("%d entries", accepted))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Accepted  int `json:"accepted"`
		Conflicts int `json:"conflicts"`
	}{Accepted: accepted, Conflicts: conflicts})
}
