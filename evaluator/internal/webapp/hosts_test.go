package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowedHostsUnrestrictedByDefault(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/status", nil)
	req.Host = "anything.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		t.Error("an empty AllowedHosts should not restrict anything")
	}
}

func TestAllowedHostsRejectsUnlisted(t *testing.T) {
	app, _ := newTestApp(t)
	app.AllowedHosts = []string{"evaluator.example"}
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/status", nil)
	req.Host = "attacker.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a Host outside the allowlist", resp.StatusCode)
	}
}

func TestAllowedHostsAcceptsListedWithPort(t *testing.T) {
	app, _ := newTestApp(t)
	app.AllowedHosts = []string{"evaluator.example"}
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/status", nil)
	req.Host = "evaluator.example:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		t.Error("a listed host (with port stripped) should be accepted")
	}
}
