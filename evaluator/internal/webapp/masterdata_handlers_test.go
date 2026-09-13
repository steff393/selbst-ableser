package webapp

import (
	"encoding/json"
	"io"
	"net/http"
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

// postMasterDataGrids saves the three tables one after the other, in the
// order the dependencies run: a meter point needs its unit, a meter needs
// its meter point. That is how the tabs are used too — each table has its
// own save button.
//
// The bodies are returned joined, so a test can look for its error
// message without caring which of the three requests produced it.
func postMasterDataGrids(t *testing.T, client *http.Client, srvURL, csrfToken string, grids masterDataGrids) string {
	t.Helper()
	var bodies []string
	for _, table := range []struct {
		name string
		rows interface{}
	}{
		{"units", grids.Units},
		{"meterpoints", grids.MeterPoints},
		{"meters", grids.Meters},
	} {
		raw, err := json.Marshal(table.rows)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.PostForm(srvURL+"/operator/masterdata", url.Values{
			"csrf_token": {csrfToken},
			"table":      {table.name},
			"rows":       {string(raw)},
		})
		if err != nil {
			t.Fatalf("POST %s: %v", table.name, err)
		}
		bodies = append(bodies, readBody(t, resp))
		resp.Body.Close()
	}
	return strings.Join(bodies, "; ")
}

// oneMeterGrids is the smallest complete submission: one unit, one meter
// point, and one meter at it.
func oneMeterGrids(kind string, meter meterRow) masterDataGrids {
	meter.MeterPointID = "mp1"
	return masterDataGrids{
		Units:       []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}},
		MeterPoints: []meterPointRow{{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: kind}},
		Meters:      []meterRow{meter},
	}
}

func TestMasterDataSaveAcceptsTheThreeTablesInOrder(t *testing.T) {
	// Saved separately but checked together: each table is written on its
	// own, and masterdata.Validate sees the whole graph every time.
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("hot_water", meterRow{Number: "10000001", InstalledAt: "01.03.2025"}))
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
		t.Errorf("InstalledAt = %q, want the German-format date converted", md.Meters[0].InstalledAt)
	}
}

func TestMasterDataSaveAllowsEmptyAESKey(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("hot_water", meterRow{Number: "10000001", InstalledAt: "2025-03-01", AESKey: ""}))
	if strings.Contains(body, "AES-Schlüssel muss") {
		t.Fatalf("expected an empty AES key to be accepted, got: %s", body)
	}

	md, _ := app.Vault.Get()
	if len(md.Meters) != 1 || md.Meters[0].AESKey != ([16]byte{}) {
		t.Fatalf("meter after save = %+v, want a zero-value AES key", md.Meters)
	}
}

func TestMasterDataSaveRejectsBadAESKey(t *testing.T) {
	_, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("hot_water", meterRow{Number: "10000001", InstalledAt: "2025-03-01", AESKey: "not-hex"}))
	if !strings.Contains(body, "AES-Schlüssel muss leer oder genau 32 Hex-Zeichen sein") {
		t.Errorf("expected the bad-AES-key error, got: %s", body)
	}
}

func TestMasterDataSaveAcceptsFreezeMonth(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("heating", meterRow{Number: "10000001", InstalledAt: "2025-03-01", FreezeMonth: "6"}))
	if strings.Contains(body, "Prüfung fehlgeschlagen") || strings.Contains(body, "ungültiger Stichtag-Monat") {
		t.Fatalf("expected freeze month 6 to be accepted, got: %s", body)
	}

	md, _ := app.Vault.Get()
	if len(md.Meters) != 1 || md.Meters[0].FreezeMonth != 6 {
		t.Fatalf("meter after save = %+v, want FreezeMonth 6", md.Meters)
	}
}

func TestMasterDataSaveRejectsFreezeMonthOutOfRange(t *testing.T) {
	_, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("heating", meterRow{Number: "10000001", InstalledAt: "2025-03-01", FreezeMonth: "13"}))
	if !strings.Contains(body, "ungültiger Stichtag-Monat") {
		t.Errorf("expected the bad-freeze-month error, got: %s", body)
	}
}

// TestMasterDataSaveRejectsHalfARemoval: an end reading without a removal
// date, or the other way round, is half a swap and cannot be evaluated.
func TestMasterDataSaveRejectsHalfARemoval(t *testing.T) {
	_, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf,
		oneMeterGrids("heating", meterRow{Number: "10000001", InstalledAt: "2025-03-01", EndReading: "500"}))
	if !strings.Contains(body, "gehören zusammen") {
		t.Errorf("expected the half-removal error, got: %s", body)
	}
}

func TestMasterDataSaveAcceptsGermanAndISODates(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		Units: []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}},
		MeterPoints: []meterPointRow{
			{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water"},
			{ID: "mp2", UnitID: "u1", Room: "Küche", Kind: "cold_water"},
		},
		Meters: []meterRow{
			{Number: "10000001", MeterPointID: "mp1", InstalledAt: "15.03.2025"},
			{Number: "10000002", MeterPointID: "mp2", InstalledAt: "2025-03-15"},
		},
	})
	if strings.Contains(body, "Prüfung fehlgeschlagen") || strings.Contains(body, "ungültiges Einbaudatum") {
		t.Fatalf("expected both date formats to be accepted, got: %s", body)
	}

	md, _ := app.Vault.Get()
	for _, m := range md.Meters {
		if m.InstalledAt != "2025-03-15" {
			t.Errorf("meter %s InstalledAt = %q, want 2025-03-15", m.Number, m.InstalledAt)
		}
	}
}

func TestMasterDataSaveRejectsEmptyUnitID(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		Units: []unitRow{{ID: "", Name: "ohne ID", AreaM2: "10"}},
	})
	if !strings.Contains(body, "ID darf nicht leer sein") {
		t.Errorf("expected the empty-ID error, got: %s", body)
	}

	md, _ := app.Vault.Get()
	if len(md.Units) != 0 {
		t.Errorf("invalid row must not have been saved, got %+v", md.Units)
	}
}

func TestMasterDataSaveRejectsUnknownKind(t *testing.T) {
	_, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		MeterPoints: []meterPointRow{{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: "solar"}},
	})
	if !strings.Contains(body, "unbekannte Verbrauchsart") {
		t.Errorf("expected the unknown-kind error, got: %s", body)
	}
}

// TestMasterDataSaveAcceptsMeterPointWithoutMeterOrUnit covers the two
// states that are normal rather than broken: a meter point whose device
// is not mounted yet, and one that belongs to no unit at all because it
// measures the house.
func TestMasterDataSaveAcceptsMeterPointWithoutMeterOrUnit(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		Units: []unitRow{{ID: "u1", Name: "Wohnung 1", AreaM2: "50"}},
		MeterPoints: []meterPointRow{
			{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water"},
			{ID: "haus", Room: "Heizraum", Kind: "heat_meter"},
		},
	})
	if strings.Contains(body, "Prüfung fehlgeschlagen") {
		t.Fatalf("both meter points are legitimate, got: %s", body)
	}

	md, _ := app.Vault.Get()
	if len(md.MeterPoints) != 2 {
		t.Fatalf("meter points = %+v, want both", md.MeterPoints)
	}
	if len(md.Meters) != 0 {
		t.Errorf("no meter should have been invented, got: %+v", md.Meters)
	}
}

func TestMasterDataSaveRejectsDuplicateMeterPointID(t *testing.T) {
	_, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		Units: []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}},
		MeterPoints: []meterPointRow{
			{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water"},
			{ID: "mp1", UnitID: "u1", Room: "Küche", Kind: "cold_water"},
		},
	})
	if !strings.Contains(body, "kommt in mehreren Zeilen vor") {
		t.Errorf("expected the duplicate-ID error, got: %s", body)
	}
}

// TestMasterDataSaveKeepsMeterHistoryAtOnePoint: two devices at the same
// meter point, the first closed off on the day the second was installed.
func TestMasterDataSaveKeepsMeterHistoryAtOnePoint(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)

	body := postMasterDataGrids(t, client, srvURL, csrf, masterDataGrids{
		Units:       []unitRow{{ID: "u1", Name: "Erdgeschoss", AreaM2: "50"}},
		MeterPoints: []meterPointRow{{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: "hot_water"}},
		Meters: []meterRow{
			{Number: "10000001", MeterPointID: "mp1", InstalledAt: "01.01.2020", RemovedAt: "01.03.2025", EndReading: "500"},
			{Number: "10000002", MeterPointID: "mp1", InstalledAt: "01.03.2025"},
		},
	})
	if strings.Contains(body, "Prüfung fehlgeschlagen") {
		t.Fatalf("expected the two-meter history to be accepted, got: %s", body)
	}

	md, _ := app.Vault.Get()
	if len(md.MeterPoints) != 1 || len(md.Meters) != 2 {
		t.Fatalf("md = %d meter points, %d meters, want 1 and 2", len(md.MeterPoints), len(md.Meters))
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
