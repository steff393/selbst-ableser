def crc16(data: bytes) -> int:
	crc = 0xFFFF
	poly = 0x8408
	for byte in data:
		for _ in range(8):
			if (byte & 1) ^ (crc & 1):
				crc = (crc >> 1) ^ poly
			else:
				crc >>= 1
			byte >>= 1
	# Invert (X-25 Style)
	crc = (~crc) & 0xFFFF
	return crc # Result is BIG endian(!) and needs to be swapped to little endian before sending
