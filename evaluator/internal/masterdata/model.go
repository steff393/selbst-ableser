package masterdata

import "selbst-ableser/internal/telegram"

// Kind is the consumption type a meter point measures. It, not the
// telegram's medium field, decides how a reading is interpreted — the
// medium field is a device's claim about itself and only good enough to
// pre-sort suggestions during data entry (see
// internal/telegram.IdentifyDeviceType).
//
// The numbering is part of the stored format: values are appended, never
// reordered, so an existing installation keeps its meaning.
type Kind int

const (
	// KindHeatCostAllocator is the German HKV: a dimensionless
	// evaporation- or electronic allocator on a radiator. It is the only
	// kind that resets itself once a year (FACH-03).
	KindHeatCostAllocator Kind = iota
	KindHotWater
	KindColdWater

	// KindHeatMeter (WMZ) counts real energy in kWh — heating measured
	// directly, as in an underfloor-heating circuit with one heat meter
	// per flat, rather than apportioned through heat cost allocators. At
	// a meter point with a unit it is part of the consumption information
	// like any other heating figure; at a meter point without one it is
	// the house's own device and only watched (see PartOfBilling).
	//
	// KindGas counts gas volume. It was added for a house-level meter the
	// operator wants to watch, is not billed, and has no decoder yet.
	KindHeatMeter
	KindGas
)

func (k Kind) String() string {
	switch k {
	case KindHeatCostAllocator:
		return "heat cost allocator"
	case KindHotWater:
		return "hot water"
	case KindColdWater:
		return "cold water"
	case KindHeatMeter:
		return "heat meter"
	case KindGas:
		return "gas"
	default:
		return "unknown"
	}
}

// Known reports whether k is one of the defined kinds — a stored file or
// a submitted form could carry anything.
func (k Kind) Known() bool {
	return k >= KindHeatCostAllocator && k <= KindGas
}

// Billable reports whether this kind takes part in the consumption
// information a tenant sees. Gas does not: a gas meter is the house's
// supply, watched but never apportioned to a flat.
func (k Kind) Billable() bool {
	return k == KindHeatCostAllocator || k == KindHeatMeter || k == KindHotWater || k == KindColdWater
}

// CountsKWh reports whether this kind's readings already are kilowatt
// hours. For a heat meter the energy figure a consumption report shows is
// the reading itself; for a heat cost allocator or hot water it is a
// conversion with an installation-specific factor. Displays need to know
// the difference so as not to print the same number twice, or to label a
// measured figure as a calculated one.
func (k Kind) CountsKWh() bool {
	return k == KindHeatMeter
}

// ResetsAnnually reports whether devices of this kind zero themselves at
// a yearly Stichtag (FACH-03). Only heat cost allocators do. It lives
// here as one sentence rather than as a comparison repeated at each call
// site, because getting it wrong for a heat meter — which counts kWh
// straight through and is never reset — would not raise an error
// anywhere; it would quietly produce a wrong number once a year.
func (k Kind) ResetsAnnually() bool {
	return k == KindHeatCostAllocator
}

// Building holds installation-wide settings. JSON field names are lower-
// snake-case so a masterdata export (STAMM-07) reads consistently with
// the rest of that document, not because the encrypted on-disk format
// itself needs it to be human-readable.
type Building struct {
	Name string `json:"name"`

	// HeatingKWhPerUnit and HotWaterKWhPerM3 convert a heat-cost-allocator
	// or hot-water reading into an informational kWh figure. Both are
	// specific to this installation and are re-derived by the operator
	// from the actual annual statement; the currently configured value
	// applies to the whole displayed history, not just future months
	// (an intentional simplification, not a bug — see docs/architektur.md).
	HeatingKWhPerUnit float64 `json:"heating_kwh_per_unit"`
	HotWaterKWhPerM3  float64 `json:"hot_water_kwh_per_m3"`
}

// EffectiveHeatingKWhPerUnit returns HeatingKWhPerUnit, or 1 if it is
// unset (zero) — a fresh installation shows the heat-cost-allocator's raw
// reading as its kWh figure until the operator enters the real factor
// from the annual statement, the same "unset means unscaled" convention
// as Meter.EffectiveKCFactor.
func (b Building) EffectiveHeatingKWhPerUnit() float64 {
	if b.HeatingKWhPerUnit == 0 {
		return 1
	}
	return b.HeatingKWhPerUnit
}

// EffectiveHotWaterKWhPerM3 is EffectiveHeatingKWhPerUnit's counterpart
// for HotWaterKWhPerM3.
func (b Building) EffectiveHotWaterKWhPerM3() float64 {
	if b.HotWaterKWhPerM3 == 0 {
		return 1
	}
	return b.HotWaterKWhPerM3
}

// Unit is a rentable unit ("Wohnung"): a fixed floor area, independent of
// who currently lives there or whether anyone does.
type Unit struct {
	ID     string // unique, operator-chosen, may be renamed freely
	Name   string
	AreaM2 float64
}

// MeterPoint is a physical, permanent measuring location ("Zählerplatz"),
// e.g. "radiator, living room, unit 3". The meter installed at it changes
// over time (see Meter); the point itself does not.
//
// UnitID may be empty. Such a point belongs to no rentable unit — the
// house's own heat meter, gas meter or main water meter. It is watched
// like any other, and every path that computes a tenant's consumption
// information walks units and therefore passes it by on its own, which is
// exactly why this is expressed as "no unit" rather than as a special
// unit that every one of those paths would have to know to skip.
type MeterPoint struct {
	ID     string // unique, operator-chosen, may be renamed freely
	UnitID string // empty for a house meter that belongs to no unit
	Room   string
	Kind   Kind
}

// Meter is one physical device as installed at a MeterPoint for some
// period of time. AESKey is fixed-size, which by construction rules out
// the "wrong key length" class of error STAMM-05 asks to catch.
type Meter struct {
	Number       string // 8-digit, as printed on the device and encoded in its telegrams
	MeterPointID string
	AESKey       [16]byte

	// InstalledAt may be empty, which reads as "has been there all along"
	// and compares earlier than any real date (telegram.Day orders as a
	// string). An installation taken over from a previous service
	// provider has no meaningful installation date for the devices it
	// finds already mounted, and inventing one would put a number in the
	// file that later looks like a fact.
	InstalledAt  telegram.Day
	StartReading int64

	// RemovedAt and EndReading are both set once the meter is taken out of
	// service, and never independently: STAMM-05 requires an end reading
	// whenever a removal date is recorded, so a meter's swap-out is
	// either fully documented or not documented at all.
	RemovedAt  *telegram.Day
	EndReading *int64

	// KCFactor scales this meter's heat-cost-allocator readings to the
	// actual heat output of its radiator (FACH-07). It is meaningless for
	// water meters and defaults to 1 (unscaled) when unset.
	KCFactor float64

	// FreezeMonth is the calendar month (1-12) whose last day is this
	// meter's Stichtag: the day it freezes its reading and starts counting
	// from zero again (FACH-03). Meaningful for heat-cost allocators only;
	// water meters count continuously and never reset.
	//
	// 0 means unset; EffectiveFreezeMonth resolves that to December, the
	// common 31 December Stichtag.
	//
	// Until 2026-09 this was ResetMonth and held the *first month of the
	// new period* instead (January for a 31 December Stichtag) while the UI
	// called it "Stichtag-Monat" — name and meaning disagreed by one month.
	// The old JSON key is not read: no installation had been distributed,
	// so a file still carrying it simply shows an empty Stichtag month that
	// the operator fills in once, rather than a converted value nobody
	// would think to check.
	FreezeMonth int
}

// PartOfBilling reports whether this meter point takes part in what
// tenants are shown and in the installation-wide figures their own are
// compared against: it belongs to a rentable unit, and it measures
// something that is billed at all.
//
// Every whole-installation aggregate has to ask this rather than simply
// walking all meter points. A house's own cold-water meter is a meter
// point of a billed kind, but summing it into the building total would
// count the building's consumption twice — once through the flats and
// once through the main meter — and nothing about the resulting number
// would look wrong.
func (mp MeterPoint) PartOfBilling() bool {
	return mp.UnitID != "" && mp.Kind.Billable()
}

// InUnit reports whether this meter point belongs to unitID and is part
// of what that unit is billed for — the question every per-unit figure in
// the consumption information asks.
//
// An empty unitID matches nothing, deliberately. Written as a plain
// comparison, a lookup that lost its unit on the way would match every
// house meter instead (they carry no unit either) and present them as one
// tenant's own consumption. The guard sits here rather than at each
// caller because it must hold at every one of them.
func (mp MeterPoint) InUnit(unitID string) bool {
	return unitID != "" && mp.UnitID == unitID && mp.Kind.Billable()
}

// Active reports whether the meter is the one considered installed at its
// meter point on the given day (STAMM-02): it has been installed by then,
// and — if it has since been removed — the day is not after removal.
func (m Meter) Active(day telegram.Day) bool {
	if day.Before(m.InstalledAt) {
		return false
	}
	if m.RemovedAt != nil && m.RemovedAt.Before(day) {
		return false
	}
	return true
}

// EffectiveKCFactor returns m.KCFactor, or 1 if it is unset (zero).
func (m Meter) EffectiveKCFactor() float64 {
	if m.KCFactor == 0 {
		return 1
	}
	return m.KCFactor
}

// EffectiveFreezeMonth returns m.FreezeMonth, or December if it is unset
// (zero) — the 31 December Stichtag most installations use.
func (m Meter) EffectiveFreezeMonth() int {
	if m.FreezeMonth == 0 {
		return 12
	}
	return m.FreezeMonth
}

// Access is a tenant's access grant ("Zugang"): a token bound to one unit
// and a time window. Session/token mechanics live in internal/access; this
// is only the master-data record of who is allowed to see what.
type Access struct {
	Token  string
	UnitID string
	Start  telegram.Day
	End    *telegram.Day // nil means still current

	// Email is the one optional piece of personal data this system
	// stores at all (SZ-6): nothing else about who holds this access —
	// no name, no address — deliberately, to keep the installation clear
	// of GDPR data-subject-rights obligations it would otherwise incur.
	// Used only to send the monthly reminder that a new UVI is available
	// (BENACHR-01); set, changed, or cleared from the Zugänge page.
	Email string
}

// MasterData is the complete, decrypted description of one installation.
type MasterData struct {
	Building    Building
	Units       []Unit
	MeterPoints []MeterPoint
	Meters      []Meter
	Accesses    []Access
}

// MetersAt returns the meters installed at meterPointID, ordered by
// installation date.
func (md MasterData) MetersAt(meterPointID string) []Meter {
	var out []Meter
	for _, m := range md.Meters {
		if m.MeterPointID == meterPointID {
			out = append(out, m)
		}
	}
	sortMetersByInstall(out)
	return out
}

// ActiveMeter returns the meter that was active at a meter point on the
// given day, per STAMM-02: the one with the latest installation date that
// is not after day. It relies on the non-overlap invariant Validate
// checks; on data that violates it, which meter is returned is undefined.
func (md MasterData) ActiveMeter(meterPointID string, day telegram.Day) (Meter, bool) {
	var best Meter
	found := false
	for _, m := range md.Meters {
		if m.MeterPointID != meterPointID {
			continue
		}
		if day.Before(m.InstalledAt) {
			continue
		}
		if !found || best.InstalledAt.Before(m.InstalledAt) {
			best = m
			found = true
		}
	}
	return best, found
}

// MeterByNumber returns the meter with the given physical Number that was
// active on day (see Meter.Active), independent of which meter point it
// belongs to — used to resolve an archived telegram (identified only by
// its meter number) back to the meter point and unit it belongs to, if
// any is currently known.
func (md MasterData) MeterByNumber(number string, day telegram.Day) (Meter, bool) {
	for _, m := range md.Meters {
		if m.Number == number && m.Active(day) {
			return m, true
		}
	}
	return Meter{}, false
}

func sortMetersByInstall(meters []Meter) {
	for i := 1; i < len(meters); i++ {
		for j := i; j > 0 && meters[j].InstalledAt.Before(meters[j-1].InstalledAt); j-- {
			meters[j], meters[j-1] = meters[j-1], meters[j]
		}
	}
}
