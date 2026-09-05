package report

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"selbst-ableser/collector/internal/store"
	"selbst-ableser/collector/internal/telegram"
)

func TestSendEmptyEntriesIsNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	if _, err := Send(t.Context(), srv.Client(), srv.URL, "s3cr3t", nil, false); err != nil {
		t.Fatalf("Send with no entries: %v", err)
	}
	if called {
		t.Error("Send with no entries should not make an HTTP request")
	}
}

func TestSendMarshalsFinalFlagAndAuth(t *testing.T) {
	var gotBody struct {
		Final   bool          `json:"final"`
		Entries []store.Entry `json:"entries"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer s3cr3t" {
			t.Errorf("Authorization = %q, want Bearer s3cr3t", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted": 1, "conflicts": 0}`))
	}))
	defer srv.Close()

	day, err := telegram.ParseDay("2026-08-20")
	if err != nil {
		t.Fatal(err)
	}
	entries := []store.Entry{{MeterID: "90000001", Day: day, ReceivedAt: time.Now(), RSSI: -80, RawHex: "aabb"}}

	result, err := Send(t.Context(), srv.Client(), srv.URL, "s3cr3t", entries, true)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !gotBody.Final {
		t.Error("expected the request body to carry final=true")
	}
	if len(gotBody.Entries) != 1 || gotBody.Entries[0].MeterID != "90000001" {
		t.Errorf("request entries = %+v", gotBody.Entries)
	}
	if result.Accepted != 1 {
		t.Errorf("result.Accepted = %d, want 1", result.Accepted)
	}
}

func TestSendErrorsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	day, _ := telegram.ParseDay("2026-08-20")
	entries := []store.Entry{{MeterID: "1", Day: day, RawHex: "aa"}}
	if _, err := Send(t.Context(), srv.Client(), srv.URL, "wrong", entries, false); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}
