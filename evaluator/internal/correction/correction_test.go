package correction

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/crypto"
	"selbst-ableser/internal/telegram"
)

const (
	difCurrentHKV   = 0x0B // storage 0 (current), 3-byte BCD
	vifHeatCostHKV  = 0x6E
	difCurrentWater = 0x04 // storage 0 (current), 4-byte binary
	vifVolumeWater  = 0x13
)

// appendManufacturer appends the little-endian 2-byte manufacturer field
// for a 3-letter code (inverse of telegram.Manufacturer).
func appendManufacturer(b []byte, code string) []byte {
	c := []byte(code)
	v := uint16(c[0]-64)<<10 | uint16(c[1]-64)<<5 | uint16(c[2]-64)
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], v)
	return append(b, buf[:]...)
}

// buildCleartextTelegram constructs a short-header (CI 0x78: no transport
// header, always cleartext) telegram carrying exactly one data record.
func buildCleartextTelegram(t *testing.T, manufacturer string, med, dif, vif byte, data []byte) string {
	t.Helper()
	a := [4]byte{0x01, 0x00, 0x00, 0x90}
	wmbus := make([]byte, 0, 20)
	wmbus = append(wmbus, 0, 0x44)
	wmbus = appendManufacturer(wmbus, manufacturer)
	wmbus = append(wmbus, a[:]...)
	wmbus = append(wmbus, 0x01, med, 0x78, dif, vif)
	wmbus = append(wmbus, data...)
	wmbus[0] = byte(len(wmbus) - 1)
	return hex.EncodeToString(wmbus)
}

// buildEncryptedTelegram constructs a short-header (CI 0x7A) telegram whose
// payload is real AES-128-CBC ciphertext of a filler-padded record, so
// Build exercises the real decrypt/re-encrypt path.
func buildEncryptedTelegram(t *testing.T, key [16]byte, dif, vif byte, data []byte) string {
	t.Helper()
	// Padded to a multiple of 32 bytes (an even block count): the fallback
	// (byte-swapped) config-word reading also treats an odd block count's
	// low bit as part of the mode nibble, corrupting it — see the identical
	// constraint in internal/crypto's own tests.
	plaintext := []byte{0x2F, 0x2F, dif, vif}
	plaintext = append(plaintext, data...)
	for len(plaintext)%32 != 0 {
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
	raw = append(raw, 0x20, byte(len(plaintext)/16)<<4|0x05) // fallback config-word reading -> mode 5
	raw = append(raw, ciphertext...)
	return hex.EncodeToString(raw)
}

// techemFrame builds a manufacturer-specific (CI 0xA0) Techem HCA frame of
// the one length decodeTechemHCA/patchTechemHCA recognize (51 bytes),
// carrying currentReading at its known offset. The manufacturer field
// (offset 2-3) and CI (offset 10) are what telegram.ParseWMBus and
// crypto.Decrypt actually read; the rest is zero-filled, which is fine —
// an all-zero A-field is still valid packed BCD.
func techemFrame(currentReading uint16) []byte {
	raw := make([]byte, 51)
	copy(raw[2:4], appendManufacturer(nil, "TCH"))
	raw[10] = 0xA0
	binary.LittleEndian.PutUint16(raw[18:20], currentReading)
	return raw
}

func TestBuildCleartextHeatCostAllocator(t *testing.T) {
	rawHex := buildCleartextTelegram(t, "QDS", 0x08, difCurrentHKV, vifHeatCostHKV, []byte{0x00, 0x04, 0x20}) // 200400
	template := archive.Entry{MeterID: "90000001", RawHex: rawHex}

	newHex, oldValue, err := Build(template, [16]byte{}, 200500)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if oldValue != 200400 {
		t.Errorf("oldValue = %d, want 200400", oldValue)
	}

	newRaw, err := hex.DecodeString(newHex)
	if err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	oldRaw, _ := hex.DecodeString(rawHex)
	if len(newRaw) != len(oldRaw) {
		t.Fatalf("length changed: %d -> %d", len(oldRaw), len(newRaw))
	}
	for i := range oldRaw {
		if i >= 13 && i <= 15 {
			continue // the patched value bytes
		}
		if newRaw[i] != oldRaw[i] {
			t.Errorf("byte %d changed from 0x%02X to 0x%02X, want untouched", i, oldRaw[i], newRaw[i])
		}
	}

	f, err := telegram.ParseWMBus(newRaw)
	if err != nil {
		t.Fatalf("ParseWMBus(corrected): %v", err)
	}
	res := crypto.Decrypt(f, [16]byte{})
	if res.Outcome != crypto.OutcomeCleartext {
		t.Fatalf("Outcome = %v, want OutcomeCleartext", res.Outcome)
	}
	want := []byte{0x0B, 0x6E, 0x00, 0x05, 0x20} // DIF VIF + 200500 as BCD
	got := res.Payload[11 : 11+len(want)]
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("patched record = % X, want % X", got, want)
	}
}

func TestBuildCleartextWater(t *testing.T) {
	rawHex := buildCleartextTelegram(t, "DWZ", 0x06, difCurrentWater, vifVolumeWater, []byte{0xF1, 0x80, 0x00, 0x00}) // 33009 l
	template := archive.Entry{MeterID: "90000004", RawHex: rawHex}

	newHex, oldValue, err := Build(template, [16]byte{}, 34000)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if oldValue != 33009 {
		t.Errorf("oldValue = %d, want 33009", oldValue)
	}

	newRaw, _ := hex.DecodeString(newHex)
	f, err := telegram.ParseWMBus(newRaw)
	if err != nil {
		t.Fatalf("ParseWMBus(corrected): %v", err)
	}
	res := crypto.Decrypt(f, [16]byte{})
	want := []byte{0x04, 0x13, 0xD0, 0x84, 0x00, 0x00} // DIF VIF + 34000 little-endian
	got := res.Payload[11 : 11+len(want)]
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("patched record = % X, want % X", got, want)
	}
}

func TestBuildEncryptedRoundTrips(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	rawHex := buildEncryptedTelegram(t, key, difCurrentHKV, vifHeatCostHKV, []byte{0x00, 0x01, 0x00}) // 100
	template := archive.Entry{MeterID: "90000001", RawHex: rawHex}

	newHex, oldValue, err := Build(template, key, 250)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if oldValue != 100 {
		t.Errorf("oldValue = %d, want 100", oldValue)
	}

	oldRaw, _ := hex.DecodeString(rawHex)
	newRaw, _ := hex.DecodeString(newHex)
	if len(newRaw) != len(oldRaw) {
		t.Fatalf("length changed: %d -> %d", len(oldRaw), len(newRaw))
	}
	if hex.EncodeToString(newRaw[:15]) != hex.EncodeToString(oldRaw[:15]) {
		t.Error("header and config word (everything before the cipher region) should be unchanged")
	}
	if hex.EncodeToString(newRaw[15:]) == hex.EncodeToString(oldRaw[15:]) {
		t.Error("ciphertext should have changed since the plaintext value changed")
	}

	f, err := telegram.ParseWMBus(newRaw)
	if err != nil {
		t.Fatalf("ParseWMBus(corrected): %v", err)
	}
	res := crypto.Decrypt(f, key)
	if res.Outcome != crypto.OutcomeDecrypted {
		t.Fatalf("Outcome = %v, want OutcomeDecrypted", res.Outcome)
	}
	want := []byte{0x0B, 0x6E, 0x50, 0x02, 0x00} // DIF VIF + 250 as BCD, least-significant byte first
	got := res.Payload[17 : 17+len(want)]        // DataOffset(15) + the two 0x2F filler bytes
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("patched record = % X, want % X", got, want)
	}
}

func TestBuildEncryptedWrongKey(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	wrongKey := [16]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	rawHex := buildEncryptedTelegram(t, key, difCurrentHKV, vifHeatCostHKV, []byte{0x00, 0x01, 0x00})
	template := archive.Entry{MeterID: "90000001", RawHex: rawHex}

	if _, _, err := Build(template, wrongKey, 250); err == nil {
		t.Error("expected an error: the template is not decryptable with the wrong key")
	}
}

func TestBuildManufacturerSpecificTechem(t *testing.T) {
	raw := techemFrame(300)
	template := archive.Entry{MeterID: "90000005", RawHex: hex.EncodeToString(raw)}

	newHex, oldValue, err := Build(template, [16]byte{}, 450)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if oldValue != 300 {
		t.Errorf("oldValue = %d, want 300", oldValue)
	}

	newRaw, _ := hex.DecodeString(newHex)
	if got := binary.LittleEndian.Uint16(newRaw[18:20]); got != 450 {
		t.Errorf("patched reading = %d, want 450", got)
	}
	oldRaw, _ := hex.DecodeString(template.RawHex)
	for i := range oldRaw {
		if i >= 18 && i < 20 {
			continue
		}
		if newRaw[i] != oldRaw[i] {
			t.Errorf("byte %d changed, want untouched outside the patched field", i)
		}
	}
}

func TestBuildRejectsOutOfRangeValue(t *testing.T) {
	rawHex := buildCleartextTelegram(t, "QDS", 0x08, difCurrentHKV, vifHeatCostHKV, []byte{0x00, 0x04, 0x20})
	template := archive.Entry{MeterID: "90000001", RawHex: rawHex}

	if _, _, err := Build(template, [16]byte{}, 1000000); err == nil {
		t.Error("expected an error: 1000000 does not fit in a 3-byte BCD field")
	}
	if _, _, err := Build(template, [16]byte{}, -1); err == nil {
		t.Error("expected an error for a negative value")
	}
}

func TestIsMarked(t *testing.T) {
	if !IsMarked(archive.Entry{RSSI: RSSI}) {
		t.Error("an entry with the correction RSSI should be marked")
	}
	if IsMarked(archive.Entry{RSSI: -80}) {
		t.Error("a normal RSSI should not be marked")
	}
}
