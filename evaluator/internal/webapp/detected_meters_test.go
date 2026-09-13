package webapp

import (
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// TestKindFromMedium pins the deliberate coarseness: only the medium
// values that say something useful are mapped, and everything else
// reports "no claim" so the page sorts that device last instead of
// hiding it or mislabelling it.
func TestKindFromMedium(t *testing.T) {
	cases := []struct {
		medium byte
		kind   masterdata.Kind
		ok     bool
	}{
		{0x08, masterdata.KindHeatCostAllocator, true},
		{0x80, masterdata.KindHeatCostAllocator, true}, // manufacturer variant
		{0x04, masterdata.KindHeatMeter, true},
		{0x03, masterdata.KindGas, true},
		{0x06, masterdata.KindHotWater, true},
		{0x72, masterdata.KindColdWater, true}, // non-standard cold water
		{0x02, 0, false},                       // electricity — nothing measured here
		{0x00, 0, false},                       // "other"
		{0xFF, 0, false},                       // unknown
	}
	for _, c := range cases {
		kind, ok := kindFromMedium(c.medium)
		if ok != c.ok || (ok && kind != c.kind) {
			t.Errorf("kindFromMedium(%#02x) = (%v, %v), want (%v, %v)", c.medium, kind, ok, c.kind, c.ok)
		}
	}
}

// TestDetectedMetersWithoutArchive: the suggestions are an aid, so a
// missing archive yields an empty list rather than breaking the page.
func TestDetectedMetersWithoutArchive(t *testing.T) {
	if got := detectedMeters(nil, masterdata.MasterData{}, "2026-09-07"); got != nil {
		t.Errorf("detectedMeters(nil) = %+v, want nothing", got)
	}
}

// TestDetectedMetersLeaveOutEverythingAlreadyEntered: a number may sit at
// one meter point only, so a device that was once installed and taken out
// again is not free — offering it would lead straight into a refusal, and
// on the page it looked as if two long-removed devices were available.
func TestDetectedMetersLeaveOutEverythingAlreadyEntered(t *testing.T) {
	removed := telegram.Day("2026-02-15")
	end := int64(412)
	md := masterdata.MasterData{Meters: []masterdata.Meter{
		{Number: "10000051", MeterPointID: "101"},
		{Number: "10000055", MeterPointID: "102", RemovedAt: &removed, EndReading: &end},
	}}

	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	for _, number := range []string{"10000051", "10000055", "10000099"} {
		if _, err := store.InsertHistorical(archive.Entry{
			MeterID: number, Day: "2026-09-01", ReceivedAt: time.Now(), RawHex: "00",
		}); err != nil {
			t.Fatalf("InsertHistorical(%s): %v", number, err)
		}
	}

	got := detectedMeters(store, md, "2026-09-07")
	if len(got) != 1 || got[0].Number != "10000099" {
		t.Errorf("detected = %+v, want only the meter no master-data row mentions", got)
	}
}
