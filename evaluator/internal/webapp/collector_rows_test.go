package webapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/telegram"
)

// postCollectorConfig sends one status report the way saCollector does on
// every poll: POST /collector/config with the report as the body.
func postCollectorConfig(t *testing.T, srv *httptest.Server, secret string, report map[string]any) {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshalling report: %v", err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/collector/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /collector/config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestCollectorConfigRecordsReportedStatus walks the whole self-reporting
// path: what saCollector puts in the request body has to come back out as
// the Collector page's table row, including the USB stick's mount point.
func TestCollectorConfigRecordsReportedStatus(t *testing.T) {
	app, _ := newTestApp(t)
	app.PushSecret = "s3cr3t"
	now := time.Date(2026, 3, 4, 14, 30, 0, 0, telegram.Local)
	app.Now = func() time.Time { return now }
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	started := now.Add(-3 * time.Hour)
	postCollectorConfig(t, srv, "s3cr3t", map[string]any{
		"name":       "dachboden",
		"version":    "0.4.2",
		"started_at": started,
		"receiver": map[string]any{
			"connected": true,
			"port":      "/dev/ttyUSB0",
			"since":     started,
		},
		"backup_medium": map[string]any{
			"connected": true,
			"path":      "E:\\",
		},
	})

	rows := app.collectorRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Name != "dachboden" {
		t.Errorf("Name = %q, want dachboden", row.Name)
	}
	if row.Version != "0.4.2" {
		t.Errorf("Version = %q, want 0.4.2", row.Version)
	}
	if row.StatusClass != "badge-success" {
		t.Errorf("StatusClass = %q, want badge-success (just reported)", row.StatusClass)
	}
	if row.ReceiverClass != "badge-success" || row.ReceiverText != "/dev/ttyUSB0" {
		t.Errorf("receiver = %s/%q, want badge-success and the port", row.ReceiverClass, row.ReceiverText)
	}
	if row.BackupClass != "badge-success" || row.BackupText != "E:\\" {
		t.Errorf("backup medium = %s/%q, want badge-success and the mount point", row.BackupClass, row.BackupText)
	}
	if row.StartedAt != "11:30:00" {
		t.Errorf("StartedAt = %q, want the wall-clock time 11:30:00", row.StartedAt)
	}

	if reporting, known := app.collectorsReporting(); reporting != 1 || known != 1 {
		t.Errorf("collectorsReporting = %d von %d, want 1 von 1", reporting, known)
	}
}

// TestCollectorRowsWithoutBackupMedium covers the no-stick case: not an
// error, because the daily backup then writes beside the collector itself
// (DATEN-06), so the row must say so without turning red.
func TestCollectorRowsWithoutBackupMedium(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	postCollectorConfig(t, srv, "", map[string]any{
		"name":     "pi",
		"receiver": map[string]any{"connected": false},
	})

	rows := app.collectorRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].BackupClass != "badge-neutral" || rows[0].BackupText != "keiner" {
		t.Errorf("backup medium = %s/%q, want badge-neutral and \"keiner\"", rows[0].BackupClass, rows[0].BackupText)
	}
	if rows[0].ReceiverClass != "badge-danger" {
		t.Errorf("ReceiverClass = %q, want badge-danger while disconnected", rows[0].ReceiverClass)
	}
	if rows[0].StartedAt != "—" {
		t.Errorf("StartedAt = %q, want the em dash for an unreported start time", rows[0].StartedAt)
	}
}

// TestCollectorRowsTimesAreAbsolute pins the deliberate choice of clock
// times over ages: a rendered page keeps standing, and "vor 12 Sekunden"
// would quietly become a lie while the reader looks at it.
func TestCollectorRowsTimesAreAbsolute(t *testing.T) {
	app, _ := newTestApp(t)
	now := time.Date(2026, 3, 4, 14, 30, 0, 0, telegram.Local)
	app.Now = func() time.Time { return now }
	app.CollectorConfig.ConfigPollSeconds = 60 // tolerance 90s

	app.markCollectorSeen(collectorReport{Name: "pi"})
	now = now.Add(10 * time.Minute) // well past the tolerance

	rows := app.collectorRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].StatusClass != "badge-danger" {
		t.Errorf("StatusClass = %q, want badge-danger 10 minutes after the last poll", rows[0].StatusClass)
	}
	if !strings.Contains(rows[0].StatusText, "14:30:00") {
		t.Errorf("StatusText = %q, want the time of the last contact", rows[0].StatusText)
	}
	for _, forbidden := range []string{"vor ", "seit "} {
		if strings.Contains(rows[0].StatusText, forbidden) {
			t.Errorf("StatusText = %q, must not be a relative age", rows[0].StatusText)
		}
	}

	// Yesterday's contact carries its date, so it cannot be misread as
	// this afternoon.
	now = now.Add(24 * time.Hour)
	if got := app.collectorRows()[0].StatusText; !strings.Contains(got, "04.03.2026") {
		t.Errorf("StatusText a day later = %q, want the date spelled out", got)
	}
}

// TestCollectorRowsPerName keeps several collectors apart and — the point
// of the table — keeps a silent one listed instead of quietly dropping it:
// only restarting the evaluator clears a row.
func TestCollectorRowsPerName(t *testing.T) {
	app, _ := newTestApp(t)
	now := time.Date(2026, 3, 4, 14, 30, 0, 0, telegram.Local)
	app.Now = func() time.Time { return now }

	app.markCollectorSeen(collectorReport{Name: "keller"})
	app.markCollectorSeen(collectorReport{Name: "dachboden"})

	rows := app.collectorRows()
	if len(rows) != 2 || rows[0].Name != "dachboden" || rows[1].Name != "keller" {
		t.Fatalf("rows = %+v, want dachboden and keller in that order", rows)
	}

	// A day later, only one of them is still reporting. The other must
	// still be listed, and must be listed as gone.
	now = now.Add(24 * time.Hour)
	app.markCollectorSeen(collectorReport{Name: "keller"})
	rows = app.collectorRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want both collectors, including the silent one", rows)
	}
	if rows[0].Name != "dachboden" || rows[0].StatusClass != "badge-danger" {
		t.Errorf("silent collector = %+v, want dachboden as badge-danger", rows[0])
	}
	if !strings.Contains(rows[0].StatusText, "04.03.2026") {
		t.Errorf("silent collector StatusText = %q, want the date of its last contact", rows[0].StatusText)
	}
	if rows[1].Name != "keller" || rows[1].StatusClass != "badge-success" {
		t.Errorf("reporting collector = %+v, want keller as badge-success", rows[1])
	}
}

// TestCollectorRowsAreBounded covers the one case in which a row is still
// dropped: not age, but a caller inventing more names than any real
// installation has.
func TestCollectorRowsAreBounded(t *testing.T) {
	app, _ := newTestApp(t)
	now := time.Date(2026, 3, 4, 14, 30, 0, 0, telegram.Local)
	app.Now = func() time.Time { return now }

	app.markCollectorSeen(collectorReport{Name: "der-echte"})
	for i := 0; i < maxCollectors+5; i++ {
		now = now.Add(time.Second)
		app.markCollectorSeen(collectorReport{Name: fmt.Sprintf("erfunden-%d", i)})
	}

	rows := app.collectorRows()
	if len(rows) != maxCollectors {
		t.Errorf("rows = %d, want the table capped at %d", len(rows), maxCollectors)
	}
	for _, row := range rows {
		if row.Name == "der-echte" {
			t.Error("the least recently seen row should have been the one dropped")
		}
	}
}

// TestCollectorReportTextIsSanitized guards the operator's page against a
// report that is not a well-behaved status: the endpoint is reachable by
// anything holding the secret (or by anything on loopback where none is
// set), so nothing it sends reaches a table cell unbounded.
func TestCollectorReportTextIsSanitized(t *testing.T) {
	app, _ := newTestApp(t)

	app.markCollectorSeen(collectorReport{
		Name:    "  pi\x00\n  ",
		Version: strings.Repeat("v", 200),
	})

	rows := app.collectorRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Name != "pi" {
		t.Errorf("Name = %q, want the trimmed, control-character-free name", rows[0].Name)
	}
	if len(rows[0].Version) > 40 {
		t.Errorf("Version is %d characters long, want it capped", len(rows[0].Version))
	}
}

// TestCollectorReportWithoutNameIsStillListed makes sure a collector that
// cannot say who it is still shows up — an unnamed row is far better than
// a silently missing one.
func TestCollectorReportWithoutNameIsStillListed(t *testing.T) {
	app, _ := newTestApp(t)
	app.markCollectorSeen(collectorReport{Version: "0.4.2"})

	rows := app.collectorRows()
	if len(rows) != 1 || rows[0].Name != "unbenannt" {
		t.Fatalf("rows = %+v, want one row named unbenannt", rows)
	}
}
