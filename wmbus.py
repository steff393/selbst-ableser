import serial
import configparser
import datetime
import time
import threading
import json
import socket
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer  
from meterReader import evaluate_uvi, serialize_monthly_results
from urllib.parse import urlparse, parse_qs



BAUDRATE = 115200
TIMEOUT  = 0.2

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

# Thread-safe RAM storage
frameList = {}
data_lock = threading.Lock()


# HTTP-Server Handler
class Handler(BaseHTTPRequestHandler):
	def do_GET(self):
		users = load_users()
		parsed = urlparse(self.path)
		params = parse_qs(parsed.query)

		if self.path == "/favicon.ico":
			self.serve_file("favicon.ico", "image/svg+xml")
			return

		if users is None:
			# no user configured or no users file -> allow all
			token = None
			subpath = self.path.strip("/")
			filter = None
		else:
			# token based user authentication
			parts = self.path.strip("/").split("/", 1)
			token = parts[0] if parts else None
			subpath = parts[1] if len(parts) > 1 else ""
			if token is None or token not in users:
				self.send_error(403, "Ungültiger Nutzer")
				return
			filter = users.get(token) # null for admin -> will become None in Python

		# /data
		if subpath=="data" or subpath=="data/":
			self.serve_data(frameList, filter)
			return

		if subpath.startswith("eval"):
			response = self.handle_uvi_request(
				params.get("start", ["2024-01-01"])[0],
				params.get("end",   ["2025-12-31"])[0],
				params.get("path",  [cfg['SnapshotDir']])[0]
			)
			self.send_response(200)
			self.send_header("Content-Type", "application/json")
			self.end_headers()
			self.wfile.write(response.encode())
			return


		if subpath == "" or subpath == "web.html":
			self.serve_file("web.html", "text/html")
			return

		if subpath == "uvi.html":
			self.serve_file("uvi.html", "text/html")
			return

		if subpath.startswith("data/"):
			date = subpath.split("/")[-1]
			snap = self.load_snapshot(date)
			self.serve_data(snap, filter)
			return

		# only for admins (no filter)
		if filter is None:
			if subpath == "list":
				self.serve_snapshot_list()
				return
			# create snapshot now
			if subpath == "snapshot":
				make_snapshot(force=True)
				self.send_response(200)
				self.send_header("Content-Type", "application/json")
				self.end_headers()
				self.wfile.write(json.dumps({
					"status": "ok",
					"snapshot": datetime.datetime.now().strftime("%Y-%m-%d"),
				}).encode())
				return

		self.send_error(404, "Seite nicht gefunden")
		
	# get a static file
	def serve_file(self, filename, content_type):
		try:
			base_path = os.path.dirname(os.path.abspath(__file__))
			with open(os.path.join(base_path, filename), "rb") as f:
				content = f.read()
			self.send_response(200)
			self.send_header("Content-Type", content_type)
			self.end_headers()
			self.wfile.write(content)
		except FileNotFoundError:
			self.send_error(404, f"{filename} nicht gefunden")

	# get current data
	def serve_data(self, data_dict, filter):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.end_headers()
		with data_lock:
			payload = {}
			for k, v in data_dict.items():
				meter_nr = str(k)

				if filter is not None:
					if filter != meter_map.get(meter_nr, {}).get("whg"):
						continue

				payload[meter_nr] = {
					"timestamp": v["timestamp"],
					"rssi": v["rssi"],
					"wmbus": v["wmbus"].hex(),
					"raum": meter_map.get(meter_nr, {}).get("raum")
				}
		self.wfile.write(json.dumps(payload).encode())


	# get snapshot by date
	def load_snapshot(self, date):
		filename = os.path.join(cfg['SnapshotDir'], f"{date}.json")
		if os.path.isfile(filename):
			with open(filename, "r", encoding="utf-8") as f:
				data = json.load(f)

			# convert wmbus from hex string to bytes (to match live data format)
			for k, v in data.items():
				wmbus_val = v.get("wmbus")
				if isinstance(wmbus_val, str):
					v["wmbus"] = bytes.fromhex(wmbus_val)
			return data
		return None


	# get list of available snapshots
	def serve_snapshot_list(self):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.end_headers()
		files = []
		if os.path.isdir(cfg['SnapshotDir']):
			for f in sorted(os.listdir(cfg['SnapshotDir'])):
				if f.endswith(".json"):
					files.append(f[:-5])
		self.wfile.write(json.dumps(files).encode())


	def handle_uvi_request(self, start, end, path):
		print(f"[UVI] Anfrage: {start} bis {end}, Pfad: {path}")

		result = evaluate_uvi(
			json_path      = path,   # folder with daily JSON files YYYY-MM-DD.json
			locations_path = cfg['Locationfile'],
			start_date     = start,
			end_date       = end
		)
		return json.dumps(serialize_monthly_results(result))


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
	server = ThreadingHTTPServer(("0.0.0.0", int(cfg['HttpPort'])), Handler)
	print(f"HTTP-Server aktiv: http://{get_local_ip()}:{int(cfg['HttpPort'])}")
	server.serve_forever()


def make_snapshot(force=False):
	global last_snapshot_key
	with data_lock:
		if not frameList:
			return

	os.makedirs(cfg['SnapshotDir'], exist_ok=True)

	key = datetime.datetime.now().strftime("%Y-%m-%d")
	if key == last_snapshot_key and not force:
		return False  # snapshot already taken for this period
	
	filename = os.path.join(cfg['SnapshotDir'], f"{key}.json")

	with open(filename, "w", encoding="utf-8") as f:
		payload = {
			str(k): {
				"timestamp": v["timestamp"],
				"rssi": v["rssi"],
				"wmbus": v["wmbus"].hex()
			}
			for k, v in frameList.items()
		}
		json.dump(payload, f, indent=2)

	last_snapshot_key = key
	print(f"[SNAPSHOT] Gespeichert: {filename}")


def time_for_snapshot(now):
	if cfg['SnapshotMode'] == "daily":
		trigger = (now.hour == 0)

	if cfg['SnapshotMode'] == "monthly":
		trigger = (now.day == 1 and now.hour == 0)

	if not trigger:  # "none" or not trigger time
		return False

	return True


def snapshot_scheduler():
	global last_snapshot_key

	while True:
		now = datetime.datetime.now()
		if time_for_snapshot(now):
			make_snapshot()
		time.sleep(30)  # s


def load_last_snapshot_key():
	if not os.path.isdir(cfg['SnapshotDir']):
		return None

	files = sorted(f for f in os.listdir(cfg['SnapshotDir']) if f.endswith(".json"))
	return files[-1][:-5] if files else None


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
	# Snapshot setup
	threading.Thread(target=snapshot_scheduler, daemon=True).start()
	
	# HTTP-Server setup
	threading.Thread(target=start_http, daemon=True).start()
	
	# Serial communication
	try:
		ser = serial.Serial(cfg['Port'], BAUDRATE, timeout=TIMEOUT)
		print(f"Verbinde mit iU891A-XL an {cfg['Port']}...")
		
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


def load_users():
    if os.path.isfile(cfg['Userfile']):
        with open(cfg['Userfile'], "r", encoding="utf-8") as f:
            return json.load(f)
    return None


# Build meter lookup: meter_nr -> meta info
def load_meter_map():
	with open(cfg['Keyfile'], "r", encoding="utf-8") as f:
		keyfile = json.load(f)

	for loc in keyfile.get("zaehlerplaetze", []):
		for meter in loc.get("zaehler", []):
			meter_id = meter["id"]

			meter_map[meter_id] = {
				"whg": loc["whg"],
				"raum": loc["raum"],
				"platz": loc.get("platz")
			}

	return meter_map



# Startup
meter_map = {}
meter_map = load_meter_map()
last_snapshot_key = load_last_snapshot_key()

if __name__ == "__main__":
	main()
