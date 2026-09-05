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

func TestCurrentValueRecordAliasesInputData(t *testing.T) {
	records := []Record{
		{DIF: 0x0B, VIF: vifHeatCostAllocator, Data: []byte{0x00, 0x04, 0x20}},
	}
	reading, record, found, err := CurrentValueRecord(records)
	if err != nil || !found {
		t.Fatalf("CurrentValueRecord: found=%v err=%v", found, err)
	}
	if reading.Value != 200400 {
		t.Errorf("reading.Value = %d, want 200400", reading.Value)
	}
	record.Data[0] = 0x99
	if records[0].Data[0] != 0x99 {
		t.Error("record.Data should alias the original records slice's backing array")
	}
}

func TestEncodeCurrentValueRoundTripsThroughDecode(t *testing.T) {
	encoded, err := EncodeCurrentValue(UnitHeatCostAllocator, 200400, 3)
	if err != nil {
		t.Fatalf("EncodeCurrentValue: %v", err)
	}
	if got := decodeBCD(encoded); got != 200400 {
		t.Errorf("decodeBCD(encoded) = %d, want 200400", got)
	}

	encoded, err = EncodeCurrentValue(UnitLiters, 33009, 4)
	if err != nil {
		t.Fatalf("EncodeCurrentValue: %v", err)
	}
	if got := decodeUintLE(encoded); got != 33009 {
		t.Errorf("decodeUintLE(encoded) = %d, want 33009", got)
	}
}

func TestEncodeCurrentValueRejectsOutOfRange(t *testing.T) {
	if _, err := EncodeCurrentValue(UnitHeatCostAllocator, 1000000, 3); err == nil {
		t.Error("expected an error for a value that does not fit in 3 BCD bytes")
	}
	if _, err := EncodeCurrentValue(UnitLiters, 1<<32, 4); err == nil {
		t.Error("expected an error for a value that does not fit in 4 little-endian bytes")
	}
	if _, err := EncodeCurrentValue(UnitHeatCostAllocator, -1, 3); err == nil {
		t.Error("expected an error for a negative value")
	}
	if _, err := EncodeCurrentValue(UnitUnknown, 1, 3); err == nil {
		t.Error("expected an error for an unknown unit")
	}
}
