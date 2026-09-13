package decode

// Qundis heat meters (Q heat 5.5 family, wmbusmeters' "qheatv2" driver)
// send an ordinary, unencrypted EN 13757-3 telegram (CI 0x78) whose
// measurement is not in standard records but packed into a single
// manufacturer-specific one: DIF 0x0D (variable length), VIF 0xFF, VIFE
// 0x5F, 53 bytes. Around it sit regular records — an enhanced
// identification in front, status and meter time behind — which is why
// the telegram as a whole is walkable and only this one record needs
// decoding by hand.
//
// Layout of the 53-byte block, offsets from its first byte:
//
//	0      0x00
//	1      0x82
//	2      rolling counter (varies per telegram)
//	3      0x00
//	4      0x00 — 0x35 marks a block encrypted on its own, see below
//	5-6    device flags (vary)
//	7      0x07
//	8      0xC0
//	9      energy scale: 0x06 = 1 kWh (0x05 100 Wh, 0x0D 100 kJ, 0x0E 1 MJ)
//	10-11  error date, type G
//	12-15  total energy, 8-digit BCD, least significant byte first
//	16-17  last year-end date, type G
//	18-21  energy at that date, BCD
//	22-23  last month-end date, type G
//	24-27  energy at that date, BCD
//	28-51  twelve monthly deltas, signed little-endian 16 bit (0x8000 = none)
//	52     0x2F filler
//
// Verified against four captured telegrams: two from this installation's
// own meters (68817138, total 0 kWh; 68817136, total 1 kWh after the
// first heat flowed) and two published as test vectors in wmbusmeters'
// qheatv2 driver — 67958979 with a total of 1153 kWh, whose bytes
// 53 11 00 00 settle BCD over binary (binary would read 4435), and
// 68369440, whose block is encrypted.
//
// Only the one variant actually seen with a readable total is decoded: an
// unencrypted block at 1 kWh scale. Everything else — another scale, an
// encrypted block, a byte in a fixed position that does not match —
// reports found=false rather than a guess. An encrypted block could be
// decrypted with the meter's key, but no telegram of that kind with a
// known plaintext has been seen to verify it against.

const (
	qundisHeatBlockLen    = 53
	qundisHeatVIFE        = 0x5F
	qundisHeatScale1kWh   = 0x06
	qundisHeatTotalOffset = 12
	qundisHeatTotalLen    = 4
)

func init() {
	RegisterManufacturerRecordDecoder("QDS", decodeQundisHeatRecord)
}

func decodeQundisHeatRecord(records []Record) (Reading, []byte, bool, error) {
	for _, r := range records {
		block, ok := qundisHeatBlock(r)
		if !ok {
			continue
		}
		field := block[qundisHeatTotalOffset : qundisHeatTotalOffset+qundisHeatTotalLen]
		if !validBCD(field) {
			// A corrupt total is a telegram that cannot be evaluated, not
			// a failure of the billing run looking at it: returning an
			// error here would stop the whole month's figure over one bad
			// telegram, where not-found lets the search move on to the
			// next one (see billing.FindReading).
			return Reading{}, nil, false, nil
		}
		return Reading{Value: int64(decodeBCD(field)), Unit: UnitKWh}, field, true, nil
	}
	return Reading{}, nil, false, nil
}

// qundisHeatBlock returns r's data if r is the unencrypted, 1-kWh-scale
// measurement block described above.
func qundisHeatBlock(r Record) ([]byte, bool) {
	if r.DIF != 0x0D || len(r.DIFE) != 0 || r.VIF != 0xFF || len(r.VIFE) != 1 || r.VIFE[0] != qundisHeatVIFE {
		return nil, false
	}
	b := r.Data
	if len(b) != qundisHeatBlockLen {
		return nil, false
	}
	if b[0] != 0x00 || b[1] != 0x82 || b[3] != 0x00 || b[7] != 0x07 || b[8] != 0xC0 {
		return nil, false
	}
	if b[4] != 0x00 {
		return nil, false // encrypted block (0x35)
	}
	if b[9] != qundisHeatScale1kWh {
		return nil, false
	}
	return b, true
}

// validBCD reports whether every nibble of data is a decimal digit.
func validBCD(data []byte) bool {
	for _, x := range data {
		if x>>4 > 9 || x&0x0F > 9 {
			return false
		}
	}
	return true
}
