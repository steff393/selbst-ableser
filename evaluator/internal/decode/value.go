package decode

import "fmt"

// Unit is the physical (or dimensionless) unit a decoded reading is
// expressed in.
type Unit int

const (
	UnitUnknown Unit = iota
	// UnitHeatCostAllocator is the dimensionless display unit of a heat
	// cost allocator, proportional to heat emitted but not itself an
	// energy value.
	UnitHeatCostAllocator
	// UnitLiters is a water volume in liters.
	UnitLiters
	// UnitKWh is thermal energy in whole kilowatt hours, as a heat meter
	// counts it — already the figure a consumption report shows, with no
	// conversion factor in between.
	UnitKWh
)

func (u Unit) String() string {
	switch u {
	case UnitHeatCostAllocator:
		return "HCA units"
	case UnitLiters:
		return "l"
	case UnitKWh:
		return "kWh"
	default:
		return "unknown"
	}
}

// Reading is one decoded value with its unit.
type Reading struct {
	Value int64
	Unit  Unit
}

// Date is a calendar date decoded from a telegram (type G: day, month, and
// a two-digit year).
type Date struct {
	Day, Month, Year int
}

const (
	vifHeatCostAllocator = 0x6E
	vifVolumeLiters      = 0x13
	vifDateTypeG         = 0x6C

	// vifEnhancedIdentification introduces a second, complete device
	// identification carried inside the application layer — see
	// EnhancedMedium.
	vifEnhancedIdentification = 0x79
)

// EnhancedMedium returns the medium byte from a telegram's enhanced
// identification record, if it carries one.
//
// A telegram's link-layer header describes the device that *transmits*.
// When that is not the meter itself — a radio converter reading meters
// over a wired bus and sending on their behalf — the transmitter's own
// medium byte says nothing about what is being measured, and EN 13757-3's
// enhanced identification record exists for exactly that case: eight
// bytes laid out like the link-layer header (4-byte BCD ID, 2-byte
// manufacturer, version, medium), naming the metering device rather than
// the radio.
//
// Qundis heat meters use it even where both halves are one physical
// device: the link layer says 0x37 (radio converter), the record says
// 0x04 (heat). The record is the half that answers "what does this
// measure", so it wins where present.
//
// The record sits in the application layer, so it is only readable once
// the payload is in cleartext — an encrypted telegram without its key
// simply has no answer here, and the caller keeps the link-layer one.
func EnhancedMedium(records []Record) (byte, bool) {
	for _, r := range records {
		if r.VIF != vifEnhancedIdentification || len(r.VIFE) > 0 {
			continue
		}
		if len(r.Data) != 8 {
			continue // not the ID/manufacturer/version/medium layout
		}
		return r.Data[7], true
	}
	return 0, false
}

// storageCurrent and storageBillingReset are the two storage numbers this
// system distinguishes: the live value, and the value frozen at the last
// billing-period reset. Devices in the supported meter families use only
// these two (no DIFE-extended storage numbers), encoded in DIF bit 6.
const (
	storageCurrent      = 0
	storageBillingReset = 1
)

func storageNumber(dif byte) int {
	return int((dif >> 6) & 0x01)
}

func findRecord(records []Record, storage int, vif byte) (Record, bool) {
	for _, r := range records {
		if len(r.DIFE) > 0 {
			continue // extended storage numbers are not used by the supported meter families
		}
		if storageNumber(r.DIF) == storage && r.VIF == vif {
			return r, true
		}
	}
	return Record{}, false
}

// Encoding is how the raw bytes of a value represent it.
type Encoding int

const (
	EncodingUnknown Encoding = iota
	// EncodingBCD is packed BCD, least significant byte first.
	EncodingBCD
	// EncodingBinary is an unsigned little-endian integer.
	EncodingBinary
)

func (e Encoding) String() string {
	switch e {
	case EncodingBCD:
		return "BCD"
	case EncodingBinary:
		return "binary"
	default:
		return "unknown"
	}
}

// recordEncoding returns how a record's value is encoded, as its DIF's
// data field (the low nibble, EN 13757-3) declares it. ok is false for the
// codes that are neither an integer nor BCD: no data, 32-bit real,
// selection for readout, variable length, special functions.
//
// The encoding is a property of the record, not of its unit: EN 13757-3
// lets any VIF be sent either way, so the same heat-cost-allocator VIF
// 0x6E can arrive as 0x0B 0x6E (6-digit BCD) from one device and as
// 0x02 0x6E (16-bit integer) from another. Reading one as the other gives
// a plausible-looking but wrong value: 0x67 0x02 is 267 as BCD and 615 as
// binary.
func recordEncoding(dif byte) (enc Encoding, ok bool) {
	switch dif & 0x0F {
	case 0x1, 0x2, 0x3, 0x4, 0x6, 0x7:
		return EncodingBinary, true
	case 0x9, 0xA, 0xB, 0xC, 0xE:
		return EncodingBCD, true
	default:
		return EncodingUnknown, false
	}
}

// Field is where a decoded value sits in the payload and how it is
// encoded there. Data aliases the payload the records were walked from,
// so internal/correction can overwrite the value in place (Overwrite)
// without rediscovering its offset or guessing its encoding.
type Field struct {
	Data     []byte
	Encoding Encoding
}

// Overwrite replaces the value in f with value, in f's own encoding and
// byte width, so the telegram's overall length never changes.
func (f Field) Overwrite(value int64) error {
	if value < 0 {
		return fmt.Errorf("decode: value must not be negative")
	}
	var encoded []byte
	var err error
	switch f.Encoding {
	case EncodingBCD:
		encoded, err = encodeBCD(uint64(value), len(f.Data))
	case EncodingBinary:
		encoded, err = encodeUintLE(uint64(value), len(f.Data))
	default:
		return fmt.Errorf("decode: cannot write a value in encoding %v", f.Encoding)
	}
	if err != nil {
		return err
	}
	copy(f.Data, encoded)
	return nil
}

// readingFromRecord decodes r's value in the encoding its DIF declares.
// ok is false for a record whose value cannot be read: an unknown VIF, an
// encoding that is neither integer nor BCD, or BCD with a nibble above 9
// (EN 13757-3 uses those for a sign or an error marker, never for a
// meter reading). Such a telegram is not evaluable, which is not an
// error: an error would stop the whole month's figure over one telegram,
// where not-found lets the search move on to the next day (see
// billing.FindReading, and decodeQundisHeatRecord for the same decision).
func readingFromRecord(r Record) (Reading, Field, bool) {
	var unit Unit
	switch r.VIF {
	case vifHeatCostAllocator:
		unit = UnitHeatCostAllocator
	case vifVolumeLiters:
		unit = UnitLiters
	default:
		return Reading{}, Field{}, false
	}
	enc, ok := recordEncoding(r.DIF)
	if !ok {
		return Reading{}, Field{}, false
	}
	var value uint64
	switch enc {
	case EncodingBCD:
		if !validBCD(r.Data) {
			return Reading{}, Field{}, false
		}
		value = decodeBCD(r.Data)
	case EncodingBinary:
		value = decodeUintLE(r.Data)
	}
	return Reading{Value: int64(value), Unit: unit}, Field{Data: r.Data, Encoding: enc}, true
}

// CurrentValue returns the live meter reading (heat-cost-allocator units or
// water volume in liters), if the payload contains one of the value types
// this system knows how to decode.
func CurrentValue(records []Record) (Reading, bool, error) {
	reading, _, found, err := valueRecordAtStorage(records, storageCurrent)
	return reading, found, err
}

// CurrentValueField is CurrentValue's counterpart for internal/correction:
// alongside the decoded reading, it returns the Field the value was read
// from, so a caller can overwrite it in place to build a corrected
// telegram.
func CurrentValueField(records []Record) (Reading, Field, bool, error) {
	return valueRecordAtStorage(records, storageCurrent)
}

// BillingResetValue returns the meter reading frozen at the last billing
// period reset (the value HCA devices hold from the prior 31 December,
// see docs/architektur.md), if present.
func BillingResetValue(records []Record) (Reading, bool, error) {
	reading, _, found, err := valueRecordAtStorage(records, storageBillingReset)
	return reading, found, err
}

// valueRecordAtStorage never returns an error today: a value record it
// cannot read is reported as not found (see readingFromRecord). The error
// stays in the signatures above so that a future failure mode does not
// have to change every caller.
func valueRecordAtStorage(records []Record, storage int) (Reading, Field, bool, error) {
	for _, vif := range []byte{vifHeatCostAllocator, vifVolumeLiters} {
		if r, ok := findRecord(records, storage, vif); ok {
			reading, field, ok := readingFromRecord(r)
			return reading, field, ok, nil
		}
	}
	return Reading{}, Field{}, false, nil
}

// encodeBCD is decodeBCD's inverse: packs v into length bytes of packed
// BCD, least significant byte first.
func encodeBCD(v uint64, length int) ([]byte, error) {
	max := uint64(1)
	for i := 0; i < length; i++ {
		max *= 100
	}
	if v >= max {
		return nil, fmt.Errorf("decode: value %d does not fit in %d BCD bytes", v, length)
	}
	out := make([]byte, length)
	for i := range out {
		pair := v % 100
		v /= 100
		out[i] = byte((pair/10)<<4 | pair%10)
	}
	return out, nil
}

// encodeUintLE is decodeUintLE's inverse: packs v into length
// little-endian bytes.
func encodeUintLE(v uint64, length int) ([]byte, error) {
	if v>>(8*uint(length)) != 0 {
		return nil, fmt.Errorf("decode: value %d does not fit in %d bytes", v, length)
	}
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(v >> (8 * uint(i)))
	}
	return out, nil
}

// BillingResetDate returns the calendar date of the last billing-period
// reset, if the payload contains one.
func BillingResetDate(records []Record) (Date, bool, error) {
	r, ok := findRecord(records, storageBillingReset, vifDateTypeG)
	if !ok {
		return Date{}, false, nil
	}
	d, err := decodeDateG(r.Data)
	return d, true, err
}

// decodeBCD reads a packed-BCD value with the least significant byte
// first: each byte holds two decimal digits (high nibble, low nibble), and
// successive bytes contribute increasingly significant digit pairs.
func decodeBCD(data []byte) uint64 {
	var v, mul uint64 = 0, 1
	for _, b := range data {
		hi, lo := uint64(b>>4), uint64(b&0x0F)
		v += (hi*10 + lo) * mul
		mul *= 100
	}
	return v
}

// decodeUintLE reads a little-endian unsigned binary value.
func decodeUintLE(data []byte) uint64 {
	var v uint64
	for i, b := range data {
		v |= uint64(b) << (8 * uint(i))
	}
	return v
}

// decodeDateG decodes a type G (date-only) value: 2 bytes, day and month in
// the low bits of each byte, a two-digit year split across the high bits of
// both.
func decodeDateG(data []byte) (Date, error) {
	if len(data) != 2 {
		return Date{}, fmt.Errorf("decode: type G date needs 2 bytes, got %d", len(data))
	}
	d0, d1 := data[0], data[1]
	day := int(d0 & 0x1F)
	month := int(d1 & 0x0F)
	year := int((d0&0xE0)>>5) | int((d1&0xF0)>>1)
	return Date{Day: day, Month: month, Year: 2000 + year}, nil
}
