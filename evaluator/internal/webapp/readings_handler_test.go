package webapp

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// buildEncryptedTelegramHex and hcaPayload mirror internal/billing's own
// test helpers of the same name: a short-header (CI 0x7A) telegram whose
// payload is real AES-128-CBC ciphertext, so these tests exercise the
// real decrypt-and-decode path rather than a cleartext shortcut.
func buildEncryptedTelegramHex(t *testing.T, key [16]byte, plaintext []byte) string {
	t.Helper()
	if len(plaintext)%32 != 0 {
		t.Fatalf("plaintext length %d must be a multiple of 32", len(plaintext))
	}

	m := uint16(0x4493)
	a := [4]byte{0x58, 0x05, 0x74, 0x40}
	ver := byte(0x36)
	med := byte(0x08)
	acc := byte(0xE1)

	iv := make([]byte, 0, aes.BlockSize)
	iv = append(iv, byte(m), byte(m>>8))
	iv = append(iv, a[:]...)
	iv = append(iv, ver, med)
	for i := 0; i < 8; i++ {
		iv = append(iv, acc)
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)

	raw := []byte{byte(len(ciphertext) + 14), 0x44}
	raw = append(raw, byte(m), byte(m>>8))
	raw = append(raw, a[:]...)
	raw = append(raw, ver, med, 0x7A, acc, 0x18)
	raw = append(raw, 0x20, byte(len(plaintext)/16)<<4|0x05)
	raw = append(raw, ciphertext...)

	return hex.EncodeToString(raw)
}

func hcaPayload(bcdValue [3]byte) []byte {
	p := []byte{0x2F, 0x2F, 0x0B, 0x6E}
	p = append(p, bcdValue[:]...)
	for len(p) < 32 {
		p = append(p, 0x2F)
	}
	return p
}

func TestBuildReadingRowsResolvesAssignedMeter(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md, _ := app.Vault.Get()
	md.Units = append(md.Units, masterdata.Unit{ID: "u1", Name: "Erdgeschoss", AreaM2: 50})
	md.MeterPoints = append(md.MeterPoints, masterdata.MeterPoint{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: masterdata.KindHeating})
	md.Meters = append(md.Meters, masterdata.Meter{
		Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2025-01-01"),
	})
	if err := app.Vault.Save(mdPath, md); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00})) // -> 100
	day := mustDayT(t, "2025-06-15")
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: day, ReceivedAt: dayTimeT(t, day), RSSI: -80, RawHex: rawHex}); err != nil {
		t.Fatal(err)
	}

	md, _ = app.Vault.Get()
	rows, err := app.buildReadingRows(md, readingsFilter{From: mustDayT(t, "2025-01-01"), To: mustDayT(t, "2025-12-31")})
	if err != nil {
		t.Fatalf("buildReadingRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	row := rows[0]
	if row.UnitName != "Erdgeschoss" || row.MeterPointID != "mp1" || row.Room != "Bad" {
		t.Errorf("row master-data fields = %+v, want Erdgeschoss/mp1/Bad", row)
	}
	if !row.Evaluable || row.Value != 100 {
		t.Errorf("row.Evaluable=%v row.Value=%d, want true/100", row.Evaluable, row.Value)
	}
	if row.DeviceType != "HKV" {
		t.Errorf("DeviceType = %q, want HKV (medium byte 0x08 in the fixture)", row.DeviceType)
	}
	if row.Manufacturer != "QDS" || row.ManufacturerName != "Qundis GmbH" {
		t.Errorf("Manufacturer/ManufacturerName = %q/%q, want QDS/Qundis GmbH (M-field 0x4493 in the fixture)", row.Manufacturer, row.ManufacturerName)
	}
	if !strings.Contains(row.DecodeURL, hex.EncodeToString(key[:])) {
		t.Errorf("DecodeURL = %q, want it to contain the AES key", row.DecodeURL)
	}
}

func TestBuildReadingRowsShowsUnassignedMeterWithBlankFields(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	day := mustDayT(t, "2025-06-15")
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "99999999", Day: day, ReceivedAt: dayTimeT(t, day), RSSI: -80, RawHex: "aabbccdd"}); err != nil {
		t.Fatal(err)
	}

	md, _ := app.Vault.Get()
	rows, err := app.buildReadingRows(md, readingsFilter{From: mustDayT(t, "2025-01-01"), To: mustDayT(t, "2025-12-31")})
	if err != nil {
		t.Fatalf("buildReadingRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	row := rows[0]
	if row.UnitName != "" || row.MeterPointID != "" {
		t.Errorf("unassigned row should have blank master-data fields, got %+v", row)
	}
	if row.Evaluable {
		t.Errorf("garbage raw_hex should not be evaluable, got %+v", row)
	}
	if row.DeviceType != "" || row.Manufacturer != "" {
		t.Errorf("DeviceType/Manufacturer = %q/%q, want blank for a too-short raw_hex", row.DeviceType, row.Manufacturer)
	}
	if row.DecodeURL != "https://wmbusmeters.org/analyze/aabbccdd" {
		t.Errorf("DecodeURL = %q, want the no-key form", row.DecodeURL)
	}
}

// TestBuildReadingRowsAppliesMeterIDFilter covers the one filter dimension
// buildReadingRows still applies server-side: an explicit MeterIDs
// allow-list, as used by the CSV export to match exactly what readings.js
// currently has visible (every other filter — Wohnung/Zählerplatz/Typ/
// Hersteller/row selection — is client-side-only, see readings.js).
func TestBuildReadingRowsAppliesMeterIDFilter(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	day := mustDayT(t, "2025-06-15")
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00}))
	for _, meterID := range []string{"90000001", "90000002"} {
		if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: meterID, Day: day, ReceivedAt: dayTimeT(t, day), RSSI: -80, RawHex: rawHex}); err != nil {
			t.Fatal(err)
		}
	}

	md, _ := app.Vault.Get()
	base := readingsFilter{From: mustDayT(t, "2025-01-01"), To: mustDayT(t, "2025-12-31")}

	rows, err := app.buildReadingRows(md, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("no MeterIDs filter = %+v, want both meters", rows)
	}

	f := base
	f.MeterIDs = []string{"90000002"}
	rows, err = app.buildReadingRows(md, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MeterID != "90000002" {
		t.Errorf("MeterIDs=[90000002] = %+v, want only 90000002", rows)
	}
}

func TestParseReadingsFilterDefaultsToHalfYearRange(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/operator/readings", nil)
	today := mustDayT(t, "2025-12-31")
	f, errMsg := parseReadingsFilter(req, today)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if f.To != today {
		t.Errorf("To = %s, want %s", f.To, today)
	}
	wantFrom := today.AddDays(-180)
	if f.From != wantFrom {
		t.Errorf("From = %s, want %s (180 days back)", f.From, wantFrom)
	}
}

func TestWmbusmetersURLFormat(t *testing.T) {
	var noKey [16]byte
	if got := wmbusmetersURL("aabb", noKey); got != "https://wmbusmeters.org/analyze/aabb" {
		t.Errorf("no-key URL = %q", got)
	}
	key := [16]byte{0x28, 0xf6, 0x4a, 0x24, 0x98, 0x80, 0x64, 0xa0, 0x79, 0xaa, 0x2c, 0x80, 0x7d, 0x61, 0x02, 0xae}
	want := "https://wmbusmeters.org/analyze/aabb:auto:28f64a24988064a079aa2c807d6102ae"
	if got := wmbusmetersURL("aabb", key); got != want {
		t.Errorf("keyed URL = %q, want %q", got, want)
	}
}

func TestReadingsHandlerRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/operator/readings")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}

func TestReadingsExportProducesCSV(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	day := mustDayT(t, "2025-06-15")
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "99999999", Day: day, ReceivedAt: dayTimeT(t, day), RSSI: -80, RawHex: "aabbccdd"}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	resp, err := client.Get(srv.URL + "/operator/readings/export?from=2025-01-01&to=2025-12-31")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "Wohnung;Zählerplatz") {
		t.Errorf("expected a CSV header, got: %s", body)
	}
	if !strings.Contains(body, "99999999") {
		t.Errorf("expected the meter ID in the export, got: %s", body)
	}
}

func dayTimeT(t *testing.T, d telegram.Day) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02", string(d), telegram.Local)
	if err != nil {
		t.Fatalf("building a timestamp for %s: %v", d, err)
	}
	return tm.Add(23*time.Hour + 55*time.Minute)
}

func TestSummarizeMetersOnePerMeterSortedByID(t *testing.T) {
	rows := []readingRow{
		{MeterID: "90000002", UnitName: "Obergeschoss", DeviceType: "WWZ", Manufacturer: "TCH"},
		{MeterID: "90000001", UnitName: "Erdgeschoss", DeviceType: "HKV", Manufacturer: "QDS"},
		{MeterID: "90000001", UnitName: "Erdgeschoss", DeviceType: "HKV", Manufacturer: "QDS"}, // second telegram, same meter
	}
	got := summarizeMeters(rows)
	if len(got) != 2 {
		t.Fatalf("summarizeMeters = %+v, want 2 distinct meters", got)
	}
	if got[0].MeterID != "90000001" || got[1].MeterID != "90000002" {
		t.Errorf("summarizeMeters order = %+v, want sorted by meter ID", got)
	}
	if got[0].DeviceType != "HKV" || got[0].Manufacturer != "QDS" {
		t.Errorf("summarizeMeters[0] device_type/manufacturer = %q/%q, want HKV/QDS", got[0].DeviceType, got[0].Manufacturer)
	}
}
