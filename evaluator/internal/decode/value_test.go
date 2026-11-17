package decode

import "testing"

func TestDecodeBCD(t *testing.T) {
	got := decodeBCD([]byte{0x00, 0x04, 0x20})
	if got != 200400 {
		t.Errorf("decodeBCD = %d, want 200400", got)
	}
}

func TestDecodeUintLE(t *testing.T) {
	got := decodeUintLE([]byte{0xF1, 0x80, 0x00, 0x00})
	if got != 33009 {
		t.Errorf("decodeUintLE = %d, want 33009", got)
	}
}

func TestDecodeDateG(t *testing.T) {
	d, err := decodeDateG([]byte{0x3F, 0x3C})
	if err != nil {
		t.Fatalf("decodeDateG: %v", err)
	}
	want := Date{Day: 31, Month: 12, Year: 2025}
	if d != want {
		t.Errorf("decodeDateG = %+v, want %+v", d, want)
	}
}

func TestCurrentValueAndBillingReset(t *testing.T) {
	records := []Record{
		{DIF: 0x0B, VIF: vifHeatCostAllocator, Data: []byte{0x00, 0x04, 0x20}}, // current, storage 0
		{DIF: 0x4B, VIF: vifHeatCostAllocator, Data: []byte{0x00, 0x00, 0x00}}, // billing reset, storage 1
		{DIF: 0x42, VIF: vifDateTypeG, Data: []byte{0x3F, 0x3C}},               // billing reset date
	}

	cur, ok, err := CurrentValue(records)
	if err != nil || !ok {
		t.Fatalf("CurrentValue: ok=%v err=%v", ok, err)
	}
	if cur.Value != 200400 || cur.Unit != UnitHeatCostAllocator {
		t.Errorf("CurrentValue = %+v, want {200400 HCA}", cur)
	}

	reset, ok, err := BillingResetValue(records)
	if err != nil || !ok {
		t.Fatalf("BillingResetValue: ok=%v err=%v", ok, err)
	}
	if reset.Value != 0 {
		t.Errorf("BillingResetValue = %+v, want 0", reset)
	}

	date, ok, err := BillingResetDate(records)
	if err != nil || !ok {
		t.Fatalf("BillingResetDate: ok=%v err=%v", ok, err)
	}
	if date != (Date{Day: 31, Month: 12, Year: 2025}) {
		t.Errorf("BillingResetDate = %+v", date)
	}
}

func TestCurrentValueWater(t *testing.T) {
	records := []Record{
		{DIF: 0x04, VIF: vifVolumeLiters, Data: []byte{0xF1, 0x80, 0x00, 0x00}},
	}
	got, ok, err := CurrentValue(records)
	if err != nil || !ok {
		t.Fatalf("CurrentValue: ok=%v err=%v", ok, err)
	}
	if got.Value != 33009 || got.Unit != UnitLiters {
		t.Errorf("CurrentValue = %+v, want {33009 Liters}", got)
	}
}

func TestCurrentValueAbsent(t *testing.T) {
	_, ok, err := CurrentValue(nil)
	if err != nil || ok {
		t.Errorf("CurrentValue(nil) = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// The DIF's data field decides the encoding, not the VIF: the same two
// bytes 0x67 0x02 are 267 as BCD (DIF 0x0A) and 615 as a 16-bit integer
// (DIF 0x02), for heat cost allocators and water alike.
func TestCurrentValueEncodingFollowsDIF(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
		want Reading
		enc  Encoding
	}{
		{"HCA, 4-digit BCD", Record{DIF: 0x0A, VIF: vifHeatCostAllocator, Data: []byte{0x67, 0x02}},
			Reading{Value: 267, Unit: UnitHeatCostAllocator}, EncodingBCD},
		{"HCA, 16-bit integer", Record{DIF: 0x02, VIF: vifHeatCostAllocator, Data: []byte{0x67, 0x02}},
			Reading{Value: 615, Unit: UnitHeatCostAllocator}, EncodingBinary},
		{"HCA, 24-bit integer", Record{DIF: 0x03, VIF: vifHeatCostAllocator, Data: []byte{0x40, 0x0E, 0x03}},
			Reading{Value: 200256, Unit: UnitHeatCostAllocator}, EncodingBinary},
		{"water, 8-digit BCD", Record{DIF: 0x0C, VIF: vifVolumeLiters, Data: []byte{0x78, 0x56, 0x34, 0x12}},
			Reading{Value: 12345678, Unit: UnitLiters}, EncodingBCD},
		{"water, 32-bit integer", Record{DIF: 0x04, VIF: vifVolumeLiters, Data: []byte{0xF1, 0x80, 0x00, 0x00}},
			Reading{Value: 33009, Unit: UnitLiters}, EncodingBinary},
		{"water, 12-digit BCD", Record{DIF: 0x0E, VIF: vifVolumeLiters, Data: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x10}},
			Reading{Value: 100000000001, Unit: UnitLiters}, EncodingBCD},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, field, found, err := CurrentValueField([]Record{c.rec})
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if got != c.want {
				t.Errorf("reading = %+v, want %+v", got, c.want)
			}
			if field.Encoding != c.enc {
				t.Errorf("encoding = %v, want %v", field.Encoding, c.enc)
			}
		})
	}
}

// A value record that cannot be read is not evaluable, not an error: an
// error would stop the whole month's figure over one telegram.
func TestCurrentValueUnreadableIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
	}{
		{"32-bit real", Record{DIF: 0x05, VIF: vifHeatCostAllocator, Data: []byte{0x00, 0x00, 0x80, 0x3F}}},
		{"no data", Record{DIF: 0x00, VIF: vifHeatCostAllocator}},
		{"variable length", Record{DIF: 0x0D, VIF: vifVolumeLiters, Data: []byte{0x01, 0x02}}},
		{"BCD with a nibble above 9", Record{DIF: 0x0B, VIF: vifHeatCostAllocator, Data: []byte{0xFF, 0xFF, 0xFF}}},
		{"BCD with a sign nibble", Record{DIF: 0x0A, VIF: vifHeatCostAllocator, Data: []byte{0x12, 0xF0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found, err := CurrentValue([]Record{c.rec})
			if err != nil || found {
				t.Errorf("got %+v found=%v err=%v, want not found and no error", got, found, err)
			}
		})
	}
}

func TestBillingResetValueEncodingFollowsDIF(t *testing.T) {
	records := []Record{
		{DIF: 0x02, VIF: vifHeatCostAllocator, Data: []byte{0x2C, 0x01}}, // current, 300
		{DIF: 0x42, VIF: vifHeatCostAllocator, Data: []byte{0xE8, 0x03}}, // billing reset, 1000
	}
	reset, found, err := BillingResetValue(records)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if reset.Value != 1000 {
		t.Errorf("BillingResetValue = %d, want 1000", reset.Value)
	}
}

func TestCurrentValueFieldAliasesInputData(t *testing.T) {
	records := []Record{
		{DIF: 0x0B, VIF: vifHeatCostAllocator, Data: []byte{0x00, 0x04, 0x20}},
	}
	reading, field, found, err := CurrentValueField(records)
	if err != nil || !found {
		t.Fatalf("CurrentValueField: found=%v err=%v", found, err)
	}
	if reading.Value != 200400 {
		t.Errorf("reading.Value = %d, want 200400", reading.Value)
	}
	field.Data[0] = 0x99
	if records[0].Data[0] != 0x99 {
		t.Error("field.Data should alias the original records slice's backing array")
	}
}

// Overwrite writes in the field's own encoding, so a corrected value reads
// back through the same record unchanged in shape.
func TestFieldOverwriteRoundTripsThroughDecode(t *testing.T) {
	cases := []struct {
		name  string
		rec   Record
		value int64
	}{
		{"HCA BCD", Record{DIF: 0x0B, VIF: vifHeatCostAllocator, Data: make([]byte, 3)}, 200400},
		{"HCA binary", Record{DIF: 0x02, VIF: vifHeatCostAllocator, Data: make([]byte, 2)}, 615},
		{"water BCD", Record{DIF: 0x0C, VIF: vifVolumeLiters, Data: make([]byte, 4)}, 12345678},
		{"water binary", Record{DIF: 0x04, VIF: vifVolumeLiters, Data: make([]byte, 4)}, 33009},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records := []Record{c.rec}
			_, field, found, _ := CurrentValueField(records)
			if !found {
				t.Fatal("no field for an all-zero record")
			}
			if err := field.Overwrite(c.value); err != nil {
				t.Fatalf("Overwrite: %v", err)
			}
			got, found, err := CurrentValue(records)
			if err != nil || !found || got.Value != c.value {
				t.Errorf("after Overwrite: %+v found=%v err=%v, want %d", got, found, err, c.value)
			}
		})
	}
}

func TestFieldOverwriteRejectsOutOfRange(t *testing.T) {
	if err := (Field{Data: make([]byte, 3), Encoding: EncodingBCD}).Overwrite(1000000); err == nil {
		t.Error("expected an error for a value that does not fit in 3 BCD bytes")
	}
	if err := (Field{Data: make([]byte, 2), Encoding: EncodingBinary}).Overwrite(1 << 16); err == nil {
		t.Error("expected an error for a value that does not fit in 2 little-endian bytes")
	}
	if err := (Field{Data: make([]byte, 3), Encoding: EncodingBCD}).Overwrite(-1); err == nil {
		t.Error("expected an error for a negative value")
	}
	if err := (Field{Data: make([]byte, 3)}).Overwrite(1); err == nil {
		t.Error("expected an error for an unknown encoding")
	}
	data := []byte{0x11, 0x22}
	_ = (Field{Data: data, Encoding: EncodingBinary}).Overwrite(1 << 16)
	if data[0] != 0x11 || data[1] != 0x22 {
		t.Errorf("a rejected Overwrite changed the field: % X", data)
	}
}
