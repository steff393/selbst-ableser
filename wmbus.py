import serial
import datetime
import time
import threading
import json
import socket
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = "COM5" #"/dev/serial/by-id/usb-IMST_iU891A_IMS3015-if00"
BAUDRATE = 115200
TIMEOUT  = 0.2
HTTP_PORT = 8080  # Webserver-Port


# Thread-safe RAM storage
frameList = {}
data_lock = threading.Lock()


# HTTP-Server Handler
class Handler(BaseHTTPRequestHandler):
	def do_GET(self):
		if self.path == "/" or self.path == "/web.html":
			try:
				base_path = os.path.dirname(os.path.abspath(__file__))
				file_path = os.path.join(base_path, "web.html")
				
				with open(file_path, "rb") as f:
					content = f.read()
				self.send_response(200)
				self.send_header("Content-Type", "text/html")
				self.end_headers()
				self.wfile.write(content)
			except FileNotFoundError:
				self.send_error(404, "web.html nicht gefunden")
			return

		if self.path == "/data":
			self.send_response(200)
			self.send_header("Content-Type", "application/json")
			self.end_headers()
			with data_lock:
				payload = {
					str(k): {
						"timestamp": v["timestamp"],
						"rssi": v["rssi"],
						"wmbus": v["wmbus"].hex()
					}
					for k, v in frameList.items()
				}
			self.wfile.write(json.dumps(payload).encode())
			return

		self.send_error(404, "Seite nicht gefunden")


def get_local_ip():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
    except Exception:
        ip = "127.0.0.1"
    finally:
        s.close()
    return ip


def start_http():
	server = HTTPServer(("0.0.0.0", HTTP_PORT), Handler)
	print(f"HTTP-Server aktiv: http://{get_local_ip()}:{HTTP_PORT}/data")
	server.serve_forever()


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


def send_slip_cmd(ser, endpoint, msg_id, payload=b"", text=""):
	msg = bytes([endpoint, msg_id]) + payload
	crc_val = crc16(msg)
	full_msg = msg + crc_val.to_bytes(2, "little")

	# SLIP Framing
	frame = bytearray([0xC0])
	for b in full_msg:
		if b == 0xC0: frame.extend([0xDB, 0xDC])
		elif b == 0xDB: frame.extend([0xDB, 0xDD])
		else: frame.append(b)
	frame.append(0xC0)
	
	print(f"-> {frame.hex().upper()} {text}")
	ser.write(frame)


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


def main():
	# HTTP-Server starten
	threading.Thread(target=start_http, daemon=True).start()

	try:
		ser = serial.Serial(PORT, BAUDRATE, timeout=TIMEOUT)
		print(f"Verbinde mit {PORT}...")
		
		# Wakeup
		ser.write(b'\xC0' * 30)
		time.sleep(0.1)
		ser.reset_input_buffer()

		# Get Device Info
		send_slip_cmd(ser, 0x01, 0x03, text="Get Device Info")
		resp = read_frame_timeout(ser)
		if resp:
			if resp.startswith(b'\xC0\x01\x04\x00') and check_crc(resp):
				print(f"<- {resp.hex().upper()} Device Info OK")
			else:
				print(f"<- {resp.hex().upper()} Device Info FAILED")
				return
		else:
			print("<- Keine Antwort!")
			return
		
		# Set Config C1/T1: Mode(03) Options(0E00) UI(0000) LED(3200) Recalib(A0BB0D00)
		send_slip_cmd(ser, 0x09, 0x03, bytes.fromhex("030E0000003200A0BB0D00"), "Set Configuration C1/T1")
		resp = read_frame_timeout(ser)
		if resp:
			if resp.startswith(b'\xC0\x09\x04\x00') and check_crc(resp):
				print(f"<- {resp.hex().upper()} Configuration OK")
			else:
				print(f"<- {resp.hex().upper()} Configuration FAILED")
				return
		else:
			print("<- Keine Antwort!")
			return
			
		print("Stick ist bereit und empfängt... (Strg+C zum Beenden)")

		# Listen for frames
		for frame in listen_frames(ser):
			if frame is None:
				continue
			if not check_crc(frame):
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültiger Frame (CRC Fehler): {frame.hex().upper()}")
				continue
			if not check_wmbus_len(frame):
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültiger Frame (Längenfehler): {frame.hex().upper()}")
				continue
			meter_id, rssi, wmbus = extract_wmbus_data(frame)
			if meter_id is None:
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültige Meter ID im Frame: {frame.hex().upper()}")
				continue
			else:
				print(f"{time.strftime('%H:%M:%S')} ✔ Zähler {meter_id} | RSSI {rssi} dBm | wmBus {wmbus.hex().upper()}")
				with data_lock:
					frameList[meter_id] = {
						"timestamp": datetime.datetime.now().strftime("%d.%m.%Y %H:%M:%S"),
						"rssi": rssi,
						"wmbus": wmbus
					}

	except KeyboardInterrupt:
		print("Beendet.")
	except Exception as e:
		print(f"Fehler: {e}")

if __name__ == "__main__":
	main()