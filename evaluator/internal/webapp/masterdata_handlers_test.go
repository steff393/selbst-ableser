package webapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// postMasterDataGrids submits both grids together, exactly as the browser
// does (one shared form, one submit) — see handleMasterDataSave for why
// units and the combined meter-point/meter grid cannot be saved
// independently.
func postMasterDataGrids(t *testing.T, client *http.Client, srvURL, csrfToken string, units []unitRow, meterGrid []meterGridRow) string {
	t.Helper()
	unitsJSON, err := json.Marshal(units)
	if err != nil {
		t.Fatal(err)
	}
	metersJSON, err := json.Marshal(meterGrid)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.PostForm(srvURL+"/operator/masterdata", url.Values{
		"csrf_token":  {csrfToken},
		"units_json":  {string(unitsJSON)},
		"meters_json": {string(metersJSON)},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	return readBody(t, resp)
}

func TestMasterDataSaveCreatesNewMeterPointAndItsFirstMeterTogether(t *testing.T) {
	// This is exactly the workflow that used to be impossible: a meter
	// point and its first meter are both brand new, so neither can be
	// saved before the other exists. Submitted together in one row,
	// Validate sees a single consistent graph and both are accepted.
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{{
		MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water",
		Number: "10000001", InstalledAt: "01.03.2025",
	}}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if strings.Contains(body, "Prüfung fehlgeschlagen") {
		t.Fatalf("expected the combined save to succeed, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	if len(md.Units) != 1 || len(md.MeterPoints) != 1 || len(md.Meters) != 1 {
		t.Fatalf("md after save = %+v, want one of each", md)
	}
	if md.Meters[0].InstalledAt != "2025-03-01" {
		t.Errorf("InstalledAt = %q, want the German-format date converted to 2025-03-01", md.Meters[0].InstalledAt)
	}
}

func TestMasterDataSaveAllowsEmptyAESKey(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{{
		MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water",
		Number: "10000001", InstalledAt: "2025-03-01", AESKey: "",
	}}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if strings.Contains(body, "AES-Schlüssel muss") {
		t.Fatalf("expected an empty AES key to be accepted, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	if len(md.Meters) != 1 || md.Meters[0].AESKey != ([16]byte{}) {
		t.Fatalf("meter after save = %+v, want a zero-value AES key", md.Meters)
	}
}

func TestMasterDataSaveRejectsBadAESKey(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{{
		MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water",
		Number: "10000001", InstalledAt: "2025-03-01", AESKey: "not-hex",
	}}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if !strings.Contains(body, "AES-Schlüssel muss leer oder genau 32 Hex-Zeichen sein") {
		t.Errorf("expected the bad-AES-key error, got: %s", body)
	}
}

func TestMasterDataSaveAcceptsResetMonth(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{{
		MeterPointID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: "heating",
		Number: "10000001", InstalledAt: "2025-03-01", ResetMonth: "6",
	}}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if strings.Contains(body, "Prüfung fehlgeschlagen") || strings.Contains(body, "ungültiger Stichtag-Monat") {
		t.Fatalf("expected reset month 6 to be accepted, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	if len(md.Meters) != 1 || md.Meters[0].ResetMonth != 6 {
		t.Fatalf("meter after save = %+v, want ResetMonth 6", md.Meters)
	}
}

func TestMasterDataSaveRejectsResetMonthOutOfRange(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{{
		MeterPointID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: "heating",
		Number: "10000001", InstalledAt: "2025-03-01", ResetMonth: "13",
	}}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if !strings.Contains(body, "ungültiger Stichtag-Monat") {
		t.Errorf("expected the bad-reset-month error, got: %s", body)
	}
}

func TestMasterDataSaveAcceptsGermanAndISODates(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{
		{MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water", Number: "10000001", InstalledAt: "15.03.2025"},
		{MeterPointID: "mp2", UnitID: "u1", Room: "Küche", Kind: "cold_water", Number: "10000002", InstalledAt: "2025-03-15"},
	}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if strings.Contains(body, "Prüfung fehlgeschlagen") || strings.Contains(body, "ungültiges Einbaudatum") {
		t.Fatalf("expected both date formats to be accepted, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	for _, m := range md.Meters {
		if m.InstalledAt != "2025-03-15" {
			t.Errorf("meter %s InstalledAt = %q, want 2025-03-15", m.Number, m.InstalledAt)
		}
	}
}

func TestMasterDataSaveRejectsEmptyUnitID(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken,
		[]unitRow{{ID: "", Name: "ohne ID", AreaM2: "10"}}, nil)
	if !strings.Contains(body, "ID darf nicht leer sein") {
		t.Errorf("expected the empty-ID error, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked")
	}
	if len(md.Units) != 0 {
		t.Errorf("invalid row must not have been saved, got %+v", md.Units)
	}
}

func TestMasterDataSaveRejectsUnknownKind(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken,
		nil, []meterGridRow{{MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "solar", Number: "10000001", InstalledAt: "2025-03-01"}})
	if !strings.Contains(body, "unbekannte Verbrauchsart") {
		t.Errorf("expected the unknown-kind error, got: %s", body)
	}
}

// TestMasterDataSaveAcceptsMeterPointWithNoMeter: laying out the rooms
// before the devices are mounted is a normal working state, so a row that
// establishes a meter point and names no meter must save as a meter point
// on its own rather than being rejected (STAMM-02 reports the gap, it
// does not forbid it).
func TestMasterDataSaveAcceptsMeterPointWithNoMeter(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	postMasterDataGrids(t, client, srv.URL, sess.CSRFToken,
		[]unitRow{{ID: "u1", Name: "Wohnung 1", AreaM2: "50"}},
		[]meterGridRow{{MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water"}})

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault should still be unlocked")
	}
	if len(md.MeterPoints) != 1 || md.MeterPoints[0].ID != "mp1" {
		t.Fatalf("the meter point should have been saved, got: %+v", md.MeterPoints)
	}
	if len(md.Meters) != 0 {
		t.Errorf("no meter should have been invented, got: %+v", md.Meters)
	}
}

func TestMasterDataSaveKeepsMeterHistoryAcrossRowsForSamePoint(t *testing.T) {
	// Two rows sharing the same MeterPointID represent that point's
	// meter history, matching how the grid already showed one row per
	// meter before meter points had their own fields too.
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{
		{
			MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water",
			Number: "10000001", InstalledAt: "01.01.2020", RemovedAt: "01.03.2025", EndReading: "500",
		},
		{
			MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water",
			Number: "10000002", InstalledAt: "01.03.2025",
		},
	}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if strings.Contains(body, "Prüfung fehlgeschlagen") {
		t.Fatalf("expected the two-meter history to be accepted, got: %s", body)
	}

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	if len(md.MeterPoints) != 1 {
		t.Fatalf("meter points = %+v, want exactly one (shared by both meters)", md.MeterPoints)
	}
	if len(md.Meters) != 2 {
		t.Fatalf("meters = %+v, want two", md.Meters)
	}
}

func TestMasterDataSaveRejectsInconsistentPointFieldsAcrossRows(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	units := []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}}
	grid := []meterGridRow{
		{MeterPointID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water", Number: "10000001", InstalledAt: "01.01.2020", RemovedAt: "01.03.2025", EndReading: "500"},
		{MeterPointID: "mp1", UnitID: "u1", Room: "GEÄNDERT", Kind: "hot_water", Number: "10000002", InstalledAt: "01.03.2025"},
	}
	body := postMasterDataGrids(t, client, srv.URL, sess.CSRFToken, units, grid)
	if !strings.Contains(body, "stimmen nicht") {
		t.Errorf("expected the inconsistent-point-fields error, got: %s", body)
	}
}

// TestMasterDataSaveChecksCSRFBeforeVaultLock and
// TestBuildingSaveChecksCSRFBeforeVaultLock lock in the order Prüfpunkt
// 6.A asked for: CSRF immediately after the role check, ahead of any
// vault access. A locked vault plus a missing CSRF token must fail as
// "bad request" (the CSRF path), not render the "Gesperrt" page (which
// would mean Vault.Get() ran first).
func TestMasterDataSaveChecksCSRFBeforeVaultLock(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	app.Vault.Lock() // undo the side effect of operatorClient's login-as-unlock

	resp, err := client.PostForm(srv.URL+"/operator/masterdata", url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d — got the locked page instead, meaning the vault was checked first", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestBuildingSaveChecksCSRFBeforeVaultLock(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	app.Vault.Lock()

	resp, err := client.PostForm(srv.URL+"/operator/masterdata/building", url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d — got the locked page instead, meaning the vault was checked first", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestMasterDataSaveRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/operator/masterdata", url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}

func TestParseGridDayAcceptsBothFormats(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2025-03-15", "2025-03-15"},
		{"15.03.2025", "2025-03-15"},
		{"1.3.2025", "2025-03-01"}, // single-digit day and month
		{"01.03.2025", "2025-03-01"},
	}
	for _, c := range cases {
		got, err := parseGridDay(c.in)
		if err != nil {
			t.Errorf("parseGridDay(%q): %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("parseGridDay(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseGridDayRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "not a date", "31.13.2025", "2025/03/15"} {
		if _, err := parseGridDay(in); err == nil {
			t.Errorf("parseGridDay(%q): expected an error", in)
		}
	}
}

func TestFormatGridDayIsParseGridDaysInverse(t *testing.T) {
	d, err := parseGridDay("2025-03-15")
	if err != nil {
		t.Fatal(err)
	}
	if got := formatGridDay(d); got != "15.03.2025" {
		t.Errorf("formatGridDay(%q) = %q, want 15.03.2025", d, got)
	}
}
