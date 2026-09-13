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
//
// An error does not invalidate the records returned with it: the walk
// reads records in order and stops where it can no longer follow the
// chain, so everything before that point is complete and correct. Real
// telegrams do end in an unreadable byte — a lone DIF, a stray filler —
// well after the value they carry, and treating that as a total loss
// throws away a reading that was sitting right there.
//
// So a caller looking for a particular record must look first and judge
// the error afterwards: report it only when what it wanted was not among
// the records it did get. The error is still worth surfacing then, since
// it says why the search came up empty.
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
