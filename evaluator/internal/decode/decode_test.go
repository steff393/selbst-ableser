package decode

import (
	"encoding/hex"
	"testing"

	"selbst-ableser/internal/telegram"
)

func TestStandardShortHeader(t *testing.T) {
	raw, err := hex.DecodeString(qundisWMBusHex)
	if err != nil {
		t.Fatalf("bad fixture hex: %v", err)
	}
	records, ok, err := Standard(0x7A, raw)
	if err != nil {
		t.Fatalf("Standard: %v", err)
	}
	if !ok {
		t.Fatal("Standard should recognize CI 0x7A")
	}
	current, found, err := CurrentValue(records)
	if err != nil || !found {
		t.Fatalf("CurrentValue: found=%v err=%v", found, err)
	}
	if current.Value != 45 || current.Unit != UnitHeatCostAllocator {
		t.Errorf("CurrentValue = %+v, want {45 HCA}", current)
	}
}

func TestStandardManufacturerSpecificNotRecognized(t *testing.T) {
	_, ok, err := Standard(0xA0, []byte{0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("Standard: unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Standard should not claim to handle a manufacturer-specific CI")
	}
}

// techemHCAWMBusHex is a real Techem "fhkvdataiii" heat-cost-allocator
// telegram (manufacturer code TCH, CI 0xA0), cross-checked against an
// independent third-party decoder: current reading 535, previous-period
// reading 1691.
const techemHCAWMBusHex = "32446850063276426980a011ff329b066005170211071a073d4e004e7837333b53090e0401000000000000000605046f4a6465"

func TestManufacturerSpecificTechemHCA(t *testing.T) {
	raw, err := hex.DecodeString(techemHCAWMBusHex)
	if err != nil {
		t.Fatalf("bad fixture hex: %v", err)
	}
	reading, ok, err := ManufacturerSpecific(telegram.Manufacturer(0x5068), raw)
	if err != nil || !ok {
		t.Fatalf("ManufacturerSpecific: ok=%v err=%v", ok, err)
	}
	if reading.Value != 535 || reading.Unit != UnitHeatCostAllocator {
		t.Errorf("reading = %+v, want {535 HCA}", reading)
	}
}

func TestManufacturerSpecificTechemHCAWrongLength(t *testing.T) {
	if _, ok, err := ManufacturerSpecific("TCH", []byte{0x00, 0x00, 0x00}); ok || err != nil {
		t.Errorf("ok=%v err=%v, want ok=false, err=nil for an unrecognized frame length", ok, err)
	}
}

func TestManufacturerSpecificExtensionPoint(t *testing.T) {
	const fakeManufacturer = "ZZZ"
	RegisterManufacturerDecoder(fakeManufacturer, func(raw []byte) (Reading, bool, error) {
		return Reading{Value: int64(raw[0]), Unit: UnitLiters}, true, nil
	})

	got, ok, err := ManufacturerSpecific(fakeManufacturer, []byte{42})
	if err != nil || !ok {
		t.Fatalf("ManufacturerSpecific: ok=%v err=%v", ok, err)
	}
	if got.Value != 42 {
		t.Errorf("ManufacturerSpecific value = %d, want 42", got.Value)
	}

	if _, ok, _ := ManufacturerSpecific("does-not-exist", nil); ok {
		t.Error("ManufacturerSpecific should report ok=false for an unregistered manufacturer")
	}
}

// qundisWMBusHex is meter 90000001's January reading from the sequence
// below (see TestCurrentValueAcrossFixtures): a Qundis heat-cost-allocator
// telegram whose payload, right after the config word, already sits in
// decrypted form (current value 45).
const qundisWMBusHex = "384493445805744036087ae11820252f2f0b6e4500004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132"

// TestCurrentValueAcrossFixtures decodes the current heat-cost-allocator
// reading for meter 90000001 across a full year-plus run of monthly
// telegrams and checks it against the hand-verified expected sequence. Each
// telegram differs from qundisWMBusHex only in the encoded reading.
func TestCurrentValueAcrossFixtures(t *testing.T) {
	cases := []struct {
		month string
		hex   string
		want  int64
	}{
		{"2024-12-31", "384493445805744036087ae11820252f2f0b6e8003004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 380},
		{"2025-01-31", qundisWMBusHex, 45},
		{"2025-02-28", "384493445805744036087ae11820252f2f0b6e3001004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 130},
		{"2025-03-31", "384493445805744036087ae11820252f2f0b6e1002004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 210},
		{"2025-04-30", "384493445805744036087ae11820252f2f0b6e4502004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 245},
		{"2025-05-31", "384493445805744036087ae11820252f2f0b6e5502004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 255},
		{"2025-06-30", "384493445805744036087ae11820252f2f0b6e5802004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 258},
		{"2025-07-31", "384493445805744036087ae11820252f2f0b6e6002004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 260},
		{"2025-08-31", "384493445805744036087ae11820252f2f0b6e6202004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 262},
		{"2025-09-30", "384493445805744036087ae11820252f2f0b6e6802004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 268},
		{"2025-10-31", "384493445805744036087ae11820252f2f0b6e0003004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 300},
		{"2025-11-30", "384493445805744036087ae11820252f2f0b6e6003004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 360},
		{"2025-12-31", "384493445805744036087ae11820252f2f0b6e3004004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 430},
		{"2026-01-31", "384493445805744036087ae11820252f2f0b6e5000004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132", 50},
	}

	for _, c := range cases {
		c := c
		t.Run(c.month, func(t *testing.T) {
			raw, err := hex.DecodeString(c.hex)
			if err != nil {
				t.Fatalf("bad fixture hex: %v", err)
			}
			ci := raw[10]
			records, ok, err := Standard(ci, raw)
			if err != nil || !ok {
				t.Fatalf("Standard: ok=%v err=%v", ok, err)
			}
			current, found, err := CurrentValue(records)
			if err != nil || !found {
				t.Fatalf("CurrentValue: found=%v err=%v", found, err)
			}
			if current.Value != c.want {
				t.Errorf("CurrentValue = %d, want %d", current.Value, c.want)
			}
		})
	}
}
