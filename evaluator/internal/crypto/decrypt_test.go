package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"testing"

	"selbst-ableser/internal/telegram"
)

// buildEncryptedFrame constructs a short-header (CI 0x7A) telegram whose
// payload is real AES-128-CBC ciphertext, so the decryption stage can be
// tested end-to-end without needing a real device's key material.
func buildEncryptedFrame(t *testing.T, key [16]byte, plaintext []byte, blocks byte) *telegram.Frame {
	t.Helper()
	if len(plaintext)%aes.BlockSize != 0 {
		t.Fatalf("plaintext length %d is not a multiple of the AES block size", len(plaintext))
	}
	if blocks%2 != 0 {
		t.Fatalf("blocks must be even: an odd value sets bit 4, which the fallback config-word reading also reads as part of the mode nibble")
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
		t.Fatalf("aes.NewCipher: %v", err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)

	raw := []byte{
		byte(len(ciphertext) + 14), // L (not used by Decrypt, kept plausible)
		0x44,                       // C
	}
	raw = append(raw, byte(m), byte(m>>8))
	raw = append(raw, a[:]...)
	raw = append(raw, ver, med, 0x7A, acc, 0x18) // VER MED CI ACC STS
	raw = append(raw, 0x20, blocks<<4|0x05)      // config word: fallback reading -> mode 5
	raw = append(raw, ciphertext...)

	return &telegram.Frame{
		L:    raw[0],
		C:    raw[1],
		M:    m,
		A:    a,
		Ver:  ver,
		Med:  med,
		CI:   0x7A,
		Rest: raw[11:],
		Raw:  raw,
	}
}

func TestDecryptRoundTrip(t *testing.T) {
	key := [16]byte{0: 1, 5: 2, 10: 3, 15: 4}
	plaintext := append([]byte{0x2F, 0x2F}, bytes.Repeat([]byte{0xAB}, 30)...) // 2 blocks
	f := buildEncryptedFrame(t, key, plaintext, 2)

	res := Decrypt(f, key)
	if res.Outcome != OutcomeDecrypted {
		t.Fatalf("Outcome = %v, want OutcomeDecrypted", res.Outcome)
	}
	if !res.Outcome.Evaluable() {
		t.Error("OutcomeDecrypted should be evaluable")
	}
	got := res.Payload[13+2 : 13+2+len(plaintext)]
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted payload = % X, want % X", got, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key := [16]byte{0: 1, 5: 2, 10: 3, 15: 4}
	wrongKey := [16]byte{0: 9, 5: 9, 10: 9, 15: 9}
	// blocks must be even here: buildEncryptedFrame packs it into the same
	// byte as the low nibble that must read back as mode 5 (see the note
	// on the fallback config-word reading in configword.go), and an odd
	// block count would corrupt that nibble.
	plaintext := append([]byte{0x2F, 0x2F}, bytes.Repeat([]byte{0xAB}, 30)...) // 2 blocks
	f := buildEncryptedFrame(t, key, plaintext, 2)

	res := Decrypt(f, wrongKey)
	if res.Outcome != OutcomeWrongKeyOrCorrupt {
		t.Fatalf("Outcome = %v, want OutcomeWrongKeyOrCorrupt", res.Outcome)
	}
	if res.Outcome.Evaluable() {
		t.Error("OutcomeWrongKeyOrCorrupt should not be evaluable")
	}
}

func TestDecryptCleartext(t *testing.T) {
	f := &telegram.Frame{CI: 0x78, Raw: []byte{0x01, 0x02, 0x03}}
	res := Decrypt(f, [16]byte{})
	if res.Outcome != OutcomeCleartext {
		t.Fatalf("Outcome = %v, want OutcomeCleartext", res.Outcome)
	}
	if !bytes.Equal(res.Payload, f.Raw) {
		t.Error("cleartext payload should be the frame's raw bytes, unchanged")
	}
}

func TestDecryptUnsupportedTransport(t *testing.T) {
	f := &telegram.Frame{CI: 0x99, Raw: []byte{0x01}}
	res := Decrypt(f, [16]byte{})
	if res.Outcome != OutcomeUnsupportedTransport {
		t.Fatalf("Outcome = %v, want OutcomeUnsupportedTransport", res.Outcome)
	}
}

func TestDecryptUnsupportedMode(t *testing.T) {
	raw := make([]byte, 20)
	raw[13], raw[14] = 0x07, 0x00 // mode 7 (Security Profile B): encrypted, but not one we support
	f := &telegram.Frame{CI: 0x7A, Raw: raw}
	res := Decrypt(f, [16]byte{})
	if res.Outcome != OutcomeUnsupportedMode {
		t.Fatalf("Outcome = %v, want OutcomeUnsupportedMode", res.Outcome)
	}
}

// TestDecryptMode0IsCleartext uses a real telegram captured from a Qundis
// warm-water meter (12454643): config word 0x0020, i.e. mode 0. That
// telegram was never encrypted — mode 0 says so in its own header — but
// used to be rejected as OutcomeUnsupportedMode because readConfigWord
// only ever answers "is this mode 5", collapsing "no encryption" and
// "encryption we can't handle" into the same non-evaluable result.
func TestDecryptMode0IsCleartext(t *testing.T) {
	raw, err := hex.DecodeString("394493444346451218067a370000200c13476905004c1312630500426c3f3ccc081312630500c2086c3f3c02bb560000326cffff046d080d5731")
	if err != nil {
		t.Fatalf("decoding fixture hex: %v", err)
	}
	f, err := telegram.ParseWMBus(raw)
	if err != nil {
		t.Fatalf("ParseWMBus: %v", err)
	}

	res := Decrypt(f, [16]byte{})

	if res.Outcome != OutcomeCleartext {
		t.Fatalf("Outcome = %v, want OutcomeCleartext", res.Outcome)
	}
	if !res.Outcome.Evaluable() {
		t.Error("OutcomeCleartext should be evaluable")
	}
	if !bytes.Equal(res.Payload, f.Raw) {
		t.Error("cleartext payload should be the frame's raw bytes, unchanged")
	}
	// DIF 0x0C (8-digit BCD) + VIF 0x13 (volume, litres) at DataOffset 15:
	// 0C 13 47 69 05 00 -> 56947 L, reading the BCD digits in wire order.
	want := []byte{0x0C, 0x13, 0x47, 0x69, 0x05, 0x00}
	got := res.Payload[15 : 15+len(want)]
	if !bytes.Equal(got, want) {
		t.Errorf("payload at DataOffset = % X, want % X", got, want)
	}
}

func TestDecryptTruncated(t *testing.T) {
	raw := make([]byte, 20)
	raw[13], raw[14] = 0x20, 0x25 // 2 blocks announced, but far fewer bytes follow
	f := &telegram.Frame{CI: 0x7A, Raw: raw}
	res := Decrypt(f, [16]byte{})
	if res.Outcome != OutcomeTruncated {
		t.Fatalf("Outcome = %v, want OutcomeTruncated", res.Outcome)
	}
}

// TestDecryptEmptyRestNoPanic covers Frame.Rest being empty while Raw is
// long enough to pass every check ahead of buildIV — Rest and Raw are two
// independent fields (nothing enforces Rest == Raw[11:]), and Frame is
// built directly from a literal in several tests here, not only through
// ParseWMBus (which does keep them in sync). buildIV reads Rest[0]; without
// its own explicit check this construction panics on an out-of-range index
// instead of reporting the same OutcomeTruncated every other too-short
// telegram gets — even though, by every check *before* it, this telegram
// looks completely well-formed.
// TestEncryptRoundTrip mirrors internal/correction's usage: decrypt,
// mutate the plaintext, re-encrypt, decrypt again, and confirm the mutated
// plaintext comes back — while every byte before the cipher region stays
// exactly as Decrypt first produced it.
func TestEncryptRoundTrip(t *testing.T) {
	key := [16]byte{0: 1, 5: 2, 10: 3, 15: 4}
	plaintext := append([]byte{0x2F, 0x2F}, bytes.Repeat([]byte{0xAB}, 30)...) // 2 blocks
	f := buildEncryptedFrame(t, key, plaintext, 2)

	res := Decrypt(f, key)
	if res.Outcome != OutcomeDecrypted {
		t.Fatalf("Outcome = %v, want OutcomeDecrypted", res.Outcome)
	}
	headerBefore := append([]byte(nil), res.Payload[:13+2]...)

	mutated := append([]byte(nil), res.Payload...)
	mutated[13+2+5] = 0xCD // inside the plaintext region, past the fixed "2F 2F" filler prefix

	reencrypted, err := Encrypt(f, mutated, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Equal(reencrypted[:13+2], headerBefore) {
		t.Error("bytes before the cipher region should be unchanged by Encrypt")
	}
	if bytes.Equal(reencrypted[13+2:], mutated[13+2:]) {
		t.Error("the cipher region should now hold ciphertext, not the mutated plaintext")
	}

	f2, err := telegram.ParseWMBus(reencrypted)
	if err != nil {
		t.Fatalf("ParseWMBus(reencrypted): %v", err)
	}
	res2 := Decrypt(f2, key)
	if res2.Outcome != OutcomeDecrypted {
		t.Fatalf("Outcome after round trip = %v, want OutcomeDecrypted", res2.Outcome)
	}
	if !bytes.Equal(res2.Payload[13+2:], mutated[13+2:]) {
		t.Errorf("decrypted mutated plaintext = % X, want % X", res2.Payload[13+2:], mutated[13+2:])
	}
}

func TestEncryptRejectsCleartextTelegram(t *testing.T) {
	f := &telegram.Frame{CI: 0x78, Raw: []byte{0x01, 0x02, 0x03}}
	if _, err := Encrypt(f, f.Raw, [16]byte{}); err == nil {
		t.Error("expected an error: a cleartext telegram has no encrypted region to re-encrypt")
	}
}

func TestEncryptRejectsUnsupportedMode(t *testing.T) {
	raw := make([]byte, 20)
	raw[13], raw[14] = 0x07, 0x00 // mode 7, not mode 5
	f := &telegram.Frame{CI: 0x7A, Raw: raw}
	if _, err := Encrypt(f, raw, [16]byte{}); err == nil {
		t.Error("expected an error for a config word that doesn't announce mode 5")
	}
}

func TestDecryptEmptyRestNoPanic(t *testing.T) {
	raw := make([]byte, 20)
	raw[10] = 0x7A                                      // short header: has a config word
	raw[13], raw[14] = 0x05, 0x00                       // mode 5, standard reading, 0 blocks
	f := &telegram.Frame{CI: 0x7A, Raw: raw, Rest: nil} // Rest inconsistent with Raw on purpose

	res := Decrypt(f, [16]byte{})

	if res.Outcome != OutcomeTruncated {
		t.Fatalf("Outcome = %v, want OutcomeTruncated", res.Outcome)
	}
}
