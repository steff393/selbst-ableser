package webapp

import (
	"fmt"
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

//
// Both bulk operations run in two steps — paste, look at what it would do,
// then apply — and the second step re-parses the pasted text from scratch
// rather than trusting a plan the browser sends back. That costs one extra
// parse and buys the guarantee that what gets written was derived from the
// operator's own text against the data as it is right now, not against
// whatever it looked like when the preview was rendered.

const maxPastedTable = 256 << 10 // a protocol for a large installation, with room to spare

// The swap list lives on the Stammdaten page, in the Tabellen tab: it is
// bulk entry like the CSV importer next to it, and it follows the same
// two steps — a preview that changes nothing, then an apply that reads
// the pasted text again rather than trusting a plan that travelled
// through the browser.
//
// It is the one entry path that cannot be a CSV of values: a swap writes
// two devices at once, the outgoing one's end reading and the incoming
// one's whole row, and both halves have to happen together or not at all.

type swapPreview struct {
	Day     string
	Text    string
	Plans   []swapPlan
	Ready   int
	Refused int
	Header  bool // the pasted block had a header line, which was skipped
}

// operatorMasterData is the preamble both swap handlers share: role and
// CSRF before the vault is touched at all.
func (a *App) operatorMasterData(w http.ResponseWriter, r *http.Request) (*access.Session, masterdata.MasterData, bool) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, masterdata.MasterData{}, false
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, masterdata.MasterData{}, false
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return nil, masterdata.MasterData{}, false
	}
	return sess, md, true
}

// planSwapsFromForm is the shared front half of preview and apply: read
// the pasted text and the date, and work out what they would do.
func (a *App) planSwapsFromForm(r *http.Request, md masterdata.MasterData) (*swapPreview, string) {
	text := r.PostFormValue("pasted")
	if strings.TrimSpace(text) == "" {
		return nil, "Bitte die Wechselliste einfügen."
	}
	if len(text) > maxPastedTable {
		return nil, "Die eingefügte Liste ist zu groß."
	}
	day, err := telegram.ParseDay(strings.TrimSpace(r.PostFormValue("day")))
	if err != nil {
		return nil, "Bitte ein gültiges Wechseldatum angeben (JJJJ-MM-TT)."
	}

	table := parsePastedTable(text)
	if len(table) == 0 {
		return nil, "In der eingefügten Liste steht nichts Auswertbares."
	}
	cols := defaultSwapColumns
	header := looksLikeHeader(table[0])
	if header {
		cols = detectSwapColumns(table[0])
		table = table[1:]
		// A header that names none of the columns is worse than no header
		// at all: fall back to the documented order rather than reading
		// every line as empty.
		if cols.OldNumber == 0 && cols.NewNumber == 0 && cols.Point == 0 {
			cols = defaultSwapColumns
		}
	}
	if len(table) == 0 {
		return nil, "Die eingefügte Liste enthält außer der Kopfzeile keine Daten."
	}

	preview := &swapPreview{Day: day.String(), Text: text, Header: header, Plans: planSwaps(md, table, cols, day)}
	for _, p := range preview.Plans {
		if p.OK() {
			preview.Ready++
		} else {
			preview.Refused++
		}
	}
	return preview, ""
}

func (a *App) handleSwapListPreview(w http.ResponseWriter, r *http.Request) {
	sess, md, ok := a.operatorMasterData(w, r)
	if !ok {
		return
	}
	preview, errMsg := a.planSwapsFromForm(r, md)
	a.renderMasterDataSwap(w, sess, md, preview, errMsg)
}

func (a *App) handleSwapListApply(w http.ResponseWriter, r *http.Request) {
	sess, md, ok := a.operatorMasterData(w, r)
	if !ok {
		return
	}
	preview, errMsg := a.planSwapsFromForm(r, md)
	if errMsg != "" {
		a.renderMasterDataSwap(w, sess, md, nil, errMsg)
		return
	}
	if preview.Ready == 0 {
		a.renderMasterDataSwap(w, sess, md, preview, "Keine übernehmbare Zeile — bitte die Hinweise unten prüfen.")
		return
	}

	day, _ := telegram.ParseDay(preview.Day)
	updated, applied := applySwaps(md, preview.Plans, day)
	if d := masterdata.Validate(updated); !d.OK() {
		a.renderMasterDataSwap(w, sess, md, preview, "Prüfung fehlgeschlagen, es wurde nichts geändert: "+joinErrors(d.Errors))
		return
	}
	if err := a.Vault.Save(a.MasterDataPath, updated); err != nil {
		a.renderMasterDataSwap(w, sess, md, preview, "Speichern fehlgeschlagen: "+err.Error())
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), fmt.Sprintf("%d meters swapped on %s", applied, preview.Day))
	http.Redirect(w, r, "/operator/masterdata?tab="+tabSwap, http.StatusSeeOther)
}
