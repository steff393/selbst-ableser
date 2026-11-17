package webapp

import (
	"encoding/hex"
	"testing"
)

// Four real telegrams, one per encryption state the live view has to tell
// apart. The first is the case this test exists for: a Qundis warm-water
// meter whose config word announces mode 0. It carries a transport header
// that *could* be encrypted and is not — and used to be shown with the
// same closed padlock as a genuinely encrypted telegram.
const (
	aesQundisWaterMode0 = "394493444346451218067a370000200c13476905004c1312630500426c3f3ccc081312630500c2086c3f3c02bb560000326cffff046d080d5731"
	aesTechemWaterMode5 = "4b4468502643660295377216395403a85cfe07b0003005155ed42729bf7f5a37d10b621e0f72adeba4110e779226de018d7453823ebfe86d613ba2d7c223c4926f52463a598d330fc43078b5"
	aesQundisHeatNoTPL  = "5944934436718168483778077936718168934448040DFF5F350082E100005E0107C006FFFF01000000FFFF000000005F38000000000080008000800080008000800080008000800080008000002F02FD170000046D33084B3949"
	aesTechemAllocator  = "32446850087579106980a0119f333201e012be0024092d09000000000000000000050a0c111410141d1f1e1b141711110d0801"
)

func TestAESLockIcon(t *testing.T) {
	cases := []struct {
		name      string
		telegram  string
		wantIcon  string
		wantTitle string
	}{
		{"mode 0 in a short header is cleartext", aesQundisWaterMode0, "🔓", "unverschlüsselt"},
		{"mode 5 in a long header is encrypted", aesTechemWaterMode5, "🔒", "verschlüsselt, kein Schlüssel hinterlegt"},
		{"no transport header at all", aesQundisHeatNoTPL, "🔓", "unverschlüsselt"},
		{"manufacturer-specific", aesTechemAllocator, "🔓", "unverschlüsselt (herstellerspezifisch)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := hex.DecodeString(c.telegram)
			if err != nil {
				t.Fatal(err)
			}
			icon, title := aesLockIcon(raw, [16]byte{})
			if icon != c.wantIcon || title != c.wantTitle {
				t.Errorf("aesLockIcon = %s %q, want %s %q", icon, title, c.wantIcon, c.wantTitle)
			}
		})
	}
}

// With a key on file the padlock says more than "encrypted": whether that
// key actually fits. That is the question the water meters in a Techem
// installation turn on.
func TestAESLockIconTellsAKeyApartFromNone(t *testing.T) {
	raw, err := hex.DecodeString(aesTechemWaterMode5)
	if err != nil {
		t.Fatal(err)
	}
	icon, title := aesLockIcon(raw, [16]byte{1, 2, 3})
	if icon != "🔒" || title != "verschlüsselt, hinterlegter Schlüssel passt nicht" {
		t.Errorf("with a wrong key: %s %q", icon, title)
	}
}

// Anything too short or with a header type this system does not know is
// reported as unknown rather than guessed at.
func TestAESLockIconUnknown(t *testing.T) {
	if icon, title := aesLockIcon([]byte{0x01, 0x02}, [16]byte{}); icon != "?" || title != "unbekannt" {
		t.Errorf("stub telegram: %s %q", icon, title)
	}
	raw, err := hex.DecodeString("0b44934401020304019900")
	if err != nil {
		t.Fatal(err)
	}
	if icon, _ := aesLockIcon(raw, [16]byte{}); icon != "?" {
		t.Errorf("unknown CI 0x99: got %s, want ?", icon)
	}
}
