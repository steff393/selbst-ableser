package webapp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

// twoUnitInstallation seeds two units of different size with heating
// meters, so the building overview has something to compare.
func twoUnitInstallation(t *testing.T) (*App, *http.Client, string) {
	t.Helper()
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-03-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Building: masterdata.Building{Name: "Musterhaus", HeatingKWhPerUnit: 2},
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Wohnung A", AreaM2: 100},
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
	// February consumption: u1 uses 20 over 100 m² (0.20/m²), u2 uses 20
	// over 50 m² (0.40/m²) — same absolute figure, opposite sides of the
	// building average, which is what the comparison column must show.
	for _, e := range []struct {
		meter, day string
		value      int64
	}{
		{"90000001", "2026-01-31", 10}, {"90000001", "2026-02-28", 30},
		{"90000002", "2026-01-31", 10}, {"90000002", "2026-02-28", 30},
	} {
		rawHex := buildEncryptedTelegramHexForExportTest(t, key, e.value)
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: e.meter, Day: mustDayT(t, e.day), RawHex: rawHex}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	return app, client, srv.URL
}

// TestOperatorLandsOnTheBuildingOverview: an operator opening the UVI
// without naming a unit gets the whole installation first, and pages into
// individual units from there (UI-02).
func TestOperatorLandsOnTheBuildingOverview(t *testing.T) {
	_, client, base := twoUnitInstallation(t)

	body := getAuditBody(t, client, base+"/uvi?month=2026-02")

	if !strings.Contains(body, "Anlagenübersicht") {
		t.Errorf("expected the installation overview, got: %s", body)
	}
	if !strings.Contains(body, "Musterhaus") {
		t.Error("the overview should name the building")
	}
	for _, want := range []string{"Wohnung A", "Wohnung B"} {
		if !strings.Contains(body, want) {
			t.Errorf("the overview should list %s", want)
		}
	}
	// The smaller unit used the same amount over half the area, so it must
	// come out above the building average and the larger one below.
	if !strings.Contains(body, "delta-up") || !strings.Contains(body, "delta-down") {
		t.Errorf("expected both an above- and a below-average unit, got: %s", body)
	}
}

// TestBuildingOverviewShowsAYearlyChart covers the installation-wide
// year-over-year chart (billing.BuildingYearOverYearSeries): it must
// render for a kind present anywhere in the building, carry both units'
// February consumption summed into its embedded JSON, and load the same
// client-side renderer the per-unit UVI page uses.
func TestBuildingOverviewShowsAYearlyChart(t *testing.T) {
	_, client, base := twoUnitInstallation(t)

	body := getAuditBody(t, client, base+"/uvi?month=2026-02")

	if !strings.Contains(body, "Jahresverlauf der Gesamtanlage") {
		t.Errorf("expected the building-wide chart section, got: %s", body)
	}
	if !strings.Contains(body, `id="uvi-chart-heating"`) {
		t.Errorf("expected the heating chart's mount point, got: %s", body)
	}
	if !strings.Contains(body, "SA_UVI_CHARTS") || !strings.Contains(body, "/static/uvi_chart.js") {
		t.Errorf("expected the chart data and renderer script, got: %s", body)
	}
	// Both units consumed 20 in February (30-10 each); the building series
	// must carry their sum, 40 — not either unit's own 20.
	if !strings.Contains(body, `"value":40`) {
		t.Errorf("expected the summed February value (40) in the chart JSON, got: %s", body)
	}
}

// TestBuildingOverviewIsOperatorOnly: a per-unit breakdown is exactly what
// SZ-4 keeps away from a tenant.
func TestBuildingOverviewIsOperatorOnly(t *testing.T) {
	app, mdPath := newTestApp(t)
	md := masterdata.MasterData{
		Units: []masterdata.Unit{
			{ID: "u1", Name: "Wohnung A", AreaM2: 50},
			{ID: "u2", Name: "Wohnung B", AreaM2: 50},
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

	client, srv := tenantClientFor(t, app, "aaaa-bbbb-cccc")
	body := getAuditBody(t, client, srv+"/uvi")

	if strings.Contains(body, "Anlagenübersicht") {
		t.Error("a tenant must never reach the installation overview")
	}
	if strings.Contains(body, "Wohnung B") {
		t.Error("a tenant must never see another unit named")
	}
}

// TestFirstUnitPagesBackToTheOverview closes the navigation loop: the
// overview sits in front of the units, so the first unit's back arrow
// returns to it instead of dead-ending.
func TestFirstUnitPagesBackToTheOverview(t *testing.T) {
	_, client, base := twoUnitInstallation(t)

	body := getAuditBody(t, client, base+"/uvi?month=2026-02&unit=u1")
	if !strings.Contains(body, `aria-label="Vorherige Wohnung"`) {
		t.Errorf("the first unit should offer a step back to the overview, got: %s", body)
	}
	if !strings.Contains(body, `href="/uvi?month=2026-02"`) {
		t.Errorf("that step should lead to the unit-less overview URL, got: %s", body)
	}
}
