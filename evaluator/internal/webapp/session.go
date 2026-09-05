package webapp

import (
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
)

const sessionCookieName = "sa_session"

func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.requestIsHTTPS(r),
	})
}

// requestIsHTTPS reports whether the connection this cookie is being set
// on is actually encrypted, which is exactly when the cookie may be
// marked Secure (BETRIEB-06).
//
// Derived per request rather than configured: as a setting it had only
// wrong answers available at the moment it was set. Marked Secure on a
// plain-HTTP installation, the browser silently never sends the cookie
// back and login fails with nothing to see; left unmarked behind a
// TLS-terminating proxy, the session cookie may travel unprotected. Both
// were recoverable only by hand-editing config.json. Asked of the live
// request, neither can happen.
//
// Behind a reverse proxy the connection to this process is plain HTTP
// even while the visitor is on HTTPS, so the proxy's own header decides.
// Deliberately **not** gated on TrustProxy, unlike X-Forwarded-For in
// clientKey, because the two headers grant opposite things: a forged
// X-Forwarded-For hands its sender someone else's rate-limit budget and
// a false line in the audit log, so it must be earned; X-Forwarded-Proto
// only ever *withdraws* something here — it marks a cookie Secure, so it
// travels over fewer paths, not more. A client forging it restricts its
// own session and nobody else's (it cannot inject a header into someone
// else's request without already being in the middle of it). Tying this
// to TrustProxy therefore bought no safety and cost real protection: an
// installation put behind a proxy without anyone setting that flag —
// the likeliest way to get it wrong — would serve its session cookie
// unmarked over a connection that genuinely is TLS.
func (a *App) requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// proxyLooksUnconfigured reports a proxy that is forwarding HTTPS while
// TrustProxy is off — the one combination nothing else notices, and the
// reason the overview carries a hint about it (see overviewPageData).
// Everything that still depends on TrustProxy degrades silently in this
// state: every caller looks like the proxy, so one attacker exhausts the
// login rate limit for everybody, and the audit log records the proxy's
// address instead of the real one.
func (a *App) proxyLooksUnconfigured(r *http.Request) bool {
	return !a.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// session returns the caller's session, or nil if there is none — an
// absent, expired, or otherwise invalid cookie are all the same "no
// session" outcome (ZUGANG-07).
func (a *App) session(r *http.Request) *access.Session {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	sess, ok := a.Sessions.Lookup(c.Value)
	if !ok {
		return nil
	}
	return sess
}

// requireCSRF checks a state-changing request's CSRF token (form field
// "csrf_token") against the session's (ZUGANG-03).
func requireCSRF(sess *access.Session, r *http.Request) bool {
	return access.VerifyCSRF(sess, r.FormValue("csrf_token"))
}

// clientKey identifies a caller for rate limiting (ZUGANG-06). It only
// looks at X-Forwarded-For when the deployment has explicitly said it sits
// behind a reverse proxy (a.TrustProxy, BETRIEB-06's "MUSS ausdrücklich
// konfiguriert werden und DARF NICHT implizit von der Umgebung abhängen"):
// with TrustProxy off, a caller could otherwise forge this header to reset
// or evade its own rate limit on every request. When trusted, the last
// entry is used — the one the proxy itself appended — not the first,
// which the client controls and could prepend fake entries to.
func (a *App) clientKey(r *http.Request) string {
	if a.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return r.RemoteAddr
}
