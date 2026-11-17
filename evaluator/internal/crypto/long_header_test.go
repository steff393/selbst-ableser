package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"selbst-ableser/internal/telegram"
)

// longHeaderCleartext is a real wM-Bus telegram with a long transport
// header, published in cleartext form as a test fixture by wmbusmeters
// (simulations/simulation_dll_tpl_diff.txt). Its two identifications
// genuinely differ, which is the whole point of it: the link layer names
// a Hydrometer device 81818181, the long header a Diehl water meter
// 56465646, and its config word announces mode 5 with five blocks.
const longHeaderCleartext = "" +
	"66442423818181816" + "40E72" + // L C MFCT(HYD) A(81818181) VER MED CI
	"46564656A51170071F005005" + // long header: ID MFCT(DME) VER MED ACC STS CW
	"2F2F15257A616F14139172137DAE3A0C000000008C2013917213000B3B0000000B26784601025AF5000266EF00046D1B08B7214C1338861200CC101300000000CC201338861200426C9F2C42EC7EBF2C"

const (
	longHeaderCipherStart = 23 // ConfigWordOffset + 2 for CI 0x72
	longHeaderIDOffset    = 11
	longHeaderACCOffset   = 19
)

// encryptLongHeader re-encrypts the fixture's data region with an IV the
// test assembles itself, so what Decrypt is measured against is the rule
// and not our own buildIV.
func encryptLongHeader(t *testing.T, plain []byte, key [16]byte, iv []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(plain))
	copy(out, plain)
	region := out[longHeaderCipherStart:]
	if len(region)%aes.BlockSize != 0 {
		t.Fatalf("cipher region is %d bytes, not a multiple of the block size", len(region))
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(region, region)
	return out
}

func longHeaderFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(longHeaderCleartext)
	if err != nil {
		t.Fatal(err)
	}
	if int(raw[0])+1 != len(raw) || raw[10] != 0x72 {
		t.Fatalf("fixture is not a %d-byte CI 0x72 telegram", int(raw[0])+1)
	}
	return raw
}

// The identification the IV is built from is the long header's, and the
// access counter sits behind it — not at the first byte after the CI
// field, which in a long header is the start of the meter number.
func TestDecryptLongHeaderUsesTheHeaderIdentity(t *testing.T) {
	plain := longHeaderFixture(t)
	key := [16]byte{0: 9, 3: 7, 15: 1}

	iv := make([]byte, 0, aes.BlockSize)
	mfct := binary.LittleEndian.Uint16(plain[longHeaderIDOffset+4 : longHeaderIDOffset+6])
	iv = append(iv, byte(mfct), byte(mfct>>8))
	iv = append(iv, plain[longHeaderIDOffset:longHeaderIDOffset+4]...) // meter number
	iv = append(iv, plain[longHeaderIDOffset+6], plain[longHeaderIDOffset+7])
	for i := 0; i < 8; i++ {
		iv = append(iv, plain[longHeaderACCOffset])
	}

	f, err := telegram.ParseWMBus(encryptLongHeader(t, plain, key, iv))
	if err != nil {
		t.Fatalf("ParseWMBus: %v", err)
	}
	res := Decrypt(f, key)
	if res.Outcome != OutcomeDecrypted {
		t.Fatalf("Outcome = %v, want OutcomeDecrypted", res.Outcome)
	}
	if hex.EncodeToString(res.Payload) != hex.EncodeToString(plain) {
		t.Errorf("payload did not come back as the original cleartext")
	}
	if res.UsedFallbackConfigWordReading {
		t.Error("a standard config word was read through the deviating-device fallback")
	}
}

// Built the way this used to work — the link layer's identification and
// the byte right after the CI field as the access counter — the same
// telegram must not decrypt. Without this, the fix above could be
// satisfied by a decryption that ignores the IV entirely.
func TestDecryptLongHeaderRejectsTheLinkLayerIdentity(t *testing.T) {
	plain := longHeaderFixture(t)
	key := [16]byte{0: 9, 3: 7, 15: 1}

	iv := make([]byte, 0, aes.BlockSize)
	iv = append(iv, plain[2], plain[3]) // link-layer manufacturer
	iv = append(iv, plain[4:8]...)      // link-layer address
	iv = append(iv, plain[8], plain[9]) // link-layer version and medium
	for i := 0; i < 8; i++ {
		iv = append(iv, plain[11]) // what used to be read as the access counter
	}

	f, err := telegram.ParseWMBus(encryptLongHeader(t, plain, key, iv))
	if err != nil {
		t.Fatalf("ParseWMBus: %v", err)
	}
	if res := Decrypt(f, key); res.Outcome != OutcomeWrongKeyOrCorrupt {
		t.Fatalf("Outcome = %v, want OutcomeWrongKeyOrCorrupt", res.Outcome)
	}
}

// Corrections re-encrypt what they patched, so Encrypt has to build the
// same IV Decrypt does.
func TestEncryptLongHeaderRoundTrips(t *testing.T) {
	plain := longHeaderFixture(t)
	key := [16]byte{2: 5, 7: 6}

	f, err := telegram.ParseWMBus(plain)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := Encrypt(f, plain, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ef, err := telegram.ParseWMBus(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	res := Decrypt(ef, key)
	if res.Outcome != OutcomeDecrypted || hex.EncodeToString(res.Payload) != hex.EncodeToString(plain) {
		t.Errorf("round trip failed: outcome %v", res.Outcome)
	}
}
