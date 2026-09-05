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
// decided by master data, not by this field (this field does not
// reliably distinguish e.g. hot from cold water in practice). Abbr is a
// short display code (not itself part of the standard) — kept identical
// to the evaluator's own table (see its internal/telegram/identify.go) so
// the same telegram reads the same way in both tools' logs.
type DeviceType struct {
	Code byte
	Abbr string
}

var deviceTypes = map[byte]string{
	0x00: "OTH",
	0x01: "OIL",
	0x02: "ELC",
	0x03: "GAS",
	0x04: "WMZ",
	0x05: "STM",
	0x06: "WWZ",
	0x07: "KWZ",
	0x08: "HKV",
	0x09: "AIR",
	0x0A: "CLR",
	0x0B: "CLF",
	0x0C: "HTF",
	0x0D: "HCX",
	0x0E: "BUS",
	0x14: "CAL",
	0x15: "BHW",
	0x16: "CLD",
	0x17: "DUW",
	0x18: "PRS",
	0x19: "ADC",
	0x1A: "SMK",
	0x1B: "RMS",
	0x1C: "GDT",
	0x20: "CBR",
	0x21: "VAL",
	0x25: "DSP",
	0x28: "SEW",
	0x29: "WST",
	0x2A: "CO2",
	0x31: "COM",
	0x32: "UNI",
	0x33: "BIR",
	0x36: "RCS",
	0x37: "RCM",
	0x38: "WIR",
	// Non-standard values observed on real devices rather than documented
	// in EN 13757-3 (vendors reusing or repurposing a medium byte).
	0x62: "WW?",
	0x72: "KW?",
	0x80: "HKV", // vendor variant of 0x08, seen on real Techem devices
	0xF0: "SM?",
}

// IdentifyDeviceType looks up the medium field. The second return value is
// false for values outside the known subset; callers must treat that as
// "unclassified", not as an error.
func IdentifyDeviceType(medium byte) (DeviceType, bool) {
	abbr, ok := deviceTypes[medium]
	if !ok {
		return DeviceType{}, false
	}
	return DeviceType{Code: medium, Abbr: abbr}, true
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

// String renders signal quality as a star rating (weakest to strongest:
// "*" .. "****") — compact enough that the console receive-log line it
// appears in stays on one line.
func (q SignalQuality) String() string {
	switch q {
	case SignalExcellent:
		return "****"
	case SignalGood:
		return "***"
	case SignalAdequate:
		return "**"
	case SignalWeak:
		return "*"
	default:
		return "?"
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
