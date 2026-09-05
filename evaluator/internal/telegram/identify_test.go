package telegram

import "testing"

func TestManufacturer(t *testing.T) {
	got := Manufacturer(0x4493)
	if got != "QDS" {
		t.Errorf("Manufacturer(0x4493) = %q, want %q", got, "QDS")
	}
}

func TestIdentifyDeviceType(t *testing.T) {
	dt, ok := IdentifyDeviceType(0x08)
	if !ok || dt.Abbr != "HKV" || dt.Name != "Heizkostenverteiler" {
		t.Errorf("IdentifyDeviceType(0x08) = %+v, %v", dt, ok)
	}

	// 0x80 is a non-standard vendor variant of 0x08 seen on real devices
	// (Techem heat-cost allocators), not documented in EN 13757-3 itself.
	if dt, ok := IdentifyDeviceType(0x80); !ok || dt.Abbr != "HKV" {
		t.Errorf("IdentifyDeviceType(0x80) = %+v, %v, want the HKV vendor-variant exception", dt, ok)
	}

	if _, ok := IdentifyDeviceType(0xFF); ok {
		t.Error("IdentifyDeviceType(0xFF) should be unclassified, got ok=true")
	}
}

func TestManufacturerName(t *testing.T) {
	name, ok := ManufacturerName("TCH")
	if !ok || name != "Techem" {
		t.Errorf("ManufacturerName(TCH) = %q, %v, want Techem, true", name, ok)
	}

	if _, ok := ManufacturerName("ZZZ_UNKNOWN"); ok {
		t.Error("ManufacturerName should report ok=false for a code not in the registry")
	}
}

func TestClassifyRSSI(t *testing.T) {
	cases := []struct {
		dBm  int
		want SignalQuality
	}{
		{-40, SignalExcellent},
		{-50, SignalExcellent},
		{-60, SignalGood},
		{-80, SignalAdequate},
		{-90, SignalWeak},
	}
	for _, c := range cases {
		if got := ClassifyRSSI(c.dBm); got != c.want {
			t.Errorf("ClassifyRSSI(%d) = %v, want %v", c.dBm, got, c.want)
		}
	}
}
