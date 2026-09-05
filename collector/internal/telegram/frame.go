package telegram

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Header identifies the receiver-added envelope in front of the actual
// wM-Bus telegram.
type Header struct {
	EP    byte
	SAP   byte
	MID   byte
	TS    uint32
	Extra [2]byte
	RSSI  int
}

const headerLength = 10

// Frame is a validated wM-Bus telegram together with its receiver header.
type Frame struct {
	Header Header

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
	ErrChecksum    = errors.New("telegram: checksum mismatch")
	ErrLength      = errors.New("telegram: inconsistent length")
	ErrTooShort    = errors.New("telegram: frame shorter than header")
	ErrMeterNumber = errors.New("telegram: invalid meter number (non-BCD digit)")
)

// Parse validates and decodes one already SLIP-unescaped frame (as returned
// by SplitFrame): receiver header, wM-Bus telegram, and trailing checksum.
//
// A frame that fails any of the three checks required by the protocol
// (checksum, length consistency, meter number plausibility) is rejected
// with a descriptive error; the caller is expected to discard it and keep
// receiving, not to abort.
func Parse(content []byte) (*Frame, error) {
	if len(content) < headerLength+2 {
		return nil, ErrTooShort
	}

	body := content[:len(content)-2]
	gotCRC := binary.LittleEndian.Uint16(content[len(content)-2:])
	wantCRC := CRC16(body)
	if gotCRC != wantCRC {
		return nil, fmt.Errorf("%w: got 0x%04X, want 0x%04X", ErrChecksum, gotCRC, wantCRC)
	}

	hdr := Header{
		EP:  body[0],
		SAP: body[1],
		MID: body[2],
		TS:  binary.LittleEndian.Uint32(body[3:7]),
	}
	copy(hdr.Extra[:], body[7:9])
	hdr.RSSI = decodeRSSI(body[9])

	wmbus := body[headerLength:]

	// Total logical frame length, including both SLIP delimiters:
	// 1 (start) + 10 (header) + 1 (length byte) + L (payload) + 2 (CRC) + 1 (end).
	if len(wmbus) < 1 {
		return nil, ErrTooShort
	}
	wantTotal := int(wmbus[0]) + 15
	gotTotal := 1 + len(content) + 1
	if gotTotal != wantTotal {
		return nil, fmt.Errorf("%w: frame length %d, expected %d (L=%d)", ErrLength, gotTotal, wantTotal, wmbus[0])
	}

	f, err := ParseWMBus(wmbus)
	if err != nil {
		return nil, err
	}
	f.Header = hdr

	return f, nil
}

// ParseWMBus decodes a wM-Bus telegram on its own (length byte through the
// last payload byte, i.e. the same shape as Frame.Raw), without the
// receiver envelope Parse also handles.
//
// The Header field of the returned Frame is left zero; only Parse (which
// has an actual receiver header to decode) fills it in.
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

// decodeRSSI interprets the receiver's RSSI byte as a signed 8-bit value in
// dBm (two's complement), rather than unconditionally subtracting 256.
func decodeRSSI(b byte) int {
	if b >= 0x80 {
		return int(b) - 256
	}
	return int(b)
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
