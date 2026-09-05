package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestWithProto(proto string) *http.Request {
	r := httptest.NewRequest("GET", "http://app.example/operator", nil)
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	return r
}

// TestRequestIsHTTPSIgnoresTrustProxy pins the asymmetry between the two
// proxy headers. X-Forwarded-For has to be earned via TrustProxy because
// believing it *grants* its sender an identity. X-Forwarded-Proto only
// withdraws something — it marks the cookie Secure, so it travels over
// fewer paths — and a client forging it constrains nothing but its own
// session. Gating it on TrustProxy therefore bought no safety and cost
// real protection: an installation put behind a TLS proxy without anyone
// setting that flag would serve its session cookie unmarked over a
// connection that genuinely is encrypted.
func TestRequestIsHTTPSIgnoresTrustProxy(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		proto      string
		want       bool
	}{
		{"plain HTTP, no proxy", false, "", false},
		{"forwarded HTTPS without trust_proxy set", false, "https", true},
		{"forwarded HTTPS with trust_proxy set", true, "https", true},
		{"forwarded plain HTTP", true, "http", false},
		{"header casing is irrelevant", false, "HTTPS", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := &App{TrustProxy: c.trustProxy}
			if got := app.requestIsHTTPS(requestWithProto(c.proto)); got != c.want {
				t.Errorf("requestIsHTTPS = %v, want %v", got, c.want)
			}
		})
	}
}

// TestProxyLooksUnconfigured: the overview's hint must appear exactly
// when a proxy forwards HTTPS while TrustProxy is off — the one state
// nothing else reports, where every caller looks like the proxy to the
// rate limiter and the audit log.
func TestProxyLooksUnconfigured(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		proto      string
		want       bool
	}{
		{"proxy forwarding HTTPS, flag off", false, "https", true},
		{"proxy forwarding HTTPS, flag on", true, "https", false},
		{"no proxy at all", false, "", false},
		{"direct local access while a proxy exists elsewhere", false, "http", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := &App{TrustProxy: c.trustProxy}
			if got := app.proxyLooksUnconfigured(requestWithProto(c.proto)); got != c.want {
				t.Errorf("proxyLooksUnconfigured = %v, want %v", got, c.want)
			}
		})
	}
}
