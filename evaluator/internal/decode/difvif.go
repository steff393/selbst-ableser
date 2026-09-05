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
	errShortRecord               = errors.New("decode: payload ends in the middle of a data record")
	errUnsupportedVariableLength = errors.New("decode: variable-length data records are not supported")
)

// walkRecords splits a decrypted payload into its data records. It skips
// filler bytes and stops with an error at the first byte it cannot
// interpret as the start of a well-formed record, rather than guessing.
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
			// Not used by any meter family this system currently
			// supports; stop rather than mis-parse what follows.
			return records, errUnsupportedVariableLength
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
