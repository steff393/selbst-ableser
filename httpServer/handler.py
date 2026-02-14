import os
import json
from datetime import datetime, date
import cgi
from http.server import BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from meterReader import evaluate_uvi, serialize_monthly_results
from .importer import import_and_encrypt
import secrets
from .users_service import UsersService


session_store = {}


class Handler(BaseHTTPRequestHandler):
	def __init__(self, *args, cfg=None, registry=None, **kwargs):
		self.cfg = cfg
		self.registry = registry
		self.sessions = session_store
		self.users_service = UsersService(cfg['Userfile'])
		super().__init__(*args, **kwargs)

	
	def get_token_from_cookie(self):
		cookie_header = self.headers.get("Cookie")
		if not cookie_header:
				return None
		cookies = {}
		for part in cookie_header.split(";"):
				if "=" in part:
						k, v = part.strip().split("=", 1)
						cookies[k] = v
		return cookies.get("session_token")


	def do_GET(self):
		parsed = urlparse(self.path)
		params = parse_qs(parsed.query)
		subpath = parsed.path.lstrip("/")

		# serve public files
		if subpath in ("favicon.ico"):
			self.serve_file("favicon.ico", "image/svg+xml")
			return
		if subpath in ("login.html", "impressum.html", "datenschutz.html"):
			self.serve_file(subpath, "text/html")
			return
		if subpath in ("chart.umd.min.js"):
			self.serve_file(subpath, "text/javascript")
			return

		session_token = self.get_token_from_cookie()
		users = self.users_service.load_users()

		if self.users_service.is_setup_mode():
			# Only users or setup page / no token -> use self.path
			if self.path == "/users.html":
				self.serve_file("users.html", "text/html")
				return
			if self.path == "/users/data":
				self.send_json(users)
				return
			self.serve_file("index.html", "text/html")
			return

		if not session_token or session_token not in self.sessions:
			self.serve_file("login.html", "text/html")
			return
		# get data object of the user, eg. {"flat": "A1", ...}
		token = self.sessions[session_token]["user"]
		user_data = users.get(token)

		# Routing
		if subpath.startswith("eval"):
			response = self.handle_uvi_combined_request(
				params.get("start", ["2024-01-31"])[0],
				params.get("end",   ["2026-01-31"])[0],
				params.get("path",  [self.cfg['SnapshotDir']])[0],
				user_data
			)
			self.send_json(response)
			return
		
		if subpath in ("", "index.html"):
			self.serve_file("index.html", "text/html")
			return
		
		if subpath in ("uvi.html"):
			self.serve_file(subpath, "text/html")
			return
		

		# only for admins (no filter)
		if self.users_service.is_admin(token):
			if subpath in ("users.html", "users"):
				self.serve_file("users.html", "text/html")
				return

			if subpath == "users/data":
				self.send_json(users)
				return

			if subpath == "users/export":
				self.export_users()
				return
			
			if subpath == "import.html":
				self.serve_file("import.html", "text/html")
				return

		self.send_error(404, "Seite nicht gefunden")


	def do_POST(self):
		parsed = urlparse(self.path)
		subpath = parsed.path.lstrip("/")

		if subpath == "login":
			self.handle_login()
			return
		
		if self.users_service.is_setup_mode():
			if self.path == "/users/save":
				self.handle_users_save()
				return

		# only for admins
		session_token = self.get_token_from_cookie()
		if not session_token or session_token not in self.sessions:
			self.send_error(403, "Nicht angemeldet")
			return

		token = self.sessions[session_token]["user"]

		if not self.users_service.is_admin(token):
			self.send_error(403, "Nur für Admins")
			return

		if subpath == "users/save":
			self.handle_users_save()
			return
		
		if subpath == "import/upload":
			self.handle_import_upload()
			return

		self.send_error(404)


	def handle_login(self):
		length = int(self.headers.get("Content-Length", 0))
		data = json.loads(self.rfile.read(length))
		token = data.get("token")

		users = self.users_service.load_users()
		if token not in users:
			self.send_error(403, "Ungültiger Token")
			return

		# Session-Token generieren
		session_token = secrets.token_urlsafe(32)
		self.sessions[session_token] = {
			"user": token,
			"created": datetime.utcnow().isoformat()
		}

		# HttpOnly-Cookie setzen
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.send_header(
				"Set-Cookie",
				f"session_token={session_token}; HttpOnly; Path=/; SameSite=Lax"
		)
		self.end_headers()
		self.wfile.write(json.dumps({"status": "ok"}).encode())


	def handle_users_save(self):
		length = int(self.headers.get("Content-Length", 0))
		try:
			data = json.loads(self.rfile.read(length))
			self.users_service.save_users(data)
			self.send_json({"status": "ok"})
		except Exception as e:
			self.send_error(500, str(e))

	
	def export_users(self):
		filename, content = self.users_service.get_export_data()
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.send_header("Content-Disposition", f'attachment; filename="{filename}"')
		self.end_headers()
		self.wfile.write(content)


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


	# calculate data for UVI
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
		users = self.users_service.load_users()

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
		filename = os.path.splitext(os.path.basename(fileitem.filename))[0]

		try:
			result = import_and_encrypt(fileitem.file.read(), password, filename + ".json.enc")
			self.send_json({
				"status": "ok",
				"output": filename + ".json.enc",
				**result
			})
		except Exception as e:
			self.send_error(500, str(e))
