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
)

func (u Unit) String() string {
	switch u {
	case UnitHeatCostAllocator:
		return "HCA units"
	case UnitLiters:
		return "l"
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
)

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

func readingFromRecord(r Record) (Reading, error) {
	switch r.VIF {
	case vifHeatCostAllocator:
		return Reading{Value: int64(decodeBCD(r.Data)), Unit: UnitHeatCostAllocator}, nil
	case vifVolumeLiters:
		return Reading{Value: int64(decodeUintLE(r.Data)), Unit: UnitLiters}, nil
	default:
		return Reading{}, fmt.Errorf("decode: no known unit for VIF 0x%02X", r.VIF)
	}
}

// CurrentValue returns the live meter reading (heat-cost-allocator units or
// water volume in liters), if the payload contains one of the value types
// this system knows how to decode.
func CurrentValue(records []Record) (Reading, bool, error) {
	reading, _, found, err := valueRecordAtStorage(records, storageCurrent)
	return reading, found, err
}

// CurrentValueRecord is CurrentValue's counterpart for internal/correction:
// alongside the decoded reading, it returns the underlying Record itself,
// whose Data aliases the payload passed to Standard — so a caller can
// overwrite it in place to build a corrected telegram without needing to
// rediscover the record's byte offset.
func CurrentValueRecord(records []Record) (Reading, Record, bool, error) {
	return valueRecordAtStorage(records, storageCurrent)
}

// BillingResetValue returns the meter reading frozen at the last billing
// period reset (the value HCA devices hold from the prior 31 December,
// see docs/architektur.md), if present.
func BillingResetValue(records []Record) (Reading, bool, error) {
	reading, _, found, err := valueRecordAtStorage(records, storageBillingReset)
	return reading, found, err
}

func valueRecordAtStorage(records []Record, storage int) (Reading, Record, bool, error) {
	for _, vif := range []byte{vifHeatCostAllocator, vifVolumeLiters} {
		if r, ok := findRecord(records, storage, vif); ok {
			reading, err := readingFromRecord(r)
			return reading, r, true, err
		}
	}
	return Reading{}, Record{}, false, nil
}

// EncodeCurrentValue is readingFromRecord's inverse: it encodes value into
// length raw bytes matching unit's own encoding (packed BCD for a
// heat-cost-allocator reading, little-endian binary for a volume in
// liters). Used by internal/correction to overwrite a Record's Data in
// place — length must match the original record's byte width, so the
// telegram's overall length never changes.
func EncodeCurrentValue(unit Unit, value int64, length int) ([]byte, error) {
	if value < 0 {
		return nil, fmt.Errorf("decode: value must not be negative")
	}
	switch unit {
	case UnitHeatCostAllocator:
		return encodeBCD(uint64(value), length)
	case UnitLiters:
		return encodeUintLE(uint64(value), length)
	default:
		return nil, fmt.Errorf("decode: no known encoding for unit %v", unit)
	}
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
