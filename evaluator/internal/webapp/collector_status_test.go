package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCollectorConfigRequestMarksHeartbeat confirms an authenticated
// POST /collector/config — the same request every collector makes on its
// normal poll cadence, independent of the live view or daily push — is
// what puts a collector into the table, and that an unauthenticated one
// does not.
func TestCollectorConfigRequestMarksHeartbeat(t *testing.T) {
	app, _ := newTestApp(t)
	app.PushSecret = "s3cr3t"
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	if rows := app.collectorRows(); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none before any request has arrived", rows)
	}

	req, _ := http.NewRequest("POST", srv.URL+"/collector/config", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if rows := app.collectorRows(); len(rows) != 1 {
		t.Errorf("rows after an authenticated request = %+v, want one", rows)
	}
	if app.collectorLastSeenAt().IsZero() {
		t.Error("an authenticated request must mark the heartbeat the overview reads")
	}

	// An unauthenticated request must not count as a heartbeat.
	app.collectorsMu.Lock()
	app.collectors = nil
	app.collectorsMu.Unlock()

	badReq, _ := http.NewRequest("POST", srv.URL+"/collector/config", nil)
	badReq.Header.Set("Authorization", "Bearer wrong-secret")
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	badResp.Body.Close()

	if rows := app.collectorRows(); len(rows) != 0 {
		t.Errorf("rows = %+v, an unauthenticated request must not appear in the table", rows)
	}
}
