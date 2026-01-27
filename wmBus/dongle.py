from .crc import crc16
import time


def check_crc(frame):
	# frame: C0 ... DATA ... CRC_H CRC_L C0
	payload = frame[1:-3]
	crc_rx = (frame[-2] << 8) | frame[-3] # received CRC is in little endian -> swap bytes
	crc_calc = crc16(payload)
	return crc_rx == crc_calc


def check_wmbus_len(frame):
	# wmBus length is at byte index 11
	return len(frame) > 12 and len(frame) == frame[11] + 15    # 15 = C0 + Dongle(10) + LEN [+ WMBUS] + CRC(2) + C0


def extract_wmbus_data(frame):
	meter_id = None
	meter_bytes = frame[15:19][::-1]
	if not any(b > 0x99 for b in meter_bytes):
		meter_id = int("".join(f"{b:02X}" for b in meter_bytes))
	rssi = frame[10] - 256  # signed RSSI
	wmbus = frame[11:-3]
	return meter_id, rssi, wmbus


def read_frame_blocking(ser):  # function has a endless loop until a full frame is read
	raw = bytearray()
	in_frame = False
	esc = False

	while True:
		data = ser.read(1)
		if not data:
			return None  # Serial port closed or error
		b = data[0]

		if b == 0xC0:
			raw.append(0xC0)
			if in_frame:
				return raw
			in_frame = True
			esc = False

		elif in_frame:
			if esc:
				raw.append(0xC0 if b == 0xDC else 0xDB if b == 0xDD else b) # escape sequence handling
				esc = False
			elif b == 0xDB:
				esc = True
			else:
				raw.append(b)


def read_frame_timeout(ser, timeout=1.0):  # function waits up to timeout seconds for a full frame
	start = time.time()
	while time.time() - start < timeout:
		if ser.in_waiting:
			return read_frame_blocking(ser)
		time.sleep(0.001)
	return None


def listen_frames(ser):                    # generator yielding frames as they are received
	while True:
		frame = read_frame_blocking(ser)
		yield frame
