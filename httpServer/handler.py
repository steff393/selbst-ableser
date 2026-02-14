import os
import json
from datetime import datetime, date
import cgi
from http.server import BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from meterReader import evaluate_uvi, evaluate_uvi_aggregated, serialize_monthly_results, serialize_monthly_aggregates
from .importer import import_and_encrypt
import tempfile


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
			# Only users or setup page / no token -> use self.path
			if self.path == "/users.html":
				self.serve_file("users.html", "text/html")
				return
			if self.path == "/users/data":
				self.send_json(users)
				return
			self.serve_file("index.html", "text/html")
			return
		else:
			if token not in users:
				self.send_error(403, "Ungültiger Token")
				return
			# get data object of the user, eg. {"flat": "A1", ...}
			user_data = users.get(token)

		# Routing
		if subpath.startswith("eval_combined"):
			response = self.handle_uvi_combined_request(
				params.get("start", ["2024-01-31"])[0],
				params.get("end",   ["2026-01-31"])[0],
				params.get("path",  [self.cfg['SnapshotDir']])[0],
				user_data
			)
			self.send_json(response)
			return
		

		if subpath.startswith("eval"):
			response = self.handle_uvi_request(
				params.get("start", ["2024-01-31"])[0],
				params.get("end",   ["2026-01-31"])[0],
				params.get("path",  [self.cfg['SnapshotDir']])[0],
				user_data
			)
			self.send_json(response)
			return
		
		if subpath.startswith("aggregate"):
			response = self.handle_uvi_aggregate_request(
				params.get("start", ["2024-01-31"])[0],
				params.get("end",   ["2026-01-31"])[0],
				params.get("path",  [self.cfg['SnapshotDir']])[0]
			)
			self.send_json(response)
			return
		
		if subpath in ("", "index.html"):
			self.serve_file("index.html", "text/html")
			return

		if subpath in ("chart.umd.min.js"):
			self.serve_file(subpath, "text/javascript")
			return
		
		if subpath in ("uvi.html", "impressum.html", "datenschutz.html"):
			self.serve_file(subpath, "text/html")
			return
		
		if subpath in ("uvialt.html"):
			self.serve_file("uviAlt.html", "text/html")
			return

		# only for admins (no filter)
		if users is not None and users.get(token).get("flat") in (None, ""):
			if subpath in ("users.html", "users"):
				self.serve_file("users.html", "text/html")
				return

			if subpath == "users/data":
				self.send_json(users)
				return

			if subpath == "users/export":
				self.export_users(users)
				return
			
			if subpath == "import.html":
				self.serve_file("import.html", "text/html")
				return

		self.send_error(404, "Seite nicht gefunden")


	def do_POST(self):
		token, subpath = self.get_token_and_subpath()
		
		users = self.load_users()
		is_setup_mode = len(users) == 0
		
		if is_setup_mode:
			if self.path == "/users/save":
				self.handle_users_save()
				return

		# Nur Admins dürfen speichern
		if users is not None and users.get(token).get("flat") not in (None, ""):
			self.send_error(403, "Nur für Admins")
			return

		if subpath == "users/save":
			self.handle_users_save()
			return
		
		if subpath == "import/upload":
			self.handle_import_upload()
			return

		self.send_error(404)


	def handle_users_save(self):
		length = int(self.headers.get("Content-Length", 0))
		try:
			data = json.loads(self.rfile.read(length))
			with open(self.cfg['Userfile'], "w", encoding="utf-8") as f:
				json.dump(data, f, indent=2)
			self.send_json({"status": "ok"})
		except Exception as e:
			self.send_error(500, str(e))


	def load_users(self):
		path = self.cfg.get("Userfile", "users.json")
		if not os.path.isfile(path):
			return {} # --> Setup-Mode

		try:
			with open(path, "r", encoding="utf-8") as f:
				return json.load(f)
		except Exception:
			return {} # --> Setup-Mode
	
	
	def export_users(self, users):
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.send_header(
			"Content-Disposition",
			f'attachment; filename="users-backup-{date.today()}.json"'
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
			path = os.path.join(base, "..", "web", filename)
			with open(path, "rb") as f:
				data = f.read()
			self.send_response(200)
			self.send_header("Content-Type", content_type)
			self.end_headers()
			self.wfile.write(data)
		except FileNotFoundError:
			self.send_error(404, f"{filename} nicht gefunden")


	# calculate data for UVI
	def handle_uvi_request(self, start, end, path, user_data):
		print(f"[UVI] Anfrage: {start} bis {end}, Pfad: {path}")
		result = evaluate_uvi(
			json_path  = path,
			registry   = self.registry,
			start_date = start,
			end_date   = end,
			flat       = user_data.get("flat")
		)
		return serialize_monthly_results(result)
	

	# calculate aggregates data for UVI
	def handle_uvi_aggregate_request(self, start, end, path):
		result = evaluate_uvi_aggregated(
			json_path  = path,
			registry   = self.registry,
			start_date = start,
			end_date   = end
		)
		return serialize_monthly_aggregates(result)
	

	def restrict_period_by_user(self, start, end, user_data):
		start_date = datetime.strptime(start, "%Y-%m-%d")
		end_date   = datetime.strptime(end,   "%Y-%m-%d")

		move_in  = user_data.get("move_in")
		move_out = user_data.get("move_out")

		if move_in:
			move_in_date = datetime.strptime(move_in, "%Y-%m-%d")
			start_date = max(start_date, move_in_date)

		if move_out:
			move_out_date = datetime.strptime(move_out, "%Y-%m-%d")
			end_date = min(end_date, move_out_date)

		if start_date > end_date:
			return None, None

		return (
			start_date.strftime("%Y-%m-%d"),
			end_date.strftime("%Y-%m-%d")
		)


	def handle_uvi_combined_request(self, start, end, path, user_data):
		start, end = self.restrict_period_by_user(start, end, user_data)
		if not start:
			return {"details": [], "house_norm": [], "area": {}	}
		
		all_results = evaluate_uvi(
			json_path  = path,
			registry   = self.registry,
			start_date = start,
			end_date   = end,
			flat       = None
		)

		user_flat = user_data.get("flat")

		if user_flat is None:
			details = all_results # admin can see all values
		else:
			details = [           # normal user only his values
				r for r in all_results
				if r.flat == user_flat
			]

		# aggregated house values
		from collections import defaultdict
		sums = defaultdict(int)

		for r in all_results:
			sums[(r.month, r.type)] += r.consumption

		# areas
		users = self.load_users()

		# Build flat → area dictionary
		all_areas = {
			u.get("flat"): u.get("area", 0)
			for u in users.values()
			if u.get("flat")
		}
		total_area = sum(all_areas.values())

		house_norm = []
		for (month, type_), consumption in sorted(sums.items()):
			if total_area > 0:
				consumption = consumption / total_area
			else:
				consumption = 0

			house_norm.append({
				"month": month,
				"type": type_,
				"consumption": round(consumption, 2)
			})

		if user_flat is None:
			area = all_areas # Admin → all flats
		else:
			area = {         # Normal user → only his flat
				user_flat: user_data.get("area", 0)
			}

		return {
			"details": serialize_monthly_results(details),
			"house_norm": house_norm,
			"area": area
		}


	def handle_import_upload(self):
		form = cgi.FieldStorage(
			fp=self.rfile,
			headers=self.headers,
			environ={"REQUEST_METHOD": "POST"}
		)

		if "file" not in form or "password" not in form:
			self.send_error(400, "Datei oder Passwort fehlt")
			return

		fileitem = form["file"]
		password = form.getvalue("password")

		filename = os.path.basename(fileitem.filename)

		with tempfile.NamedTemporaryFile(delete=False, suffix=".xlsx") as tmp:
			tmp.write(fileitem.file.read())
			tmp_path = tmp.name

		out_name = os.path.splitext(filename)[0] + ".json.enc"

		try:
			result = import_and_encrypt(tmp_path, password, out_name)
			self.send_json({
				"status": "ok",
				"output": out_name,
				**result
			})
		except Exception as e:
			self.send_error(500, str(e))
		finally:
			try:
				os.remove(tmp_path)
			except OSError:
				pass
