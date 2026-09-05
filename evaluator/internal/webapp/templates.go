package webapp

import (
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"path"

	"selbst-ableser/internal/masterdata"
)

var templateFuncs = template.FuncMap{
	"percent":   func(v float64) string { return fmt.Sprintf("%+.0f %%", v) },
	"number":    func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"abs":       func(v float64) float64 { return math.Abs(v) },
	"add":       func(a, b int) int { return a + b },
	"sub":       func(a, b int) int { return a - b },
	"kindLabel": kindLabel,
	"unitLabel": kindValueUnit,
	// consumption formats a heating (dimensionless HKV units, no decimals)
	// or water (stored/computed in liters, shown as m³ with 3 decimals —
	// i.e. down to the liter, matching what a physical water meter's dial
	// actually shows) reading or consumption figure for display. v may be
	// float64 (a computed consumption/comparison figure) or int64 (a raw
	// meter reading) — both occur in the UVI view.
	"consumption": consumptionLabel,
	"monthLabel":  germanMonthLabel,
	"bytesHuman":  bytesHuman,
}

// kindValueUnit is the display unit for a consumption kind. Named rather
// than inlined into templateFuncs because the chart handler needs the
// exact same answer (see buildChartView): a chart labelled in different
// units than the table under it would be worse than no chart.
func kindValueUnit(k masterdata.Kind) string {
	if k == masterdata.KindHeating {
		return "Einheiten"
	}
	return "m³" // FACH-05: water's display unit is m³, not the liters it's stored/computed in
}

func consumptionLabel(k masterdata.Kind, v any) string {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int64:
		f = float64(n)
	}
	if k == masterdata.KindHeating {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.3f", f/1000)
}

// kindLabel is the German display label for a consumption kind, shared
// between the template FuncMap above and any Go code that needs the same
// label outside a template (e.g. the readings export) — QUAL-06 forbids
// a second copy of this mapping.
func kindLabel(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeating:
		return "Heizung"
	case masterdata.KindHotWater:
		return "Warmwasser"
	case masterdata.KindColdWater:
		return "Kaltwasser"
	default:
		return "unbekannt"
	}
}

// bytesHuman renders a byte count the way an operator would read it
// (DATEN-08/UI-08's "Belegung des Speicherorts"), not as a raw integer.
func bytesHuman(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

var germanMonths = [...]string{"", "Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember"}

// germanMonthLabel turns "YYYY-MM" into "<Monatsname> YYYY" (UI-11:
// German only, no translation infrastructure).
func germanMonthLabel(ym string) string {
	if len(ym) != 7 {
		return ym
	}
	month := int(ym[5]-'0')*10 + int(ym[6]-'0')
	if month < 1 || month > 12 {
		return ym
	}
	return germanMonths[month] + " " + ym[:4]
}

// LoadTemplates parses every page in templates/*.html together with
// layout.html, one combined set per page (each page defines its own
// "content" block; keeping them in separate sets avoids the block-name
// collision that would come from parsing every page into one shared set).
func LoadTemplates(fsys fs.FS) (map[string]*template.Template, error) {
	layoutPath := "templates/layout.html"
	pages, err := fs.Glob(fsys, "templates/*.html")
	if err != nil {
		return nil, err
	}

	out := make(map[string]*template.Template)
	for _, p := range pages {
		name := path.Base(p)
		if name == "layout.html" {
			continue
		}
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(fsys, layoutPath, p)
		if err != nil {
			return nil, fmt.Errorf("webapp: parsing %s: %w", p, err)
		}
		out[name] = t
	}
	return out, nil
}

// render executes a page's "layout" template. Errors here mean a template
// bug, not a user-facing condition, so they are logged and answered with a
// generic 500 rather than leaking detail (UI-09's "technischer Fehler"
// case).
func (a *App) render(w http.ResponseWriter, page string, data any) {
	t, ok := a.Templates[page]
	if !ok {
		a.logger().Error("no such template", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		a.logger().Error("rendering template", "page", page, "err", err)
	}
}
