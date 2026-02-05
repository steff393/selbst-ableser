import os
import json
import datetime
from http.server import BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from meterReader import evaluate_uvi, serialize_monthly_results, decrypt


class Handler(BaseHTTPRequestHandler):
	def __init__(self, *args, cfg=None, frame_store=None, registry=None, **kwargs):
		self.cfg = cfg
		self.frame_store = frame_store
		self.registry = registry
		super().__init__(*args, **kwargs)

	def get_token_and_subpath(self):
		parts = self.path.strip("/").split("/", 1)
		token = parts[0] if parts else None
		subpath = parts[1] if len(parts) > 1 else ""
		return token, subpath

	def do_GET(self):
		parsed = urlparse(self.path)
		params = parse_qs(parsed.query)
		token, subpath = self.get_token_and_subpath()

		if self.path == "/favicon.ico":
			self.serve_file("favicon.ico", "image/svg+xml")
			return

		users = self.load_users()
		is_setup_mode = len(users) == 0
		
		if is_setup_mode:
			# Im Setup-Modus ist jeder ein Admin
			filter = None 
		else:
			if token not in users:
				self.send_error(403, "Ungültiger Token")
				return
			
			# Hole das Daten-Objekt des Nutzers (z.B. {"flat": "A1", ...})
			user_data = users.get(token)
			# Der Filter ist der Wert von 'flat'. Wenn dieser None/null ist -> Admin
			filter = user_data.get("flat")

		# Routing
		if subpath in ("data", "data/"):
			self.serve_data(self.frame_store, filter)
			return

		if subpath.startswith("eval"):
			response = self.handle_uvi_request(
				params.get("start", ["2024-01-01"])[0],
				params.get("end",   ["2025-12-31"])[0],
				params.get("path",  [self.cfg['SnapshotDir']])[0],
				filter
			)
			self.send_json(response)
			return

		if subpath in ("", "web.html"):
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
			
			if subpath == "snapshot":
				self.frame_store.make_snapshot(force=True)
				self.send_json({
					"status": "ok",
					"snapshot": datetime.datetime.now().strftime("%Y-%m-%d")
				})
				return
			
			if subpath in ("users.html", "users"):
				self.serve_file("users.html", "text/html")
				return

			if subpath == "users/data":
				self.send_json(users)
				return

			if subpath == "users/export":
				self.export_users(users)
				return

		self.send_error(404, "Seite nicht gefunden")


	def do_POST(self):
		token, subpath = self.get_token_and_subpath()
		users = self.load_users()

		# Nur Admins dürfen speichern
		if users is not None and users.get(token).get("flat") is not None:
			self.send_error(403, "Nur für Admins")
			return

		if subpath == "users/save":
			length = int(self.headers.get("Content-Length", 0))
			try:
				data = json.loads(self.rfile.read(length))
				with open(self.cfg['Userfile'], "w", encoding="utf-8") as f:
					json.dump(data, f, indent=2)
				self.send_json({"status": "ok"})
			except Exception as e:
				self.send_error(500, str(e))
			return

		self.send_error(404)

	def load_users(self):
		path = self.cfg.get('Userfile', 'users.json')
		if os.path.isfile(path):
			with open(path, "r", encoding="utf-8") as f:
				return json.load(f)
		return None
	
	
	def export_users(self, users):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.send_header(
			"Content-Disposition",
			f'attachment; filename="users-backup-{datetime.date.today()}.json"'
		)
		self.end_headers()
		self.wfile.write(json.dumps(users, indent=2).encode())


	def send_json(self, data, status=200):
		self.send_response(status)
		self.send_header("Content-Type", "application/json")
		self.end_headers()
		if isinstance(data, (dict, list)):
			data = json.dumps(data)
		self.wfile.write(data.encode())

	# get a static file
	def serve_file(self, filename, content_type):
		try:
			base = os.path.dirname(os.path.abspath(__file__))
			path = os.path.join(base, "..", filename)
			with open(path, "rb") as f:
				data = f.read()
			self.send_response(200)
			self.send_header("Content-Type", content_type)
			self.end_headers()
			self.wfile.write(data)
		except FileNotFoundError:
			self.send_error(404, f"{filename} nicht gefunden")


	# get current data
	def serve_data(self, store, filter):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.end_headers()

		payload = {}
		if store is None:
			self.wfile.write(b"{}")
			return

		for meter_nr, data in store.get_all().items():
			meter = self.registry.get_meter(meter_nr)

			if filter is not None:
				if meter is None or filter != meter.flat:
					continue

			wmbus = None
			if meter and meter.aes_key:
				wmbus = decrypt(data["wmbus"], meter.aes_key)
			if wmbus is None:
				wmbus = data["wmbus"] # no decryption possible, take original value

			payload[meter_nr] = {
				"timestamp": data["timestamp"],
				"rssi": data["rssi"],
				"wmbus": wmbus.hex(),
				"raum": meter.room if meter else None
			}
		self.wfile.write(json.dumps(payload).encode())


	# get snapshot by date
	def load_snapshot(self, date):
		filename = os.path.join(self.cfg['SnapshotDir'], f"{date}.json")
		if not os.path.isfile(filename):
			return None

		with open(filename, "r", encoding="utf-8") as f:
			data = json.load(f)

		# convert wmbus from hex string to bytes (to match live data format)
		for v in data.values():
			if isinstance(v.get("wmbus"), str):
				v["wmbus"] = bytes.fromhex(v["wmbus"])
		return data


	# get list of available snapshots
	def serve_snapshot_list(self):
		files = []
		if os.path.isdir(self.cfg['SnapshotDir']):
			for f in sorted(os.listdir(self.cfg['SnapshotDir'])):
				if f.endswith(".json"):
					files.append(f[:-5])
		self.send_json(files)


	# calculate data for UVI
	def handle_uvi_request(self, start, end, path, filter=None):
		print(f"[UVI] Anfrage: {start} bis {end}, Pfad: {path}")
		result = evaluate_uvi(
			json_path  = path,
			registry   = self.registry,
			start_date = start,
			end_date   = end,
			flat       = filter
		)
		return serialize_monthly_results(result)
	