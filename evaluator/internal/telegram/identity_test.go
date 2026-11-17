package telegram

import (
	"encoding/hex"
	"testing"
)

// Two real telegrams whose link layer and long header name different
// devices. The first is wmbusmeters' own fixture for exactly this case;
// the second was captured on 2026-09-14 with an RTL-SDR: a Techem radio
// converter transmitting for a Wehrle cold-water meter.
const (
	longHeaderHydrometer = "66442423818181816" + "40E72" + "46564656A51170071F005005" +
		"2F2F15257A616F14139172137DAE3A0C000000008C2013917213000B3B0000000B26784601025AF5000266EF00046D1B08B7214C1338861200CC101300000000CC201338861200426C9F2C42EC7EBF2C"
	longHeaderTechemWater = "4b4468502643660295377216395403a85cfe07b0003005155ed42729bf7f5a37d10b621e0f72adeba4110e779226de018d7453823ebfe86d613ba2d7c223c4926f52463a598d330fc43078b5"
)

func TestMeterNumberComesFromTheLongHeader(t *testing.T) {
	cases := []struct {
		name         string
		telegram     string
		wantNumber   string
		wantMfct     string
		wantMedium   byte
		linkLayerNum string
	}{
		{"wmbusmeters fixture", longHeaderHydrometer, "56465646", "DME", 0x07, "81818181"},
		{"Techem converter, Wehrle water meter", longHeaderTechemWater, "03543916", "WEH", 0x07, "02664326"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := hex.DecodeString(c.telegram)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseWMBus(raw)
			if err != nil {
				t.Fatalf("ParseWMBus: %v", err)
			}
			got, err := f.MeterNumber()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.wantNumber {
				t.Errorf("MeterNumber = %s, want %s (the meter, not the radio converter %s)", got, c.wantNumber, c.linkLayerNum)
			}
			id := f.Identity()
			if Manufacturer(id.M) != c.wantMfct || id.Med != c.wantMedium {
				t.Errorf("Identity = %s/medium 0x%02X, want %s/0x%02X", Manufacturer(id.M), id.Med, c.wantMfct, c.wantMedium)
			}
			// The link-layer fields stay untouched: they still describe
			// the device that transmitted, which the diagnostics want.
			if Manufacturer(f.M) != "TCH" && Manufacturer(f.M) != "HYD" {
				t.Errorf("link-layer manufacturer = %s, want it left as transmitted", Manufacturer(f.M))
			}
		})
	}
}

// Without a long header there is nothing to prefer, and the number has to
// stay the link layer's — every meter read so far depends on that.
func TestMeterNumberFallsBackToTheLinkLayer(t *testing.T) {
	// Real Qundis heat meter, CI 0x78: no transport header at all.
	raw, err := hex.DecodeString("5944934436718168483778077936718168934448040DFF5F35")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseWMBus(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.MeterNumber()
	if err != nil {
		t.Fatal(err)
	}
	if got != "68817136" {
		t.Errorf("MeterNumber = %s, want 68817136", got)
	}
}

// A long header cut off before its own identification names no meter at
// all. Falling back to the link layer would file the data under the radio
// converter, mixing several meters into one number.
func TestParseRejectsATruncatedLongHeader(t *testing.T) {
	raw, err := hex.DecodeString("12442423818181816" + "40E72" + "4656")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseWMBus(raw); err == nil {
		t.Error("expected a truncated long header to be rejected")
	}
}
