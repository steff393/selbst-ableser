package decode

import (
	"fmt"

	"selbst-ableser/internal/telegram"
)

// Standard parses a standard-compliant (EN 13757-3) telegram's
// self-describing data records. raw is the full telegram in cleartext
// (telegram.Frame.Raw, or the equivalent decrypted form produced by
// internal/crypto); ci is the telegram's CI field.
//
// ok is false for manufacturer-specific or otherwise unrecognized
// transport headers, which have no self-describing structure to parse;
// callers should fall back to ManufacturerSpecific for those.
func Standard(ci byte, raw []byte) (records []Record, ok bool, err error) {
	h, known := telegram.IdentifyTransportHeader(ci)
	if !known {
		return nil, false, nil
	}
	if h.DataOffset > len(raw) {
		return nil, true, fmt.Errorf("decode: telegram shorter than its transport header")
	}
	records, err = walkRecords(raw[h.DataOffset:])
	return records, true, err
}
