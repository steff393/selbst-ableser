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

// A variable-length record is walked over rather than aborted on, so the
// records after it stay reachable — the whole point of reading its length
// instead of giving up (see walkRecords).
func TestWalkRecordsStepsOverVariableLength(t *testing.T) {
	payload := []byte{
		0x0D, 0x13, 0x02, 0xAA, 0xBB, // variable length, 2 bytes of value
		0x04, 0x13, 0x01, 0x02, 0x03, 0x04, // an ordinary record behind it
	}
	records, err := walkRecords(payload)
	if err != nil {
		t.Fatalf("walkRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].DIF != 0x0D || string(records[0].Data) != string([]byte{0xAA, 0xBB}) {
		t.Errorf("record 0 = %+v, want the 2-byte variable-length value", records[0])
	}
	if records[1].DIF != 0x04 || records[1].VIF != 0x13 {
		t.Errorf("record 1: unexpected identifier DIF=0x%02X VIF=0x%02X", records[1].DIF, records[1].VIF)
	}
}

// The reserved LVAR codes define no length at all, so where the record
// ends is genuinely unknowable and stopping is the only honest answer.
func TestWalkRecordsStopsAtReservedVariableLength(t *testing.T) {
	payload := []byte{0x0D, 0x13, 0xFF, 0xAA, 0xBB}
	if _, err := walkRecords(payload); err == nil {
		t.Fatal("expected an error for a reserved LVAR length code")
	}
}

func TestLVARLength(t *testing.T) {
	cases := []struct {
		lvar byte
		want int
		ok   bool
	}{
		{0x00, 0, true},
		{0x35, 53, true}, // the Qundis measurement block
		{0xBF, 191, true},
		{0xC4, 4, true},  // positive BCD
		{0xD4, 4, true},  // negative BCD
		{0xE8, 8, true},  // binary
		{0xF2, 8, true},  // two 4-byte floats
		{0xF5, 0, false}, // reserved
		{0xFF, 0, false},
	}
	for _, c := range cases {
		got, ok := lvarLength(c.lvar)
		if got != c.want || ok != c.ok {
			t.Errorf("lvarLength(0x%02X) = %d, %v; want %d, %v", c.lvar, got, ok, c.want, c.ok)
		}
	}
}
