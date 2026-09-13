package decode

import "errors"

// Record is one self-describing data record from a standard-compliant
// telegram payload, as defined by EN 13757-3: an identifier (DIF/DIFE,
// VIF/VIFE) followed by a value of a length and encoding determined by the
// DIF.
type Record struct {
	DIF  byte
	DIFE []byte
	VIF  byte
	VIFE []byte
	Data []byte // raw value bytes, meaning depends on DIF/VIF
}

const fillerByte = 0x2F

var (
	errShortRecord            = errors.New("decode: payload ends in the middle of a data record")
	errReservedVariableLength = errors.New("decode: variable-length data record with a reserved length code")
)

// walkRecords splits a decrypted payload into its data records. It skips
// filler bytes and stops with an error at the first byte it cannot
// interpret as the start of a well-formed record, rather than guessing.
//
// A record whose contents this package cannot interpret is still walked
// over and returned: its length is known from the DIF alone, so the
// records after it stay reachable. That matters for telegrams that carry
// one manufacturer-specific block among otherwise standard records — a
// Qundis heat meter puts its whole measurement into a single
// variable-length record and follows it with an ordinary status and
// timestamp. Stopping there would discard not just the block but every
// record before and after it, including the enhanced identification that
// says what the device even is.
func walkRecords(payload []byte) ([]Record, error) {
	var records []Record
	i := 0
	for i < len(payload) {
		if payload[i] == fillerByte {
			i++
			continue
		}

		dif := payload[i]
		cont := dif
		i++
		var dife []byte
		for cont&0x80 != 0 {
			if i >= len(payload) {
				return records, errShortRecord
			}
			cont = payload[i]
			dife = append(dife, cont)
			i++
			if cont&0x80 == 0 {
				break
			}
		}

		if i >= len(payload) {
			return records, errShortRecord
		}
		vif := payload[i]
		vcont := vif
		i++
		var vife []byte
		for vcont&0x80 != 0 {
			if i >= len(payload) {
				return records, errShortRecord
			}
			vcont = payload[i]
			vife = append(vife, vcont)
			i++
			if vcont&0x80 == 0 {
				break
			}
		}

		length, isVariable, err := dataLength(dif)
		if err != nil {
			return records, err
		}
		if isVariable {
			if i >= len(payload) {
				return records, errShortRecord
			}
			n, ok := lvarLength(payload[i])
			if !ok {
				// Without a length there is no way to tell where this
				// record ends and the next one begins, so the rest of
				// the payload is genuinely unreadable from here.
				return records, errReservedVariableLength
			}
			i++
			length = n
		}
		if i+length > len(payload) {
			return records, errShortRecord
		}

		records = append(records, Record{
			DIF:  dif,
			DIFE: dife,
			VIF:  vif,
			VIFE: vife,
			Data: payload[i : i+length],
		})
		i += length
	}
	return records, nil
}

// lvarLength decodes the length byte introducing a variable-length (LVAR)
// data record, per EN 13757-3: its value selects an encoding and, with it,
// a byte count. Only the count is of interest here — what the bytes mean
// is the business of whichever decoder claims the record, and a
// manufacturer-specific block (the case this is actually met in) does not
// honour the announced encoding anyway.
//
// ok is false for the reserved values 0xF5-0xFF, which define no length.
func lvarLength(lvar byte) (length int, ok bool) {
	switch {
	case lvar <= 0xBF:
		return int(lvar), true // ASCII string, lvar characters
	case lvar <= 0xCF:
		return int(lvar - 0xC0), true // positive BCD
	case lvar <= 0xDF:
		return int(lvar - 0xD0), true // negative BCD
	case lvar <= 0xEF:
		return int(lvar - 0xE0), true // binary
	case lvar <= 0xF4:
		return int(lvar-0xF0) * 4, true // floating point
	default:
		return 0, false
	}
}

// dataLength returns the number of value bytes a DIF's data field code
// (the low nibble) implies.
func dataLength(dif byte) (length int, variable bool, err error) {
	switch dif & 0x0F {
	case 0x0:
		return 0, false, nil
	case 0x1:
		return 1, false, nil
	case 0x2:
		return 2, false, nil
	case 0x3:
		return 3, false, nil
	case 0x4:
		return 4, false, nil
	case 0x5:
		return 4, false, nil // 32-bit real
	case 0x6:
		return 6, false, nil
	case 0x7:
		return 8, false, nil
	case 0x9:
		return 1, false, nil // 2-digit BCD
	case 0xA:
		return 2, false, nil // 4-digit BCD
	case 0xB:
		return 3, false, nil // 6-digit BCD
	case 0xC:
		return 4, false, nil // 8-digit BCD
	case 0xD:
		return 0, true, nil // variable length (LVAR)
	case 0xE:
		return 6, false, nil // 12-digit BCD
	default:
		return 0, false, errors.New("decode: unsupported DIF data field")
	}
}
