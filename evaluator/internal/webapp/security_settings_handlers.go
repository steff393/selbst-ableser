package webapp

import (
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/config"
)

type securitySettingsPageData struct {
	Base

	AllowedHosts string
	TrustProxy   bool

	// CurrentHost is what this very request arrived as. Shown because it
	// is the value the operator almost always wants in the list, and
	// because it is the one the save below insists on keeping (see
	// handleSecuritySettingsSave).
	CurrentHost string
	// RequestIsHTTPS reports how this page itself was reached, which is
	// what decides the session cookie's Secure flag (see requestIsHTTPS)
	// — displayed so an operator can tell whether the proxy in front is
	// actually forwarding what this setting assumes.
	RequestIsHTTPS bool

	Error string
	Saved bool
}

func (a *App) securitySettingsPageData(sess *access.Session, r *http.Request, errMsg string, saved bool) securitySettingsPageData {
	return securitySettingsPageData{
		Base:           a.base("Sicherheit", sess),
		AllowedHosts:   strings.Join(a.AllowedHosts, ", "),
		TrustProxy:     a.TrustProxy,
		CurrentHost:    r.Host,
		RequestIsHTTPS: a.requestIsHTTPS(r),
		Error:          errMsg,
		Saved:          saved,
	}
}

// handleSecuritySettingsView is BETRIEB-06's configuration surface: the
// two settings that decide how safely this evaluator can be exposed.
//
// They live here rather than in the one-time setup on the command line
// because that is the moment an operator knows least — before a domain
// exists, before a proxy is configured, sometimes before it is decided
// whether the installation will be reachable from outside at all — and
// because a wrong answer then used to be correctable only by hand-editing
// config.json. Both are decisions that change over an installation's
// life; both belong somewhere they can be changed.
func (a *App) handleSecuritySettingsView(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "security.html", a.securitySettingsPageData(sess, r, "", false))
}

// handleSecuritySettingsSave stores both settings, refusing the one
// change that cannot be undone from here: a host list that does not
// include the host this request arrived as would reject the very next
// request to this page (see App.hostAllowed), leaving no way back except
// editing config.json by hand — exactly the dead end this page exists to
// remove. Everything else is recoverable by simply saving again.
func (a *App) handleSecuritySettingsSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	hosts := splitNonEmpty(r.PostFormValue("allowed_hosts"), ",")
	if len(hosts) > 0 && !hostListAllows(hosts, r.Host) {
		a.render(w, "security.html", a.securitySettingsPageData(sess, r,
			"Diese Liste enthält nicht den Namen, unter dem Sie gerade zugreifen ("+r.Host+
				") — damit wäre diese Seite sofort nicht mehr erreichbar. Bitte ergänzen.", false))
		return
	}

	a.AllowedHosts = hosts
	a.TrustProxy = r.PostFormValue("trust_proxy") != ""

	if err := a.persistSecuritySettings(); err != nil {
		a.render(w, "security.html", a.securitySettingsPageData(sess, r, "Speichern fehlgeschlagen: "+err.Error(), false))
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "security settings")
	a.render(w, "security.html", a.securitySettingsPageData(sess, r, "", true))
}

func (a *App) persistSecuritySettings() error {
	if a.ConfigPath == "" {
		return nil
	}
	cfg, err := config.LoadOrEmpty(a.ConfigPath)
	if err != nil {
		return err
	}
	cfg.Evaluator.AllowedHosts = a.AllowedHosts
	cfg.Evaluator.TrustProxy = a.TrustProxy
	return config.Save(a.ConfigPath, cfg)
}

// splitNonEmpty splits s on sep and drops empty/whitespace-only pieces,
// so an empty field yields a nil slice (meaning "unrestricted") rather
// than a one-element slice containing "".
func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
