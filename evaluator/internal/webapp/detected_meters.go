package webapp

import (
	"encoding/hex"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// detectedMeterWindowDays is how far back a meter counts as "detected"
// for the suggestions on the master-data page. Long enough that a device
// mounted before the operator got round to entering it is still offered,
// short enough that a meter taken out of service last year stops
// cluttering the list — it is a convenience, not a record, and anything
// missing from it can still be typed.
const detectedMeterWindowDays = 90

// detectedMeter is one meter the receiver has picked up, offered when a
// meter point is given its device. Kind is what the telegram's medium
// field claims, resolved onto a meter point kind — a claim by the device
// about itself, good enough to sort the likely candidates to the top and
// not good enough to refuse anything (see kindFromMedium).
type detectedMeter struct {
	Number   string
	Kind     masterdata.Kind
	KindOK   bool
	TypeName string
	LastSeen string
}

// detectedMeters lists the meters heard from within the window, newest
// day formatted for display, leaving out those the master data already
// places somewhere.
//
// Errors are swallowed on purpose. The suggestions are an aid; a broken
// or missing archive must not stop the operator from entering master data
// by hand, which an empty list still allows.
func detectedMeters(store *archive.Store, md masterdata.MasterData, today telegram.Day) []detectedMeter {
	if store == nil {
		return nil
	}
	seen, err := store.MetersSeenSince(today.AddDays(-detectedMeterWindowDays))
	if err != nil {
		return nil
	}

	// Every number the master data already knows is taken, an
	// already-removed device included: a number may sit at one meter
	// point only (Validate refuses it twice over), so offering a device
	// that was once installed and taken out again would lead straight
	// into a refusal.
	known := make(map[string]bool, len(md.Meters))
	for _, m := range md.Meters {
		known[m.Number] = true
	}

	out := make([]detectedMeter, 0, len(seen))
	for _, s := range seen {
		if known[s.ID] {
			continue
		}
		d := detectedMeter{Number: s.ID, LastSeen: formatGridDay(s.LastDay)}
		if raw, err := hex.DecodeString(s.RawHex); err == nil && len(raw) > 9 {
			// A meter this page lists is by definition not in the master
			// data, so there is no key to pass — deviceMedium then falls
			// back to the link-layer medium for an encrypted telegram,
			// which is all this page ever had.
			medium := deviceMedium(raw, [16]byte{})
			if dt, ok := telegram.IdentifyDeviceType(medium); ok {
				d.TypeName = dt.Name
			}
			d.Kind, d.KindOK = kindFromMedium(medium)
		}
		out = append(out, d)
	}
	return out
}

// kindFromMedium maps a telegram's medium field onto the meter point kind
// it most likely belongs to. Only the values that say something useful
// are listed; everything else reports false, and the page then makes no
// claim about that device rather than a wrong one.
//
// This is an entry aid and never a check: the operator may put any device
// at any meter point. The medium field is a manufacturer's statement
// about a device, not a fact about the installation, and it is not
// dependable enough to refuse anything — telegram.DeviceType's own
// documentation notes it does not reliably tell hot water from cold, and
// vendors reuse values.
func kindFromMedium(medium byte) (masterdata.Kind, bool) {
	switch medium {
	case 0x03:
		return masterdata.KindGas, true
	case 0x04:
		return masterdata.KindHeatMeter, true
	case 0x06, 0x62: // warm water, and a non-standard variant of it
		return masterdata.KindHotWater, true
	case 0x07, 0x72: // cold water, and a non-standard variant of it
		return masterdata.KindColdWater, true
	case 0x08, 0x80: // heat cost allocator, and a manufacturer variant
		return masterdata.KindHeatCostAllocator, true
	default:
		return 0, false
	}
}
