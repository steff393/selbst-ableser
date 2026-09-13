package webapp

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// meterNumberPattern rejects a mistyped meter number here, where the
// operator still sees the field it came from. masterdata.Validate checks
// the same thing as the last word before saving, but its message speaks
// about the whole installation rather than about this dialog.
var meterNumberPattern = regexp.MustCompile(`^\d{8}$`)

// The Anlage tab shows the installation the way the operator talks about
// it: a unit, the meter points in it, and at each of those the device
// that hangs there now, with its predecessors underneath. Every ID and
// every meter number opens the dialog for that thing; one button per row
// does the only action that row can have — assign a meter, or swap it.
//
// It is deliberately not a grid. A grid is right for changing forty
// values at once (that is the other tab) and wrong for the questions this
// view answers: what is where, and what is still missing.

type meterView struct {
	Number    string
	AESKey    string
	HasKey    bool
	LastSeen  string // display form, empty when nothing was ever received
	NeverSeen bool
	Installed string // display form, empty means "has been there all along"
	Start     string
	KCFactor  string
	Freeze    string
	Removed   string
	End       string

	// Summary is the one-line description under a meter point's room,
	// assembled server-side so the template stays free of formatting.
	Summary string
}

type placeView struct {
	ID     string
	UnitID string
	Room   string

	Kind      string // wire value, as the form posts it
	KindLabel string // "HKV"
	KindName  string // "Heizkostenverteiler"

	Active  *meterView
	History []meterView
}

type unitView struct {
	ID     string
	Name   string
	Area   string
	Places []placeView

	// Empty is how many of this unit's meter points have no device right
	// now — the number that answers "what is still missing" without
	// opening the unit.
	Empty int
}

type detectedView struct {
	Number   string
	Kind     string // wire value, empty when the telegram says nothing useful
	TypeName string
	LastSeen string
}

// structureView lays out the whole installation for the Anlage tab. The
// archive is asked when it is there: whether a device has ever been heard
// from is the question commissioning turns on, and the answer is not in
// the master data.
func structureView(md masterdata.MasterData, store *archive.Store) ([]unitView, []placeView) {
	lastSeen := func(number string) (string, bool) {
		if store == nil {
			return "", false
		}
		day, ok, err := store.LastSeen(number)
		if err != nil || !ok {
			return "", true
		}
		return formatGridDay(day), false
	}

	metersAt := make(map[string][]masterdata.Meter, len(md.MeterPoints))
	for _, m := range md.Meters {
		metersAt[m.MeterPointID] = append(metersAt[m.MeterPointID], m)
	}
	for id := range metersAt {
		chain := metersAt[id]
		sort.SliceStable(chain, func(i, j int) bool {
			return chain[i].InstalledAt.Before(chain[j].InstalledAt)
		})
	}

	placesOf := func(unitID string) []placeView {
		var out []placeView
		for _, mp := range md.MeterPoints {
			if mp.UnitID != unitID {
				continue
			}
			out = append(out, placeViewOf(mp, metersAt[mp.ID], lastSeen))
		}
		return out
	}

	units := make([]unitView, 0, len(md.Units))
	for _, u := range md.Units {
		places := placesOf(u.ID)
		empty := 0
		for _, p := range places {
			if p.Active == nil {
				empty++
			}
		}
		units = append(units, unitView{
			ID:     u.ID,
			Name:   u.Name,
			Area:   strconv.FormatFloat(u.AreaM2, 'f', -1, 64),
			Places: places,
			Empty:  empty,
		})
	}
	return units, placesOf("")
}

func placeViewOf(mp masterdata.MeterPoint, chain []masterdata.Meter, lastSeen func(string) (string, bool)) placeView {
	p := placeView{
		ID:        mp.ID,
		UnitID:    mp.UnitID,
		Room:      mp.Room,
		Kind:      kindToString(mp.Kind),
		KindLabel: kindShortLabel(mp.Kind),
		KindName:  kindDisplayName(mp.Kind),
	}
	for _, m := range chain {
		v := meterViewOf(m, mp.Kind)
		v.LastSeen, v.NeverSeen = lastSeen(m.Number)
		if m.RemovedAt == nil {
			active := v
			p.Active = &active
			continue
		}
		p.History = append(p.History, v)
	}
	return p
}

func meterViewOf(m masterdata.Meter, kind masterdata.Kind) meterView {
	v := meterView{
		Number:    m.Number,
		Installed: formatOpenDay(m.InstalledAt),
		Start:     formatReadingValue(kind, m.StartReading),
	}
	if m.AESKey != ([16]byte{}) {
		v.AESKey = hex.EncodeToString(m.AESKey[:])
		v.HasKey = true
	}
	if m.KCFactor != 0 {
		v.KCFactor = strconv.FormatFloat(m.KCFactor, 'f', -1, 64)
	}
	if m.FreezeMonth != 0 {
		v.Freeze = strconv.Itoa(m.FreezeMonth)
	}
	if m.RemovedAt != nil {
		v.Removed = formatGridDay(*m.RemovedAt)
	}
	if m.EndReading != nil {
		v.End = formatReadingValue(kind, *m.EndReading)
	}

	parts := []string{}
	if v.KCFactor != "" {
		parts = append(parts, "kc "+v.KCFactor)
	}
	if v.Freeze != "" {
		parts = append(parts, "Stichtag "+freezeDayLabel(m.FreezeMonth))
	}
	if v.Installed == "" {
		parts = append(parts, "Einbau offen")
	} else {
		parts = append(parts, "Einbau "+v.Installed)
	}
	if v.Removed != "" {
		parts = append(parts, "ausgebaut "+v.Removed+", Endstand "+v.End)
	}
	v.Summary = strings.Join(parts, " · ")
	return v
}

// formatOpenDay is formatGridDay for a day that may legitimately be
// unset: an empty installation date reads as "has been there all along"
// (E-5) and must not be rendered as a zero date.
func formatOpenDay(d telegram.Day) string {
	if d == "" {
		return ""
	}
	return formatGridDay(d)
}

// freezeDayLabel writes a Stichtag month as the day it actually falls on
// — that is how it appears on a bill, and it reads better than a number.
func freezeDayLabel(month int) string {
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month < 1 || month > 12 {
		return ""
	}
	return fmt.Sprintf("%d.%02d.", days[month-1], month)
}

func kindShortLabel(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeatCostAllocator:
		return "HKV"
	case masterdata.KindHotWater:
		return "WWZ"
	case masterdata.KindColdWater:
		return "KWZ"
	case masterdata.KindHeatMeter:
		return "WMZ"
	case masterdata.KindGas:
		return "GAS"
	default:
		return "?"
	}
}

func kindDisplayName(k masterdata.Kind) string {
	switch k {
	case masterdata.KindHeatCostAllocator:
		return "Heizkostenverteiler"
	case masterdata.KindHotWater:
		return "Warmwasserzähler"
	case masterdata.KindColdWater:
		return "Kaltwasserzähler"
	case masterdata.KindHeatMeter:
		return "Wärmemengenzähler"
	case masterdata.KindGas:
		return "Gaszähler"
	default:
		return "unbekannt"
	}
}

// structureAction is the shape every edit on the Anlage tab has: check
// the role and the CSRF token before touching the vault, let apply change
// a copy of the master data, and let masterdata.Validate have the last
// word before anything is written. A message from apply, or a failed
// validation, re-renders the page with that message instead of saving —
// STAMM-05's "reject entirely, not partially" for a single dialog.
func (a *App) structureAction(w http.ResponseWriter, r *http.Request, what string, apply func(md *masterdata.MasterData) string) {
	a.structureActionThen(w, r, what, apply, nil)
}

// structureActionThen is structureAction with something to do once the
// change is on disk — for the one case where changing master data has to
// reach beyond it (see handleUnitSave).
func (a *App) structureActionThen(w http.ResponseWriter, r *http.Request, what string, apply func(md *masterdata.MasterData) string, afterSave func()) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	updated := md
	updated.Units = append([]masterdata.Unit(nil), md.Units...)
	updated.MeterPoints = append([]masterdata.MeterPoint(nil), md.MeterPoints...)
	updated.Meters = append([]masterdata.Meter(nil), md.Meters...)
	updated.Accesses = append([]masterdata.Access(nil), md.Accesses...)

	if msg := apply(&updated); msg != "" {
		a.renderMasterData(w, sess, md, msg)
		return
	}
	if d := masterdata.Validate(updated); !d.OK() {
		a.renderMasterData(w, sess, md, "Prüfung fehlgeschlagen: "+joinErrors(d.Errors))
		return
	}
	if err := a.Vault.Save(a.MasterDataPath, updated); err != nil {
		a.renderMasterData(w, sess, md, "Speichern fehlgeschlagen: "+err.Error())
		return
	}
	if afterSave != nil {
		afterSave()
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), what)
	http.Redirect(w, r, "/operator/masterdata", http.StatusSeeOther)
}

// handleUnitSave creates or changes one unit. A changed ID is not a new
// unit: everything that pointed at the old one is moved along with it,
// meter points and access grants alike. Leaving the grants behind would
// be the worse half of a rename — the unit would look right while a
// tenant sees somebody else's consumption information.
func (a *App) handleUnitSave(w http.ResponseWriter, r *http.Request) {
	// A renamed unit ends the sessions that were logged in for the old ID.
	// A tenant's session carries the unit it was opened for, and after a
	// rename that string means something else — with two IDs exchanged, it
	// means somebody else's flat. The grant itself travels with the rename
	// (see renameUnit), so the tenant logs back in with the same token and
	// lands where they belong.
	var renamedFrom string

	a.structureActionThen(w, r, "unit saved", func(md *masterdata.MasterData) string {
		id := strings.TrimSpace(r.PostFormValue("id"))
		original := strings.TrimSpace(r.PostFormValue("original_id"))
		name := strings.TrimSpace(r.PostFormValue("name"))
		areaText := strings.TrimSpace(r.PostFormValue("area"))

		if id == "" {
			return "Die Wohnung braucht eine ID."
		}
		for _, u := range md.Units {
			if u.ID == id && u.ID != original {
				return fmt.Sprintf("Die Wohnungs-ID %s gibt es schon.", id)
			}
		}
		area, err := strconv.ParseFloat(strings.Replace(areaText, ",", ".", 1), 64)
		if err != nil || area <= 0 {
			return "Die Fläche muss eine Zahl größer als 0 sein."
		}

		if original == "" {
			md.Units = append(md.Units, masterdata.Unit{ID: id, Name: name, AreaM2: area})
			return ""
		}
		found := false
		for i := range md.Units {
			if md.Units[i].ID == original {
				md.Units[i] = masterdata.Unit{ID: id, Name: name, AreaM2: area}
				found = true
			}
		}
		if !found {
			return fmt.Sprintf("Die Wohnung %s gibt es nicht mehr — bitte Seite neu laden.", original)
		}
		if id != original {
			renameUnit(md, original, id)
			renamedFrom = original
		}
		return ""
	}, func() {
		if renamedFrom != "" {
			a.Sessions.RevokeByUnit(renamedFrom)
		}
	})
}

// renameUnit carries a unit's new ID to everything that referenced the
// old one. Both lists matter: meter points decide what is measured for
// the unit, access grants decide who may see it.
func renameUnit(md *masterdata.MasterData, from, to string) {
	for i := range md.MeterPoints {
		if md.MeterPoints[i].UnitID == from {
			md.MeterPoints[i].UnitID = to
		}
	}
	for i := range md.Accesses {
		if md.Accesses[i].UnitID == from {
			md.Accesses[i].UnitID = to
		}
	}
}

func (a *App) handleUnitDelete(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "unit deleted", func(md *masterdata.MasterData) string {
		id := strings.TrimSpace(r.PostFormValue("id"))
		var used int
		for _, mp := range md.MeterPoints {
			if mp.UnitID == id {
				used++
			}
		}
		if used > 0 {
			return fmt.Sprintf("Die Wohnung %s hat noch %d Zählerplätze. Erst die umhängen oder entfernen.", id, used)
		}
		var grants int
		for _, ac := range md.Accesses {
			if ac.UnitID == id {
				grants++
			}
		}
		if grants > 0 {
			return fmt.Sprintf("Für die Wohnung %s gibt es noch %d Zugänge. Die zuerst im Reiter Zugänge entfernen.", id, grants)
		}
		out := md.Units[:0]
		for _, u := range md.Units {
			if u.ID != id {
				out = append(out, u)
			}
		}
		md.Units = out
		return ""
	})
}

// handleMeterPointSave creates or changes one meter point. An empty unit
// is not an omission but a house meter (E-11); a unit that was named and
// does not exist is refused here rather than left to Validate, so the
// operator gets back the ID they typed.
func (a *App) handleMeterPointSave(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "meter point saved", func(md *masterdata.MasterData) string {
		id := strings.TrimSpace(r.PostFormValue("id"))
		original := strings.TrimSpace(r.PostFormValue("original_id"))
		unitID := strings.TrimSpace(r.PostFormValue("unit_id"))
		room := strings.TrimSpace(r.PostFormValue("room"))

		if id == "" {
			return "Der Zählerplatz braucht eine ID."
		}
		for _, mp := range md.MeterPoints {
			if mp.ID == id && mp.ID != original {
				return fmt.Sprintf("Den Zählerplatz %s gibt es schon.", id)
			}
		}
		kind, err := parseKind(r.PostFormValue("kind"))
		if err != nil {
			return fmt.Sprintf("Zählerplatz %s: %v", id, err)
		}
		if unitID != "" {
			known := false
			for _, u := range md.Units {
				if u.ID == unitID {
					known = true
				}
			}
			if !known {
				return fmt.Sprintf("Die Wohnung %s gibt es nicht. Leer lassen heißt: Hauszähler ohne Wohnung.", unitID)
			}
		}

		point := masterdata.MeterPoint{ID: id, UnitID: unitID, Room: room, Kind: kind}
		if original == "" {
			md.MeterPoints = append(md.MeterPoints, point)
			return ""
		}
		found := false
		for i := range md.MeterPoints {
			if md.MeterPoints[i].ID == original {
				md.MeterPoints[i] = point
				found = true
			}
		}
		if !found {
			return fmt.Sprintf("Den Zählerplatz %s gibt es nicht mehr — bitte Seite neu laden.", original)
		}
		if id != original {
			for i := range md.Meters {
				if md.Meters[i].MeterPointID == original {
					md.Meters[i].MeterPointID = id
				}
			}
		}
		return ""
	})
}

func (a *App) handleMeterPointDelete(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "meter point deleted", func(md *masterdata.MasterData) string {
		id := strings.TrimSpace(r.PostFormValue("id"))
		var used int
		for _, m := range md.Meters {
			if m.MeterPointID == id {
				used++
			}
		}
		if used > 0 {
			return fmt.Sprintf("Am Zählerplatz %s hängen noch %d Zähler, auch ausgebaute. Erst die entfernen.", id, used)
		}
		out := md.MeterPoints[:0]
		for _, mp := range md.MeterPoints {
			if mp.ID != id {
				out = append(out, mp)
			}
		}
		md.MeterPoints = out
		return ""
	})
}

// meterFromForm reads one meter out of a dialog's fields, reporting the
// first thing that does not hold. place is where a newly created device
// goes; for an edit it is ignored in favour of the meter's own. kind
// decides how the two reading fields are read — m³ with a comma for
// water, a plain integer otherwise (see reading_value.go) — and is the
// caller's responsibility to resolve from the meter's actual (or
// intended) meter point before calling this.
func meterFromForm(r *http.Request, md *masterdata.MasterData, original string, kind masterdata.Kind) (masterdata.Meter, string) {
	number := strings.TrimSpace(r.PostFormValue("number"))
	if !meterNumberPattern.MatchString(number) {
		return masterdata.Meter{}, "Die Zählernummer besteht aus genau acht Ziffern."
	}
	for _, m := range md.Meters {
		if m.Number == number && m.Number != original {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s ist schon am Zählerplatz %s eingetragen.", number, m.MeterPointID)
		}
	}

	var key [16]byte
	if raw := strings.TrimSpace(r.PostFormValue("aes_key")); raw != "" {
		decoded, err := hex.DecodeString(raw)
		if err != nil || len(decoded) != 16 {
			return masterdata.Meter{}, "Der AES-Schlüssel muss leer oder genau 32 Hex-Zeichen sein."
		}
		copy(key[:], decoded)
	}

	m := masterdata.Meter{Number: number, AESKey: key}
	if installed := strings.TrimSpace(r.PostFormValue("installed_at")); installed != "" {
		day, err := parseGridDay(installed)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiges Einbaudatum (TT.MM.JJJJ erwartet).", number)
		}
		m.InstalledAt = day
	}
	start, msg := parseReadingValue(kind, "Anfangsstand", r.PostFormValue("start_reading"))
	if msg != "" {
		return masterdata.Meter{}, fmt.Sprintf("Zähler %s: %s", number, msg)
	}
	m.StartReading = start

	if kc := strings.TrimSpace(r.PostFormValue("kc_factor")); kc != "" {
		f, err := strconv.ParseFloat(strings.Replace(kc, ",", ".", 1), 64)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiger kc-Faktor.", number)
		}
		m.KCFactor = f
	}
	if freeze := strings.TrimSpace(r.PostFormValue("freeze_month")); freeze != "" {
		n, err := strconv.Atoi(freeze)
		if err != nil || n < 1 || n > 12 {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiger Stichtag-Monat (1-12 erwartet).", number)
		}
		m.FreezeMonth = n
	}

	removedText := strings.TrimSpace(r.PostFormValue("removed_at"))
	endText := strings.TrimSpace(r.PostFormValue("end_reading"))
	if (removedText == "") != (endText == "") {
		return masterdata.Meter{}, fmt.Sprintf("Zähler %s: Ausbaudatum und Endstand gehören zusammen — beide oder keines.", number)
	}
	if removedText != "" {
		day, err := parseGridDay(removedText)
		if err != nil {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: ungültiges Ausbaudatum.", number)
		}
		end, msg := parseReadingValue(kind, "Endstand", endText)
		if msg != "" {
			return masterdata.Meter{}, fmt.Sprintf("Zähler %s: %s", number, msg)
		}
		m.RemovedAt = &day
		m.EndReading = &end
	}
	return m, ""
}

// handleMeterSave creates or changes one meter.
//
// Changing a removal date moves the successor's installation date with
// it. The two are the same day by definition — the device that comes out
// and the one that goes in change places once — so setting them together
// makes a gap impossible instead of leaving Validate to complain about
// one afterwards.
func (a *App) handleMeterSave(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "meter saved", func(md *masterdata.MasterData) string {
		original := strings.TrimSpace(r.PostFormValue("original_number"))
		placeID := strings.TrimSpace(r.PostFormValue("meter_point_id"))

		if original == "" {
			meter, msg := meterFromForm(r, md, original, kindOfPlace(md.MeterPoints, placeID))
			if msg != "" {
				return msg
			}
			meter.MeterPointID = placeID
			md.Meters = append(md.Meters, meter)
			return ""
		}

		index := -1
		for i := range md.Meters {
			if md.Meters[i].Number == original {
				index = i
			}
		}
		if index < 0 {
			return fmt.Sprintf("Den Zähler %s gibt es nicht mehr — bitte Seite neu laden.", original)
		}
		previous := md.Meters[index]

		// Ein Gerät an einen anderen Zählerplatz zu hängen ist eine
		// Berichtigung: Jemand hat beim Zuordnen danebengegriffen. Auf
		// einen belegten Platz wäre es dagegen ein Zählerwechsel — der
		// braucht den Endstand des weichenden Geräts und gehört in den
		// Wechsel-Dialog.
		targetPlace := previous.MeterPointID
		if placeID != "" && placeID != previous.MeterPointID {
			if !meterPointExists(*md, placeID) {
				return fmt.Sprintf("Den Zählerplatz %s gibt es nicht.", placeID)
			}
			targetPlace = placeID
		}

		meter, msg := meterFromForm(r, md, original, kindOfPlace(md.MeterPoints, targetPlace))
		if msg != "" {
			return msg
		}
		meter.MeterPointID = previous.MeterPointID
		if targetPlace != previous.MeterPointID {
			if occupant, taken := activeMeterAt(*md, targetPlace); taken && meter.RemovedAt == nil {
				return fmt.Sprintf("Am Zählerplatz %s hängt %s — das wäre ein Zählerwechsel, kein Umhängen.", targetPlace, occupant)
			}
			meter.MeterPointID = targetPlace
		}
		md.Meters[index] = meter

		if removalChanged(previous, meter) {
			moveSuccessorInstallation(md, meter)
		}
		return ""
	})
}

func removalChanged(before, after masterdata.Meter) bool {
	if (before.RemovedAt == nil) != (after.RemovedAt == nil) {
		return true
	}
	return after.RemovedAt != nil && *before.RemovedAt != *after.RemovedAt
}

// moveSuccessorInstallation keeps the chain at one meter point contiguous
// after its removal date was corrected: the device installed right after
// this one starts on the day this one ends.
func moveSuccessorInstallation(md *masterdata.MasterData, m masterdata.Meter) {
	if m.RemovedAt == nil {
		return
	}
	best := -1
	for i := range md.Meters {
		other := md.Meters[i]
		if other.MeterPointID != m.MeterPointID || other.Number == m.Number {
			continue
		}
		if other.InstalledAt.Before(m.InstalledAt) {
			continue // a predecessor, not the successor
		}
		if best < 0 || md.Meters[i].InstalledAt.Before(md.Meters[best].InstalledAt) {
			best = i
		}
	}
	if best >= 0 {
		md.Meters[best].InstalledAt = *m.RemovedAt
	}
}

func (a *App) handleMeterDelete(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "meter deleted", func(md *masterdata.MasterData) string {
		number := strings.TrimSpace(r.PostFormValue("number"))
		out := md.Meters[:0]
		for _, m := range md.Meters {
			if m.Number != number {
				out = append(out, m)
			}
		}
		md.Meters = out
		return ""
	})
}

// handleMeterSwap is the single-device counterpart of the swap list: it
// closes off the device at one meter point and installs its replacement
// on the same day. Both halves happen together or not at all, which is
// what keeps the chain contiguous — an end reading without a successor,
// or a successor without an end reading, cannot be produced here.
func (a *App) handleMeterSwap(w http.ResponseWriter, r *http.Request) {
	a.structureAction(w, r, "meter swapped", func(md *masterdata.MasterData) string {
		placeID := strings.TrimSpace(r.PostFormValue("meter_point_id"))
		day, err := parseGridDay(strings.TrimSpace(r.PostFormValue("day")))
		if err != nil {
			return "Ungültiger Wechseltag (TT.MM.JJJJ erwartet)."
		}
		// Deliberately not "end_reading": that name belongs to the
		// incoming device's own removal fields, which meterFromForm reads
		// out of the same request. Two meanings under one name would have
		// the dialog refuse itself.
		kind := kindOfPlace(md.MeterPoints, placeID)
		endText := strings.TrimSpace(r.PostFormValue("outgoing_end_reading"))
		if endText == "" {
			return "Ohne den Endstand des alten Geräts lässt sich der Verbrauch über den Wechsel nicht berechnen."
		}
		end, msg := parseReadingValue(kind, "Endstand", endText)
		if msg != "" {
			return msg
		}

		outgoing := -1
		for i := range md.Meters {
			if md.Meters[i].MeterPointID == placeID && md.Meters[i].RemovedAt == nil {
				outgoing = i
			}
		}
		if outgoing < 0 {
			return fmt.Sprintf("Am Zählerplatz %s hängt gerade kein Zähler.", placeID)
		}
		if day.Before(md.Meters[outgoing].InstalledAt) {
			return fmt.Sprintf("Der Wechseltag liegt vor dem Einbau von %s.", md.Meters[outgoing].Number)
		}

		incoming, msg := meterFromForm(r, md, "", kind)
		if msg != "" {
			return msg
		}
		incoming.MeterPointID = placeID
		incoming.InstalledAt = day

		md.Meters[outgoing].RemovedAt = &day
		md.Meters[outgoing].EndReading = &end
		md.Meters = append(md.Meters, incoming)
		return ""
	})
}
