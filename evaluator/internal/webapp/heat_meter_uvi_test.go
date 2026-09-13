package webapp

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/livepush"
	"selbst-ableser/internal/masterdata"
)

// qundisHeatTelegramHexWithTotal takes the real Qundis heat-meter telegram
// captured from meter 68817136 and rewrites only its 4-byte BCD total.
func qundisHeatTelegramHexWithTotal(t *testing.T, kWh int64) string {
	t.Helper()
	raw, err := hex.DecodeString("5944934436718168483778077936718168934448040DFF5F350082E100005E0107C006FFFF01000000FFFF000000005F38000000000080008000800080008000800080008000800080008000002F02FD170000046D33084B3949")
	if err != nil {
		t.Fatal(err)
	}
	digits := fmt.Sprintf("%08d", kWh)
	for i := 0; i < 4; i++ {
		pair := digits[6-2*i : 8-2*i]
		raw[37+i] = (pair[0]-'0')<<4 | (pair[1] - '0')
	}
	return hex.EncodeToString(raw)
}

// tenantUVIPage logs in with the unit's access code and returns the UVI
// page it lands on.
func tenantUVIPage(t *testing.T, app *App) string {
	t.Helper()
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"credential": {"aaaa-bbbb-cccc"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// A flat whose heating is measured by a heat meter sees that heating in
// its consumption information in kWh as read: not divided by 1000 like
// water, not shown a second time as a "converted" kWh hint, labelled as
// heating, and without the footnote claiming its kWh were calculated from
// allocator units — which for this flat would simply be untrue.
func TestUVIShowsAHeatMeterInKWh(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-12-05") }

	md := masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 80}},
		MeterPoints: []masterdata.MeterPoint{{ID: "mp1", UnitID: "u1", Room: "Fußbodenheizung", Kind: masterdata.KindHeatMeter}},
		Meters:      []masterdata.Meter{{Number: "68817136", MeterPointID: "mp1", InstalledAt: mustDayT(t, "2026-09-01")}},
		Accesses:    []masterdata.Access{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")}},
		Building:    masterdata.Building{HeatingKWhPerUnit: 1.42},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	for day, kWh := range map[string]int64{"2026-09-30": 1, "2026-10-31": 121, "2026-11-30": 1401} {
		entry := archive.Entry{MeterID: "68817136", Day: mustDayT(t, day), RawHex: qundisHeatTelegramHexWithTotal(t, kWh)}
		if _, err := app.Store.InsertHistorical(entry); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	page := tenantUVIPage(t, app)

	for _, want := range []string{
		"November 2026",
		"Heizung (Wärmemenge)",
		"1280 kWh",      // November: 1401 - 121, shown as counted
		"1401 kWh",      // the meter reading in the Zählerstände table
		"kWh/m²",        // the comparison is an energy comparison
		"direkt in kWh", // the footnote says what actually happened
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, unwanted := range []string{
		"1.280",                            // divided by 1000 as if it were liters
		"unbekannt",                        // no label for the kind
		`<div class="hint">1280 kWh</div>`, // the same figure again as a "converted" hint
		"Umrechnung von Heizkostenverteiler-Einheiten",
	} {
		if strings.Contains(page, unwanted) {
			t.Errorf("page contains %q", unwanted)
		}
	}
}

// The footnote change must not touch an installation with allocators: its
// wording there stays exactly what it was.
func TestUVIAllocatorFootnoteUnchanged(t *testing.T) {
	app, mdPath := newTestApp(t)
	app.Now = func() time.Time { return mustDayTimeT(t, "2026-08-15") }

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: masterdata.KindHeatCostAllocator}},
		Meters:      []masterdata.Meter{{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2024-01-01")}},
		Accesses:    []masterdata.Access{{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDayT(t, "2020-01-01")}},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	for day, value := range map[string]int64{"2025-12-31": 100, "2026-01-31": 130} {
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: mustDayT(t, day), RawHex: buildEncryptedTelegramHexForExportTest(t, key, value)}); err != nil {
			t.Fatalf("InsertHistorical: %v", err)
		}
	}

	page := tenantUVIPage(t, app)
	if !strings.Contains(page, "Umrechnung von Heizkostenverteiler-Einheiten") {
		t.Error("the allocator footnote is gone from an allocator installation")
	}
	if strings.Contains(page, "direkt in kWh") {
		t.Error("an allocator installation claims its kWh were measured")
	}
}

// The live view is where a newly connected heat meter is first checked:
// its row has to carry the kWh value and the device type the meter itself
// reports, not "n.a." and not the radio converter it transmits through.
func TestLiveRowShowsQundisHeatMeterValue(t *testing.T) {
	app, _ := newTestApp(t)
	raw, err := hex.DecodeString(qundisHeatTelegramHexWithTotal(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	row := app.buildLiveRow(livepush.Telegram{MeterID: "68817136", ReceivedAt: time.Now(), RawHex: hex.EncodeToString(raw)}, raw, masterdata.MasterData{}, false)
	if !row.Evaluable || row.Value != "1" {
		t.Errorf("value = %q (evaluable=%v), want 1", row.Value, row.Evaluable)
	}
	if row.DeviceType != "WMZ" {
		t.Errorf("device type = %q, want WMZ (from the enhanced identification, not RCM)", row.DeviceType)
	}
	if row.RSSIReported {
		t.Error("a telegram without signal strength reported one")
	}
}
