import configparser
import time
import datetime

import threading
import json
import socket
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer  
from meterReader import evaluate_uvi, serialize_monthly_results, decrypt
from urllib.parse import urlparse, parse_qs
from wmBus import WMBusReceiver
from frame_store import FrameStore


config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

frame_store = FrameStore()


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
			self.serve_data(frame_store, filter)
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
				frame_store.make_snapshot(cfg, force=True)
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
	def serve_data(self, store, filter):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.end_headers()

		payload = {}
		for meter_nr, data in store.get_all().items():
			if filter is not None:
				if filter != meter_map.get(meter_nr, {}).get("whg"):
					continue
			
			wmbus = None
			aes_key = meter_map.get(meter_nr, {}).get("aes_key")
			if aes_key:
				wmbus = decrypt(data["wmbus"], aes_key)
			if wmbus is None:
				wmbus = data["wmbus"] # no decryption possible, take original value

			payload[meter_nr] = {
				"timestamp": data["timestamp"],
				"rssi": data["rssi"],
				"wmbus": wmbus.hex(),
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


def snapshot_scheduler():
	while True:
		now = datetime.datetime.now()
		if frame_store.time_for_snapshot(now, cfg):
			frame_store.make_snapshot(cfg)
		time.sleep(30)  # s


def main():
	# Load existing data
	if len(sys.argv) == 2:
		frame_store.load_snapshot_file(cfg, sys.argv[1])

	# Snapshot setup
	threading.Thread(target=snapshot_scheduler, daemon=True).start()
	
	# HTTP-Server setup
	threading.Thread(target=start_http, daemon=True).start()
	
	# Serial communication
	iu891a = WMBusReceiver(cfg['Port'])
	try:
		iu891a.init_stick()
		iu891a.get_device_info()
		iu891a.set_config()
		for meter_id, rssi, wmbus in iu891a.frames():
			block = meter_map.get(meter_id, {}).get("blockMsg")
			if block and f"{wmbus[0]:02X}" == block.upper():
				print(f"Telegramm beginnt mit {block}... => blockiert")
				continue
			frame_store.update(meter_id, rssi, wmbus)
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
				"platz": loc.get("platz"),
				"aes_key": meter["aes_key"],
				"blockMsg": meter["blockMsg"]
			}

	return meter_map



# Startup
meter_map = {}
meter_map = load_meter_map()

if __name__ == "__main__":
	main()
