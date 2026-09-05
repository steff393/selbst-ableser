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

func TestEffectiveResetMonthDefault(t *testing.T) {
	if got := (Meter{}).EffectiveResetMonth(); got != 1 {
		t.Errorf("EffectiveResetMonth of an unset meter = %v, want 1 (January)", got)
	}
	if got := (Meter{ResetMonth: 6}).EffectiveResetMonth(); got != 6 {
		t.Errorf("EffectiveResetMonth = %v, want 6", got)
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
