package webapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

type accessPageData struct {
	Base
	AccessGridJSON template.JS // pre-serialized for the bulk-edit grid's initial state
	Error          string
}

// accessGridRow is one row of the Zugänge bulk-edit grid: a
// masterdata.Access with its dates in the grid's display format. Token is
// rendered read-only in the grid (see grid.js's readOnlyColumns) — the
// server only ever reads it back as a lookup key, never as a value to
// store; see parseAccessGridRows.
type accessGridRow struct {
	Token  string `json:"token"`
	UnitID string `json:"unit_id"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Email  string `json:"email"`
}

func accessGridRowFromAccess(a masterdata.Access) accessGridRow {
	row := accessGridRow{Token: a.Token, UnitID: a.UnitID, Start: formatGridDay(a.Start), Email: a.Email}
	if a.End != nil {
		row.End = formatGridDay(*a.End)
	}
	return row
}

func accessGridRowsFrom(accesses []masterdata.Access) []accessGridRow {
	rows := make([]accessGridRow, len(accesses))
	for i, a := range accesses {
		rows[i] = accessGridRowFromAccess(a)
	}
	return rows
}

// handleAccessList is UI-04's overview: every access grant, editable as one
// bulk grid (STAMM-06/UI-05's pattern, shared with Wohnungen and
// Zählerplätze/Zähler in Stammdaten).
func (a *App) handleAccessList(w http.ResponseWriter, r *http.Request) {
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
	a.renderAccessRows(w, sess, accessGridRowsFrom(md.Accesses), "")
}

// renderAccessRows draws the Zugänge page with the given rows — either the
// stored grants, or (after a rejected save) exactly what the operator typed
// or pasted, so one bad row doesn't discard the rest of the edit.
func (a *App) renderAccessRows(w http.ResponseWriter, sess *access.Session, rows []accessGridRow, errMsg string) {
	gridJSON, _ := json.Marshal(rows)
	data := accessPageData{
		Base:           a.base("Zugänge", sess),
		AccessGridJSON: template.JS(gridJSON),
		Error:          errMsg,
	}
	a.render(w, "access.html", data)
}

// renderAccessPage and renderAccessPageWithFlash are handleChangePassword's
// hook back into this page: the operator password form lives here too
// (next to the tenant access tokens — every credential of the installation
// in one place), so a password change has to redraw the same grid state as
// a plain GET.
func (a *App) renderAccessPage(w http.ResponseWriter, sess *access.Session, errMsg string) {
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}
	a.renderAccessRows(w, sess, accessGridRowsFrom(md.Accesses), errMsg)
}

func (a *App) renderAccessPageWithFlash(w http.ResponseWriter, sess *access.Session, flash string) {
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}
	gridJSON, _ := json.Marshal(accessGridRowsFrom(md.Accesses))
	data := accessPageData{Base: a.base("Zugänge", sess), AccessGridJSON: template.JS(gridJSON)}
	data.Flash = flash
	a.render(w, "access.html", data)
}

// handleAccessSave replaces the full set of access grants in one request —
// the grid's save action. Each submitted row is either an existing grant,
// identified by its (read-only, so never hand-edited) token, or a new one
// with a blank token that gets a freshly generated one here. A unit whose
// only grant is missing from the submitted rows — removed by the operator —
// is not itself special-cased: disappearing from the grid is exactly how
// "revoke" is expressed now, so its session is torn down the same way the
// old dedicated revoke action did (ZUGANG-04).
func (a *App) handleAccessSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	var rows []accessGridRow
	if err := json.Unmarshal([]byte(r.PostFormValue("accesses_json")), &rows); err != nil {
		a.renderAccessRows(w, sess, accessGridRowsFrom(md.Accesses), "Ungültige Übertragung.")
		return
	}

	newAccesses, newCount, errMsg := parseAccessGridRows(rows, md.Units, md.Accesses)
	if errMsg != "" {
		a.renderAccessRows(w, sess, rows, errMsg)
		return
	}

	stillPresent := make(map[string]bool, len(newAccesses))
	for _, acc := range newAccesses {
		stillPresent[acc.Token] = true
	}
	revoked := 0
	for _, old := range md.Accesses {
		if !stillPresent[old.Token] {
			a.Sessions.RevokeByUnit(old.UnitID)
			revoked++
		}
	}

	md.Accesses = newAccesses
	if err := a.Vault.Save(a.MasterDataPath, md); err != nil {
		a.renderAccessRows(w, sess, rows, "Speichern fehlgeschlagen.")
		return
	}

	a.audit(access.EventAccessChange, sess.AuditActor(), fmt.Sprintf("saved access grants: %d total, %d new, %d revoked", len(newAccesses), newCount, revoked))
	data := accessPageData{Base: a.base("Zugänge", sess)}
	data.Flash = "Gespeichert."
	gridJSON, _ := json.Marshal(accessGridRowsFrom(newAccesses))
	data.AccessGridJSON = template.JS(gridJSON)
	a.render(w, "access.html", data)
}

// parseAccessGridRows converts the Zugänge grid's rows into access grants.
// A row's own "token" cell is never trusted as the value to store — a
// tampered or stale submission could otherwise supply an arbitrary,
// attacker-chosen string as if it were genuinely random (see
// access.GenerateAccessToken, the only place a token is allowed to come
// from). It is only ever used to look up which existing grant a row is
// editing: a blank token means a new grant and always gets a freshly
// generated one; a token that doesn't match any currently known grant is
// rejected outright rather than silently accepted as new, since that would
// open exactly the same hole.
func parseAccessGridRows(rows []accessGridRow, units []masterdata.Unit, existing []masterdata.Access) (accesses []masterdata.Access, newCount int, errMsg string) {
	validUnit := make(map[string]bool, len(units))
	for _, u := range units {
		validUnit[u.ID] = true
	}
	existingByToken := make(map[string]masterdata.Access, len(existing))
	for _, ac := range existing {
		existingByToken[ac.Token] = ac
	}

	for i, row := range rows {
		if row.Token == "" && row.UnitID == "" && row.Start == "" && row.End == "" && row.Email == "" {
			continue // the grid's trailing blank row, not a real entry
		}
		label := row.Token
		if label == "" {
			label = fmt.Sprintf("Zeile %d", i+1)
		}
		if !validUnit[row.UnitID] {
			return nil, 0, fmt.Sprintf("Zugang %s: unbekannte Wohnung.", label)
		}
		start, err := parseGridDay(row.Start)
		if err != nil {
			return nil, 0, fmt.Sprintf("Zugang %s: ungültiges Beginn-Datum.", label)
		}
		var end *telegram.Day
		if row.End != "" {
			e, err := parseGridDay(row.End)
			if err != nil {
				return nil, 0, fmt.Sprintf("Zugang %s: ungültiges Ende-Datum.", label)
			}
			end = &e
		}
		email := strings.TrimSpace(row.Email)
		if email != "" && !looksLikeEmail(email) {
			return nil, 0, fmt.Sprintf("Zugang %s: E-Mail-Adresse sieht ungültig aus.", label)
		}

		if row.Token == "" {
			token, err := access.GenerateAccessToken()
			if err != nil {
				return nil, 0, "Token konnte nicht erzeugt werden."
			}
			accesses = append(accesses, masterdata.Access{Token: token, UnitID: row.UnitID, Start: start, End: end, Email: email})
			newCount++
			continue
		}

		existingAcc, ok := existingByToken[row.Token]
		if !ok {
			return nil, 0, fmt.Sprintf("Zugang %s: unbekannt oder in der Zwischenzeit entfernt — bitte Seite neu laden.", label)
		}
		accesses = append(accesses, masterdata.Access{Token: existingAcc.Token, UnitID: row.UnitID, Start: start, End: end, Email: email})
	}
	return accesses, newCount, ""
}

// looksLikeEmail is a light sanity check, not RFC 5322 validation: this
// only ever gates a single character class of typo (forgetting the "@"
// entirely), and a stricter check would just as easily reject some
// legitimate address SendMonthlyReminders' actual SMTP send is better
// placed to judge than a regex here.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t")
}
