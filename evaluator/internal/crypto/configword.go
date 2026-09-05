package crypto

// oms5 is the OMS Security Profile A encryption mode this system supports.
const oms5 = 5

// readConfigWord interprets a telegram's two-byte encryption configuration
// word. The standard places the encryption mode in the low byte and the
// block count in the high byte; at least one encountered device family
// swaps that layout. Both readings are tried, standard first; the first one
// that yields mode 5 is used. usedFallback reports whether the
// non-standard reading was the one that worked, so callers can log it.
//
// If neither reading yields mode 5, ok is false: either the telegram uses
// an encryption mode this system does not support, or it was never
// encrypted at all (mode 0) — see configWordIsCleartext for telling those
// two apart.
func readConfigWord(b0, b1 byte) (blocks int, usedFallback bool, ok bool) {
	if mode := b0 & 0x1F; mode == oms5 {
		return int(b1&0xF0) >> 4, false, true
	}
	if mode := b1 & 0x1F; mode == oms5 {
		return int(b0&0xF0) >> 4, true, true
	}
	return 0, false, false
}

// configWordIsCleartext reports whether a config word's mode field, read
// the standard way, is 0 — meaning the telegram announces, in its own
// header, that it was never encrypted in the first place. This is a
// distinct case from an unsupported encryption mode: there is no cipher to
// fail at, and the payload past the header is already usable.
//
// Only the standard byte order is checked here. The swapped reading used
// as a fallback above exists because one device family (Befund B02) was
// confirmed misencoding mode 5 specifically; there is no evidence any
// device does the same for mode 0, so guessing at a swapped mode-0 reading
// would be unverified behavior, not a documented device quirk.
func configWordIsCleartext(b0 byte) bool {
	return b0&0x1F == 0
}
