package webapp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// mustDayTimeT parses "YYYY-MM-DD" as a local (Europe/Berlin) time.Time,
// for stubbing App.Now — the tests here need a concrete "today" that is
// months past the archived test data, which mustDayT's telegram.Day alone
// cannot provide.
func mustDayTimeT(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02", s, telegram.Local)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return ts
}

// TestUVIDefaultsToLatestAvailableMonthWhenTodayHasNone covers the case
// where data collection has fallen behind "today" by a wide margin (the
// operator hasn't run the evaluator in months, or — as with a fixed test
// data set — there simply is no data past a certain date): a tenant
// opening /uvi with no explicit ?month= must land on their most recent
// actual reading, not an empty "no data" page for the current month.
func TestUVIDefaultsToLatestAvailableMonthWhenTodayHasNone(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-08-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
		Accesses: []masterdata.Access{
			{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	// The only data on record ends in January 2026, months before "today".
	for day, value := range map[string]int64{"2025-12-31": 100, "2026-01-31": 130} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: mustDayT(t, day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "Keine Daten") {
		t.Fatalf("expected the January 2026 UVI, got the no-data page: %s", body)
	}
	if !strings.Contains(string(body), "Januar 2026") {
		t.Errorf("expected the page to default to Januar 2026 (the latest month with data), got: %s", body)
	}
}

// TestOperatorCanBrowseAnyUnitsUVI covers UI-02: an operator may open any
// unit's UVI, defaults to the first configured unit when no ?unit= is
// given, and gets working Vor-/nächste-Wohnung navigation links at the
// non-boundary end and none at all past the boundary (see also
// TestTenantUnitNavigationAlwaysDisabled for the tenant side of the same
// page).
func TestOperatorCanBrowseAnyUnitsUVI(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-02-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Wohnung A", AreaM2: 50},
			{ID: "u2", Name: "Wohnung B", AreaM2: 50},
		},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
			{ID: "mp2", UnitID: "u2", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
			{Number: "90000002", MeterPointID: "mp2", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, e := range []struct {
		meter, day string
		value      int64
	}{
		{"90000001", "2025-12-31", 100}, {"90000001", "2026-01-31", 130},
		{"90000002", "2025-12-31", 200}, {"90000002", "2026-01-31", 260},
	} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, e.value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: e.meter, Day: mustDayT(t, e.day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	get := func(path string) string {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}

	// No ?unit=: defaults to the first configured unit.
	body := get("/uvi")
	if !strings.Contains(body, "Wohnung A") {
		t.Errorf("expected the default /uvi to show Wohnung A, got: %s", body)
	}
	if strings.Contains(body, `aria-label="Vorherige Wohnung"`) {
		t.Error("first unit: 'vorherige Wohnung' should not be a link (no unit before it)")
	}
	if !strings.Contains(body, `aria-label="Nächste Wohnung"`) || !strings.Contains(body, "unit=u2") {
		t.Errorf("first unit: expected a working link to the next unit (u2), got: %s", body)
	}

	// Explicit ?unit=u2: the second unit, now at the other boundary.
	body = get("/uvi?unit=u2")
	if !strings.Contains(body, "Wohnung B") {
		t.Errorf("expected /uvi?unit=u2 to show Wohnung B, got: %s", body)
	}
	if !strings.Contains(body, "unit=u1") {
		t.Errorf("second unit: expected a working link back to the first unit (u1), got: %s", body)
	}
	if strings.Contains(body, `aria-label="Nächste Wohnung"`) {
		t.Error("last unit: 'nächste Wohnung' should not be a link (no unit after it)")
	}
}

// TestTenantUnitNavigationAlwaysDisabled covers the other half of UI-02: a
// tenant sees the same Wohnung-navigation buttons (so the page looks
// consistent regardless of role) but they are never links, and an
// explicit ?unit= for another unit is silently ignored rather than
// leaking that unit's data.
func TestTenantUnitNavigationAlwaysDisabled(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-02-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Wohnung A", AreaM2: 50},
			{ID: "u2", Name: "Wohnung B", AreaM2: 50},
		},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
			{ID: "mp2", UnitID: "u2", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
			{Number: "90000002", MeterPointID: "mp2", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
		Accesses: []masterdata.Access{
			{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, e := range []struct {
		meter, day string
		value      int64
	}{
		{"90000001", "2025-12-31", 100}, {"90000001", "2026-01-31", 130},
		{"90000002", "2025-12-31", 200}, {"90000002", "2026-01-31", 260},
	} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, e.value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: e.meter, Day: mustDayT(t, e.day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Trying to view the other unit through the URL must not work: the
	// tenant still only ever sees their own unit's data.
	resp, err := client.Get(srv.URL + "/uvi?unit=u2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "Wohnung B") {
		t.Errorf("?unit=u2 must not leak another unit's data to a tenant, got: %s", body)
	}
	if !strings.Contains(string(body), "Wohnung A") {
		t.Errorf("expected the tenant's own unit (Wohnung A) regardless of ?unit=, got: %s", body)
	}
	if strings.Contains(string(body), `aria-label="Vorherige Wohnung"`) || strings.Contains(string(body), `aria-label="Nächste Wohnung"`) {
		t.Errorf("a tenant's unit-navigation buttons must never be links, got: %s", body)
	}
}

// Note: a tenant whose access has already ended before "today" cannot log
// in at all (see internal/access/token.go), so the handler's AccessEnd
// bound on the default month can never actually be exercised through a
// real login — see internal/billing's own test for that bound instead
// (LatestAvailableMonth's notAfter parameter, which the handler feeds
// min(today, AccessEnd) into).

// TestUVIMonthNavigationStopsAtTheDataBoundary covers the paging fix: the
// arrows must not offer a step that lands outside the months the archive
// actually holds. Before this, paging one month too far produced a
// standalone "no data" page with no navigation on it, leaving the browser
// back button as the only way out.
func TestUVIMonthNavigationStopsAtTheDataBoundary(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-06-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Wohnung A", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Data spans January..March 2026 only.
	for _, e := range []struct {
		day   string
		value int64
	}{
		{"2026-01-31", 10}, {"2026-02-28", 25}, {"2026-03-31", 40},
	} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, e.value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: mustDayT(t, e.day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)

	// Newest month with data: the forward arrow must be inert, the
	// backward one live.
	newest := getAuditBody(t, client, srv.URL+"/uvi?month=2026-03&unit=u1")
	if strings.Contains(newest, `aria-label="Folgemonat"`) {
		t.Error("the newest month must not offer a step forward")
	}
	if !strings.Contains(newest, `aria-label="Vormonat"`) {
		t.Error("the newest month should still offer a step back")
	}

	// Oldest month with data: mirror image.
	oldest := getAuditBody(t, client, srv.URL+"/uvi?month=2026-01&unit=u1")
	if strings.Contains(oldest, `aria-label="Vormonat"`) {
		t.Error("the oldest month must not offer a step back")
	}

	// A month past the end, reached by a stale or hand-edited URL, is
	// clamped back into range rather than rendering a dead end.
	beyond := getAuditBody(t, client, srv.URL+"/uvi?month=2026-12&unit=u1")
	if !strings.Contains(beyond, "März 2026") {
		t.Errorf("an out-of-range month should clamp to the newest available, got: %s", beyond)
	}
}

// TestUVISurvivesMeterPointWithoutMeter is the end-to-end half of
// allowing a meter point to exist before its device does: every
// evaluating path (report, building comparison, charts, readings table)
// must treat such a point as having no readings, not assume one exists.
func TestUVISurvivesMeterPointWithoutMeter(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-03-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Wohnung A", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeating},
			// Configured, no device mounted yet — and of a kind nothing
			// else in the unit covers, so it cannot hide behind a sibling.
			{ID: "mp2", UnitID: "u1", Room: "Bad", Kind: masterdata.KindColdWater},
		},
		Meters: []masterdata.Meter{
			{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, e := range []struct {
		day   string
		value int64
	}{{"2026-01-31", 10}, {"2026-02-28", 25}, {"2026-03-31", 40}} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, e.value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: mustDayT(t, e.day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	body := getAuditBody(t, client, srv.URL+"/uvi?month=2026-02&unit=u1")

	// The heating half still evaluates normally...
	if !strings.Contains(body, "Heizung") {
		t.Errorf("the unit's real meter point should still report, got: %s", body)
	}
	// ...and the empty cold-water point contributes nothing rather than
	// producing a section built on a meter that does not exist.
	if strings.Contains(body, "Kaltwasser") {
		t.Error("a meter point with no meter must not produce a Verbrauchsart section")
	}

	// The other operator pages that walk meter points must survive it too.
	for _, path := range []string{"/operator/meterstatus", "/operator/masterdata"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 with a meter-less meter point configured", path, resp.StatusCode)
		}
	}
}
