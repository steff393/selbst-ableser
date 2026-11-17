package telegram

// TransportHeader describes, for a given CI value, the layout of a
// telegram's transport header: where its data-record area begins, and —
// for header types that carry one — where the two-byte encryption config
// word sits.
type TransportHeader struct {
	HasConfigWord    bool
	ConfigWordOffset int // offset into Frame.Raw; valid only if HasConfigWord
	DataOffset       int // offset into Frame.Raw where data records begin

	// AccessNumberOffset is where the transport header's access counter
	// (ACC) sits in Frame.Raw, or -1 for a header that has none. It is
	// the byte the decryption IV repeats (see internal/crypto), and it
	// does not sit at a fixed distance from the CI field: a short header
	// starts with it, a long header carries eight bytes of its own
	// identification first.
	AccessNumberOffset int
}

// IdentifyTransportHeader looks up the transport header layout for a CI
// value. ok is false for manufacturer-specific (0xA0-0xB7) and unknown CI
// values, which do not have this self-describing structure.
func IdentifyTransportHeader(ci byte) (h TransportHeader, ok bool) {
	switch ci {
	case 0x78: // no transport header
		return TransportHeader{DataOffset: 11, AccessNumberOffset: -1}, true
	case 0x7A: // short header: ACC STS CW CW
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 13, DataOffset: 15, AccessNumberOffset: 11}, true
	case 0x72: // long header: ID(4) MFCT(2) VER MED ACC STS CW CW
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 21, DataOffset: 23, AccessNumberOffset: 19}, true
	case 0x8C: // extended link layer
		// Kept at the link layer's own first byte after CI, which is what
		// this has always used: no telegram of this kind has been seen
		// here, so there is nothing to verify a different choice against.
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 33, DataOffset: 35, AccessNumberOffset: 11}, true
	default:
		return TransportHeader{}, false
	}
}

// ManufacturerSpecificCI reports whether ci marks a manufacturer-specific
// telegram: no standard transport header, no self-describing data
// records, and — since there is no transport layer to carry a config word
// — never encrypted at the transport level.
func ManufacturerSpecificCI(ci byte) bool {
	return ci >= 0xA0 && ci <= 0xB7
}
