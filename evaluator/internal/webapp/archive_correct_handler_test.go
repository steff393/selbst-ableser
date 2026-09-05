package webapp

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/correction"
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

func setupCorrectionMeter(t *testing.T, app *App, mdPath string, key [16]byte) {
	t.Helper()
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	md, _ := app.Vault.Get()
	md.Units = append(md.Units, masterdata.Unit{ID: "u1", Name: "Erdgeschoss", AreaM2: 50})
	md.MeterPoints = append(md.MeterPoints, masterdata.MeterPoint{ID: "mp1", UnitID: "u1", Room: "Bad", Kind: masterdata.KindHeating})
	md.Meters = append(md.Meters, masterdata.Meter{
		Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: mustDayT(t, "2025-01-01"),
	})
	if err := app.Vault.Save(mdPath, md); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestArchiveCorrectFixesAnExistingDay(t *testing.T) {
	app, mdPath := newTestApp(t)
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	setupCorrectionMeter(t, app, mdPath, key)

	day := mustDayT(t, "2025-04-30")
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00})) // -> 100
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: day, ReceivedAt: dayTimeT(t, day), RSSI: -80, RawHex: rawHex}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"csrf_token": {sess.CSRFToken},
		"meter":      {"90000001"},
		"day":        {"2025-04-30"},
		"value":      {"430"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "100 → 430") {
		t.Errorf("expected the old->new value in the response, got: %s", body)
	}

	entry, found, err := app.Store.Get("90000001", day)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if entry.RSSI != correction.RSSI {
		t.Errorf("RSSI = %d, want the correction marker %d", entry.RSSI, correction.RSSI)
	}
	if !entry.ReceivedAt.Equal(dayTimeT(t, day)) {
		t.Errorf("ReceivedAt = %v, want the original entry's ReceivedAt preserved", entry.ReceivedAt)
	}

	f, err := telegram.ParseWMBus(mustHex(t, entry.RawHex))
	if err != nil {
		t.Fatalf("ParseWMBus: %v", err)
	}
	res := crypto.Decrypt(f, key)
	if res.Outcome != crypto.OutcomeDecrypted {
		t.Fatalf("Outcome = %v, want OutcomeDecrypted", res.Outcome)
	}
	want := []byte{0x0B, 0x6E, 0x30, 0x04, 0x00} // DIF VIF + 430 as BCD, LSB first
	if got := res.Payload[17 : 17+len(want)]; hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("corrected record = % X, want % X", got, want)
	}
}

func TestArchiveCorrectBackfillsFromNearestDay(t *testing.T) {
	app, mdPath := newTestApp(t)
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	setupCorrectionMeter(t, app, mdPath, key)

	existing := mustDayT(t, "2025-04-28")
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00})) // -> 100
	if _, err := app.Store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: existing, ReceivedAt: dayTimeT(t, existing), RSSI: -80, RawHex: rawHex}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	// 2025-04-30 has no archived entry at all; 2025-04-28 is the nearest.
	resp, err := client.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"csrf_token": {sess.CSRFToken},
		"meter":      {"90000001"},
		"day":        {"2025-04-30"},
		"value":      {"250"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "neu angelegt") {
		t.Errorf("expected the backfill wording in the response, got: %s", body)
	}

	target := mustDayT(t, "2025-04-30")
	entry, found, err := app.Store.Get("90000001", target)
	if err != nil || !found {
		t.Fatalf("Get(target day): found=%v err=%v", found, err)
	}
	if entry.RSSI != correction.RSSI {
		t.Errorf("RSSI = %d, want the correction marker %d", entry.RSSI, correction.RSSI)
	}

	// The original 2025-04-28 entry must be untouched.
	original, found, err := app.Store.Get("90000001", existing)
	if err != nil || !found || original.RawHex != rawHex {
		t.Errorf("original template entry should be untouched: found=%v err=%v", found, err)
	}
}

func TestArchiveCorrectRequiresUnlockedVault(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)
	app.Vault.Lock() // undo the side effect of loginAsOperator's login-as-unlock

	resp, err := client.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"csrf_token": {sess.CSRFToken},
		"meter":      {"90000001"},
		"day":        {"2025-04-30"},
		"value":      {"430"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Gesperrt") {
		t.Errorf("expected the locked-vault page, got: %s", string(raw))
	}
}

func TestArchiveCorrectRejectsUnknownMeter(t *testing.T) {
	app, mdPath := newTestApp(t)
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	setupCorrectionMeter(t, app, mdPath, key)

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"csrf_token": {sess.CSRFToken},
		"meter":      {"99999999"},
		"day":        {"2025-04-30"},
		"value":      {"430"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "aktiv") {
		t.Errorf("expected the unknown-meter error, got: %s", string(raw))
	}
}

func TestArchiveCorrectRejectsInvalidValue(t *testing.T) {
	app, mdPath := newTestApp(t)
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	setupCorrectionMeter(t, app, mdPath, key)

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)
	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"csrf_token": {sess.CSRFToken},
		"meter":      {"90000001"},
		"day":        {"2025-04-30"},
		"value":      {"-5"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Ungültiger Zählerstand") {
		t.Errorf("expected the invalid-value error, got: %s", string(raw))
	}
}

func TestArchiveCorrectRequiresLogin(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/operator/archive/correct", url.Values{
		"meter": {"90000001"}, "day": {"2025-04-30"}, "value": {"430"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Errorf("expected a redirect to /login, ended up at %s", resp.Request.URL.Path)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", s, err)
	}
	return b
}
