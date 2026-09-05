package billing

import "fmt"

// BuildingComparison computes the area-normalized average consumption
// across every unit in the building for one month and consumption kind
// (FACH-04): a single aggregate figure a tenant can compare their own
// consumption against.
//
// Both maps must cover every unit in the building, including ones the
// caller's own tenant has no access to — the result is an aggregate that
// cannot be used to reconstruct any single other unit's consumption
// (SZ-4); callers must never expose the input maps themselves to a
// tenant, only this function's return value.
func BuildingComparison(consumptionPerUnit map[string]float64, areaPerUnitM2 map[string]float64) (float64, error) {
	var totalConsumption, totalArea float64
	for unitID, area := range areaPerUnitM2 {
		totalArea += area
		totalConsumption += consumptionPerUnit[unitID]
	}
	if totalArea <= 0 {
		return 0, fmt.Errorf("billing: total building area must be greater than zero")
	}
	return totalConsumption / totalArea, nil
}
