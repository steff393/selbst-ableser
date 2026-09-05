package webapp

import (
	"net/http/httptest"
	"testing"
)

func TestClientKeyIgnoresForwardedForByDefault(t *testing.T) {
	a := &App{TrustProxy: false}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := a.clientKey(req); got != "10.0.0.1:1234" {
		t.Errorf("clientKey = %q, want the real RemoteAddr (X-Forwarded-For must be ignored untrusted)", got)
	}
}

func TestClientKeyUsesLastForwardedForEntryWhenTrusted(t *testing.T) {
	a := &App{TrustProxy: true}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234" // the trusted proxy itself
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	if got := a.clientKey(req); got != "10.0.0.1" {
		t.Errorf("clientKey = %q, want the last (proxy-appended) entry, not the client-controlled first one", got)
	}
}
