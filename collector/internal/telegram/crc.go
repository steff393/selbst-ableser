package telegram

// CRC16 computes the CRC-16/X-25 checksum used to validate a received frame.
//
// Parameters: polynomial 0x8408 (reflected form of 0x1021), initial value
// 0xFFFF, input and output reflected, final value XORed with 0xFFFF.
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFF
}
