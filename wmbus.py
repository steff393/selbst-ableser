import configparser
import time
import datetime

import threading
import json
import socket
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer  
from meterReader import evaluate_uvi, serialize_monthly_results
from urllib.parse import urlparse, parse_qs
from wmBus import WMBusReceiver



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


def main():
	# Snapshot setup
	threading.Thread(target=snapshot_scheduler, daemon=True).start()
	
	# HTTP-Server setup
	threading.Thread(target=start_http, daemon=True).start()
	
	# Serial communication
	wmbus = WMBusReceiver(cfg['Port'])
	try:
		wmbus.init_stick()
		wmbus.get_device_info()
		wmbus.set_config()
		for meter_id, rssi, wmbus in wmbus.frames():
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
	with open(cfg['Locationfile'], "r", encoding="utf-8") as f:
		Locationfile = json.load(f)

	for loc in Locationfile.get("zaehlerplaetze", []):
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
