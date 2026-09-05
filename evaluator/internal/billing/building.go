package billing

import (
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// UnitLine is one unit's row in the building-wide overview: what it used
// this month per consumption kind, and that figure normalized by its own
// floor area so units of different sizes can be read against each other.
//
// Found separates "used nothing" from "no figure available" — a unit
// whose meters are silent must not read as a unit that consumed zero.
type UnitLine struct {
	UnitID string
	Name   string
	AreaM2 float64

	Consumption map[masterdata.Kind]float64
	PerM2       map[masterdata.Kind]float64
	Found       map[masterdata.Kind]bool

	// KWh is the informational energy figure where one applies (heating
	// and hot water; see ToKWh), keyed the same way.
	KWh          map[masterdata.Kind]float64
	KWhAvailable map[masterdata.Kind]bool
}

// BuildingReport is the whole installation for one month: every unit
// side by side, plus the building's own totals and area-normalized
// averages. It is the operator's overview (UI-02/UI-03) and is never
// shown to a tenant — a per-unit breakdown is exactly what SZ-4 keeps
// out of a tenant's reach, which is why FACH-04 hands a tenant only the
// single aggregate figure instead.
type BuildingReport struct {
	Month string // "YYYY-MM"

	// Kinds are the consumption kinds actually present anywhere in the
	// installation, in kindDisplayOrder.
	Kinds []masterdata.Kind

	Units []UnitLine

	Total       map[masterdata.Kind]float64
	PerM2       map[masterdata.Kind]float64
	Found       map[masterdata.Kind]bool
	TotalAreaM2 float64

	// UnitsReporting counts units with at least one usable figure this
	// month, against len(Units) — the quickest read on whether the month
	// is complete enough to act on.
	UnitsReporting int
}

// BuildBuildingReport computes every unit's consumption for targetMonth in
// a single pass over the installation's meter points.
//
// Deliberately not "call BuildUnitReport once per unit": that would
// recompute the building-wide comparison from scratch for every unit,
// walking every meter point in the installation each time. Here each
// meter point's series is computed exactly once and accumulated into the
// unit it belongs to.
func BuildBuildingReport(store *archive.Store, md masterdata.MasterData, building masterdata.Building, targetMonth telegram.Day, lookbackDays int) (BuildingReport, error) {
	targetLabel := string(targetMonth)[:7]
	report := BuildingReport{
		Month: targetLabel,
		Total: make(map[masterdata.Kind]float64),
		PerM2: make(map[masterdata.Kind]float64),
		Found: make(map[masterdata.Kind]bool),
	}

	lines := make(map[string]*UnitLine, len(md.Units))
	order := make([]string, 0, len(md.Units))
	for _, u := range md.Units {
		lines[u.ID] = &UnitLine{
			UnitID:       u.ID,
			Name:         u.Name,
			AreaM2:       u.AreaM2,
			Consumption:  make(map[masterdata.Kind]float64),
			PerM2:        make(map[masterdata.Kind]float64),
			Found:        make(map[masterdata.Kind]bool),
			KWh:          make(map[masterdata.Kind]float64),
			KWhAvailable: make(map[masterdata.Kind]bool),
		}
		order = append(order, u.ID)
		report.TotalAreaM2 += u.AreaM2
	}

	// A short range is enough: only the target month's own figure is
	// wanted, and that needs just the preceding month-end to diff against.
	rangeStart := targetMonth.AddDays(-40)

	kindsPresent := make(map[masterdata.Kind]bool)
	for _, mp := range md.MeterPoints {
		line, ok := lines[mp.UnitID]
		if !ok {
			continue // a meter point pointing at no known unit contributes to nothing
		}
		kindsPresent[mp.Kind] = true

		results, err := seriesForMeterPoint(store, md, mp, rangeStart, targetMonth, lookbackDays)
		if err != nil {
			return BuildingReport{}, err
		}
		r := monthResultFor(results, targetLabel)
		if r == nil {
			continue // no usable figure for this meter point this month
		}

		line.Consumption[mp.Kind] += r.Consumption
		line.Found[mp.Kind] = true
		report.Total[mp.Kind] += r.Consumption
		report.Found[mp.Kind] = true
	}

	for _, kind := range kindDisplayOrder {
		if kindsPresent[kind] {
			report.Kinds = append(report.Kinds, kind)
		}
	}

	for _, id := range order {
		line := lines[id]
		reporting := false
		for _, kind := range report.Kinds {
			if !line.Found[kind] {
				continue
			}
			reporting = true
			if line.AreaM2 > 0 {
				line.PerM2[kind] = line.Consumption[kind] / line.AreaM2
			}
			if kwh, ok := ToKWh(kind, line.Consumption[kind], building); ok {
				line.KWh[kind] = kwh
				line.KWhAvailable[kind] = true
			}
		}
		if reporting {
			report.UnitsReporting++
		}
		report.Units = append(report.Units, *line)
	}

	if report.TotalAreaM2 > 0 {
		for _, kind := range report.Kinds {
			report.PerM2[kind] = report.Total[kind] / report.TotalAreaM2
		}
	}

	return report, nil
}
