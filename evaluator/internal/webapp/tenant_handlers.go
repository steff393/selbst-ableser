package webapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/billing"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// kindView adds the template-ready percent-change figures that
// billing.KindReport leaves as a (value, ok) pair — html/template cannot
// call a method with two non-error return values directly, and computing
// them here (rather than accepting that limitation with a template
// helper) keeps the "no logic in the template" rule (UI-13) unambiguous:
// this is still handler code, not markup.
type kindView struct {
	billing.KindReport
	PriorMonthPercent       float64
	PriorMonthPercentOK     bool
	PriorYearMonthPercent   float64
	PriorYearMonthPercentOK bool
}

// uviReadingRow is one physical meter's contribution to the page-bottom
// Zählerstände table (UI-01's one "je Zählerplatz" requirement), flattened
// across every KindReport into a single table — Kind names which
// Verbrauchsart it belongs to, since the table itself no longer lives
// inside that kind's own section.
type uviReadingRow struct {
	Kind masterdata.Kind
	billing.MeterReading
}

type uviPageData struct {
	Base
	Kinds    []kindView
	Month    string
	PrevLink string
	NextLink string

	// HasPrevMonth/HasNextMonth bound the month navigation to the range
	// that actually has data (clipped further to a tenant's own access
	// period). Without them, paging one step too far lands on a dead-end
	// "no data" page with no way back except the browser's own button.
	HasPrevMonth bool
	HasNextMonth bool

	// NoDataForMonth marks a month inside that range that still has no
	// computable figures — a gap rather than an edge. The page then
	// renders its navigation with a note instead of becoming a dead end.
	NoDataForMonth bool

	// AllReadings is every meter point's reading across every kind, for
	// the single combined Zählerstände table at the bottom of the page.
	AllReadings []uviReadingRow

	// UnitName and the Has*Unit/*UnitLink fields drive UI-02's "zwischen
	// Wohnungen blättern" navigation. For a tenant the buttons are always
	// rendered but always disabled (Has*Unit false) — visible so the page
	// looks the same regardless of role, never usable, since a tenant may
	// only ever see their own unit.
	UnitName     string
	HasPrevUnit  bool
	PrevUnitLink string
	HasNextUnit  bool
	NextUnitLink string

	// Charts carries one year-over-year chart per consumption kind present
	// in this unit (UI-01's "Verlauf ... im Vergleich zum Vorjahr", see
	// uvi_chart.js), in kindDisplayOrder. Keyed by kind rather than being
	// one heating-only field so each Verbrauchsart's section renders its
	// own chart with its own unit and scale — mixing Einheiten and m³ onto
	// one pair of axes would be the dual-axis mistake in disguise.
	Charts []uviChartView
}

// uviChartView is one Verbrauchsart's chart: the JSON the client-side
// renderer consumes, plus the identical numbers as plain server-rendered
// rows (a collapsed "Werte als Tabelle" — see uvi.html) so they stay
// reachable without JavaScript at all, not just without hovering. The
// chart is the enhancement; the table is the baseline.
type uviChartView struct {
	Kind      masterdata.Kind
	DOMID     string // unique per kind, so several charts can share a page
	JSON      template.JS
	Year      int
	PriorYear int
	Rows      []uviChartRow
}

// uviChartRow is one calendar month's pair of values for the heating
// year-over-year table/chart — MonthName is German-only (STAMM-07/UI-11:
// no i18n infrastructure in this system), reused from templates.go's own
// germanMonths so the two spellings can never drift apart.
type uviChartRow struct {
	MonthName string
	Current   billing.YearPoint
	Prior     billing.YearPoint
}

// chartPoint is one calendar month's value for the year-over-year chart —
// JSON field names are the JS side's contract (see uvi_chart.js).
type chartPoint struct {
	Month string  `json:"month"` // "YYYY-MM"
	Value float64 `json:"value"`
	Found bool    `json:"found"`
}

// chartData is embedded into the page as JSON (see readings.html's
// identical RowsJSON pattern) rather than rendered to SVG server-side:
// the hover crosshair, tooltip, and keyboard-accessible focus this chart
// needs are all pointer/DOM-driven, which only makes sense client-side —
// see uvi_chart.js.
type chartData struct {
	DOMID       string       `json:"dom_id"`
	Year        int          `json:"year"`
	PriorYear   int          `json:"prior_year"`
	ActiveIndex int          `json:"active_index"` // 0 = January
	ValueUnit   string       `json:"value_unit"`
	Decimals    int          `json:"decimals"`
	Current     []chartPoint `json:"current"`
	Prior       []chartPoint `json:"prior"`
}

// chartScale converts a kind's internally computed consumption into the
// figure actually shown, and says how many decimals it wants — the same
// conversion templates.go's "consumption" func applies, kept in step with
// it so a chart's numbers and the table beneath it can never disagree.
// Water is computed in liters throughout and displayed in m³.
func chartScale(k masterdata.Kind) (divisor float64, decimals int) {
	if k == masterdata.KindHeating {
		return 1, 0
	}
	return 1000, 3
}

func toChartPoints(pts [12]billing.YearPoint, divisor float64) []chartPoint {
	out := make([]chartPoint, 12)
	for i, p := range pts {
		out[i] = chartPoint{Month: p.Month, Value: p.Value / divisor, Found: p.Found}
	}
	return out
}

// chartDOMID is the mount point id for one kind's chart. Several charts
// share the UVI page now, so the id has to distinguish them.
func chartDOMID(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeating:
		return "uvi-chart-heating"
	case masterdata.KindHotWater:
		return "uvi-chart-hotwater"
	default:
		return "uvi-chart-coldwater"
	}
}

// buildChartView assembles one Verbrauchsart's year-over-year chart for
// the unit: the JSON its client-side renderer reads, and the same twelve
// months as plain rows for the no-JavaScript fallback table.
func (a *App) buildChartView(md masterdata.MasterData, unitID string, kind masterdata.Kind, year, month int) (uviChartView, error) {
	yoy, err := billing.YearOverYearSeries(a.Store, md, unitID, kind, year, a.lookbackDays())
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

// handleUVI is UI-01/UI-02: one unit's monthly consumption information —
// the tenant's own, or (UI-02) any unit an operator picks, for control
// and support ("der Mieter aus Wohnung 4 fragt nach"). All computation
// happens in internal/billing; this handler resolves which unit and month
// to show, clips to the tenant's access period where that applies
// (FACH-10), and hands the result to the template (UI-13).
func (a *App) handleUVI(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)

	var unitID string
	switch {
	case sess == nil:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case sess.Role == access.RoleTenant:
		unitID = sess.UnitID // a tenant's own unit only, never overridable by ?unit=
	case sess.Role == access.RoleOperator:
		unitID = r.URL.Query().Get("unit")
		if unitID == "" {
			// An operator arriving without a unit gets the whole
			// installation first and pages into individual units from
			// there (UI-02), rather than landing arbitrarily on whichever
			// unit happens to be first.
			a.handleBuildingOverview(w, r)
			return
		}
	default:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := access.RequireUVIAccess(sess, unitID); err != nil {
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
	building := md.Building

	units := md.Units
	unitIndex := -1
	for i, u := range units {
		if u.ID == unitID {
			unitIndex = i
			break
		}
	}
	if unitIndex == -1 && sess.Role == access.RoleOperator {
		// A stale ?unit= (renamed or deleted since the link was made):
		// fall back to the installation overview rather than silently
		// showing some other unit's figures under the requested URL.
		a.handleBuildingOverview(w, r)
		return
	}
	var unitName string
	if unitIndex >= 0 {
		unitName = units[unitIndex].Name
	}

	// The navigable month range: what the archive holds for this unit,
	// clipped to the tenant's own access period where there is one. Both
	// ends bound the arrows below, so paging can never leave the range.
	upperBound := a.today()
	if sess.AccessEnd != nil && sess.AccessEnd.Before(upperBound) {
		upperBound = *sess.AccessEnd
	}
	latestMonth, hasLatest, err := billing.LatestAvailableMonth(a.Store, md, unitID, upperBound)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	earliestMonth, hasEarliest, err := billing.EarliestAvailableMonth(a.Store, md, unitID)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	if hasEarliest && sess.AccessStart != "" && earliestMonth.Before(sess.AccessStart) {
		earliestMonth = sess.AccessStart
	}

	// Only target's "YYYY-MM" prefix is ever used below, so any day within
	// the intended month works — the 1st is as good as the last.
	target := a.today()
	if m := r.URL.Query().Get("month"); m != "" {
		if d, err := telegram.ParseDay(m + "-01"); err == nil {
			target = d
		}
	} else if hasLatest {
		// No month explicitly requested: land on the most recent one that
		// actually has data for this unit, not necessarily the current
		// calendar month — data collection can lag "today" by a wide
		// margin, and a tenant should see their most recent real UVI
		// rather than an empty "no data yet" page. Never past the
		// tenant's own access end, though: a former tenant's default
		// landing month must not run past their own move-out just
		// because the meter (now with a different tenant) kept reporting.
		// (An operator session carries no AccessEnd at all, so this is a
		// no-op bound for that role — see access.Session's own doc
		// comment on why those fields stay unset for RoleOperator.)
		target = latestMonth
	}

	// Clamp an explicitly requested month into the navigable range, so a
	// hand-edited or stale URL lands on the nearest real month instead of
	// a dead end.
	if hasLatest && latestMonth.Before(target) {
		target = latestMonth
	}
	if hasEarliest && target.Before(earliestMonth) {
		target = earliestMonth
	}

	_, _, empty := billing.ClipToAccess(target, target, sess.AccessStart, sess.AccessEnd)
	if empty {
		if sess.AccessEnd != nil {
			a.renderExpired(w, sess, formatDay(*sess.AccessEnd))
			return
		}
		a.renderNoData(w, sess)
		return
	}

	report, err := billing.BuildUnitReport(a.Store, md, building, unitID, target, a.lookbackDays())
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	views := make([]kindView, 0, len(report.Kinds))
	var allReadings []uviReadingRow
	for _, k := range report.Kinds {
		v := kindView{KindReport: k}
		v.PriorMonthPercent, v.PriorMonthPercentOK = k.CurrentMonth.PercentChange(k.PriorMonth)
		v.PriorYearMonthPercent, v.PriorYearMonthPercentOK = k.CurrentMonth.PercentChange(k.PriorYearMonth)
		views = append(views, v)
		for _, reading := range k.Readings {
			allReadings = append(allReadings, uviReadingRow{Kind: k.Kind, MeterReading: reading})
		}
	}

	monthLabel := string(target)[:7]
	data := uviPageData{
		Base:           a.base("Verbrauchsinformation", sess),
		Kinds:          views,
		Month:          monthLabel,
		PrevLink:       uviLink(sess, unitID, billing.OffsetMonth(monthLabel, -1)),
		NextLink:       uviLink(sess, unitID, billing.OffsetMonth(monthLabel, 1)),
		UnitName:       unitName,
		AllReadings:    allReadings,
		NoDataForMonth: len(report.Kinds) == 0 && unitHasMeterPoints(md, unitID),
	}
	// Compared as "YYYY-MM" prefixes: the bounds are arbitrary days within
	// their month, so comparing whole days would make the first and last
	// month navigable or not depending on which day of it happened to
	// carry a reading.
	if hasEarliest {
		data.HasPrevMonth = billing.OffsetMonth(monthLabel, -1) >= string(earliestMonth)[:7]
	}
	if hasLatest {
		data.HasNextMonth = billing.OffsetMonth(monthLabel, 1) <= string(latestMonth)[:7]
	}

	if year, yerr := strconv.Atoi(monthLabel[:4]); yerr == nil {
		if month, merr := strconv.Atoi(monthLabel[5:7]); merr == nil {
			for _, k := range report.Kinds {
				chart, err := a.buildChartView(md, unitID, k.Kind, year, month)
				if err != nil {
					a.renderTechnicalError(w, sess, err)
					return
				}
				data.Charts = append(data.Charts, chart)
			}
		}
	}
	if sess.Role == access.RoleOperator {
		// The installation overview sits in front of the units, so paging
		// back from the first unit returns to it rather than dead-ending
		// (UI-02).
		data.HasPrevUnit = true
		if unitIndex > 0 {
			data.PrevUnitLink = uviLink(sess, units[unitIndex-1].ID, monthLabel)
		} else {
			data.PrevUnitLink = "/uvi?month=" + url.QueryEscape(monthLabel)
		}
		data.HasNextUnit = unitIndex < len(units)-1
		if data.HasNextUnit {
			data.NextUnitLink = uviLink(sess, units[unitIndex+1].ID, monthLabel)
		}
	}
	a.render(w, "uvi.html", data)
}

// unitHasMeterPoints separates "nothing is configured for this unit yet"
// from "configured, but this particular month has no figures" — the two
// need different wording on an otherwise identical empty page.
func unitHasMeterPoints(md masterdata.MasterData, unitID string) bool {
	for _, mp := range md.MeterPoints {
		if mp.UnitID == unitID {
			return true
		}
	}
	return false
}

// uviLink builds a /uvi URL for monthLabel, adding ?unit= only for an
// operator (UI-02) — a tenant's URLs stay exactly as before, since ?unit=
// would be silently ignored for that role anyway (see handleUVI).
func uviLink(sess *access.Session, unitID, monthLabel string) string {
	link := "/uvi?month=" + url.QueryEscape(monthLabel)
	if sess.Role == access.RoleOperator {
		link += "&unit=" + url.QueryEscape(unitID)
	}
	return link
}

func formatDay(d telegram.Day) string {
	s := string(d)
	if len(s) != 10 {
		return s
	}
	return fmt.Sprintf("%s.%s.%s", s[8:10], s[5:7], s[0:4])
}
