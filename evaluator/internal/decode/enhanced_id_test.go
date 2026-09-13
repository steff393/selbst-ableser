package decode

import (
	"encoding/hex"
	"testing"
)

// qundisHeatTelegram is a real, unencrypted (CI 0x78) telegram from a
// Qundis heat meter, captured 2026-09-09. It is the reason walkRecords
// steps over variable-length records: the whole measurement sits in a
// single manufacturer-specific one (DIF 0x0D, VIF 0xFF 0x5F), with
// ordinary records both before and after it.
const qundisHeatTelegram = "" +
	"5944934438718168483778" + // header: ... Med 0x37, CI 0x78
	"07793871816893444804" + // enhanced identification: DIF 07, VIF 79, 8 bytes
	"0dff5f350082690000f00007c006ffff00000000ffff0000" +
	"00005f380000000000800080008000800080008000800080" +
	"00800080008000002f02fd170000046d0908493964"

func TestQundisHeatTelegramRecordsSurviveTheVariableLengthBlock(t *testing.T) {
	raw, err := hex.DecodeString(qundisHeatTelegram)
	if err != nil {
		t.Fatal(err)
	}
	records, ok, walkErr := Standard(0x78, raw)
	if !ok {
		t.Fatal("Standard reported CI 0x78 as not self-describing")
	}
	// The telegram ends on a lone DIF byte (0x64) with nothing behind it,
	// so the walk does end in an error — but only after every complete
	// record has been read. Callers that want an identification out of a
	// telegram rely on exactly that: the records already read are usable
	// even when the tail is not.
	if walkErr == nil {
		t.Error("expected the trailing lone DIF to end the walk in an error")
	}
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4 (enhanced id, measurement block, status, timestamp)", len(records))
	}

	want := []struct{ dif, vif byte }{
		{0x07, 0x79}, // enhanced identification
		{0x0D, 0xFF}, // manufacturer-specific measurement block
		{0x02, 0xFD}, // status / error flags
		{0x04, 0x6D}, // meter date and time
	}
	for i, w := range want {
		if records[i].DIF != w.dif || records[i].VIF != w.vif {
			t.Errorf("record %d = DIF 0x%02X VIF 0x%02X, want DIF 0x%02X VIF 0x%02X",
				i, records[i].DIF, records[i].VIF, w.dif, w.vif)
		}
	}
	if got := len(records[1].Data); got != 53 {
		t.Errorf("measurement block is %d bytes, want the announced 53", got)
	}
}

func TestEnhancedMediumReadsTheMeterBehindTheRadio(t *testing.T) {
	raw, err := hex.DecodeString(qundisHeatTelegram)
	if err != nil {
		t.Fatal(err)
	}
	records, _, _ := Standard(0x78, raw)

	medium, found := EnhancedMedium(records)
	if !found {
		t.Fatal("no enhanced identification record found")
	}
	// The link layer (byte 9) says 0x37, radio converter. The record says
	// what is actually being measured.
	if raw[9] != 0x37 {
		t.Fatalf("fixture changed: link-layer medium is 0x%02X, expected 0x37", raw[9])
	}
	if medium != 0x04 {
		t.Errorf("EnhancedMedium = 0x%02X, want 0x04 (heat)", medium)
	}
}

func TestEnhancedMediumAbsent(t *testing.T) {
	records := []Record{{DIF: 0x04, VIF: 0x13, Data: []byte{1, 2, 3, 4}}}
	if _, found := EnhancedMedium(records); found {
		t.Error("reported an enhanced identification where there is none")
	}
}

func TestEnhancedMediumIgnoresAWrongLengthRecord(t *testing.T) {
	// Same VIF, but not the eight-byte identification layout — a value
	// this function must not read a medium out of.
	records := []Record{{DIF: 0x04, VIF: 0x79, Data: []byte{1, 2, 3, 4}}}
	if _, found := EnhancedMedium(records); found {
		t.Error("accepted a 4-byte record as an enhanced identification")
	}
}
