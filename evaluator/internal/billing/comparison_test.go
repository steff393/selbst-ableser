package billing

import "testing"

func TestBuildingComparison(t *testing.T) {
	consumption := map[string]float64{"u1": 100, "u2": 50, "u3": 0}
	area := map[string]float64{"u1": 60, "u2": 40, "u3": 30}
	// total consumption 150, total area 130
	got, err := BuildingComparison(consumption, area)
	if err != nil {
		t.Fatalf("BuildingComparison: %v", err)
	}
	want := 150.0 / 130.0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildingComparisonIncludesUnitsWithNoConsumptionEntry(t *testing.T) {
	// A unit with no current tenant/access still counts toward the area
	// sum (FACH-04) — it just has no consumption entry.
	consumption := map[string]float64{"u1": 100}
	area := map[string]float64{"u1": 60, "u2": 40}
	got, err := BuildingComparison(consumption, area)
	if err != nil {
		t.Fatalf("BuildingComparison: %v", err)
	}
	want := 100.0 / 100.0
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildingComparisonZeroArea(t *testing.T) {
	if _, err := BuildingComparison(nil, nil); err == nil {
		t.Fatal("expected an error when total area is zero")
	}
}
