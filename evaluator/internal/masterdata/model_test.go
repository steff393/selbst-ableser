package masterdata

import (
	"testing"

	"selbst-ableser/internal/telegram"
)

func day(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func TestActiveMeterAcrossSwap(t *testing.T) {
	removed := day(t, "2025-02-15")
	end := int64(150)
	md := MasterData{
		Meters: []Meter{
			{Number: "90000010", MeterPointID: "mp1", InstalledAt: day(t, "2024-01-01"), RemovedAt: &removed, EndReading: &end},
			{Number: "90000011", MeterPointID: "mp1", InstalledAt: day(t, "2025-02-15"), StartReading: 0},
		},
	}

	m, ok := md.ActiveMeter("mp1", day(t, "2025-01-31"))
	if !ok || m.Number != "90000010" {
		t.Errorf("ActiveMeter before swap = %+v, ok=%v, want 90000010", m, ok)
	}
	m, ok = md.ActiveMeter("mp1", day(t, "2025-03-31"))
	if !ok || m.Number != "90000011" {
		t.Errorf("ActiveMeter after swap = %+v, ok=%v, want 90000011", m, ok)
	}
	if _, ok := md.ActiveMeter("mp1", day(t, "2023-12-31")); ok {
		t.Error("ActiveMeter before any meter was installed should report ok=false")
	}
}

func TestMeterByNumberAcrossSwap(t *testing.T) {
	removed := day(t, "2025-02-15")
	md := MasterData{
		Meters: []Meter{
			{Number: "90000010", MeterPointID: "mp1", InstalledAt: day(t, "2024-01-01"), RemovedAt: &removed},
			{Number: "90000011", MeterPointID: "mp1", InstalledAt: day(t, "2025-02-15")},
		},
	}

	m, ok := md.MeterByNumber("90000010", day(t, "2025-01-31"))
	if !ok || m.MeterPointID != "mp1" {
		t.Errorf("MeterByNumber(90000010, before swap) = %+v, ok=%v", m, ok)
	}
	if _, ok := md.MeterByNumber("90000010", day(t, "2025-03-31")); ok {
		t.Error("MeterByNumber for a removed meter after its removal should report ok=false")
	}
	if _, ok := md.MeterByNumber("unknown-number", day(t, "2025-01-31")); ok {
		t.Error("MeterByNumber for a number that was never installed should report ok=false")
	}
}

func TestEffectiveKCFactorDefault(t *testing.T) {
	if got := (Meter{}).EffectiveKCFactor(); got != 1 {
		t.Errorf("EffectiveKCFactor of an unset meter = %v, want 1", got)
	}
	if got := (Meter{KCFactor: 1.7}).EffectiveKCFactor(); got != 1.7 {
		t.Errorf("EffectiveKCFactor = %v, want 1.7", got)
	}
}

func TestEffectiveFreezeMonthDefault(t *testing.T) {
	if got := (Meter{}).EffectiveFreezeMonth(); got != 12 {
		t.Errorf("EffectiveFreezeMonth of an unset meter = %v, want 12 (Stichtag 31 December)", got)
	}
	if got := (Meter{FreezeMonth: 6}).EffectiveFreezeMonth(); got != 6 {
		t.Errorf("EffectiveFreezeMonth = %v, want 6", got)
	}
}

func TestEffectiveBuildingKWhFactorsDefault(t *testing.T) {
	if got := (Building{}).EffectiveHeatingKWhPerUnit(); got != 1 {
		t.Errorf("EffectiveHeatingKWhPerUnit of an unset building = %v, want 1", got)
	}
	if got := (Building{HeatingKWhPerUnit: 1.42}).EffectiveHeatingKWhPerUnit(); got != 1.42 {
		t.Errorf("EffectiveHeatingKWhPerUnit = %v, want 1.42", got)
	}
	if got := (Building{}).EffectiveHotWaterKWhPerM3(); got != 1 {
		t.Errorf("EffectiveHotWaterKWhPerM3 of an unset building = %v, want 1", got)
	}
	if got := (Building{HotWaterKWhPerM3: 124.3}).EffectiveHotWaterKWhPerM3(); got != 124.3 {
		t.Errorf("EffectiveHotWaterKWhPerM3 = %v, want 124.3", got)
	}
}

// TestKindKnownAndBillable pins the questions the rest of the system asks
// a Kind: is it a value we defined, does it take part in a tenant's
// consumption information, and are its readings already kWh? A heat
// meter is billed — heating measured directly — and the only kind whose
// readings are kWh; a gas meter is watched, not billed.
func TestKindKnownAndBillable(t *testing.T) {
	cases := []struct {
		kind   Kind
		known  bool
		billed bool
		kWh    bool
	}{
		{KindHeatCostAllocator, true, true, false},
		{KindHotWater, true, true, false},
		{KindColdWater, true, true, false},
		{KindHeatMeter, true, true, true},
		{KindGas, true, false, false},
		{Kind(99), false, false, false},
	}
	for _, c := range cases {
		if got := c.kind.Known(); got != c.known {
			t.Errorf("Kind(%d).Known() = %v, want %v", int(c.kind), got, c.known)
		}
		if got := c.kind.Billable(); got != c.billed {
			t.Errorf("Kind(%d).Billable() = %v, want %v", int(c.kind), got, c.billed)
		}
		if got := c.kind.CountsKWh(); got != c.kWh {
			t.Errorf("Kind(%d).CountsKWh() = %v, want %v", int(c.kind), got, c.kWh)
		}
	}
}

// TestKindNumberingIsStable guards the stored format: the values are
// written to masterdata.enc as plain integers, so appending is safe and
// reordering would silently turn every heat cost allocator in an existing
// installation into something else.
func TestKindNumberingIsStable(t *testing.T) {
	for kind, want := range map[Kind]int{
		KindHeatCostAllocator: 0,
		KindHotWater:          1,
		KindColdWater:         2,
		KindHeatMeter:         3,
		KindGas:               4,
	} {
		if int(kind) != want {
			t.Errorf("%s = %d, want %d — existing files depend on this", kind, int(kind), want)
		}
	}
}

// TestMeterWithOpenInstallationIsAlwaysActive: an empty installation date
// compares earlier than any real date, so the meter counts as installed
// on every day it is asked about.
func TestMeterWithOpenInstallationIsAlwaysActive(t *testing.T) {
	m := Meter{Number: "90000010", MeterPointID: "mp1"}
	if !m.Active(day(t, "2019-01-01")) {
		t.Error("a meter with an open installation date must be active even long ago")
	}
	if !m.Active(day(t, "2026-09-07")) {
		t.Error("a meter with an open installation date must be active today")
	}
}

// TestActiveMeterPrefersTheDatedSuccessor: with an open start on the
// first device and a real date on its replacement, the replacement wins
// from its installation day on.
func TestActiveMeterPrefersTheDatedSuccessor(t *testing.T) {
	removed := day(t, "2025-02-15")
	end := int64(150)
	md := MasterData{
		MeterPoints: []MeterPoint{{ID: "mp1", UnitID: "u1", Kind: KindHeatCostAllocator}},
		Meters: []Meter{
			{Number: "90000010", MeterPointID: "mp1", RemovedAt: &removed, EndReading: &end},
			{Number: "90000011", MeterPointID: "mp1", InstalledAt: day(t, "2025-02-15")},
		},
	}

	if m, ok := md.ActiveMeter("mp1", day(t, "2024-06-01")); !ok || m.Number != "90000010" {
		t.Errorf("before the swap = %+v (%v), want the open-start meter", m, ok)
	}
	if m, ok := md.ActiveMeter("mp1", day(t, "2025-06-01")); !ok || m.Number != "90000011" {
		t.Errorf("after the swap = %+v (%v), want the replacement", m, ok)
	}
}

// TestOnlyHeatCostAllocatorsReset guards the one mistake in this area
// that produces no error message: a heat meter counts kWh through and is
// never zeroed, so treating it like a heat cost allocator would show a
// year's worth of energy as a single month's consumption.
func TestOnlyHeatCostAllocatorsReset(t *testing.T) {
	if !KindHeatCostAllocator.ResetsAnnually() {
		t.Error("a heat cost allocator resets at its Stichtag")
	}
	for _, kind := range []Kind{KindHotWater, KindColdWater, KindHeatMeter, KindGas} {
		if kind.ResetsAnnually() {
			t.Errorf("%s must not be treated as resetting annually", kind)
		}
	}
}

// TestInUnitNeverMatchesWithoutAUnit is the guard the consumption
// information depends on: a house meter carries no unit, and neither does
// a lookup that lost its unit somewhere. Comparing the two as plain
// strings would make every house meter look like that tenant's own.
func TestInUnitNeverMatchesWithoutAUnit(t *testing.T) {
	flat := MeterPoint{ID: "101", UnitID: "u1", Kind: KindHeatCostAllocator}
	house := MeterPoint{ID: "h1", Kind: KindColdWater}                   // the building's main water meter
	flatHeat := MeterPoint{ID: "102", UnitID: "u1", Kind: KindHeatMeter} // underfloor heating, one per flat
	houseHeat := MeterPoint{ID: "h2", Kind: KindHeatMeter}               // the boiler's own heat meter
	watched := MeterPoint{ID: "103", UnitID: "u1", Kind: KindGas}

	if !flat.InUnit("u1") {
		t.Error("a unit's own meter point must belong to it")
	}
	if flat.InUnit("u2") {
		t.Error("a meter point must not belong to a different unit")
	}
	if flat.InUnit("") {
		t.Error("an empty unit must match nothing at all")
	}
	if house.InUnit("") {
		t.Error("a house meter must not answer to a lookup that lost its unit")
	}
	if house.InUnit("u1") {
		t.Error("a house meter belongs to no unit")
	}
	if !flatHeat.InUnit("u1") {
		t.Error("a flat's own heat meter is part of that flat's figures")
	}
	if houseHeat.InUnit("") || houseHeat.PartOfBilling() {
		t.Error("a heat meter without a unit is the house's own device and must stay out of every flat's figures")
	}
	if watched.InUnit("u1") {
		t.Error("a gas meter is watched, not billed, so it stays out of a unit's figures")
	}
}
