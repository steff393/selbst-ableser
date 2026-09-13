package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// unlockedOperatorClient is the preamble every test here shares: a
// running server, an unlocked vault, and a logged-in operator whose CSRF
// token the forms need.
func unlockedOperatorClient(t *testing.T) (*App, *http.Client, string, string) {
	t.Helper()
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	srv := httptest.NewServer(app.Routes())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)
	return app, client, srv.URL, sess.CSRFToken
}

// seedStructure gives the Anlage tab something to show: two units, three
// meter points including one that stands empty, a house meter that
// belongs to no unit, and one meter point with a history.
func seedStructure(t *testing.T, app *App) {
	t.Helper()
	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault locked")
	}
	removed := mustGridDay(t, "15.02.2026")
	end := int64(412)
	md.Units = []masterdata.Unit{
		{ID: "1", Name: "EG links", AreaM2: 62.4},
		{ID: "2", Name: "OG", AreaM2: 74},
	}
	md.MeterPoints = []masterdata.MeterPoint{
		{ID: "101", UnitID: "1", Room: "WZ", Kind: masterdata.KindHeatCostAllocator},
		{ID: "102", UnitID: "1", Room: "SZ", Kind: masterdata.KindHeatCostAllocator},
		{ID: "201", UnitID: "2", Room: "KUE", Kind: masterdata.KindHeatCostAllocator},
		{ID: "H01", Room: "Heizraum", Kind: masterdata.KindHeatMeter},
	}
	md.Meters = []masterdata.Meter{
		{Number: "10004711", MeterPointID: "101", KCFactor: 1.2, FreezeMonth: 7},
		{Number: "10004690", MeterPointID: "201", RemovedAt: &removed, EndReading: &end},
		{Number: "10004718", MeterPointID: "201", InstalledAt: removed},
		{Number: "20001100", MeterPointID: "H01"},
	}
	if err := app.Vault.Save(app.MasterDataPath, md); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func mustGridDay(t *testing.T, s string) telegram.Day {
	t.Helper()
	day, err := parseGridDay(s)
	if err != nil {
		t.Fatalf("parseGridDay(%q): %v", s, err)
	}
	return day
}

// TestAnlageTabShowsTheInstallation: the structure view has to answer
// "what is where, and what is still missing" at a glance — every meter
// point of a unit, the device at it, the ones before it, and the meter
// point that has none.
func TestAnlageTabShowsTheInstallation(t *testing.T) {
	app, client, srvURL, _ := unlockedOperatorClient(t)
	seedStructure(t, app)

	resp, err := client.Get(srvURL + "/operator/masterdata")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	for _, want := range []string{
		"EG links",        // the unit
		"101",             // its meter point
		"10004711",        // the device there now
		"kein Zähler",     // the meter point that stands empty
		"10004690",        // the predecessor at 201, still reachable
		"Zählerwechsel",   // the action a taken meter point offers
		"Zähler zuordnen", // the action an empty one offers
		"Heizraum",        // the house meter's place
		"20001100",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Anlage tab does not mention %q", want)
		}
	}
	if strings.Contains(body, "units-grid") {
		t.Error("the Anlage tab must not carry the Tabellen tab's grid")
	}
}

// TestEachTableHasItsOwnTab: five tabs, each carrying exactly its own
// thing. A tab that also rendered its neighbours would defeat the point
// of splitting them, and one that rendered the Anlage tab's dialogs would
// offer edits it cannot save.
func TestEachTableHasItsOwnTab(t *testing.T) {
	app, client, srvURL, _ := unlockedOperatorClient(t)
	seedStructure(t, app)

	for _, c := range []struct {
		tab     string
		wants   []string
		unwants []string
	}{
		{"units", []string{"units-grid", "Wohnungen speichern"}, []string{"meters-grid", "sa-dlg-meter"}},
		{"meterpoints", []string{"meterpoints-grid", "Zählerplätze speichern"}, []string{"units-grid", "sa-dlg-meter"}},
		{"meters", []string{"meters-grid", "Zähler speichern", "Nur aktuelle Zähler"}, []string{"units-grid", "sa-dlg-meter"}},
		{"swap", []string{"Wechselliste", "Zählerwechsel übernehmen"}, []string{"units-grid", "meters-grid"}},
	} {
		resp, err := client.Get(srvURL + "/operator/masterdata?tab=" + c.tab)
		if err != nil {
			t.Fatalf("GET %s: %v", c.tab, err)
		}
		body := readBody(t, resp)
		resp.Body.Close()
		for _, want := range c.wants {
			if !strings.Contains(body, want) {
				t.Errorf("tab %s does not mention %q", c.tab, want)
			}
		}
		for _, unwanted := range c.unwants {
			if strings.Contains(body, unwanted) {
				t.Errorf("tab %s carries %q, which belongs somewhere else", c.tab, unwanted)
			}
		}
	}
}

// TestEachTableSavesOnItsOwn is what the tabs promise: saving one table
// leaves the other two exactly as they were.
func TestEachTableSavesOnItsOwn(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	before, _ := app.Vault.Get()
	rows, err := json.Marshal([]unitRow{
		{ID: "1", Name: "EG links, neu benannt", AreaM2: "62,4"},
		{ID: "2", Name: "OG", AreaM2: "74"},
	})
	if err != nil {
		t.Fatal(err)
	}
	post(t, client, srvURL+"/operator/masterdata", url.Values{
		"csrf_token": {csrf},
		"table":      {"units"},
		"rows":       {string(rows)},
	})

	after, _ := app.Vault.Get()
	if after.Units[0].Name != "EG links, neu benannt" {
		t.Errorf("unit = %+v, want the new name", after.Units[0])
	}
	if len(after.MeterPoints) != len(before.MeterPoints) || len(after.Meters) != len(before.Meters) {
		t.Error("saving one table must leave the other two alone")
	}
}

// TestRenamingAUnitCarriesEverythingAlong is E-22: the meter points move,
// and so do the access grants. Leaving the grants behind would be the
// worse half — the unit would look right while a tenant sees somebody
// else's consumption information.
func TestRenamingAUnitCarriesEverythingAlong(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	md, _ := app.Vault.Get()
	md.Accesses = []masterdata.Access{{Token: "abcdefghijkl", UnitID: "1", Start: "2026-01-01"}}
	if err := app.Vault.Save(app.MasterDataPath, md); err != nil {
		t.Fatalf("Save: %v", err)
	}

	post(t, client, srvURL+"/operator/masterdata/unit", url.Values{
		"csrf_token":  {csrf},
		"original_id": {"1"},
		"id":          {"5"},
		"name":        {"EG links"},
		"area":        {"62,4"},
	})

	md, _ = app.Vault.Get()
	if md.Units[0].ID != "5" {
		t.Fatalf("unit = %+v, want the new ID", md.Units[0])
	}
	for _, mp := range md.MeterPoints {
		if mp.ID == "101" && mp.UnitID != "5" {
			t.Errorf("meter point 101 stayed at unit %q", mp.UnitID)
		}
	}
	if md.Accesses[0].UnitID != "5" {
		t.Errorf("the access grant stayed at unit %q — the tenant would now see another flat", md.Accesses[0].UnitID)
	}
}

func TestDeletingAUnitThatStillHasMeterPointsIsRefused(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	body := post(t, client, srvURL+"/operator/masterdata/unit/delete", url.Values{
		"csrf_token": {csrf},
		"id":         {"1"},
	})
	if !strings.Contains(body, "Zählerplätze") {
		t.Errorf("expected the refusal to name what is in the way, got: %s", firstLines(body))
	}
	md, _ := app.Vault.Get()
	if len(md.Units) != 2 {
		t.Errorf("nothing may have been deleted, units = %+v", md.Units)
	}
}

// TestSwapClosesTheOldDeviceAndInstallsTheNew: both halves happen
// together, on the same day, or not at all.
func TestSwapClosesTheOldDeviceAndInstallsTheNew(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	post(t, client, srvURL+"/operator/masterdata/meter/swap", url.Values{
		"csrf_token":           {csrf},
		"meter_point_id":       {"101"},
		"day":                  {"01.09.2026"},
		"outgoing_end_reading": {"510"},
		"number":               {"10004733"},
		"start_reading":        {"0"},
		"kc_factor":            {"1,2"},
		"freeze_month":         {"7"},
	})

	md, _ := app.Vault.Get()
	var outgoing, incoming *masterdata.Meter
	for i := range md.Meters {
		switch md.Meters[i].Number {
		case "10004711":
			outgoing = &md.Meters[i]
		case "10004733":
			incoming = &md.Meters[i]
		}
	}
	if outgoing == nil || outgoing.RemovedAt == nil || *outgoing.RemovedAt != "2026-09-01" || outgoing.EndReading == nil || *outgoing.EndReading != 510 {
		t.Fatalf("outgoing meter = %+v, want closed off on 2026-09-01 with 510", outgoing)
	}
	if incoming == nil || incoming.MeterPointID != "101" || incoming.InstalledAt != "2026-09-01" {
		t.Fatalf("incoming meter = %+v, want installed at 101 on the same day", incoming)
	}
	if incoming.FreezeMonth != 7 {
		t.Errorf("FreezeMonth = %d, want the 7 the dialog carried over", incoming.FreezeMonth)
	}
}

func TestSwapWithoutAnEndReadingIsRefused(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	body := post(t, client, srvURL+"/operator/masterdata/meter/swap", url.Values{
		"csrf_token":     {csrf},
		"meter_point_id": {"101"},
		"day":            {"01.09.2026"},
		"number":         {"10004733"},
	})
	if !strings.Contains(body, "Endstand") {
		t.Errorf("expected the refusal to be about the missing end reading, got: %s", firstLines(body))
	}
	md, _ := app.Vault.Get()
	if len(md.Meters) != 4 {
		t.Errorf("nothing may have been written, meters = %d", len(md.Meters))
	}
}

// TestCorrectingARemovalDateMovesTheSuccessor: the day one device comes
// out is the day the next goes in, so correcting one moves the other and
// no gap can appear between them.
func TestCorrectingARemovalDateMovesTheSuccessor(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	post(t, client, srvURL+"/operator/masterdata/meter", url.Values{
		"csrf_token":      {csrf},
		"original_number": {"10004690"},
		"number":          {"10004690"},
		"start_reading":   {"0"},
		"removed_at":      {"01.03.2026"},
		"end_reading":     {"412"},
	})

	md, _ := app.Vault.Get()
	for _, m := range md.Meters {
		if m.Number == "10004690" && (m.RemovedAt == nil || *m.RemovedAt != "2026-03-01") {
			t.Errorf("removal date = %v, want 2026-03-01", m.RemovedAt)
		}
		if m.Number == "10004718" && m.InstalledAt != "2026-03-01" {
			t.Errorf("the successor's installation = %q, want it to have moved along to 2026-03-01", m.InstalledAt)
		}
	}
}

func post(t *testing.T, client *http.Client, url string, form url.Values) string {
	t.Helper()
	resp, err := client.PostForm(url, form)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	return readBody(t, resp)
}

func firstLines(body string) string {
	if i := strings.Index(body, "class=\"error\""); i >= 0 {
		end := i + 200
		if end > len(body) {
			end = len(body)
		}
		return body[i:end]
	}
	if len(body) > 200 {
		return body[:200]
	}
	return body
}

// TestAMisassignedMeterCanBeMovedToAFreePlace closes a gap the rebuild
// had opened: the old combined grid let a meter point be corrected by
// editing the cell, and for a while afterwards nothing did. A wrong
// assignment is a correction (UC-9), not a swap — as long as the target
// is free.
func TestAMisassignedMeterCanBeMovedToAFreePlace(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	post(t, client, srvURL+"/operator/masterdata/meter", url.Values{
		"csrf_token":      {csrf},
		"original_number": {"10004711"},
		"number":          {"10004711"},
		"meter_point_id":  {"102"}, // stands empty in the fixture
		"start_reading":   {"0"},
	})

	md, _ := app.Vault.Get()
	for _, m := range md.Meters {
		if m.Number == "10004711" && m.MeterPointID != "102" {
			t.Errorf("meter is still at %q, want it moved to the free meter point", m.MeterPointID)
		}
	}
}

func TestMovingAMeterOntoATakenPlaceIsRefused(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	body := post(t, client, srvURL+"/operator/masterdata/meter", url.Values{
		"csrf_token":      {csrf},
		"original_number": {"10004711"},
		"number":          {"10004711"},
		"meter_point_id":  {"201"}, // 10004718 hangs there
		"start_reading":   {"0"},
	})
	if !strings.Contains(body, "Zählerwechsel") {
		t.Errorf("expected a refusal that names the swap, got: %s", firstLines(body))
	}
	md, _ := app.Vault.Get()
	for _, m := range md.Meters {
		if m.Number == "10004711" && m.MeterPointID != "101" {
			t.Errorf("meter moved to %q although the target was taken", m.MeterPointID)
		}
	}
}

// TestRenamingAMeterPointInTheGridSaysWhereToDoIt: in a table that
// replaces the whole list, a changed ID cannot be told apart from
// "deleted and a new one added", and everything that pointed at the old
// one would dangle. Validate catches it either way; the point of this
// test is that the operator is told what to do instead.
func TestRenamingAMeterPointInTheGridSaysWhereToDoIt(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	rows, err := json.Marshal([]meterPointRow{
		{ID: "999", UnitID: "1", Room: "WZ", Kind: "heating"}, // war 101
		{ID: "102", UnitID: "1", Room: "SZ", Kind: "heating"},
		{ID: "201", UnitID: "2", Room: "KUE", Kind: "heating"},
		{ID: "H01", Room: "Heizraum", Kind: "heat_meter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := post(t, client, srvURL+"/operator/masterdata", url.Values{
		"csrf_token": {csrf},
		"table":      {"meterpoints"},
		"rows":       {string(rows)},
	})
	if !strings.Contains(body, "Reiter Gesamtansicht") {
		t.Errorf("expected the refusal to point at the tab that renames properly, got: %s", firstLines(body))
	}

	md, _ := app.Vault.Get()
	for _, mp := range md.MeterPoints {
		if mp.ID == "999" {
			t.Error("nothing may have been saved")
		}
	}
}

// TestRenamingAUnitEndsItsTenantSessions closes a disclosure the rename
// cascade opened. A tenant's session carries the unit ID it was opened
// for. After a rename that string means something else — and with two IDs
// exchanged, it means the other flat, whose consumption the tenant would
// then be shown. The grant travels with the rename, so logging in again
// with the unchanged token lands where it should.
func TestRenamingAUnitEndsItsTenantSessions(t *testing.T) {
	app, client, srvURL, csrf := unlockedOperatorClient(t)
	seedStructure(t, app)

	tenant, err := app.Sessions.CreateTenant("1", "2026-01-01", nil)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	other, err := app.Sessions.CreateTenant("2", "2026-01-01", nil)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	post(t, client, srvURL+"/operator/masterdata/unit", url.Values{
		"csrf_token":  {csrf},
		"original_id": {"1"},
		"id":          {"5"},
		"name":        {"EG links"},
		"area":        {"62,4"},
	})

	if _, ok := app.Sessions.Lookup(tenant.ID); ok {
		t.Error("the session for the renamed unit is still live — it would now resolve to a different flat")
	}
	if _, ok := app.Sessions.Lookup(other.ID); !ok {
		t.Error("an unrelated tenant's session must not be ended")
	}
}
