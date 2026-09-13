package masterdata

import (
	"strings"
	"testing"
)

func wellFormed(t *testing.T) MasterData {
	t.Helper()
	end := int64(150)
	removed := day(t, "2025-02-15")
	return MasterData{
		Units: []Unit{
			{ID: "u1", Name: "Unit 1", AreaM2: 60},
		},
		MeterPoints: []MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Living room", Kind: KindHeatCostAllocator},
		},
		Meters: []Meter{
			{Number: "90000010", MeterPointID: "mp1", InstalledAt: day(t, "2024-01-01"), RemovedAt: &removed, EndReading: &end},
			{Number: "90000011", MeterPointID: "mp1", InstalledAt: day(t, "2025-02-15")},
		},
	}
}

func TestValidateWellFormed(t *testing.T) {
	d := Validate(wellFormed(t))
	if !d.OK() {
		t.Fatalf("expected no errors, got %v", d.Errors)
	}
}

func TestValidateDuplicateMeterNumberAcrossPoints(t *testing.T) {
	md := wellFormed(t)
	md.MeterPoints = append(md.MeterPoints, MeterPoint{ID: "mp2", UnitID: "u1", Kind: KindColdWater})
	md.Meters = append(md.Meters, Meter{Number: "90000010", MeterPointID: "mp2", InstalledAt: day(t, "2024-01-01")})

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a meter number reused at a different meter point")
	}
	if !containsSubstring(d.Errors, "90000010") {
		t.Errorf("errors don't mention the duplicated meter number: %v", d.Errors)
	}
}

func TestValidateOverlappingMeters(t *testing.T) {
	md := wellFormed(t)
	// The second meter is installed before the first was removed.
	md.Meters[1].InstalledAt = day(t, "2025-01-01")

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for overlapping meter installation periods")
	}
}

func TestValidateGapIsError(t *testing.T) {
	md := wellFormed(t)
	md.Meters[1].InstalledAt = day(t, "2025-03-01") // a two-week gap after removal on 2025-02-15

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a gap between removal and the next installation")
	}
}

func TestValidateRemovalWithoutEndReading(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].EndReading = nil

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error: removal date without an end reading")
	}
}

func TestValidateAreaMustBePositive(t *testing.T) {
	md := wellFormed(t)
	md.Units[0].AreaM2 = 0

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a unit with a meter point but zero area")
	}
}

func TestValidateMeterPointWithoutMeter(t *testing.T) {
	md := wellFormed(t)
	md.MeterPoints = append(md.MeterPoints, MeterPoint{ID: "mp-empty", UnitID: "u1", Kind: KindColdWater})

	d := Validate(md)
	if !d.OK() {
		t.Fatalf("a meter point with no meter must not block saving, got: %v", d.Errors)
	}
	if len(d.Warnings) == 0 {
		t.Error("a meter point with no meter should be reported as a warning")
	}
}

func TestValidateBadMeterNumberFormat(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].Number = "not-a-number"

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a non-numeric meter number")
	}
}

func TestValidateFreezeMonthOutOfRange(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].FreezeMonth = 13

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a reset month outside 1-12")
	}
}

func containsSubstring(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// TestMeterPointWithoutMeterIsAllowed pins STAMM-02's "report, don't
// forbid": an operator lays out the installation's rooms before the
// devices are mounted, and a meter point sits empty between a removal and
// its replacement. Neither may block saving.
func TestMeterPointWithoutMeterIsAllowed(t *testing.T) {
	md := MasterData{
		Units: []Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 50}},
		MeterPoints: []MeterPoint{
			{ID: "mp1", UnitID: "u1", Room: "Wohnzimmer", Kind: KindHeatCostAllocator},
		},
	}
	d := Validate(md)
	if !d.OK() {
		t.Errorf("a meter point without a meter must not block saving, got errors: %v", d.Errors)
	}
	if len(d.Warnings) == 0 {
		t.Error("it should still be reported as a warning")
	}
}

// TestValidateMeterPointWithoutUnit: a meter point with no unit is the
// house's own device — the heat meter in the boiler room, the gas meter
// at the connection. It belongs to no rentable unit, appears in no
// tenant's consumption information, and is not an error.
func TestValidateMeterPointWithoutUnit(t *testing.T) {
	md := wellFormed(t)
	md.MeterPoints = append(md.MeterPoints, MeterPoint{ID: "h1", Room: "Boiler room", Kind: KindHeatMeter})
	md.Meters = append(md.Meters, Meter{Number: "90000020", MeterPointID: "h1", InstalledAt: day(t, "2024-01-01")})

	d := Validate(md)
	if !d.OK() {
		t.Fatalf("a house meter without a unit must be accepted, got %v", d.Errors)
	}
}

func TestValidateMeterPointWithUnknownUnitIsStillAnError(t *testing.T) {
	md := wellFormed(t)
	md.MeterPoints[0].UnitID = "typo"

	d := Validate(md)
	if d.OK() {
		t.Fatal("a named but non-existent unit must still be refused")
	}
	if !containsSubstring(d.Errors, "typo") {
		t.Errorf("errors don't name the unit that does not exist: %v", d.Errors)
	}
}

func TestValidateUnknownKind(t *testing.T) {
	md := wellFormed(t)
	md.MeterPoints[0].Kind = Kind(99)

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a consumption type that does not exist")
	}
}

// TestValidateOpenInstallationDate: an installation taken over from a
// previous provider has no meaningful installation date for the devices
// already mounted. An empty date reads as "has been there all along" and
// must validate — including as the first link of a chain that continues
// with a real swap.
func TestValidateOpenInstallationDate(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].InstalledAt = ""

	d := Validate(md)
	if !d.OK() {
		t.Fatalf("an open installation date must be accepted, got %v", d.Errors)
	}
}

// TestValidateAccessToUnknownUnit pins the cross-reference nothing used
// to check: a grant left pointing at a unit that was renamed or deleted
// does no visible damage — the tenant simply sees nothing — which is why
// it would otherwise stay unnoticed.
func TestValidateAccessToUnknownUnit(t *testing.T) {
	md := wellFormed(t)
	md.Accesses = []Access{{Token: "abcdefghijkl", UnitID: "gone", Start: day(t, "2025-01-01")}}

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for an access grant pointing at a unit that does not exist")
	}
	if !containsSubstring(d.Errors, "gone") {
		t.Errorf("errors don't name the unit: %v", d.Errors)
	}
	if containsSubstring(d.Errors, "abcdefghijkl") {
		t.Errorf("the token itself must not appear in a validation message: %v", d.Errors)
	}
}

func TestValidateAccessDuplicateToken(t *testing.T) {
	md := wellFormed(t)
	md.Accesses = []Access{
		{Token: "abcdefghijkl", UnitID: "u1", Start: day(t, "2025-01-01")},
		{Token: "abcdefghijkl", UnitID: "u1", Start: day(t, "2025-06-01")},
	}

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for two grants sharing one token")
	}
}

func TestValidateAccessEndsBeforeItStarts(t *testing.T) {
	md := wellFormed(t)
	end := day(t, "2024-12-31")
	md.Accesses = []Access{{Token: "abcdefghijkl", UnitID: "u1", Start: day(t, "2025-01-01"), End: &end}}

	d := Validate(md)
	if d.OK() {
		t.Fatal("expected an error for a grant that ends before it starts")
	}
}

func TestValidateWellFormedAccessIsAccepted(t *testing.T) {
	md := wellFormed(t)
	end := day(t, "2025-12-31")
	md.Accesses = []Access{
		{Token: "abcdefghijkl", UnitID: "u1", Start: day(t, "2025-01-01"), End: &end},
		{Token: "mnopqrstuvwx", UnitID: "u1", Start: day(t, "2026-01-01")},
	}

	d := Validate(md)
	if !d.OK() {
		t.Fatalf("expected the grants to be accepted, got %v", d.Errors)
	}
}
