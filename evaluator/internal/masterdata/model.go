package masterdata

import "selbst-ableser/internal/telegram"

// Kind is the consumption type a meter point measures. It, not the
// telegram's medium field, decides how a reading is interpreted (see
// internal/telegram.IdentifyDeviceType).
type Kind int

const (
	KindHeating Kind = iota
	KindHotWater
	KindColdWater
)

func (k Kind) String() string {
	switch k {
	case KindHeating:
		return "heating"
	case KindHotWater:
		return "hot water"
	case KindColdWater:
		return "cold water"
	default:
		return "unknown"
	}
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
type MeterPoint struct {
	ID     string // unique, operator-chosen, may be renamed freely
	UnitID string
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

	// ResetMonth is the calendar month (1-12) this meter's billing period
	// resets in ("Stichtag", FACH-03) — meaningful for heat-cost
	// allocators only; water meters count continuously and never reset.
	// 0 means unset; EffectiveResetMonth resolves that to January.
	ResetMonth int
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

// EffectiveResetMonth returns m.ResetMonth, or January if it is unset (zero).
func (m Meter) EffectiveResetMonth() int {
	if m.ResetMonth == 0 {
		return 1
	}
	return m.ResetMonth
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
