package billing

import "selbst-ableser/internal/masterdata"

// ToKWh converts a heat-cost-allocator or hot-water consumption figure
// into an informational energy value (FACH-06). Cold water has no energy
// figure (applicable is false). consumption is in the meter's native unit
// — heat-cost-allocator units, liters for water, kWh for a heat meter —
// matching internal/decode.Reading and MonthResult.Consumption.
//
// A heat meter's consumption already is kWh, so it comes back unchanged
// and applicable: it has an energy figure, just no conversion behind it
// (see masterdata.Kind.CountsKWh). Its factor is 1, not the installation's
// heating factor, which was derived for allocator units and would be
// wrong applied to a measured value. Gas has no energy figure here.
func ToKWh(kind masterdata.Kind, consumption float64, b masterdata.Building) (kwh float64, applicable bool) {
	switch kind {
	case masterdata.KindHeatMeter:
		return consumption, true
	case masterdata.KindHeatCostAllocator:
		return consumption * b.EffectiveHeatingKWhPerUnit(), true
	case masterdata.KindHotWater:
		cubicMeters := consumption / 1000
		return cubicMeters * b.EffectiveHotWaterKWhPerM3(), true
	default:
		return 0, false
	}
}
