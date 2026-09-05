package decode

import (
	"encoding/binary"
	"fmt"
)

// Techem's "fhkvdataiii"-family heat-cost-allocator devices send a
// manufacturer-specific (CI 0xA0-0xB7) telegram whose payload has no
// self-describing DIF/VIF structure — the byte layout below was recovered
// by cross-referencing several archived telegrams against their decoded
// values.
//
// Layout, as an offset from the start of the full raw telegram (so offset
// 11 is the byte immediately after the CI field at offset 10):
//
//	11     constant marker byte
//	12-13  previous-period date, a Techem-internal encoding (unused here)
//	14-15  previous-period reading, little-endian uint16
//	16-17  current-period date, a Techem-internal encoding (unused here)
//	18-19  current reading, little-endian uint16
//	20-21  room temperature, little-endian uint16 / 100 = °C (unused here)
//	22-23  radiator temperature, same encoding (unused here)
//
// Only the one frame length actually observed in practice (51 bytes total,
// i.e. L = 0x32) is recognized; anything else reports ok=false rather than
// guessing at an unconfirmed layout.
const techemHCAFrameLen = 51

func init() {
	RegisterManufacturerDecoder("TCH", decodeTechemHCA)
	RegisterManufacturerPatcher("TCH", patchTechemHCA)
}

func decodeTechemHCA(raw []byte) (Reading, bool, error) {
	if len(raw) != techemHCAFrameLen {
		return Reading{}, false, nil
	}
	current := decodeUintLE(raw[18:20])
	return Reading{Value: int64(current), Unit: UnitHeatCostAllocator}, true, nil
}

// patchTechemHCA is decodeTechemHCA's write-back counterpart: it
// overwrites just the current-reading field (offset 18-19), leaving every
// other byte — including the previous-period reading and both dates —
// untouched.
func patchTechemHCA(raw []byte, newValue int64) error {
	if len(raw) != techemHCAFrameLen {
		return fmt.Errorf("decode: techem HCA frame must be %d bytes, got %d", techemHCAFrameLen, len(raw))
	}
	if newValue < 0 || newValue > 0xFFFF {
		return fmt.Errorf("decode: value %d does not fit in a 16-bit reading", newValue)
	}
	binary.LittleEndian.PutUint16(raw[18:20], uint16(newValue))
	return nil
}
