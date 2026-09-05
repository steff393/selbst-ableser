package telegram

// Manufacturer decodes the three-letter manufacturer code from the M-field
// (as transmitted, little-endian): three 5-bit letters, offset by 'A'-1.
func Manufacturer(m uint16) string {
	letter := func(shift uint) byte {
		return byte((m>>shift)&0x1F) + 64
	}
	return string([]byte{letter(10), letter(5), letter(0)})
}

// DeviceType is a coarse classification of the metered medium, taken from
// EN 13757-3's medium field. It is a diagnostic value only: which of
// heating, hot water, or cold water a meter point actually measures is
// decided by master data, not by this field (this field does not reliably
// distinguish e.g. hot from cold water in practice). Abbr is a short
// display code (not itself part of the standard) for compact display; Name
// is the German term.
type DeviceType struct {
	Code byte
	Abbr string
	Name string
}

var deviceTypes = map[byte]struct{ Abbr, Name string }{
	0x00: {"OTH", "Anderes"},
	0x01: {"OIL", "Öl"},
	0x02: {"ELC", "Elektrizität"},
	0x03: {"GAS", "Gas"},
	0x04: {"WMZ", "Wärme (Rücklauf)"},
	0x05: {"STM", "Dampf"},
	0x06: {"WWZ", "Warmwasserzähler"},
	0x07: {"KWZ", "Kaltwasserzähler"},
	0x08: {"HKV", "Heizkostenverteiler"},
	0x09: {"AIR", "Pressluft"},
	0x0A: {"CLR", "Kühlung (Rücklauf)"},
	0x0B: {"CLF", "Kühlung (Vorlauf)"},
	0x0C: {"HTF", "Wärme (Vorlauf)"},
	0x0D: {"HCX", "Wärme/Kühlung"},
	0x0E: {"BUS", "Bus / System"},
	0x14: {"CAL", "Heiz-/Brennwert"},
	0x15: {"BHW", "Heißwasser"},
	0x16: {"CLD", "Kaltwasser"},
	0x17: {"DUW", "Mischwasser"},
	0x18: {"PRS", "Druck"},
	0x19: {"ADC", "A/D Wandler"},
	0x1A: {"SMK", "Rauchmelder"},
	0x1B: {"RMS", "Raumsensor"},
	0x1C: {"GDT", "Gasdetektor"},
	0x20: {"CBR", "Unterbrecher"},
	0x21: {"VAL", "Ventil"},
	0x25: {"DSP", "Anzeige"},
	0x28: {"SEW", "Abwasser"},
	0x29: {"WST", "Abfall"},
	0x2A: {"CO2", "Kohlendioxid"},
	0x31: {"COM", "Kommunikationssteuergerät"},
	0x32: {"UNI", "Unidirektionaler Repeater"},
	0x33: {"BIR", "Bidirektionaler Repeater"},
	0x36: {"RCS", "Funkumsetzer (Systemseite)"},
	0x37: {"RCM", "Funkumsetzer (Zählerseite)"},
	0x38: {"WIR", "Drahtgebundener Adapter"},
	// Non-standard values observed on real devices rather than documented
	// in EN 13757-3 (vendors reusing or repurposing a medium byte).
	0x62: {"WW?", "Warmwasser (nicht genormt)"},
	0x72: {"KW?", "Kaltwasser (nicht genormt)"},
	0x80: {"HKV", "Heizkostenverteiler (Herstellervariante)"},
	0xF0: {"SM?", "Rauchmelder (nicht genormt)"},
}

// IdentifyDeviceType looks up the medium field. The second return value is
// false for values outside the known subset; callers must treat that as
// "unclassified", not as an error.
func IdentifyDeviceType(medium byte) (DeviceType, bool) {
	dt, ok := deviceTypes[medium]
	if !ok {
		return DeviceType{}, false
	}
	return DeviceType{Code: medium, Abbr: dt.Abbr, Name: dt.Name}, true
}

// SignalQuality is a coarse classification of received signal strength,
// used during commissioning and diagnostics.
type SignalQuality int

const (
	SignalUnknown SignalQuality = iota
	SignalWeak
	SignalAdequate
	SignalGood
	SignalExcellent
)

func (q SignalQuality) String() string {
	switch q {
	case SignalExcellent:
		return "excellent"
	case SignalGood:
		return "good"
	case SignalAdequate:
		return "adequate"
	case SignalWeak:
		return "weak"
	default:
		return "unknown"
	}
}

// ClassifyRSSI buckets a signal strength reading in dBm.
func ClassifyRSSI(dBm int) SignalQuality {
	switch {
	case dBm >= -50:
		return SignalExcellent
	case dBm >= -70:
		return SignalGood
	case dBm >= -85:
		return SignalAdequate
	default:
		return SignalWeak
	}
}
