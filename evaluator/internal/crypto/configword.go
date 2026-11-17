package crypto

// oms5 is the OMS Security Profile A encryption mode this system supports.
const oms5 = 5

// readConfigWord interprets a telegram's two-byte encryption configuration
// word, transmitted least significant byte first: the encryption mode sits
// in the *second* transmitted byte, the number of encrypted blocks in the
// high nibble of the first. Every real mode-5 telegram seen so far reads
// that way — Techem radio converters (`30 05`, `20 25`, `40 05`), the
// Diehl and Sharky test telegrams published with wmbusmeters, and the OMS
// specification's own example.
//
// At least one encountered device family (Befund B02) swaps the two bytes,
// so that reading is tried second. usedFallback reports that it was the
// one that worked, which is worth logging because it marks a device that
// deviates from the standard.
//
// The order matters beyond the flag: a first byte whose low bits happen to
// be 5 — they carry accessibility and content bits, not the mode — would
// otherwise be read as mode 5 with a block count taken from the mode byte,
// which yields an empty cipher region and looks like a wrong key.
//
// If neither reading yields mode 5, ok is false: either the telegram uses
// an encryption mode this system does not support, or it was never
// encrypted at all (mode 0) — see configWordIsCleartext for telling those
// two apart.
func readConfigWord(b0, b1 byte) (blocks int, usedFallback bool, ok bool) {
	if mode := b1 & 0x1F; mode == oms5 {
		return int(b0&0xF0) >> 4, false, true
	}
	if mode := b0 & 0x1F; mode == oms5 {
		return int(b1&0xF0) >> 4, true, true
	}
	return 0, false, false
}

// configWordIsCleartext reports whether a config word's mode field, read
// the standard way (the second transmitted byte, see readConfigWord), is
// 0 — meaning the telegram announces, in its own header, that it was
// never encrypted in the first place. This is a distinct case from an
// unsupported encryption mode: there is no cipher to fail at, and the
// payload past the header is already usable.
//
// Only the standard byte order is checked here. The swapped reading used
// as a fallback above exists because one device family (Befund B02) was
// confirmed misencoding mode 5 specifically; there is no evidence any
// device does the same for mode 0, so guessing at a swapped mode-0 reading
// would be unverified behavior, not a documented device quirk.
func configWordIsCleartext(mode byte) bool {
	return mode&0x1F == 0
}
