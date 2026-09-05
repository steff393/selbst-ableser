package billing

import (
	"fmt"
	"sort"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// historyMonths is how many months of consumption UI-01's "Verlauf" shows.
const historyMonths = 12

// MeterPointReport is one meter point's own computed series: the current
// reading, this month's consumption, the two comparison points a heating
// bill actually needs (last month, same month last year), and the last 12
// months for the trend display. It is an internal building block for
// KindReport (see aggregateKindReport) — UI-01 asks for its headline
// figures "je Verbrauchsart" (per consumption kind), not per physical
// meter, so this type is never handed to a template directly.
type MeterPointReport struct {
	MeterPointID string
	Room         string

	CurrentReading MonthlyValue

	CurrentMonth   MonthResult
	PriorMonth     *MonthResult // nil if there is no earlier data at all
	PriorYearMonth *MonthResult // nil if there is no data from a year earlier

	History []MonthResult // up to the last 12 months, oldest first, ending at CurrentMonth
}

// MeterReading is one physical meter's contribution to a KindReport's
// itemized readings — UI-01's one "je Zählerplatz" requirement (Raum,
// Zählernummer, Zählerstand, Ablesedatum) in an otherwise per-Verbrauchsart
// view, plus the prior reading for anyone who wants to verify the month's
// consumption by hand (kc-Faktor deliberately left out of the display —
// it already goes into Consumption above, and showing it separately read
// as more clutter than transparency).
type MeterReading struct {
	MeterPointID string
	Room         string
	Previous     MonthlyValue // zero value (empty Day) if there is no earlier reading
	Current      MonthlyValue
}

// KindReport is one consumption category's (Heizung/Warmwasser/Kaltwasser)
// contribution to a unit's UVI — the unit of aggregation UI-01 actually
// asks for: a unit with two heating meter points (say, two rooms) reports
// as one combined Heizung section, not two. Readings is the exception,
// itemized per meter point.
type KindReport struct {
	Kind masterdata.Kind

	// CurrentMonth/PriorMonth/PriorYearMonth carry a combined Consumption
	// summed across every meter point of this kind; MonthResult's other
	// fields (Previous/Current/KCFactor/...) are meaningful per meter
	// only and stay zero-valued here — see Readings for those.
	CurrentMonth   MonthResult
	PriorMonth     *MonthResult
	PriorYearMonth *MonthResult
	History        []MonthResult

	KWh           float64
	KWhApplicable bool

	// ComparisonPercent is how far this kind's combined, area-normalized
	// consumption is above (positive) or below (negative) the building
	// average for its kind (UI-01/FACH-04). false if there is no
	// comparison to make (e.g. the unit has no area on record).
	ComparisonPercent    float64
	ComparisonApplicable bool

	// ComparisonOwnPerM2/ComparisonAvgPerM2 are the two absolute,
	// area-normalized figures ComparisonPercent is the ratio of — in
	// kWh/m² when KWhApplicable, otherwise in this kind's own unit per m²
	// (m³/m² for water, matching consumptionLabel's liter-to-m³
	// conversion). Only meaningful when ComparisonApplicable.
	ComparisonOwnPerM2 float64
	ComparisonAvgPerM2 float64

	Readings []MeterReading
}

// PercentChange returns the percentage change from other to r's
// consumption (e.g. this month vs. last month), and false if there is
// nothing to compare against or the comparison is undefined (other is
// zero).
func (r MonthResult) PercentChange(other *MonthResult) (float64, bool) {
	if other == nil || other.Consumption == 0 {
		return 0, false
	}
	return (r.Consumption - other.Consumption) / other.Consumption * 100, true
}

// UnitReport is everything UI-01 needs to render one unit's UVI for one
// month.
type UnitReport struct {
	UnitID string
	Month  string // "YYYY-MM"
	// Kinds holds at most one entry per consumption kind actually present
	// for this unit, in kindDisplayOrder.
	Kinds []KindReport
	// ComparisonPerM2 is the building-wide, area-normalized average
	// consumption for the month, keyed by consumption kind (FACH-04).
	ComparisonPerM2 map[masterdata.Kind]float64
}

// kindDisplayOrder is UI-01's fixed reading order: heating first (the
// figure with the most riding on it), then hot water — both required by
// § 6a HeizkostenV — then cold water last, since it is informational only
// (outside that law's scope; see FACH-05/FACH-06, which never mention it
// for the kWh conversion).
var kindDisplayOrder = []masterdata.Kind{masterdata.KindHeating, masterdata.KindHotWater, masterdata.KindColdWater}

// LatestAvailableMonth finds the most recent day, no later than notAfter,
// that any of unitID's meters has an archived entry for — a day within
// whatever month should be UI-01's default landing page when "today"
// itself has no data yet (data collection can lag "today" by a wide
// margin; a tenant should land on their most recent actual UVI, not an
// empty "no data" page for the current calendar month). found is false if
// the unit has no archived data at all up to notAfter.
//
// This walks every meter ever assigned to any of the unit's meter points,
// not just the currently active ones, so a meter swap does not hide older
// data behind a newer, still-empty meter.
func LatestAvailableMonth(store *archive.Store, md masterdata.MasterData, unitID string, notAfter telegram.Day) (telegram.Day, bool, error) {
	var latest telegram.Day
	found := false
	for _, mp := range md.MeterPoints {
		if mp.UnitID != unitID {
			continue
		}
		for _, m := range md.Meters {
			if m.MeterPointID != mp.ID {
				continue
			}
			day, ok, err := store.LastDayAtOrBefore(m.Number, notAfter)
			if err != nil {
				return "", false, err
			}
			if ok && (!found || latest.Before(day)) {
				latest = day
				found = true
			}
		}
	}
	return latest, found, nil
}

// EarliestAvailableMonth is LatestAvailableMonth's counterpart: the
// oldest day any of unitID's meters has archived data for. Together the
// two bound how far the UVI's month navigation may page in either
// direction, so it cannot run off either end of the data into a dead-end
// "no data" page (UI-01).
//
// The bound is the first day with a *reading*, not the first month with a
// computable consumption — the two differ by at most one month (a
// consumption needs a prior reading to subtract from), and stopping one
// month early would hide a month that does have a figure whenever the
// meter's own annual reset makes its first month self-sufficient.
func EarliestAvailableMonth(store *archive.Store, md masterdata.MasterData, unitID string) (telegram.Day, bool, error) {
	var earliest telegram.Day
	found := false
	for _, mp := range md.MeterPoints {
		if mp.UnitID != unitID {
			continue
		}
		for _, m := range md.Meters {
			if m.MeterPointID != mp.ID {
				continue
			}
			day, ok, err := store.FirstDay(m.Number)
			if err != nil {
				return "", false, err
			}
			if ok && (!found || day.Before(earliest)) {
				earliest = day
				found = true
			}
		}
	}
	return earliest, found, nil
}

// BuildUnitReport assembles one unit's UVI for targetMonth (UI-01).
func BuildUnitReport(store *archive.Store, md masterdata.MasterData, building masterdata.Building, unitID string, targetMonth telegram.Day, lookbackDays int) (UnitReport, error) {
	report := UnitReport{
		UnitID:          unitID,
		Month:           string(targetMonth)[:7],
		ComparisonPerM2: make(map[masterdata.Kind]float64),
	}

	kindsPresent := map[masterdata.Kind]bool{}
	for _, mp := range md.MeterPoints {
		if mp.UnitID == unitID {
			kindsPresent[mp.Kind] = true
		}
	}
	for kind := range kindsPresent {
		cmp, err := BuildingComparisonForMonth(store, md, kind, targetMonth, lookbackDays)
		if err != nil {
			return UnitReport{}, err
		}
		report.ComparisonPerM2[kind] = cmp
	}

	var unitArea float64
	for _, u := range md.Units {
		if u.ID == unitID {
			unitArea = u.AreaM2
		}
	}

	byKind := make(map[masterdata.Kind][]MeterPointReport)
	for _, mp := range md.MeterPoints {
		if mp.UnitID != unitID {
			continue
		}
		mpReport, err := buildMeterPointReport(store, md, mp, targetMonth, lookbackDays)
		if err != nil {
			return UnitReport{}, fmt.Errorf("billing: meter point %s: %w", mp.ID, err)
		}
		if mpReport == nil {
			continue
		}
		byKind[mp.Kind] = append(byKind[mp.Kind], *mpReport)
	}

	for _, kind := range kindDisplayOrder {
		reports := byKind[kind]
		if len(reports) == 0 {
			continue
		}
		report.Kinds = append(report.Kinds, aggregateKindReport(kind, reports, building, unitArea, report.ComparisonPerM2[kind]))
	}

	return report, nil
}

// aggregateKindReport combines every meter point report of one kind into
// the single KindReport UI-01 asks for. Consumption figures are already
// kc-Faktor-scaled per meter (FACH-07) by the time they reach here, so
// summing them here is correct — no further scaling happens at this
// level, and the building comparison (unlike the old per-meter-point
// version) is computed once against the combined figure, not once per
// meter against the whole unit's area.
func aggregateKindReport(kind masterdata.Kind, reports []MeterPointReport, building masterdata.Building, unitArea float64, buildingAvgPerM2 float64) KindReport {
	kr := KindReport{Kind: kind}

	var currentSum float64
	for _, r := range reports {
		currentSum += r.CurrentMonth.Consumption
	}
	kr.CurrentMonth = MonthResult{Month: reports[0].CurrentMonth.Month, Consumption: currentSum}

	if sum, label, ok := sumOptionalMonth(reports, func(r MeterPointReport) *MonthResult { return r.PriorMonth }); ok {
		kr.PriorMonth = &MonthResult{Month: label, Consumption: sum}
	}
	if sum, label, ok := sumOptionalMonth(reports, func(r MeterPointReport) *MonthResult { return r.PriorYearMonth }); ok {
		kr.PriorYearMonth = &MonthResult{Month: label, Consumption: sum}
	}

	// Every report's History is a suffix of the same at-most-12-month
	// window ending at CurrentMonth.Month (all built for the same
	// targetMonth), so the union of month labels across them can never
	// exceed historyMonths entries either.
	histSums := make(map[string]float64)
	for _, r := range reports {
		for _, m := range r.History {
			histSums[m.Month] += m.Consumption
		}
	}
	months := make([]string, 0, len(histSums))
	for m := range histSums {
		months = append(months, m)
	}
	sort.Strings(months) // "YYYY-MM" sorts lexically = chronologically
	for _, m := range months {
		kr.History = append(kr.History, MonthResult{Month: m, Consumption: histSums[m]})
	}

	kr.KWh, kr.KWhApplicable = ToKWh(kind, kr.CurrentMonth.Consumption, building)

	if unitArea > 0 && buildingAvgPerM2 > 0 {
		ownPerM2 := kr.CurrentMonth.Consumption / unitArea
		kr.ComparisonPercent = (ownPerM2/buildingAvgPerM2 - 1) * 100
		kr.ComparisonApplicable = true

		if kr.KWhApplicable {
			kr.ComparisonOwnPerM2, _ = ToKWh(kind, ownPerM2, building)
			kr.ComparisonAvgPerM2, _ = ToKWh(kind, buildingAvgPerM2, building)
		} else {
			kr.ComparisonOwnPerM2 = ownPerM2 / 1000
			kr.ComparisonAvgPerM2 = buildingAvgPerM2 / 1000
		}
	}

	for _, r := range reports {
		kr.Readings = append(kr.Readings, MeterReading{
			MeterPointID: r.MeterPointID,
			Room:         r.Room,
			Previous:     r.CurrentMonth.Previous,
			Current:      r.CurrentReading,
		})
	}

	return kr
}

// sumOptionalMonth sums get(r).Consumption across reports for every
// report where it is non-nil, reporting ok=false if none of them have a
// value at all (rather than a misleading sum of zero). label comes from
// whichever report(s) supplied a value — they all agree, since every
// report was built for the same targetMonth.
func sumOptionalMonth(reports []MeterPointReport, get func(MeterPointReport) *MonthResult) (sum float64, label string, ok bool) {
	for _, r := range reports {
		if m := get(r); m != nil {
			sum += m.Consumption
			label = m.Month
			ok = true
		}
	}
	return sum, label, ok
}

// seriesForMeterPoint computes one meter point's full consumption series
// (FACH-01/02/03/07) over every month-end between rangeStart and rangeEnd,
// inclusive. Shared by buildMeterPointReport, BuildingComparisonForMonth,
// and YearOverYearSeries — all three need the same per-meter-point
// computation, just over a different range or aggregated differently.
func seriesForMeterPoint(store *archive.Store, md masterdata.MasterData, mp masterdata.MeterPoint, rangeStart, rangeEnd telegram.Day, lookbackDays int) ([]MonthResult, error) {
	monthEnds := MonthEnds(rangeStart, rangeEnd)
	readings, err := MonthlyReadingsForMeterPoint(store, md, mp.ID, monthEnds, lookbackDays)
	if err != nil {
		return nil, err
	}
	if len(readings) < 2 {
		return nil, nil // not enough data yet to report any consumption
	}

	resetsAnnually := mp.Kind == masterdata.KindHeating
	resetMonth := func(meterNumber string, day telegram.Day) int {
		if m, ok := md.MeterByNumber(meterNumber, day); ok {
			return m.EffectiveResetMonth()
		}
		return 1
	}
	kcFactor := func(meterNumber string, day telegram.Day) float64 {
		if m, ok := md.MeterByNumber(meterNumber, day); ok {
			return m.EffectiveKCFactor()
		}
		return 1
	}
	return CalculateSeries(readings, resetsAnnually, resetMonth, kcFactor, SwapLookupFromMasterData(md, mp.ID))
}

func buildMeterPointReport(store *archive.Store, md masterdata.MasterData, mp masterdata.MeterPoint, targetMonth telegram.Day, lookbackDays int) (*MeterPointReport, error) {
	rangeStart := targetMonth.AddDays(-400) // comfortably more than 13 months, to find a prior-year reading even across gaps
	results, err := seriesForMeterPoint(store, md, mp, rangeStart, targetMonth, lookbackDays)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}

	targetLabel := string(targetMonth)[:7]
	var current *MonthResult
	for i := range results {
		if results[i].Month == targetLabel {
			current = &results[i]
			break
		}
	}
	if current == nil {
		return nil, nil // no reading for the target month itself (a gap FACH-08 hasn't been carried into yet)
	}

	report := &MeterPointReport{
		MeterPointID:   mp.ID,
		Room:           mp.Room,
		CurrentReading: current.Current,
		CurrentMonth:   *current,
	}
	report.PriorMonth = monthResultFor(results, OffsetMonth(targetLabel, -1))
	report.PriorYearMonth = monthResultFor(results, OffsetMonth(targetLabel, -12))
	report.History = lastNResultsThrough(results, targetLabel, historyMonths)

	return report, nil
}

func monthResultFor(results []MonthResult, label string) *MonthResult {
	for i := range results {
		if results[i].Month == label {
			return &results[i]
		}
	}
	return nil
}

// lastNResultsThrough returns up to n consecutive results ending at
// (and including) the one labeled through, oldest first.
func lastNResultsThrough(results []MonthResult, through string, n int) []MonthResult {
	end := -1
	for i := range results {
		if results[i].Month == through {
			end = i
			break
		}
	}
	if end == -1 {
		return nil
	}
	start := end - n + 1
	if start < 0 {
		start = 0
	}
	return results[start : end+1]
}

// OffsetMonth shifts a "YYYY-MM" label by n calendar months.
func OffsetMonth(label string, n int) string {
	year := int(label[0]-'0')*1000 + int(label[1]-'0')*100 + int(label[2]-'0')*10 + int(label[3]-'0')
	month := int(label[5]-'0')*10 + int(label[6]-'0')
	total := year*12 + (month - 1) + n
	year = total / 12
	month = total%12 + 1
	return fmt.Sprintf("%04d-%02d", year, month)
}

// YearPoint is one calendar month's combined consumption for a unit and
// kind, as used by YearOverYearSeries. Found is false for a month with no
// computable consumption at all (before the meter point existed, or a
// FACH-08 gap) — Value stays 0 in that case too, but callers must not
// treat that 0 as "no consumption"; check Found first.
type YearPoint struct {
	Month string // "YYYY-MM"
	Value float64
	Found bool
}

// YearSeries is one unit/kind's combined monthly consumption for a
// calendar year and the year before it, aligned month-for-month — the
// shape UI-01's "Verlauf ... im Vergleich zum Vorjahr" chart needs.
type YearSeries struct {
	Kind        masterdata.Kind
	Year        int
	CurrentYear [12]YearPoint // January..December of Year
	PriorYear   [12]YearPoint // January..December of Year-1
}

// YearOverYearSeries computes one unit's combined (summed across every
// meter point of kind, already kc-Faktor-scaled — see aggregateKindReport)
// monthly consumption for calendar year and the year before it.
//
// Deliberately keyed by a fixed calendar year rather than "12 months
// ending at whatever month is currently displayed": paging through
// individual months must not shift this series (see webapp's chart
// handler) — only which point within it is marked active. The series
// itself only changes when the viewed month crosses into a different
// calendar year.
func YearOverYearSeries(store *archive.Store, md masterdata.MasterData, unitID string, kind masterdata.Kind, year int, lookbackDays int) (YearSeries, error) {
	return yearOverYearSeries(store, md, kind, year, lookbackDays, func(mp masterdata.MeterPoint) bool {
		return mp.UnitID == unitID
	})
}

// BuildingYearOverYearSeries is YearOverYearSeries's whole-installation
// counterpart: every meter point of kind across every unit, summed into
// one series instead of one unit's — UI-02's "Gesamtanlage" year chart.
// Same fixed-calendar-year rule as YearOverYearSeries applies, for the
// same reason.
func BuildingYearOverYearSeries(store *archive.Store, md masterdata.MasterData, kind masterdata.Kind, year int, lookbackDays int) (YearSeries, error) {
	return yearOverYearSeries(store, md, kind, year, lookbackDays, func(masterdata.MeterPoint) bool { return true })
}

// yearOverYearSeries is the shared core of YearOverYearSeries and
// BuildingYearOverYearSeries: which meter points of kind to sum over is
// the only thing that differs between "one unit" and "the whole
// installation", so that is the one parameter that varies.
func yearOverYearSeries(store *archive.Store, md masterdata.MasterData, kind masterdata.Kind, year int, lookbackDays int, include func(masterdata.MeterPoint) bool) (YearSeries, error) {
	rangeEnd, err := telegram.ParseDay(fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return YearSeries{}, err
	}
	// One extra December before Year-1 so January of Year-1 has a prior
	// reading to compute a consumption delta against.
	rangeStart, err := telegram.ParseDay(fmt.Sprintf("%04d-12-01", year-2))
	if err != nil {
		return YearSeries{}, err
	}

	sums := make(map[string]float64)
	found := make(map[string]bool)
	for _, mp := range md.MeterPoints {
		if mp.Kind != kind || !include(mp) {
			continue
		}
		results, err := seriesForMeterPoint(store, md, mp, rangeStart, rangeEnd, lookbackDays)
		if err != nil {
			return YearSeries{}, err
		}
		for _, r := range results {
			sums[r.Month] += r.Consumption
			found[r.Month] = true
		}
	}

	series := YearSeries{Kind: kind, Year: year}
	for m := 1; m <= 12; m++ {
		curLabel := fmt.Sprintf("%04d-%02d", year, m)
		priorLabel := fmt.Sprintf("%04d-%02d", year-1, m)
		series.CurrentYear[m-1] = YearPoint{Month: curLabel, Value: sums[curLabel], Found: found[curLabel]}
		series.PriorYear[m-1] = YearPoint{Month: priorLabel, Value: sums[priorLabel], Found: found[priorLabel]}
	}
	return series, nil
}

// BuildingComparisonForMonth computes FACH-04's area-normalized average
// consumption across every unit for one month and consumption kind.
func BuildingComparisonForMonth(store *archive.Store, md masterdata.MasterData, kind masterdata.Kind, targetMonth telegram.Day, lookbackDays int) (float64, error) {
	consumptionPerUnit := make(map[string]float64)
	areaPerUnit := make(map[string]float64)
	for _, u := range md.Units {
		areaPerUnit[u.ID] = u.AreaM2
	}

	rangeStart := targetMonth.AddDays(-40) // just enough to compute the target month's own consumption
	targetLabel := string(targetMonth)[:7]

	for _, mp := range md.MeterPoints {
		if mp.Kind != kind {
			continue
		}
		results, err := seriesForMeterPoint(store, md, mp, rangeStart, targetMonth, lookbackDays)
		if err != nil {
			return 0, err
		}
		if r := monthResultFor(results, targetLabel); r != nil {
			consumptionPerUnit[mp.UnitID] += r.Consumption
		}
	}

	return BuildingComparison(consumptionPerUnit, areaPerUnit)
}
