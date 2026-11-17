package webapp

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/qr"
)

// accessLetter is one tenant's page of the printable access letter.
type accessLetter struct {
	UnitName string
	Code     string
	Start    string
	End      string        // empty for an open-ended grant
	QR       template.HTML // the login link with the code, as an inline SVG
	HasEmail bool          // a reminder address is already on file for this grant
}

type accessLettersPageData struct {
	Base
	Units        []unitRow // for the screen-only filter
	Unit         string    // the filter's current value, empty for all units
	BuildingName string
	Date         string
	Address      string
	AddressSet   bool   // Address came from the configured base address, not from this request
	PrivacyURL   string // empty while the privacy policy is not filled in
	Reminders    bool   // monthly reminder mails are switched on
	Letters      []accessLetter
}

// handleAccessLetters renders the letter a tenant gets with their access
// code, one A4 page per grant that has not ended yet — the "handed to a
// tenant on paper" that access.GenerateAccessToken is designed for.
//
// Its QR code carries the login link with the code in the URL fragment
// (/login#code=…), which login.html moves into the credential field: the
// fragment is never sent to the server, so the code ends up in no request
// line, proxy log or Referer, and the letter holds nothing the code
// printed next to it does not already give away.
func (a *App) handleAccessLetters(w http.ResponseWriter, r *http.Request) {
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

	address, addressSet := a.letterAddress(r)
	today := a.today()
	now := a.now()
	data := accessLettersPageData{
		Base:         a.base("Zugangsschreiben", sess),
		Units:        unitRowsFrom(md.Units),
		Unit:         r.URL.Query().Get("unit"),
		BuildingName: md.Building.Name,
		Date:         fmt.Sprintf("%d. %s %d", now.Day(), germanMonths[now.Month()], now.Year()),
		Address:      address,
		AddressSet:   addressSet,
		Reminders:    a.NotifyConfig.Enabled,
	}
	if text := a.LegalConfig.PrivacyPolicyText; text != "" && !hasPlaceholders(text) {
		data.PrivacyURL = address + "/datenschutz"
	}

	unitNames := make(map[string]string, len(md.Units))
	for _, u := range md.Units {
		unitNames[u.ID] = u.Name
	}
	for _, acc := range md.Accesses {
		if acc.End != nil && acc.End.Before(today) {
			continue // the tenancy is over, there is nobody to write to
		}
		if data.Unit != "" && acc.UnitID != data.Unit {
			continue
		}
		code, err := qr.Encode(address + "/login#code=" + acc.Token)
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
		letter := accessLetter{
			UnitName: unitNames[acc.UnitID],
			Code:     acc.Token,
			Start:    formatDay(acc.Start),
			QR:       template.HTML(code.SVG()), // generated here from fixed markup, no input reaches it unescaped
			HasEmail: acc.Email != "",
		}
		if letter.UnitName == "" {
			letter.UnitName = acc.UnitID
		}
		if acc.End != nil {
			letter.End = formatDay(*acc.End)
		}
		data.Letters = append(data.Letters, letter)
	}

	// Every tenant's code on one page: nothing a browser or proxy should keep.
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, "access_letter.html", data)
}

// letterAddress is the address printed on the letter, without a trailing
// slash: the base address the reminder mails link to, where one is set —
// it is by definition the one tenants use — and otherwise the one this
// request came in on. The second is right wherever operator and tenants
// share an address, and shows up as visibly wrong (localhost, a LAN IP)
// where they do not, which the page then points out.
func (a *App) letterAddress(r *http.Request) (address string, configured bool) {
	if base := strings.TrimRight(strings.TrimSpace(a.NotifyConfig.BaseURL), "/"); base != "" {
		return strings.TrimSuffix(base, "/login"), true
	}
	scheme := "http"
	if a.requestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host, false
}
