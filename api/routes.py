import os
import secrets
from datetime import datetime
from collections import defaultdict
from typing import Optional

from fastapi import APIRouter, Request, Response, UploadFile, File, Form, Depends, HTTPException
from fastapi.responses import JSONResponse, FileResponse, RedirectResponse

from meterReader import evaluate_uvi, serialize_monthly_results
from .importer import import_and_encrypt
from .users_service import UsersService


session_store = {}

def get_router(cfg, registry):
	router = APIRouter()
	users_service = UsersService(cfg["Userfile"])

	# -----------------------------------------------------------------------------
	# Helper
	# -----------------------------------------------------------------------------
	
	def get_session_user_or_redirect(request: Request):
		token = request.cookies.get("session_token")

		if not token or token not in session_store:
			return None

		return session_store[token]["user"]


	def require_login(request: Request):
		token = request.cookies.get("session_token")
		if not token or token not in session_store:
			raise HTTPException(status_code=401)
		return session_store[token]["user"]


	def require_admin(request: Request):
		user = require_login(request)
		if not users_service.is_admin(user):
			raise HTTPException(status_code=403)
		return user


	def serve_file(filename: str, content_type: str):
		base = os.path.dirname(os.path.abspath(__file__))
		path = os.path.join(base, "..", "web", filename)

		if not os.path.exists(path):
			raise HTTPException(status_code=404, detail=f"{filename} nicht gefunden")

		return FileResponse(path, media_type=content_type)


	# -----------------------------------------------------------------------------
	# Static files
	# -----------------------------------------------------------------------------

	@router.get("/favicon.ico")
	def favicon():
		return serve_file("favicon.ico", "image/svg+xml")

	@router.get("/chart.umd.min.js")
	def chart_js():
		return serve_file("chart.umd.min.js", "text/javascript")

	PAGE_CONFIG = {
		"index": "admin",
		"users": "admin",
		"import": "admin",
		"uvi": "login",
	}

	@router.get("/{page_name}.html")
	def serve_html_page(
		page_name: str,
		request: Request
	):
		# öffentlich erlaubte Seiten
		if page_name in ["login", "impressum", "datenschutz"]:
				return serve_file(page_name + ".html", "text/html")

		# geschützte Seiten
		access = PAGE_CONFIG.get(page_name)
		if access == "admin":
				require_admin(request)
		elif access == "login":
				require_login(request)
		else:
				raise HTTPException(status_code=404)

		return serve_file(page_name + ".html", "text/html")

	# -----------------------------------------------------------------------------
	# Login
	# -----------------------------------------------------------------------------

	@router.post("/login")
	async def login(request: Request):
		data = await request.json()
		token = data.get("token")

		users = users_service.load_users()

		if token not in users:
			raise HTTPException(status_code=403, detail="Ungültiger Token")

		session_token = secrets.token_urlsafe(32)

		session_store[session_token] = {
			"user": token,
			"created": datetime.utcnow().isoformat()
		}
		response = JSONResponse({"status": "ok"})
		response.set_cookie(
			key="session_token",
			value=session_token,
			httponly=True,
			samesite="lax",
			path="/"
		)
		return response


	@router.get("/logout")
	def logout(request: Request):
		token = request.cookies.get("session_token")

		# Token aus Store entfernen
		if token and token in session_store:
				del session_store[token]

		# Redirect zurück auf Login + Cookie löschen
		response = RedirectResponse("/login.html", status_code=302)
		response.delete_cookie("session_token", path="/")
		return response

	# -----------------------------------------------------------------------------
	# Users (Setup + Admin)
	# -----------------------------------------------------------------------------

	@router.get("/users/data")
	def users_data(user=Depends(require_admin)):
		return users_service.load_users()


	@router.post("/users/save")
	async def users_save(request: Request, user=Depends(require_admin)):
		data = await request.json()
		users_service.save_users(data)
		return {"status": "ok"}


	@router.get("/users/export")
	def users_export(user=Depends(require_admin)):
		filename, content = users_service.get_export_data()

		return Response(
			content=content,
			media_type="application/json",
			headers={
				"Content-Disposition": f'attachment; filename="{filename}"'
			}
		)

	# -----------------------------------------------------------------------------
	# UVI
	# -----------------------------------------------------------------------------

	def restrict_period_by_user(start, end, user_data):
		start_date = datetime.strptime(start, "%Y-%m-%d")
		end_date = datetime.strptime(end, "%Y-%m-%d")

		move_in = user_data.get("move_in")
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


	@router.get("/eval")
	def eval_uvi(
		start: str = "2024-01-31",
		end: str = "2026-01-31",
		path: Optional[str] = None,
		user_token=Depends(get_session_user_or_redirect)
	):

		users = users_service.load_users()
		user_data = users.get(user_token)

		if path is None:
			path = cfg["SnapshotDir"]

		start, end = restrict_period_by_user(start, end, user_data)

		if not start:
			return {"details": [], "house_norm": [], "area": {}}

		all_results = evaluate_uvi(
			json_path=path,
			registry=registry,
			start_date=start,
			end_date=end,
			flat=None
		)

		user_flat = user_data.get("flat")

		if user_flat is None:
			details = all_results
		else:
			details = [r for r in all_results if r.flat == user_flat]

		sums = defaultdict(int)

		for r in all_results:
			sums[(r.month, r.type)] += r.consumption

		users = users_service.load_users()

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
			area = all_areas
		else:
			area = {
				user_flat: user_data.get("area", 0)
			}

		return {
			"details": serialize_monthly_results(details),
			"house_norm": house_norm,
			"area": area
		}
	

	# -----------------------------------------------------------------------------
	# Import Upload (Admin only)
	# -----------------------------------------------------------------------------

	@router.post("/import/upload")
	async def import_upload(
		file: UploadFile = File(...),
		password: str = Form(...),
		user=Depends(require_admin)
	):
		filename = os.path.splitext(os.path.basename(file.filename))[0]

		content = await file.read()

		result = import_and_encrypt(
			content,
			password,
			filename + ".json.enc"
		)

		return {
			"status": "ok",
			"output": filename + ".json.enc",
			**result
		}

	# -----------------------------------------------------------------------------
	# Return the complete router
	# -----------------------------------------------------------------------------

	return(router)
