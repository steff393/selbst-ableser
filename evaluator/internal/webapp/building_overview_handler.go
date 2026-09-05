package webapp

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/billing"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// buildingKindCell is one unit's figure for one consumption kind, already
// formatted for display — the template cannot index a Go map by a
// non-string key, so the per-kind values are flattened into a row of
// cells in the same order as the table's own header.
type buildingKindCell struct {
	Kind     masterdata.Kind
	Found    bool
	Value    float64
	PerM2    float64
	KWh      float64
	KWhFound bool

	// AboveAverage marks a unit consuming more per m² than the building
	// average for this kind — the one thing an operator scanning the
	// table is actually looking for.
	AboveAverage bool
	PercentOfAvg float64
	HasAverage   bool
}

type buildingUnitRow struct {
	UnitID string
	Name   string
	AreaM2 float64
	Link   string
	Cells  []buildingKindCell
}

type buildingTotalCell struct {
	Kind  masterdata.Kind
	Found bool
	Value float64
	PerM2 float64
}

type buildingOverviewData struct {
	Base
	Month    string
	PrevLink string
	NextLink string

	HasPrevMonth bool
	HasNextMonth bool

	// FirstUnitLink continues the same arrow navigation the unit pages
	// use: the overview sits in front of the units, so its "next" step
	// enters the first one (UI-02).
	HasUnits      bool
	FirstUnitLink string

	BuildingName   string
	Kinds          []masterdata.Kind
	Units          []buildingUnitRow
	Totals         []buildingTotalCell
	TotalAreaM2    float64
	UnitsReporting int
	UnitCount      int

	// Charts carries one year-over-year chart per consumption kind present
	// across the whole installation, in report.Kinds order — the same
	// chart type and JSON contract as the per-unit UVI page's (uvi_chart.js),
	// just summed over every unit instead of one (see
	// billing.BuildingYearOverYearSeries).
	Charts []uviChartView
}

// handleBuildingOverview is the operator's entry point to the UVI (UI-02):
// the whole installation for one month, one row per unit, before paging
// into any single unit with the arrows. Operator-only — a per-unit
// breakdown is exactly what SZ-4 keeps away from a tenant, who gets the
// building only as the single aggregate comparison figure on their own
// UVI page.
func (a *App) handleBuildingOverview(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if a.Vault.Locked() {
		a.renderLocked(w, sess)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	target := a.today()
	if m := r.URL.Query().Get("month"); m != "" {
		if d, err := telegram.ParseDay(m + "-01"); err == nil {
			target = d
		}
	} else if latest, found, err := billingLatestAcrossUnits(a, md, target); err == nil && found {
		target = latest
	}

	report, err := billing.BuildBuildingReport(a.Store, md, md.Building, target, a.lookbackDays())
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	monthLabel := report.Month
	data := buildingOverviewData{
		Base:           a.base("Anlagenübersicht", sess),
		Month:          monthLabel,
		PrevLink:       "/uvi?month=" + billing.OffsetMonth(monthLabel, -1),
		NextLink:       "/uvi?month=" + billing.OffsetMonth(monthLabel, 1),
		BuildingName:   md.Building.Name,
		Kinds:          report.Kinds,
		TotalAreaM2:    report.TotalAreaM2,
		UnitsReporting: report.UnitsReporting,
		UnitCount:      len(report.Units),
		// The overview spans every unit, so it offers whatever month any
		// of them has data for; bounding it to a single unit's range would
		// be wrong here.
		HasPrevMonth: true,
		HasNextMonth: true,
	}

	if len(md.Units) > 0 {
		data.HasUnits = true
		data.FirstUnitLink = uviLink(sess, md.Units[0].ID, monthLabel)
	}

	for _, kind := range report.Kinds {
		data.Totals = append(data.Totals, buildingTotalCell{
			Kind:  kind,
			Found: report.Found[kind],
			Value: report.Total[kind],
			PerM2: report.PerM2[kind],
		})
	}

	if year, yerr := strconv.Atoi(monthLabel[:4]); yerr == nil {
		if month, merr := strconv.Atoi(monthLabel[5:7]); merr == nil {
			for _, kind := range report.Kinds {
				chart, err := a.buildBuildingChartView(md, kind, year, month)
				if err != nil {
					a.renderTechnicalError(w, sess, err)
					return
				}
				data.Charts = append(data.Charts, chart)
			}
		}
	}

	for _, u := range report.Units {
		row := buildingUnitRow{
			UnitID: u.UnitID,
			Name:   u.Name,
			AreaM2: u.AreaM2,
			Link:   uviLink(sess, u.UnitID, monthLabel),
		}
		for _, kind := range report.Kinds {
			cell := buildingKindCell{
				Kind:     kind,
				Found:    u.Found[kind],
				Value:    u.Consumption[kind],
				PerM2:    u.PerM2[kind],
				KWh:      u.KWh[kind],
				KWhFound: u.KWhAvailable[kind],
			}
			if avg := report.PerM2[kind]; avg > 0 && u.Found[kind] && u.AreaM2 > 0 {
				cell.HasAverage = true
				cell.PercentOfAvg = (u.PerM2[kind]/avg - 1) * 100
				cell.AboveAverage = cell.PercentOfAvg > 0
			}
			row.Cells = append(row.Cells, cell)
		}
		data.Units = append(data.Units, row)
	}

	a.render(w, "uvi_overview.html", data)
}

// buildBuildingChartView is buildChartView's whole-installation
// counterpart: the same year-over-year chart, summed across every unit
// instead of scoped to one (billing.BuildingYearOverYearSeries). Kept as
// its own small function, rather than adding a "no unit" case to
// buildChartView, because the two already differ only in which billing
// function they call — everything about turning a YearSeries into a
// uviChartView (scale, DOM id, JSON shape) is identical and shared as-is.
func (a *App) buildBuildingChartView(md masterdata.MasterData, kind masterdata.Kind, year, month int) (uviChartView, error) {
	yoy, err := billing.BuildingYearOverYearSeries(a.Store, md, kind, year, a.lookbackDays())
	if err != nil {
		return uviChartView{}, err
	}

	divisor, decimals := chartScale(kind)
	view := uviChartView{
		Kind:      kind,
		DOMID:     chartDOMID(kind),
		Year:      yoy.Year,
		PriorYear: yoy.Year - 1,
		Rows:      make([]uviChartRow, 12),
	}

	payload, err := json.Marshal(chartData{
		DOMID:       view.DOMID,
		Year:        yoy.Year,
		PriorYear:   yoy.Year - 1,
		ActiveIndex: month - 1,
		ValueUnit:   kindValueUnit(kind),
		Decimals:    decimals,
		Current:     toChartPoints(yoy.CurrentYear, divisor),
		Prior:       toChartPoints(yoy.PriorYear, divisor),
	})
	if err != nil {
		return uviChartView{}, err
	}
	view.JSON = template.JS(payload)

	for i := 0; i < 12; i++ {
		view.Rows[i] = uviChartRow{
			MonthName: germanMonths[i+1],
			Current:   yoy.CurrentYear[i],
			Prior:     yoy.PriorYear[i],
		}
	}
	return view, nil
}

// billingLatestAcrossUnits finds the most recent month any unit in the
// installation has data for, so the overview lands on a month with
// something in it rather than on an empty current month.
func billingLatestAcrossUnits(a *App, md masterdata.MasterData, notAfter telegram.Day) (telegram.Day, bool, error) {
	var latest telegram.Day
	found := false
	for _, u := range md.Units {
		day, ok, err := billing.LatestAvailableMonth(a.Store, md, u.ID, notAfter)
		if err != nil {
			return "", false, err
		}
		if ok && (!found || latest.Before(day)) {
			latest = day
			found = true
		}
	}
	return latest, found, nil
}
