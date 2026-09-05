package billing

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/telegram"
)

// buildEncryptedTelegramHex constructs a short-header (CI 0x7A) telegram
// whose payload is real AES-128-CBC ciphertext for plaintext (which must
// start with 0x2F 0x2F and have a length that is a multiple of 32 bytes,
// so the config word's block count stays even — see the equivalent note
// in internal/crypto's tests), so FindReading exercises the real
// decrypt-and-decode path rather than the "not really encrypted" test
// fixtures used elsewhere.
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
	raw = append(raw, 0x20, byte(len(plaintext)/16)<<4|0x05) // fallback config-word reading -> mode 5
	raw = append(raw, ciphertext...)

	return hex.EncodeToString(raw)
}

// hcaPayload builds a 32-byte decrypted payload holding one current
// heat-cost-allocator reading (DIF 0x0B, VIF 0x6E), padded with filler.
func hcaPayload(bcdValue [3]byte) []byte {
	p := []byte{0x2F, 0x2F, 0x0B, 0x6E}
	p = append(p, bcdValue[:]...)
	for len(p) < 32 {
		p = append(p, 0x2F)
	}
	return p
}

func TestReadValueRoundTrip(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00})) // -> 100

	entry := archive.Entry{MeterID: "90000001", RawHex: rawHex}
	reading, ok, err := ReadValue(entry, key)
	if err != nil || !ok {
		t.Fatalf("ReadValue: ok=%v err=%v", ok, err)
	}
	if reading.Value != 100 {
		t.Errorf("Value = %d, want 100", reading.Value)
	}
}

func TestReadValueWrongKeyIsNotEvaluable(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	wrongKey := [16]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00}))

	entry := archive.Entry{MeterID: "90000001", RawHex: rawHex}
	_, ok, err := ReadValue(entry, wrongKey)
	if err != nil {
		t.Fatalf("ReadValue with the wrong key should not itself be an error, got %v", err)
	}
	if ok {
		t.Fatal("ReadValue with the wrong key should report ok=false")
	}
}

func TestFindReading_ExactDay(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	target := mustDay(t, "2025-01-31")
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00}))
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: target, ReceivedAt: dayTime(t, target), RSSI: -80, RawHex: rawHex}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	got, ok, err := FindReading(store, "90000001", key, target, 0)
	if err != nil || !ok {
		t.Fatalf("FindReading: ok=%v err=%v", ok, err)
	}
	if got.Value != 100 || got.Day != target {
		t.Errorf("FindReading = %+v, want value 100 on %s", got, target)
	}
}

func TestFindReading_BackwardSearch(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Nothing on the 31st itself; the last available day is the 29th.
	available := mustDay(t, "2025-01-29")
	rawHex := buildEncryptedTelegramHex(t, key, hcaPayload([3]byte{0x00, 0x01, 0x00}))
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: available, ReceivedAt: dayTime(t, available), RSSI: -80, RawHex: rawHex}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	got, ok, err := FindReading(store, "90000001", key, mustDay(t, "2025-01-31"), DefaultLookbackDays)
	if err != nil || !ok {
		t.Fatalf("FindReading: ok=%v err=%v", ok, err)
	}
	if got.Day != available {
		t.Errorf("FindReading found day %s, want %s (the backward search should have reached it)", got.Day, available)
	}
}

func TestFindReading_NothingWithinLookbackIsAGap(t *testing.T) {
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	_, ok, err := FindReading(store, "90000001", [16]byte{}, mustDay(t, "2025-01-31"), 5)
	if err != nil {
		t.Fatalf("FindReading on an empty archive should not itself error, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when nothing is archived within the lookback window")
	}
}

// dayTime builds a plausible local receive timestamp for a Day, for
// fixtures that need one but don't care about the exact time of day.
func dayTime(t *testing.T, d telegram.Day) time.Time {
	t.Helper()
	midnight, err := time.ParseInLocation("2006-01-02", string(d), telegram.Local)
	if err != nil {
		t.Fatalf("building a timestamp for %s: %v", d, err)
	}
	return midnight.Add(23*time.Hour + 55*time.Minute)
}
