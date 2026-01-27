from .crc import crc16

def encode(endpoint, msg_id, payload=b"", text=""):
	msg = bytes([endpoint, msg_id]) + payload
	crc_val = crc16(msg)
	full_msg = msg + crc_val.to_bytes(2, "little")

	# SLIP Framing
	frame = bytearray([0xC0])
	for b in full_msg:
		if b == 0xC0: frame.extend([0xDB, 0xDC])    # Escape handling
		elif b == 0xDB: frame.extend([0xDB, 0xDD])  # Escape handling
		else: frame.append(b)
	frame.append(0xC0)
	
	print(f"-> {frame.hex().upper()} {text}")
	return(frame)
