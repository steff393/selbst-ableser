package webapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusEndpointNoLoginRequired(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, want 200", resp.StatusCode)
	}

	var got statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.Ready {
		t.Error("Ready = false, want true (archive is reachable)")
	}
	if !got.Locked {
		t.Error("Locked = false, want true (vault starts locked)")
	}
}

func TestStatusEndpointDoesNotLeakSensitiveFields(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, forbidden := range []string{"password", "aes_key", "meter", "kwh"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("status response contains %q, which BETRIEB-08 forbids: %s", forbidden, body)
		}
	}
}
