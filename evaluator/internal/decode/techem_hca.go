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
//	12-13  previous-period date (the billing reset day), see decodeTechemDate
//	14-15  previous-period reading, little-endian uint16 — the value frozen
//	       at that day, i.e. the Stichtagswert
//	16-17  current-period date, same encoding shifted four bits (unused here)
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
	RegisterManufacturerBillingReset("TCH", decodeTechemHCABillingReset)
}

// decodeTechemHCABillingReset reads the value this device froze at its last
// billing-period reset, and the day it belongs to.
//
// The date is not the standard type G encoding decodeDateG reads: Techem
// packs day, month and year into one little-endian uint16 with its own bit
// positions (day in bits 0-4, month in 5-8, year in 9-14). Verified against
// a captured telegram whose frozen value was 592 on 2026-07-31, decoded
// identically by wmbusmeters' fhkvdataiii driver.
func decodeTechemHCABillingReset(raw []byte) (BillingReset, bool, error) {
	if len(raw) != techemHCAFrameLen {
		return BillingReset{}, false, nil
	}
	reset := BillingReset{
		Reading: Reading{Value: int64(decodeUintLE(raw[14:16])), Unit: UnitHeatCostAllocator},
	}
	if date, ok := decodeTechemDate(raw[12:14]); ok {
		reset.Date, reset.HasDate = date, true
	}
	return reset, true, nil
}

// decodeTechemDate unpacks Techem's previous-period date. An implausible
// field (day 0, month 0 or month > 12 — what a device that has not reached
// its first reset yet reports) is refused rather than turned into a
// nonsense date the billing would then compare against.
func decodeTechemDate(data []byte) (Date, bool) {
	if len(data) != 2 {
		return Date{}, false
	}
	raw := uint16(data[0]) | uint16(data[1])<<8
	date := Date{
		Day:   int(raw & 0x1F),
		Month: int((raw >> 5) & 0x0F),
		Year:  2000 + int((raw>>9)&0x3F),
	}
	if date.Day == 0 || date.Month == 0 || date.Month > 12 {
		return Date{}, false
	}
	return date, true
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
