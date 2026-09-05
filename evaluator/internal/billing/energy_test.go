package billing

import (
	"testing"

	"selbst-ableser/internal/masterdata"
)

func TestToKWhHeating(t *testing.T) {
	b := masterdata.Building{HeatingKWhPerUnit: 1.42}
	kwh, ok := ToKWh(masterdata.KindHeating, 100, b)
	if !ok {
		t.Fatal("expected heating to have a kWh figure")
	}
	if kwh != 142 {
		t.Errorf("kWh = %v, want 142", kwh)
	}
}

func TestToKWhHotWater(t *testing.T) {
	b := masterdata.Building{HotWaterKWhPerM3: 124.3}
	// 33009 liters = 33.009 m3, matching the reference vector's corrected value.
	kwh, ok := ToKWh(masterdata.KindHotWater, 33009, b)
	if !ok {
		t.Fatal("expected hot water to have a kWh figure")
	}
	want := 33.009 * 124.3
	if diff := kwh - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("kWh = %v, want %v", kwh, want)
	}
}

func TestToKWhColdWaterNotApplicable(t *testing.T) {
	_, ok := ToKWh(masterdata.KindColdWater, 5000, masterdata.Building{})
	if ok {
		t.Error("cold water should not have a kWh figure")
	}
}
