package webapp

import (
	"net/http"

	"selbst-ableser/internal/access"
)

type loginPageData struct {
	Base
	Error  string
	Notice string
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if sess := a.session(r); sess != nil {
		a.redirectHome(w, r, sess)
		return
	}
	a.render(w, "login.html", loginPageData{Base: a.base("Anmelden", nil), Notice: r.URL.Query().Get("notice")})
}

// handleLogin authenticates either the operator (master-data password —
// see docs/architektur.md on why there is no separate operator credential) or
// a tenant (access token), from a single "credential" form field. Which
// one is attempted is decided by the credential's own shape (see
// access.LooksLikeAccessToken) rather than by asking the caller to pick a
// role first. ZUGANG-07 requires both to fail identically from the
// caller's point of view, so both go through their own rate limiter and
// the same generic error message.
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	credential := r.PostFormValue("credential")
	key := a.clientKey(r)
	const genericError = "Anmeldung fehlgeschlagen. Bitte Eingabe prüfen."

	if credential != "" && access.LooksLikeAccessToken(credential) {
		if !a.LoginLimiter.Allow("login:" + key) {
			a.audit(access.EventLoginFailure, "", "rate limited")
			a.renderLoginError(w, genericError)
			return
		}
		if a.Vault.Locked() {
			a.renderLocked(w, nil)
			return
		}
		md, _ := a.Vault.Get()
		grant, ok := access.VerifyAccessToken(md.Accesses, credential, a.today())
		if !ok {
			a.audit(access.EventLoginFailure, "", "tenant token "+access.RedactToken(credential))
			a.renderLoginError(w, genericError)
			return
		}

		sess, err := a.Sessions.CreateTenant(grant.UnitID, grant.Start, grant.End)
		if err != nil {
			a.renderTechnicalError(w, nil, err)
			return
		}
		a.audit(access.EventLoginSuccess, sess.AuditActor(), "tenant unit "+grant.UnitID)
		a.setSessionCookie(w, r, sess.ID)
		http.Redirect(w, r, "/uvi", http.StatusSeeOther)
		return
	}

	if credential != "" {
		if !a.UnlockLimiter.Allow("unlock:" + key) {
			a.audit(access.EventUnlockAttempt, "", "rate limited")
			a.renderLoginError(w, genericError)
			return
		}
		if err := a.Vault.Unlock(a.MasterDataPath, credential); err != nil {
			a.audit(access.EventUnlockAttempt, "", "failed")
			a.audit(access.EventLoginFailure, "", "operator")
			a.renderLoginError(w, genericError)
			return
		}
		a.audit(access.EventUnlockAttempt, "", "succeeded")

		sess, err := a.Sessions.Create(access.RoleOperator, "")
		if err != nil {
			a.renderTechnicalError(w, nil, err)
			return
		}
		a.audit(access.EventLoginSuccess, sess.AuditActor(), "operator")
		a.setSessionCookie(w, r, sess.ID)
		http.Redirect(w, r, "/operator", http.StatusSeeOther)
		return
	}

	a.renderLoginError(w, genericError)
}

func (a *App) renderLoginError(w http.ResponseWriter, msg string) {
	a.render(w, "login.html", loginPageData{Base: a.base("Anmelden", nil), Error: msg})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if sess == nil || !requireCSRF(sess, r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.Sessions.Revoke(sess.ID)
	a.audit(access.EventLogout, sess.AuditActor(), "")
	a.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.redirectHome(w, r, sess)
}

func (a *App) redirectHome(w http.ResponseWriter, r *http.Request, sess *access.Session) {
	if sess.Role == access.RoleOperator {
		http.Redirect(w, r, "/operator", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/uvi", http.StatusSeeOther)
}

// audit records a security event without failing the request if logging
// itself fails — a missing audit entry is a defect to fix, not a reason to
// deny the user an otherwise-successful action.
func (a *App) audit(t access.EventType, actor, detail string) {
	if a.Audit == nil {
		return
	}
	if err := a.Audit.Record(access.Event{Type: t, At: a.now(), Actor: actor, Detail: detail}); err != nil {
		// Deliberately not fatal — see the doc comment above.
		_ = err
	}
}
