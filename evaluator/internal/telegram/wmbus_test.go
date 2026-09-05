package telegram

import (
	"encoding/hex"
	"testing"
)

// qundisWMBusHex is a Qundis heat-cost-allocator telegram (short header,
// mode 5 encryption announced), meter number 40740558, manufacturer field
// 0x4493, filler bytes 0x2F2F right after the config word.
const qundisWMBusHex = "384493445805744036087ae11820252f2f0b6e4500004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132"

func TestParseWMBus(t *testing.T) {
	wmbus, err := hex.DecodeString(qundisWMBusHex)
	if err != nil {
		t.Fatalf("bad fixture hex: %v", err)
	}
	f, err := ParseWMBus(wmbus)
	if err != nil {
		t.Fatalf("ParseWMBus: %v", err)
	}
	meterNo, err := f.MeterNumber()
	if err != nil || meterNo != "40740558" {
		t.Errorf("MeterNumber = %q, err=%v, want 40740558", meterNo, err)
	}
	if f.CI != 0x7A {
		t.Errorf("CI = 0x%02X, want 0x7A", f.CI)
	}
}

func TestParseWMBusRejectsInvalidMeterNumber(t *testing.T) {
	wmbus, _ := hex.DecodeString(qundisWMBusHex)
	wmbus[4] = 0xFA // nibble > 9, not valid BCD

	if _, err := ParseWMBus(wmbus); err == nil {
		t.Fatal("ParseWMBus accepted a frame with a non-BCD meter number")
	}
}
