package telegram

import (
	"encoding/binary"
	"errors"
)

// Frame is a validated wM-Bus telegram.
type Frame struct {
	L    byte // length byte of the wM-Bus telegram
	C    byte
	M    uint16 // manufacturer field, as transmitted (little-endian)
	A    [4]byte
	Ver  byte
	Med  byte
	CI   byte
	Rest []byte // everything from ACC (inclusive) to the end of the telegram

	// Raw is the complete wM-Bus telegram (L byte through the last payload
	// byte), unmodified. Downstream stages (encryption detection,
	// decryption, value decoding) operate on offsets into Raw.
	Raw []byte
}

var (
	ErrTooShort    = errors.New("telegram: frame shorter than header")
	ErrMeterNumber = errors.New("telegram: invalid meter number (non-BCD digit)")
)

// ParseWMBus decodes a wM-Bus telegram (length byte through the last
// payload byte, i.e. the same shape as Frame.Raw). This is what the archive
// stores (see internal/archive), so reconstructing a Frame from an archived
// entry goes through this function.
func ParseWMBus(wmbus []byte) (*Frame, error) {
	if len(wmbus) < 11 {
		return nil, ErrTooShort
	}

	if wmbus[10] == 0x72 && len(wmbus) < longHeaderIdentityOffset+8 {
		// A long header that is cut off before its own identification:
		// the meter this data belongs to cannot be named at all, and
		// falling back to the link layer would file it under the radio
		// converter instead (see Identity).
		return nil, ErrTooShort
	}

	f := &Frame{
		L:    wmbus[0],
		C:    wmbus[1],
		M:    binary.LittleEndian.Uint16(wmbus[2:4]),
		Ver:  wmbus[8],
		Med:  wmbus[9],
		CI:   wmbus[10],
		Rest: wmbus[11:],
		Raw:  wmbus,
	}
	copy(f.A[:], wmbus[4:8])

	if _, err := f.MeterNumber(); err != nil {
		return nil, err
	}
	return f, nil
}

// Identity names one device: manufacturer, address, version and medium,
// in the same four fields the link layer and the long transport header
// both carry.
type Identity struct {
	M   uint16
	A   [4]byte
	Ver byte
	Med byte
}

// longHeaderIdentityOffset is where a long transport header's own
// identification starts, immediately after the CI byte: four address
// bytes, two manufacturer bytes, version, medium.
const longHeaderIdentityOffset = 11

// Identity returns the identification of the device a telegram's *data*
// belongs to.
//
// Usually that is the link layer's, the device that transmits. But a
// radio converter reads meters over a wired bus and sends on their
// behalf: the link layer then names the converter, and only the long
// transport header (CI 0x72) names the meter the data actually came from.
// A Techem converter in front of a Hydrometer water meter is exactly that
// case — link layer TCH, header HYD, and two entirely different numbers.
//
// Everything that identifies the metering device goes through here: the
// meter number, the decryption IV (the OMS security profile builds it
// from the header's identification where there is one), and the
// manufacturer a value decoder is chosen by.
func (f *Frame) Identity() Identity {
	id := Identity{M: f.M, A: f.A, Ver: f.Ver, Med: f.Med}
	if f.CI != 0x72 || len(f.Raw) < longHeaderIdentityOffset+8 {
		return id
	}
	h := f.Raw[longHeaderIdentityOffset:]
	copy(id.A[:], h[0:4])
	id.M = binary.LittleEndian.Uint16(h[4:6])
	id.Ver, id.Med = h[6], h[7]
	return id
}

// MeterNumber decodes the 8-digit meter number printed on the device from
// its address field (see Identity): byte-reversed, read as packed BCD.
func (f *Frame) MeterNumber() (string, error) {
	a := f.Identity().A
	digits := make([]byte, 0, 8)
	for i := 3; i >= 0; i-- {
		b := a[i]
		hi, lo := b>>4, b&0x0F
		if hi > 9 || lo > 9 {
			return "", ErrMeterNumber
		}
		digits = append(digits, '0'+hi, '0'+lo)
	}
	return string(digits), nil
}
