package decode

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Real Qundis heat-meter telegrams. The underscore in wmbusmeters' notation
// is stripped; nothing else is changed.
const (
	// This installation's meter 68817138 before any heat had flowed.
	qundisHeat0kWh = "5944934438718168483778077938718168934448040DFF5F350082690000F00007C006FFFF00000000FFFF000000005F38000000000080008000800080008000800080008000800080008000002F02FD170000046D0908493964"
	// Meter 68817136 in the same installation, after the first kWh.
	qundisHeat1kWh = "5944934436718168483778077936718168934448040DFF5F350082E100005E0107C006FFFF01000000FFFF000000005F38000000000080008000800080008000800080008000800080008000002F02FD170000046D33084B3949"
	// Test vector from wmbusmeters' qheatv2 driver: 1153 kWh. The only one
	// of the four with a total large enough to tell BCD from binary.
	qundisHeat1153kWh = "58449344798995673E377807797989956793443E040DFF5F3500827D0000810007C006FFFF531100003F3C490900005F318010000083003C0022000B0000000000000000000A002B00510083002F02FD170000046D16085832"
	// Test vector from the same driver whose block is encrypted (byte 4 of
	// the block is 0x35). wmbusmeters reads 3052 kWh from it with the key.
	qundisHeatEncrypted = "58449344409436683F377807794094366893443F040DFF5F350082110035D3B5EA13344D42345FD6C9B468DC8A7FC0A56DCECD9861481F7C87A367564D55E1576B5A9B00E00CF10466C038D6393902FD170000046D00135832"
)

func qundisRecords(t *testing.T, telegramHex string) ([]Record, []byte) {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(telegramHex, "_", ""))
	if err != nil {
		t.Fatal(err)
	}
	records, ok, _ := Standard(raw[10], raw)
	if !ok {
		t.Fatalf("CI 0x%02X not treated as a standard telegram", raw[10])
	}
	return records, raw
}

func TestQundisHeatTotals(t *testing.T) {
	cases := []struct {
		name     string
		telegram string
		want     int64
	}{
		{"before first heat", qundisHeat0kWh, 0},
		{"first kWh", qundisHeat1kWh, 1},
		{"wmbusmeters test vector", qundisHeat1153kWh, 1153},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records, _ := qundisRecords(t, c.telegram)
			reading, field, found, err := ManufacturerRecordValue("QDS", records)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if reading.Value != c.want || reading.Unit != UnitKWh {
				t.Errorf("reading = %+v, want %d kWh", reading, c.want)
			}
			if len(field) != 4 {
				t.Errorf("field is %d bytes, want the 4-byte total", len(field))
			}
		})
	}
}

// An encrypted block must not be read at all: its bytes at the total's
// position are ciphertext, and reading them would produce a number with
// nothing to do with the meter.
func TestQundisHeatEncryptedBlockIsNotRead(t *testing.T) {
	records, _ := qundisRecords(t, qundisHeatEncrypted)
	if len(records) < 2 || records[1].DIF != 0x0D || len(records[1].Data) != 53 || records[1].Data[4] != 0x35 {
		t.Fatalf("fixture does not reach the encrypted block: %d records", len(records))
	}
	if _, _, found, err := ManufacturerRecordValue("QDS", records); found || err != nil {
		t.Errorf("found=%v err=%v, want neither for an encrypted block", found, err)
	}
}

// The decoder is registered for Qundis only. Another manufacturer sending
// a block that happens to look the same gets no reading out of it.
func TestQundisHeatOnlyForQundis(t *testing.T) {
	records, _ := qundisRecords(t, qundisHeat1kWh)
	if _, _, found, _ := ManufacturerRecordValue("TCH", records); found {
		t.Error("a non-Qundis manufacturer code got a reading from the Qundis decoder")
	}
}

// Anything outside the one verified variant fails closed: a different
// energy scale, a fixed byte that does not match, a total that is not
// valid BCD.
func TestQundisHeatFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		offset int // within the 53-byte block
		value  byte
	}{
		{"100 Wh scale", 9, 0x05},
		{"unexpected marker", 1, 0x83},
		{"unexpected type", 8, 0xC1},
		{"non-BCD total", 12, 0x1A},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records, _ := qundisRecords(t, qundisHeat1153kWh)
			block := records[1].Data
			block[c.offset] = c.value
			if reading, _, found, err := ManufacturerRecordValue("QDS", records); found || err != nil {
				t.Errorf("got %+v found=%v err=%v, want no reading", reading, found, err)
			}
		})
	}
}

// The returned field aliases the telegram, so a correction written into it
// lands in the telegram itself and reads back as the new value.
func TestQundisHeatFieldAliasesTheTelegram(t *testing.T) {
	records, raw := qundisRecords(t, qundisHeat1153kWh)
	_, field, found, err := ManufacturerRecordValue("QDS", records)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	encoded, err := EncodeCurrentValue(UnitKWh, 1200, len(field))
	if err != nil {
		t.Fatal(err)
	}
	copy(field, encoded)

	records2, _, _ := Standard(raw[10], raw)
	reading, _, found, err := ManufacturerRecordValue("QDS", records2)
	if err != nil || !found || reading.Value != 1200 {
		t.Errorf("after patching: %+v found=%v err=%v, want 1200 kWh", reading, found, err)
	}
}
