package filter

import (
	"testing"

	"selbst-ableser/collector/internal/settings"
)

func TestKeepNilFilterKeepsEverything(t *testing.T) {
	var f *Filter
	if !f.Keep("90000001", "aabb") {
		t.Error("a nil Filter should keep everything")
	}
}

func TestKeepBlocksMatchingMeterAndPrefix(t *testing.T) {
	f := New([]settings.FilterRule{{MeterID: "90000001", BlockedPrefixes: []string{"aabb"}}})
	if f.Keep("90000001", "aabbcc") {
		t.Error("expected the matching meter+prefix to be blocked")
	}
	if !f.Keep("90000002", "aabbcc") {
		t.Error("a different meter should not be blocked by this rule")
	}
	if !f.Keep("90000001", "ccdd") {
		t.Error("a non-matching prefix should not be blocked")
	}
}

// TestKeepIsCaseInsensitive covers Prüfpunkt 7.B: rawHex is lowercased in
// practice by hex.EncodeToString, but nothing here should depend on that —
// an uppercase-hex caller must be blocked exactly like a lowercase one.
func TestKeepIsCaseInsensitive(t *testing.T) {
	f := New([]settings.FilterRule{{MeterID: "90000001", BlockedPrefixes: []string{"AaBb"}}})
	if f.Keep("90000001", "AABBCC") {
		t.Error("expected an uppercase-hex telegram to be blocked by a mixed-case rule prefix")
	}
	if f.Keep("90000001", "aabbcc") {
		t.Error("expected a lowercase-hex telegram to still be blocked")
	}
}

func TestKeepAnyMeterWildcard(t *testing.T) {
	f := New([]settings.FilterRule{{MeterID: AnyMeter, BlockedPrefixes: []string{"ff"}}})
	if f.Keep("90000001", "ffaa") {
		t.Error("the wildcard rule should apply to every meter")
	}
}
