package crypto

import "testing"

func TestReadConfigWord(t *testing.T) {
	// Bytes as observed on a real device: standard reading fails (mode
	// would be 0), the byte-swapped fallback reading succeeds (mode 5,
	// 2 blocks).
	blocks, fallback, ok := readConfigWord(0x20, 0x25)
	if !ok || blocks != 2 || !fallback {
		t.Errorf("readConfigWord(0x20, 0x25) = (%d, %v, %v), want (2, true, true)", blocks, fallback, ok)
	}

	// The same two nibble pairs, standard-compliant byte order: mode in
	// the low byte, block count in the high byte. Should succeed directly.
	blocks, fallback, ok = readConfigWord(0x25, 0x20)
	if !ok || blocks != 2 || fallback {
		t.Errorf("readConfigWord(0x25, 0x20) = (%d, %v, %v), want (2, false, true)", blocks, fallback, ok)
	}

	// Neither reading yields mode 5: not evaluable, not an error.
	if _, _, ok := readConfigWord(0x00, 0x00); ok {
		t.Error("readConfigWord(0x00, 0x00) should not resolve to mode 5")
	}
}

func TestConfigWordIsCleartext(t *testing.T) {
	if !configWordIsCleartext(0x00) {
		t.Error("configWordIsCleartext(0x00) = false, want true (mode 0, no encryption)")
	}
	if configWordIsCleartext(0x05) {
		t.Error("configWordIsCleartext(0x05) = true, want false (mode 5, encrypted)")
	}
	if configWordIsCleartext(0x07) {
		t.Error("configWordIsCleartext(0x07) = true, want false (mode 7, encrypted)")
	}
}
