package billing

import (
	"testing"

	"selbst-ableser/internal/masterdata"
)

func TestToKWhHeating(t *testing.T) {
	b := masterdata.Building{HeatingKWhPerUnit: 1.42}
	kwh, ok := ToKWh(masterdata.KindHeatCostAllocator, 100, b)
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

// TestToKWhHasNothingToSayAboutGasOrColdWater: a gas meter counts gas and
// cold water carries no energy, so neither has an energy figure — and in
// particular neither may be run through a factor that was derived for
// heat cost allocators.
func TestToKWhHasNothingToSayAboutGasOrColdWater(t *testing.T) {
	b := masterdata.Building{HeatingKWhPerUnit: 2.5, HotWaterKWhPerM3: 40}
	for _, kind := range []masterdata.Kind{masterdata.KindGas, masterdata.KindColdWater} {
		if kwh, ok := ToKWh(kind, 100, b); ok || kwh != 0 {
			t.Errorf("ToKWh(%s) = (%v, %v), want (0, false)", kind, kwh, ok)
		}
	}
}

// TestToKWhPassesAHeatMeterThrough: a heat meter's consumption already is
// kWh. It has an energy figure — the UVI needs one for its kWh/m²
// comparison — but that figure is the consumption itself, never scaled by
// the installation's heating factor, which exists for allocator units.
func TestToKWhPassesAHeatMeterThrough(t *testing.T) {
	b := masterdata.Building{HeatingKWhPerUnit: 2.5, HotWaterKWhPerM3: 40}
	kwh, ok := ToKWh(masterdata.KindHeatMeter, 100, b)
	if !ok || kwh != 100 {
		t.Errorf("ToKWh(heat meter, 100) = (%v, %v), want (100, true)", kwh, ok)
	}
}
