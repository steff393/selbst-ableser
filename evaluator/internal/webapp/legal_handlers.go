package webapp

import (
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/config"
)

type legalPageData struct {
	Base
	Text string

	// Draft marks a text that is missing or still carries template
	// placeholders. UI-12 asks for these notices to be present when the
	// interface is publicly reachable; presenting an unfinished template
	// as if it were the real notice would be worse than saying plainly
	// that it is not filled in yet.
	Draft bool
}

const notFilledIn = "Diese Angaben sind noch nicht hinterlegt."

// handleImprint and handlePrivacyPolicy are UI-12: both are reachable
// without a session, since the requirement applies whenever the interface
// is publicly reachable at all, not only to logged-in visitors. Both read
// straight from config.Legal — deliberately not the vault, even though
// they still carry the operator's name and address: see config.Legal's
// doc comment for why these two, unlike the rest of what an operator
// enters, are not treated as secret.
func (a *App) handleImprint(w http.ResponseWriter, r *http.Request) {
	a.renderLegal(w, r, "Impressum", a.LegalConfig.ImprintText)
}

func (a *App) handlePrivacyPolicy(w http.ResponseWriter, r *http.Request) {
	a.renderLegal(w, r, "Datenschutzerklärung", a.LegalConfig.PrivacyPolicyText)
}

func (a *App) renderLegal(w http.ResponseWriter, r *http.Request, title, text string) {
	data := legalPageData{Base: a.base(title, a.session(r))}
	switch {
	case text == "":
		data.Text = notFilledIn
		data.Draft = true
	case hasPlaceholders(text):
		data.Text = text
		data.Draft = true
	default:
		data.Text = text
	}
	a.render(w, "legal.html", data)
}

// legalEditData is the operator-facing editor's view of UI-12's two
// notices — on the Benachrichtigungen page, not Stammdaten, since neither
// is building data any more (see config.Legal). Mirrors the shape the
// masterdata grids use elsewhere: the stored text, or a starting template
// when nothing is stored yet, and whether either still carries
// [Platzhalter].
type legalEditData struct {
	ImprintText          string
	PrivacyPolicyText    string
	ImprintDefault       string
	PrivacyPolicyDefault string
	LegalDraft           bool
}

func (a *App) legalEditData() legalEditData {
	imprint := a.LegalConfig.ImprintText
	if imprint == "" {
		imprint = defaultImprintTemplate
	}
	privacy := a.LegalConfig.PrivacyPolicyText
	if privacy == "" {
		privacy = defaultPrivacyPolicyTemplate
	}
	return legalEditData{
		ImprintText:          imprint,
		PrivacyPolicyText:    privacy,
		ImprintDefault:       defaultImprintTemplate,
		PrivacyPolicyDefault: defaultPrivacyPolicyTemplate,
		LegalDraft:           hasPlaceholders(imprint) || hasPlaceholders(privacy),
	}
}

// handleLegalSave stores UI-12's two notices. Its own save, independent of
// handleNotifySettingsSave even though both now render on the
// Benachrichtigungen page: unlike Mailserver/Benachrichtigungen, which an
// operator experiences as one setting (see that handler's own doc
// comment), these are a genuinely separate concern that only happens to
// share a page now that neither lives in the vault.
func (a *App) handleLegalSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	a.LegalConfig.ImprintText = strings.TrimSpace(r.PostFormValue("imprint_text"))
	a.LegalConfig.PrivacyPolicyText = strings.TrimSpace(r.PostFormValue("privacy_policy_text"))

	if err := a.persistLegalConfig(); err != nil {
		a.render(w, "notify.html", a.notifySettingsPageData(sess, "Speichern fehlgeschlagen: "+err.Error()))
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "legal texts")
	http.Redirect(w, r, "/operator/notify", http.StatusSeeOther)
}

// persistLegalConfig writes LegalConfig back to ConfigPath, the same
// read-modify-write pattern persistNotifySettings/persistCollectorConfig
// use for their own slice of config.json.
func (a *App) persistLegalConfig() error {
	if a.ConfigPath == "" {
		return nil
	}
	cfg, err := config.LoadOrEmpty(a.ConfigPath)
	if err != nil {
		return err
	}
	cfg.Legal = a.LegalConfig
	return config.Save(a.ConfigPath, cfg)
}
