package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"selbst-ableser/internal/telegram"
)

// Outcome classifies what happened when trying to make a telegram's payload
// available in cleartext.
type Outcome int

const (
	// OutcomeCleartext: the telegram was never encrypted.
	OutcomeCleartext Outcome = iota
	// OutcomeDecrypted: the telegram was encrypted and was successfully
	// decrypted; Result.Payload holds the cleartext form.
	OutcomeDecrypted
	// OutcomeUnsupportedTransport: the telegram's transport header (CI
	// field) is not one this system knows how to locate a config word in.
	OutcomeUnsupportedTransport
	// OutcomeUnsupportedMode: a config word was found but does not
	// announce OMS Security Profile A, mode 5, under either byte-order
	// reading.
	OutcomeUnsupportedMode
	// OutcomeWrongKeyOrCorrupt: decryption ran, but the fixed filler bytes
	// that must open the decrypted payload were not present. Either the
	// wrong key was used, or the telegram is damaged.
	OutcomeWrongKeyOrCorrupt
	// OutcomeTruncated: the telegram is shorter than the config word
	// claims; it is structurally malformed.
	OutcomeTruncated
)

// Evaluable reports whether Result.Payload holds usable cleartext.
func (o Outcome) Evaluable() bool {
	return o == OutcomeCleartext || o == OutcomeDecrypted
}

// Result is the outcome of attempting to obtain a telegram's payload in
// cleartext.
type Result struct {
	Outcome Outcome
	// Payload is the wM-Bus telegram (same shape as telegram.Frame.Raw)
	// with any encrypted region replaced by its decrypted content; the
	// header and any unencrypted trailing bytes are unchanged. Only valid
	// when Outcome.Evaluable() is true.
	Payload []byte
	// UsedFallbackConfigWordReading is set when the config word had to be
	// read with the non-standard (byte-swapped) interpretation to arrive
	// at mode 5. Worth logging; not an error.
	UsedFallbackConfigWordReading bool
}

// isManufacturerSpecific reports whether ci marks a manufacturer-specific
// telegram (no standard transport header, no self-describing data
// records). Such telegrams are never encrypted at the transport level
// handled here.
func isManufacturerSpecific(ci byte) bool {
	return ci >= 0xA0 && ci <= 0xB7
}

// Decrypt makes a telegram's payload available in cleartext, using key for
// OMS Security Profile A, mode 5 (AES-128-CBC).
func Decrypt(f *telegram.Frame, key [16]byte) Result {
	if isManufacturerSpecific(f.CI) {
		return Result{Outcome: OutcomeCleartext, Payload: f.Raw}
	}
	h, known := telegram.IdentifyTransportHeader(f.CI)
	if !known {
		return Result{Outcome: OutcomeUnsupportedTransport}
	}
	if !h.HasConfigWord {
		return Result{Outcome: OutcomeCleartext, Payload: f.Raw}
	}

	if h.ConfigWordOffset+2 > len(f.Raw) {
		return Result{Outcome: OutcomeTruncated}
	}
	blocks, fallback, ok := readConfigWord(f.Raw[h.ConfigWordOffset], f.Raw[h.ConfigWordOffset+1])
	if !ok {
		if configWordIsCleartext(f.Raw[h.ConfigWordOffset]) {
			return Result{Outcome: OutcomeCleartext, Payload: f.Raw}
		}
		return Result{Outcome: OutcomeUnsupportedMode}
	}

	cipherStart := h.ConfigWordOffset + 2
	cipherLen := blocks * aes.BlockSize
	if cipherStart+cipherLen > len(f.Raw) {
		return Result{Outcome: OutcomeTruncated}
	}

	// buildIV reads f.Rest[0] (the access counter, ACC). The check above
	// already implies len(f.Raw) is large enough for every known header
	// type to make that safe — but only as long as that check stays
	// exactly where it is. Checking it here too, right next to the read
	// it protects, means Rest[0] can never panic even if a future change
	// reorders or removes the check above: a telegram this short is
	// exactly what OutcomeTruncated already exists to report.
	if len(f.Rest) == 0 {
		return Result{Outcome: OutcomeTruncated}
	}

	iv := buildIV(f)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		// key is always 16 bytes here, so this cannot fail in practice.
		panic(fmt.Sprintf("crypto: unexpected AES key error: %v", err))
	}
	plaintext := make([]byte, cipherLen)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, f.Raw[cipherStart:cipherStart+cipherLen])

	if len(plaintext) < 2 || plaintext[0] != 0x2F || plaintext[1] != 0x2F {
		return Result{Outcome: OutcomeWrongKeyOrCorrupt}
	}

	payload := make([]byte, len(f.Raw))
	copy(payload, f.Raw)
	copy(payload[cipherStart:cipherStart+cipherLen], plaintext)

	return Result{Outcome: OutcomeDecrypted, Payload: payload, UsedFallbackConfigWordReading: fallback}
}

// Encrypt is Decrypt's inverse for the region Decrypt would have
// decrypted: given payload in the same shape as Decrypt's Result.Payload
// (f's own header and any bytes outside the encrypted region already in
// place — only the cipher region itself holds new plaintext), it
// re-encrypts that region with key and f's own IV (see buildIV) and
// returns the complete raw telegram. Used by internal/correction to write
// a corrected value back without needing to know anything about
// config-word offsets itself; every byte outside the cipher region is
// copied through unchanged.
func Encrypt(f *telegram.Frame, payload []byte, key [16]byte) ([]byte, error) {
	h, known := telegram.IdentifyTransportHeader(f.CI)
	if !known || !h.HasConfigWord {
		return nil, fmt.Errorf("crypto: telegram has no encrypted region to re-encrypt")
	}
	if h.ConfigWordOffset+2 > len(payload) {
		return nil, fmt.Errorf("crypto: telegram shorter than its config word")
	}
	blocks, _, ok := readConfigWord(payload[h.ConfigWordOffset], payload[h.ConfigWordOffset+1])
	if !ok {
		return nil, fmt.Errorf("crypto: telegram's config word does not announce mode 5")
	}

	cipherStart := h.ConfigWordOffset + 2
	cipherLen := blocks * aes.BlockSize
	if cipherStart+cipherLen > len(payload) {
		return nil, fmt.Errorf("crypto: telegram shorter than its config word claims")
	}
	if len(f.Rest) == 0 {
		return nil, fmt.Errorf("crypto: telegram too short for an access counter")
	}

	iv := buildIV(f)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		// key is always 16 bytes here, so this cannot fail in practice.
		panic(fmt.Sprintf("crypto: unexpected AES key error: %v", err))
	}

	out := make([]byte, len(payload))
	copy(out, payload)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[cipherStart:cipherStart+cipherLen], payload[cipherStart:cipherStart+cipherLen])
	return out, nil
}

// buildIV constructs the 16-byte initialization vector for OMS Security
// Profile A: manufacturer field and address field as transmitted, followed
// by version, medium, and the access counter repeated to fill the block.
func buildIV(f *telegram.Frame) []byte {
	iv := make([]byte, 0, aes.BlockSize)
	iv = append(iv, byte(f.M), byte(f.M>>8))
	iv = append(iv, f.A[:]...)
	iv = append(iv, f.Ver, f.Med)
	acc := f.Rest[0] // ACC is the first byte after CI
	for i := 0; i < 8; i++ {
		iv = append(iv, acc)
	}
	return iv
}
