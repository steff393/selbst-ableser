package qr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// TestMatchesReferenceEncoder pins every mask of one symbol per version
// family (single block, two blocks, version information, blocks of two
// lengths, 16-bit character count) to the output of an independent
// encoder: the hashes were computed with segno 1.6 (Python), forcing the
// same version, level M, byte mode and mask, over the module rows as
// "0"/"1" lines, masks 0 to 7 in a row. One step of segno was replaced
// for that: its write_padding_bits appends a whole zero byte when the bit
// stream already ends on a codeword boundary, which in byte mode is always
// the case; ISO 18004 7.4.10 pads only a stream that does not (as zxing
// does too). Scanners read either, but only one can be pinned here.
func TestMatchesReferenceEncoder(t *testing.T) {
	cases := []struct {
		text    string
		version int
		want    string
	}{
		{"selbst-ableser", 1, "c26ba7a87e6cc4c2536efe27b8c029492005ed54f6b8c95d2d3041a0b4f93583"},
		{"https://verbrauch.example.de/login#code=ABCD-EFGH-JKMN", 4, "dd509650d34188435131d910bbdd0e64b13931369fb2d675a4d3bb9c013e9b39"},
		{"https://verbrauch.example.de/login#code=ABCD-EFGH-JKMN&x=" + strings.Repeat("7", 60), 7, "b9c6c897b180679423dba643d4e87fdcc315ba6d1bb4ee2310bf46976d05060e"},
		{strings.Repeat("0123456789abcdefghijklmnopqrstuvwxyz", 4), 8, "8db2f9a0eea418d60e48ce40eef25816854ef63e4473243f2a1f3bc8d6f656d5"},
		{strings.Repeat("Zugangscode fuer Wohnung 3: 2345-6789-ABCD ", 4), 10, "2859a4adf11d27de568bca01b00108127e4a41347f47be12a277d9229ed6e8e3"},
	}
	for _, tc := range cases {
		h := sha256.New()
		for mask := 0; mask < 8; mask++ {
			c := encode([]byte(tc.text), tc.version, mask)
			for y := 0; y < c.Size; y++ {
				var row strings.Builder
				for x := 0; x < c.Size; x++ {
					if c.Dark(x, y) {
						row.WriteByte('1')
					} else {
						row.WriteByte('0')
					}
				}
				row.WriteByte('\n')
				h.Write([]byte(row.String()))
			}
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != tc.want {
			t.Errorf("version %d: matrix hash %s, want %s", tc.version, got, tc.want)
		}
	}
}

func TestEncodePicksSmallestVersion(t *testing.T) {
	for _, tc := range []struct {
		n, size int
	}{
		{14, 21},  // version 1 holds 14 bytes
		{15, 25},  // one more needs version 2
		{62, 33},  // version 4, the login link's usual size
		{213, 57}, // version 10 at its limit
	} {
		c, err := Encode(strings.Repeat("x", tc.n))
		if err != nil {
			t.Fatalf("%d bytes: %v", tc.n, err)
		}
		if c.Size != tc.size {
			t.Errorf("%d bytes: size %d, want %d", tc.n, c.Size, tc.size)
		}
	}
	if _, err := Encode(strings.Repeat("x", 214)); !errors.Is(err, ErrTooLong) {
		t.Errorf("214 bytes: err %v, want ErrTooLong", err)
	}
}
