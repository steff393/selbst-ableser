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

// MeterNumber decodes the 8-digit meter number printed on the device from
// the A-field: byte-reversed, read as packed BCD.
func (f *Frame) MeterNumber() (string, error) {
	digits := make([]byte, 0, 8)
	for i := 3; i >= 0; i-- {
		b := f.A[i]
		hi, lo := b>>4, b&0x0F
		if hi > 9 || lo > 9 {
			return "", ErrMeterNumber
		}
		digits = append(digits, '0'+hi, '0'+lo)
	}
	return string(digits), nil
}
