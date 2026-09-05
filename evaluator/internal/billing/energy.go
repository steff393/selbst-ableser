package billing

import "selbst-ableser/internal/masterdata"

// ToKWh converts a heat-cost-allocator or hot-water consumption figure
// into an informational energy value (FACH-06). Cold water has no energy
// figure (applicable is false). consumption is in the meter's native unit
// — heat-cost-allocator units, or liters for water — matching
// internal/decode.Reading and MonthResult.Consumption.
func ToKWh(kind masterdata.Kind, consumption float64, b masterdata.Building) (kwh float64, applicable bool) {
	switch kind {
	case masterdata.KindHeating:
		return consumption * b.EffectiveHeatingKWhPerUnit(), true
	case masterdata.KindHotWater:
		cubicMeters := consumption / 1000
		return cubicMeters * b.EffectiveHotWaterKWhPerM3(), true
	default:
		return 0, false
	}
}
