package telegram

import (
	"encoding/hex"
	"testing"
)

// A Techem radio converter transmitting for a Wehrle cold-water meter,
// captured 2026-09-14. Link layer and long transport header name two
// different devices, and the number that belongs to the data is the
// meter's — the evaluator keys its archive on what this returns.
func TestMeterNumberComesFromTheLongHeader(t *testing.T) {
	raw, err := hex.DecodeString("4b4468502643660295377216395403a85cfe07b0003005155ed42729bf7f5a37d10b621e0f72adeba4110e779226de018d7453823ebfe86d613ba2d7c223c4926f52463a598d330fc43078b5")
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
	if got != "03543916" {
		t.Errorf("MeterNumber = %s, want 03543916 (the meter, not the converter 02664326)", got)
	}
}

// Without a long header the number stays the link layer's.
func TestMeterNumberFallsBackToTheLinkLayer(t *testing.T) {
	raw, err := hex.DecodeString("32446850087579106980a0119f333201e012be0024092d09000000000000000000050a0c111410141d1f1e1b141711110d0801")
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
	if got != "10797508" {
		t.Errorf("MeterNumber = %s, want 10797508", got)
	}
}

func TestParseRejectsATruncatedLongHeader(t *testing.T) {
	raw, err := hex.DecodeString("124424238181818164" + "0E72" + "4656")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseWMBus(raw); err == nil {
		t.Error("expected a truncated long header to be rejected")
	}
}
