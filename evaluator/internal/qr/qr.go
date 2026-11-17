// Package qr encodes text as a QR code (ISO/IEC 18004) for the tenant
// access letter: scanned with a phone camera, it opens the login page with
// the access code already filled in.
//
// Only what that letter needs is here — byte mode, error correction level
// M, versions 1 to 10 (up to 213 bytes) — instead of a dependency: a QR
// encoder is a few hundred lines of a frozen standard, and the program is
// meant to keep building for years with as few outside modules as
// possible. qr_test.go pins the output against an independent encoder.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTooLong is returned for text that does not fit into version 10.
var ErrTooLong = errors.New("qr: text too long")

// Code is an encoded QR symbol: Size×Size modules, without the quiet zone.
type Code struct {
	Size    int
	modules []bool // row-major, true = dark
}

// Dark reports whether the module in column x, row y is dark.
func (c *Code) Dark(x, y int) bool { return c.modules[y*c.Size+x] }

// Encode returns the smallest symbol that holds text, with the mask whose
// pattern a scanner reads most reliably (lowest ISO 18004 penalty).
func Encode(text string) (*Code, error) {
	data := []byte(text)
	for version := 1; version < len(levelM); version++ {
		if 4+countBits(version)+8*len(data) <= 8*levelM[version].dataCodewords() {
			var best *Code
			bestPenalty := 0
			for mask := 0; mask < 8; mask++ {
				c := encode(data, version, mask)
				if p := c.penalty(); best == nil || p < bestPenalty {
					best, bestPenalty = c, p
				}
			}
			return best, nil
		}
	}
	return nil, ErrTooLong
}

// SVG draws the code with the four-module quiet zone the standard asks
// for, one unit per module; the caller sizes it.
func (c *Code) SVG() string {
	const quiet = 4
	w := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, w, w)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, w, w)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Dark(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < c.Size && c.Dark(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// blockLayout is one version's error correction structure at level M:
// ec codewords per block, then g1Blocks blocks of g1Data data codewords,
// followed by g2Blocks blocks one codeword longer.
type blockLayout struct{ ec, g1Blocks, g1Data, g2Blocks int }

func (l blockLayout) dataCodewords() int { return l.g1Blocks*l.g1Data + l.g2Blocks*(l.g1Data+1) }

// levelM is ISO 18004 table 9, level M, versions 1 to 10.
var levelM = [...]blockLayout{
	1:  {10, 1, 16, 0},
	2:  {16, 1, 28, 0},
	3:  {26, 1, 44, 0},
	4:  {18, 2, 32, 0},
	5:  {24, 2, 43, 0},
	6:  {16, 4, 27, 0},
	7:  {18, 4, 31, 0},
	8:  {22, 2, 38, 2},
	9:  {22, 3, 36, 2},
	10: {26, 4, 43, 1},
}

// alignmentCenters is ISO 18004 annex E: the row/column coordinates whose
// every pairing (except the three finder corners) carries an alignment
// pattern.
var alignmentCenters = [...][]int{
	2:  {6, 18},
	3:  {6, 22},
	4:  {6, 26},
	5:  {6, 30},
	6:  {6, 34},
	7:  {6, 22, 38},
	8:  {6, 24, 42},
	9:  {6, 26, 46},
	10: {6, 28, 50},
}

// countBits is the width of byte mode's character count field.
func countBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

func encode(data []byte, version, mask int) *Code {
	s := newSymbol(version)
	s.placeCodewords(codewords(data, version))
	s.applyMask(mask)
	s.drawFormat(mask)
	return &Code{Size: s.size, modules: s.dark}
}

// symbol is a code under construction. function marks the modules that
// belong to a pattern or to format/version information: data skips them,
// and masking leaves them alone.
type symbol struct {
	size     int
	dark     []bool
	function []bool
}

func (s *symbol) set(x, y int, dark bool) {
	s.dark[y*s.size+x] = dark
	s.function[y*s.size+x] = true
}

func newSymbol(version int) *symbol {
	size := 17 + 4*version
	s := &symbol{size: size, dark: make([]bool, size*size), function: make([]bool, size*size)}
	for i := 0; i < size; i++ {
		s.set(6, i, i%2 == 0)
		s.set(i, 6, i%2 == 0)
	}
	s.finder(3, 3)
	s.finder(size-4, 3)
	s.finder(3, size-4)
	centers := alignmentCenters[version]
	last := len(centers) - 1
	for i, cx := range centers {
		for j, cy := range centers {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue // that corner holds a finder pattern
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					s.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	s.drawFormat(0) // only reserves the area; the real mask is known after masking
	if version >= 7 {
		s.drawVersion(version)
	}
	return s
}

// finder draws a finder pattern centred on (cx, cy) together with its
// light separator, clipped at the symbol's edge.
func (s *symbol) finder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || x >= s.size || y < 0 || y >= s.size {
				continue
			}
			d := max(abs(dx), abs(dy))
			s.set(x, y, d != 2 && d != 4)
		}
	}
}

// drawFormat writes both copies of the format information: level M (00)
// and the mask, protected by a BCH(15,5) code and XORed with 0x5412.
func (s *symbol) drawFormat(mask int) {
	data := 0b00<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }

	for i := 0; i <= 5; i++ {
		s.set(8, i, bit(i))
	}
	s.set(8, 7, bit(6))
	s.set(8, 8, bit(7))
	s.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		s.set(14-i, 8, bit(i))
	}

	for i := 0; i < 8; i++ {
		s.set(s.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		s.set(8, s.size-15+i, bit(i))
	}
	s.set(8, s.size-8, true) // the dark module, always set
}

// drawVersion writes both copies of the version information (version 7
// on), protected by a BCH(18,6) code.
func (s *symbol) drawVersion(version int) {
	rem := version
	for i := 0; i < 12; i++ {
		rem = rem<<1 ^ (rem>>11)*0x1F25
	}
	bits := version<<12 | rem
	for i := 0; i < 18; i++ {
		dark := bits>>i&1 == 1
		a, b := s.size-11+i%3, i/3
		s.set(a, b, dark)
		s.set(b, a, dark)
	}
}

// codewords builds the final codeword sequence: the byte-mode segment,
// terminated and padded to the version's capacity, split into blocks,
// each block followed by its Reed-Solomon codewords, all interleaved.
func codewords(data []byte, version int) []byte {
	l := levelM[version]
	capacity := 8 * l.dataCodewords()

	var bits []bool
	appendBits := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, v>>i&1 == 1)
		}
	}
	appendBits(0b0100, 4) // byte mode
	appendBits(len(data), countBits(version))
	for _, b := range data {
		appendBits(int(b), 8)
	}
	appendBits(0, min(4, capacity-len(bits))) // terminator
	appendBits(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity; pad ^= 0xEC ^ 0x11 {
		appendBits(pad, 8)
	}
	msg := make([]byte, len(bits)/8)
	for i, b := range bits {
		if b {
			msg[i/8] |= 0x80 >> (i % 8)
		}
	}

	divisor := rsDivisor(l.ec)
	blocks := make([][]byte, l.g1Blocks+l.g2Blocks)
	ecs := make([][]byte, len(blocks))
	for i, k := 0, 0; i < len(blocks); i++ {
		n := l.g1Data
		if i >= l.g1Blocks {
			n++
		}
		blocks[i] = msg[k : k+n]
		ecs[i] = rsRemainder(blocks[i], divisor)
		k += n
	}
	var out []byte
	for i := 0; i <= l.g1Data; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < l.ec; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// placeCodewords fills the non-function modules in the standard zigzag:
// two-module-wide columns from the right edge, alternately upward and
// downward, skipping the vertical timing pattern. Modules left over (the
// remainder bits of some versions) stay light.
func (s *symbol) placeCodewords(cw []byte) {
	i := 0
	for right := s.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		upward := (right+1)&2 == 0
		for vert := 0; vert < s.size; vert++ {
			y := vert
			if upward {
				y = s.size - 1 - vert
			}
			for j := 0; j < 2; j++ {
				x := right - j
				if s.function[y*s.size+x] || i >= len(cw)*8 {
					continue
				}
				s.dark[y*s.size+x] = cw[i/8]>>(7-i%8)&1 == 1
				i++
			}
		}
	}
}

func (s *symbol) applyMask(mask int) {
	for y := 0; y < s.size; y++ {
		for x := 0; x < s.size; x++ {
			if !s.function[y*s.size+x] && maskHit(mask, x, y) {
				s.dark[y*s.size+x] = !s.dark[y*s.size+x]
			}
		}
	}
}

// maskHit is ISO 18004 table 10, with x the column and y the row.
func maskHit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

// penalty scores a finished symbol by the four rules of ISO 18004 7.8.3:
// long runs, 2×2 blocks, finder-like sequences and an unbalanced share of
// dark modules all make a scanner's job harder.
func (c *Code) penalty() int {
	n := c.Size
	p := 0
	for i := 0; i < n; i++ {
		p += linePenalty(n, func(j int) bool { return c.Dark(j, i) })
		p += linePenalty(n, func(j int) bool { return c.Dark(i, j) })
	}
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			d := c.Dark(x, y)
			if d == c.Dark(x+1, y) && d == c.Dark(x, y+1) && d == c.Dark(x+1, y+1) {
				p += 3
			}
		}
	}
	dark := 0
	for _, d := range c.modules {
		if d {
			dark++
		}
	}
	return p + abs(dark*20-n*n*10)/(n*n)*10
}

// finderLike is dark-light-dark-dark-dark-light-dark preceded by four
// light modules; the same sequence reversed counts as well.
var finderLike = [11]bool{false, false, false, false, true, false, true, true, true, false, true}

// linePenalty scores one row or column: rule 1 (runs of five or more
// modules of one colour) and rule 3 (finder-like sequences).
func linePenalty(n int, at func(int) bool) int {
	p := 0
	run := 1
	for j := 1; j <= n; j++ {
		if j < n && at(j) == at(j-1) {
			run++
			continue
		}
		if run >= 5 {
			p += 3 + run - 5
		}
		run = 1
	}
	for j := 0; j+len(finderLike) <= n; j++ {
		forward, backward := true, true
		for k, want := range finderLike {
			forward = forward && at(j+k) == want
			backward = backward && at(j+len(finderLike)-1-k) == want
		}
		if forward || backward {
			p += 40
		}
	}
	return p
}

// gfMul multiplies in GF(2^8) modulo x^8+x^4+x^3+x^2+1, the field of the
// code's Reed-Solomon error correction.
func gfMul(x, y byte) byte {
	z := 0
	for i := 7; i >= 0; i-- {
		z = z<<1 ^ (z>>7)*0x11D
		z ^= int(y>>i&1) * int(x)
	}
	return byte(z)
}

// rsDivisor returns the generator polynomial of the given degree, the
// product of (x - 2^i) for i below degree, without its leading 1.
func rsDivisor(degree int) []byte {
	d := make([]byte, degree)
	d[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range d {
			d[j] = gfMul(d[j], root)
			if j+1 < degree {
				d[j] ^= d[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return d
}

// rsRemainder is the error correction for one block: the remainder of
// the block, as a polynomial, divided by the generator.
func rsRemainder(data, divisor []byte) []byte {
	r := make([]byte, len(divisor))
	for _, b := range data {
		f := b ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i := range r {
			r[i] ^= gfMul(divisor[i], f)
		}
	}
	return r
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
