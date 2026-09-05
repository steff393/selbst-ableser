package decode

import "testing"

func TestWalkRecordsSkipsFiller(t *testing.T) {
	// filler, then one fixed 1-byte record (DIF 0x01: 8-bit binary, VIF
	// 0x13: liters) with value 0x2A.
	payload := []byte{0x2F, 0x2F, 0x01, 0x13, 0x2A}
	records, err := walkRecords(payload)
	if err != nil {
		t.Fatalf("walkRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	r := records[0]
	if r.DIF != 0x01 || r.VIF != 0x13 || len(r.Data) != 1 || r.Data[0] != 0x2A {
		t.Errorf("unexpected record: %+v", r)
	}
}

func TestWalkRecordsMultipleRecords(t *testing.T) {
	payload := []byte{
		0x0B, 0x6E, 0x00, 0x04, 0x20, // 6-digit BCD, HCA units
		0x04, 0x13, 0xF1, 0x80, 0x00, 0x00, // 32-bit binary, liters
	}
	records, err := walkRecords(payload)
	if err != nil {
		t.Fatalf("walkRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].DIF != 0x0B || records[0].VIF != 0x6E {
		t.Errorf("record 0: unexpected identifier DIF=0x%02X VIF=0x%02X", records[0].DIF, records[0].VIF)
	}
	if records[1].DIF != 0x04 || records[1].VIF != 0x13 {
		t.Errorf("record 1: unexpected identifier DIF=0x%02X VIF=0x%02X", records[1].DIF, records[1].VIF)
	}
}

func TestWalkRecordsShortRecord(t *testing.T) {
	// DIF announces a 4-byte value, but only 2 bytes follow.
	payload := []byte{0x04, 0x13, 0x01, 0x02}
	if _, err := walkRecords(payload); err == nil {
		t.Fatal("expected an error for a truncated record")
	}
}

func TestWalkRecordsVariableLengthUnsupported(t *testing.T) {
	payload := []byte{0x0D, 0x13, 0x02, 0xAA, 0xBB}
	if _, err := walkRecords(payload); err == nil {
		t.Fatal("expected an error for a variable-length record")
	}
}
