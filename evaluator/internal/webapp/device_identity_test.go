package webapp

import (
	"encoding/hex"
	"testing"

	"selbst-ableser/internal/telegram"
)

// The same real Qundis heat-meter telegram internal/decode tests against
// (captured 2026-09-09): its link layer says "radio converter", and only
// the application layer says what the device measures.
const qundisHeatTelegramHex = "" +
	"5944934438718168483778" +
	"07793871816893444804" +
	"0dff5f350082690000f00007c006ffff00000000ffff0000" +
	"00005f380000000000800080008000800080008000800080" +
	"00800080008000002f02fd170000046d0908493964"

func TestDeviceIdentityPrefersTheMeterOverTheRadio(t *testing.T) {
	raw, err := hex.DecodeString(qundisHeatTelegramHex)
	if err != nil {
		t.Fatal(err)
	}
	if raw[9] != 0x37 {
		t.Fatalf("fixture changed: link-layer medium 0x%02X, expected 0x37", raw[9])
	}

	_, got := deviceIdentity(raw, [16]byte{})
	if got != 0x04 {
		t.Fatalf("deviceIdentity medium = 0x%02X, want 0x04 (heat)", got)
	}

	// The whole point of the exercise: the operator-facing label, and the
	// meter kind the "Erkannte Zähler" page suggests, both follow.
	dt, ok := telegram.IdentifyDeviceType(got)
	if !ok || dt.Abbr != "WMZ" {
		t.Errorf("IdentifyDeviceType(0x%02X) = %+v, %v; want WMZ", got, dt, ok)
	}
	if kind, ok := kindFromMedium(got); !ok || kind.String() != "heat meter" {
		t.Errorf("kindFromMedium(0x%02X) = %v, %v; want heat meter", got, kind, ok)
	}
}

// Without an enhanced identification record there is nothing to prefer,
// and the link-layer medium has to come through unchanged — every meter
// this system billed before this function existed depends on it.
func TestDeviceIdentityFallsBackToTheHeader(t *testing.T) {
	// A minimal CI 0x78 telegram: header, then one volume record.
	raw, err := hex.DecodeString("0b44934401020304010778041301020304")
	if err != nil {
		t.Fatal(err)
	}
	if _, got := deviceIdentity(raw, [16]byte{}); got != 0x07 {
		t.Errorf("deviceIdentity medium = 0x%02X, want the link-layer 0x07", got)
	}
}

// An encrypted telegram whose key is not at hand has no readable
// application layer, so the header stays the answer rather than a guess
// off ciphertext.
func TestDeviceIdentityKeepsTheHeaderForUnreadableCiphertext(t *testing.T) {
	// CI 0x7A (short header) with config word announcing mode 5.
	raw, err := hex.DecodeString("1e449344010203040107" + "7a" + "01000025" + "8877665544332211008877665544332211")
	if err != nil {
		t.Fatal(err)
	}
	if _, got := deviceIdentity(raw, [16]byte{}); got != 0x07 {
		t.Errorf("deviceIdentity medium = 0x%02X, want the link-layer 0x07", got)
	}
}

func TestDeviceIdentityTooShort(t *testing.T) {
	if _, got := deviceIdentity([]byte{0x01, 0x02}, [16]byte{}); got != 0x00 {
		t.Errorf("deviceIdentity on a stub = 0x%02X, want 0x00", got)
	}
}

// A Techem radio converter transmitting for a Wehrle water meter, captured
// 2026-09-14. The link layer says Techem and "radio converter"; the long
// transport header names the meter itself. The operator has to see the
// meter, since that is what the number shown beside it now refers to.
func TestDeviceIdentityPrefersTheLongHeader(t *testing.T) {
	raw, err := hex.DecodeString("4b4468502643660295377216395403a85cfe07b0003005155ed42729bf7f5a37d10b621e0f72adeba4110e779226de018d7453823ebfe86d613ba2d7c223c4926f52463a598d330fc43078b5")
	if err != nil {
		t.Fatal(err)
	}
	manufacturer, medium := deviceIdentity(raw, [16]byte{})
	if manufacturer != "WEH" {
		t.Errorf("manufacturer = %q, want WEH (the meter, not the TCH converter)", manufacturer)
	}
	dt, ok := telegram.IdentifyDeviceType(medium)
	if !ok || dt.Abbr != "KWZ" {
		t.Errorf("device type = %+v (ok=%v), want KWZ, not the converter's RCM", dt, ok)
	}
}
