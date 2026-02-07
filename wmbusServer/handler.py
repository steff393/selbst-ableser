import os
import json
import datetime
from http.server import BaseHTTPRequestHandler


class Handler(BaseHTTPRequestHandler):
	def __init__(self, *args, cfg=None, frame_store=None, **kwargs):
		self.cfg = cfg
		self.frame_store = frame_store
		super().__init__(*args, **kwargs)


	def do_GET(self):
		path = self.path.lstrip("/")
		if path == "/favicon.ico":
			self.serve_file("favicon.ico", "image/svg+xml")
			return

		if path in ("data", "data/"):
			self.serve_data(self.frame_store)
			return

		if path in ("", "web.html"):
			self.serve_file("web.html", "text/html")
			return
		
		if path.startswith("data/"):
			date = path.split("/")[-1]
			snap = self.load_snapshot(date)
			self.serve_data(snap)
			return

		if path == "list":
			self.serve_snapshot_list()
			return
		
		if path == "snapshot":
			self.frame_store.make_snapshot(force=True)
			self.send_json({
				"status": "ok",
				"snapshot": datetime.datetime.now().strftime("%Y-%m-%d")
			})
			return

		self.send_error(404, "Seite nicht gefunden")


	# Send a JSON response
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
	def serve_data(self, store):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.end_headers()

		payload = {}
		if store is None:
			self.wfile.write(b"{}")
			return

		for meter_nr, data in store.get_all().items():
			payload[meter_nr] = {
				"timestamp": data["timestamp"],
				"rssi": data["rssi"],
				"wmbus": data["wmbus"].hex(),
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
