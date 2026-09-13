package decode

import (
	"encoding/hex"
	"testing"
)

// A captured fhkvdataiii telegram, byte for byte as received, with only
// the device address replaced (offsets 4-7) — nothing else about the frame
// depends on it, and a real installation's meter number does not belong in
// a public repository.
//
// Reference decoding of the original, by wmbusmeters:
//
//	previous_hca:  592          previous_date: 2026-07-31
//	current_hca:   0            current_date:  2026-09-03
//
// The device's billing reset day was 31 July 2026, which is why its live
// counter reads 0 five weeks later: it had been reset in between.
const techemCapturedFrame = "32446850" + "00000000" + "6980a0" +
	"01FF345002301200006D086B0800000000000000000000000000215C5852864B3A0C110100000000"

func techemFrame(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(techemCapturedFrame)
	if err != nil {
		t.Fatalf("decoding the captured frame: %v", err)
	}
	if len(raw) != techemHCAFrameLen {
		t.Fatalf("captured frame is %d bytes, want %d", len(raw), techemHCAFrameLen)
	}
	return raw
}

// TestTechemCapturedFrameCurrentValue is the anchor: if the offsets in this
// file ever drift, this fails against a real device's telegram rather than
// against an assumption.
func TestTechemCapturedFrameCurrentValue(t *testing.T) {
	reading, ok, err := decodeTechemHCA(techemFrame(t))
	if err != nil || !ok {
		t.Fatalf("decodeTechemHCA: ok=%v err=%v", ok, err)
	}
	if reading.Value != 0 || reading.Unit != UnitHeatCostAllocator {
		t.Errorf("reading = %+v, want the live counter 0 in HCA units", reading)
	}
}

func TestTechemCapturedFrameBillingReset(t *testing.T) {
	reset, ok, err := decodeTechemHCABillingReset(techemFrame(t))
	if err != nil || !ok {
		t.Fatalf("decodeTechemHCABillingReset: ok=%v err=%v", ok, err)
	}
	if reset.Reading.Value != 592 {
		t.Errorf("frozen value = %d, want 592", reset.Reading.Value)
	}
	if reset.Reading.Unit != UnitHeatCostAllocator {
		t.Errorf("unit = %v, want HCA units", reset.Reading.Unit)
	}
	if !reset.HasDate {
		t.Fatal("no reset date decoded, want 2026-07-31")
	}
	if reset.Date != (Date{Day: 31, Month: 7, Year: 2026}) {
		t.Errorf("reset date = %+v, want 2026-07-31", reset.Date)
	}
}

// TestDecodeTechemDateRefusesNonsense covers a device that has not reached
// its first billing reset: the field is empty, and an empty field must not
// become a date the billing then compares a Stichtag against.
func TestDecodeTechemDateRefusesNonsense(t *testing.T) {
	if _, ok := decodeTechemDate([]byte{0x00, 0x00}); ok {
		t.Error("an all-zero date field must be refused")
	}
	if _, ok := decodeTechemDate([]byte{0xFF, 0xFF}); ok {
		t.Error("month 15 must be refused")
	}
	if _, ok := decodeTechemDate([]byte{0x00}); ok {
		t.Error("a one-byte field must be refused")
	}
}

// TestTechemBillingResetRejectsOtherFrameLengths keeps the decoder from
// reading offsets out of a frame whose layout it has never been confirmed
// against.
func TestTechemBillingResetRejectsOtherFrameLengths(t *testing.T) {
	if _, ok, _ := decodeTechemHCABillingReset(make([]byte, techemHCAFrameLen+1)); ok {
		t.Error("a frame of another length must not be decoded")
	}
}
