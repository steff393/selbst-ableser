package telegram

// TransportHeader describes, for a given CI value, the layout of a
// telegram's transport header: where its data-record area begins, and —
// for header types that carry one — where the two-byte encryption config
// word sits.
type TransportHeader struct {
	HasConfigWord    bool
	ConfigWordOffset int // offset into Frame.Raw; valid only if HasConfigWord
	DataOffset       int // offset into Frame.Raw where data records begin
}

// IdentifyTransportHeader looks up the transport header layout for a CI
// value. ok is false for manufacturer-specific (0xA0-0xB7) and unknown CI
// values, which do not have this self-describing structure.
func IdentifyTransportHeader(ci byte) (h TransportHeader, ok bool) {
	switch ci {
	case 0x78: // no transport header
		return TransportHeader{DataOffset: 11}, true
	case 0x7A: // short header
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 13, DataOffset: 15}, true
	case 0x72: // long header
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 21, DataOffset: 23}, true
	case 0x8C: // extended link layer
		return TransportHeader{HasConfigWord: true, ConfigWordOffset: 33, DataOffset: 35}, true
	default:
		return TransportHeader{}, false
	}
}

// EncryptionStatusLabel reports a telegram's encryption state from its CI
// byte alone, in German for direct display — no key needed, so it works
// even for a meter with no configured AES key at all (UI-06).
func EncryptionStatusLabel(ci byte) string {
	if ci >= 0xA0 && ci <= 0xB7 {
		return "unverschlüsselt (herstellerspezifisch)"
	}
	h, ok := IdentifyTransportHeader(ci)
	if !ok {
		return "unbekannt"
	}
	if !h.HasConfigWord {
		return "unverschlüsselt"
	}
	return "verschlüsselt"
}
