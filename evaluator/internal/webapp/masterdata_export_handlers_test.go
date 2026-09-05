package webapp

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"selbst-ableser/internal/masterdata"
)

// buildEncryptedTelegramHexForExportTest builds a short-header (CI 0x7A)
// telegram carrying one real AES-128-CBC-encrypted heat-cost-allocator
// reading, mirroring internal/billing's own equivalent test helper (not
// exported, so duplicated here rather than reused across packages).
func buildEncryptedTelegramHexForExportTest(t *testing.T, key [16]byte, value int64) string {
	t.Helper()
	pair := func(n int64) byte { return byte((n/10)<<4 | (n % 10)) }
	bcd := [3]byte{pair(value % 100), pair((value / 100) % 100), pair((value / 10000) % 100)}
	plaintext := []byte{0x2F, 0x2F, 0x0B, 0x6E}
	plaintext = append(plaintext, bcd[:]...)
	for len(plaintext) < 32 {
		plaintext = append(plaintext, 0x2F)
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

func newExportTestMasterData(t *testing.T) (*App, string) {
	t.Helper()
	app, mdPath := newTestApp(t)
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Building: masterdata.Building{Name: "Musterhaus", HeatingKWhPerUnit: 1.42},
		Units:    []masterdata.Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 60}},
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
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	return app, mdPath
}

func TestMasterDataExportIncludesAESKeyAndFactors(t *testing.T) {
	app, _ := newExportTestMasterData(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	resp, err := client.Get(srv.URL + "/operator/masterdata/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got masterDataExport
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Building.Name != "Musterhaus" || got.Building.HeatingKWhPerUnit != 1.42 {
		t.Errorf("Building = %+v", got.Building)
	}
	if len(got.Meters) != 1 || got.Meters[0].AESKey != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("Meters = %+v, want the AES key hex-encoded", got.Meters)
	}
	if len(got.MeterPoints) != 1 || got.MeterPoints[0].Kind != "heating" {
		t.Errorf("MeterPoints = %+v", got.MeterPoints)
	}
}

func TestMasterDataExportRequiresOperator(t *testing.T) {
	app, _ := newExportTestMasterData(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/operator/masterdata/export")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want a redirect to /login", resp.StatusCode)
	}
}
