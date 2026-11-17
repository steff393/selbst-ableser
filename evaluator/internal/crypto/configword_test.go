package crypto

import "testing"

func TestReadConfigWord(t *testing.T) {
	// Config words as real devices send them — block count in the high
	// nibble of the first byte, mode 5 in the second. All four come from
	// captured telegrams: the first three from Techem radio converters in
	// front of water meters, the last from wmbusmeters' published test
	// telegrams.
	for _, c := range []struct {
		b0, b1 byte
		blocks int
	}{
		{0x20, 0x25, 2},
		{0x30, 0x05, 3},
		{0x40, 0x05, 4},
		{0x50, 0x05, 5},
	} {
		blocks, fallback, ok := readConfigWord(c.b0, c.b1)
		if !ok || blocks != c.blocks || fallback {
			t.Errorf("readConfigWord(0x%02X, 0x%02X) = (%d, %v, %v), want (%d, false, true)",
				c.b0, c.b1, blocks, fallback, ok, c.blocks)
		}
	}

	// The same two bytes swapped, as the one device family that gets the
	// order wrong sends them: still mode 5 with 2 blocks, but flagged.
	blocks, fallback, ok := readConfigWord(0x25, 0x20)
	if !ok || blocks != 2 || !fallback {
		t.Errorf("readConfigWord(0x25, 0x20) = (%d, %v, %v), want (2, true, true)", blocks, fallback, ok)
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
