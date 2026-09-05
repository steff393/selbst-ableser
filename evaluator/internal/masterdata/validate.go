package masterdata

import (
	"fmt"
	"regexp"
)

// Diagnostics is the result of validating a MasterData value. Errors block
// saving (STAMM-05: reject entirely rather than partially adopt); Warnings
// are reported but do not. The one remaining Warning case (a meter point
// with no meter at all yet) covers laying out an installation's rooms
// before its devices are actually mounted — an expected, if imperfect,
// starting state. A meter point that once had an active meter and now
// shows a gap before the next one is an Error instead: a swap is recorded
// once both ends are known, not the moment the old meter is physically
// pulled, so a saved gap means the removal and the next installation were
// entered inconsistently, not that time genuinely passed on-site.
type Diagnostics struct {
	Errors   []string
	Warnings []string
}

// OK reports whether md may be saved.
func (d Diagnostics) OK() bool { return len(d.Errors) == 0 }

var meterNumberPattern = regexp.MustCompile(`^\d{8}$`)

// Validate checks a MasterData value against STAMM-02 and STAMM-05.
func Validate(md MasterData) Diagnostics {
	var d Diagnostics

	units := make(map[string]Unit, len(md.Units))
	for _, u := range md.Units {
		if _, dup := units[u.ID]; dup {
			d.Errors = append(d.Errors, fmt.Sprintf("unit %q: ID is not unique", u.ID))
		}
		units[u.ID] = u
	}

	meterPointsByID := make(map[string]MeterPoint, len(md.MeterPoints))
	metersByPoint := make(map[string][]Meter)
	for _, mp := range md.MeterPoints {
		if _, dup := meterPointsByID[mp.ID]; dup {
			d.Errors = append(d.Errors, fmt.Sprintf("meter point %q: ID is not unique", mp.ID))
		}
		meterPointsByID[mp.ID] = mp
		if _, ok := units[mp.UnitID]; !ok {
			d.Errors = append(d.Errors, fmt.Sprintf("meter point %q: refers to unknown unit %q", mp.ID, mp.UnitID))
		}
	}

	meterNumbers := make(map[string]string) // number -> meter point ID it was first seen at
	for _, m := range md.Meters {
		if !meterNumberPattern.MatchString(m.Number) {
			d.Errors = append(d.Errors, fmt.Sprintf("meter %q: number must be exactly 8 digits", m.Number))
		} else if firstPoint, seen := meterNumbers[m.Number]; seen && firstPoint != m.MeterPointID {
			d.Errors = append(d.Errors, fmt.Sprintf("meter %q: appears at both meter point %q and %q", m.Number, firstPoint, m.MeterPointID))
		} else {
			meterNumbers[m.Number] = m.MeterPointID
		}

		if _, ok := meterPointsByID[m.MeterPointID]; !ok {
			d.Errors = append(d.Errors, fmt.Sprintf("meter %q: refers to unknown meter point %q", m.Number, m.MeterPointID))
		}
		if (m.RemovedAt != nil) != (m.EndReading != nil) {
			d.Errors = append(d.Errors, fmt.Sprintf("meter %q: removal date and end reading must both be set, or neither", m.Number))
		}
		if m.ResetMonth != 0 && (m.ResetMonth < 1 || m.ResetMonth > 12) {
			d.Errors = append(d.Errors, fmt.Sprintf("meter %q: reset month must be between 1 and 12", m.Number))
		}

		metersByPoint[m.MeterPointID] = append(metersByPoint[m.MeterPointID], m)
	}

	for _, u := range md.Units {
		hasMeterPoint := false
		for _, mp := range md.MeterPoints {
			if mp.UnitID == u.ID {
				hasMeterPoint = true
				break
			}
		}
		if hasMeterPoint && u.AreaM2 <= 0 {
			d.Errors = append(d.Errors, fmt.Sprintf("unit %q: area must be greater than zero", u.ID))
		}
	}

	for _, mp := range md.MeterPoints {
		meters := metersByPoint[mp.ID]
		if len(meters) == 0 {
			// A warning, not an error (STAMM-02): a meter point with no
			// meter is a normal intermediate state — the operator lays out
			// the installation's rooms first and fills in device numbers
			// and keys as the devices are actually mounted, and a meter
			// point sits empty between a removal and its replacement.
			// Everything downstream treats such a point as having no
			// readings rather than assuming one exists.
			d.Warnings = append(d.Warnings, fmt.Sprintf("meter point %q: has no meter", mp.ID))
			continue
		}
		sortMetersByInstall(meters)

		for i, m := range meters {
			if i == 0 {
				continue
			}
			prev := meters[i-1]
			// Strictly sequential: the previous meter must be fully closed
			// off (removal date and end reading) before the next one's
			// installation, with no overlap.
			switch {
			case prev.RemovedAt == nil:
				d.Errors = append(d.Errors, fmt.Sprintf(
					"meter point %q: meter %q was never closed off (no removal date) before meter %q was installed",
					mp.ID, prev.Number, m.Number))
			case m.InstalledAt.Before(*prev.RemovedAt):
				d.Errors = append(d.Errors, fmt.Sprintf(
					"meter point %q: meter %q is installed before meter %q was removed (overlap)",
					mp.ID, m.Number, prev.Number))
			case prev.RemovedAt.Before(m.InstalledAt):
				d.Errors = append(d.Errors, fmt.Sprintf(
					"meter point %q: gap between meter %q's removal (%s) and meter %q's installation (%s)",
					mp.ID, prev.Number, *prev.RemovedAt, m.Number, m.InstalledAt))
			}
		}
	}

	return d
}
