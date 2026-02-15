import os
import secrets
from datetime import datetime, timedelta

from fastapi import APIRouter, Request, UploadFile, File, Form, Depends, HTTPException
from fastapi.responses import JSONResponse, FileResponse, RedirectResponse, Response

from .users import User
from .uvi_service import UviService
from .importer import import_and_encrypt


session_store = {}
SESSION_LIFETIME = timedelta(hours=2)


def get_router(cfg, registry):
	router = APIRouter()
	User.set_userfile(cfg["Userfile"])

	uvi_service = UviService(cfg["SnapshotDir"], registry)
	
	def require_login(request: Request) -> User:
		token = request.cookies.get("session_token")
		if not token or token not in session_store:
			raise HTTPException(status_code=401)

		session = session_store[token]

		created = datetime.fromisoformat(session["created"]) # check for expiry
		if datetime.utcnow() - created > SESSION_LIFETIME:
			del session_store[token]
			raise HTTPException(status_code=401)

		user_token = session["user"]
		user = User.get(user_token)
		if not user:
			raise HTTPException(status_code=401)

		return user

	
	def require_admin(request: Request) -> User:
		user = require_login(request)
		if not user.is_admin:
			raise HTTPException(status_code=403)
		return user
	

	def require_csrf(request: Request):
		session_token = request.cookies.get("session_token")
		if not session_token or session_token not in session_store:
			raise HTTPException(status_code=401)

		session = session_store[session_token]
		csrf_session = session.get("csrf")
		csrf_header = request.headers.get("X-CSRF-Token")

		if not csrf_session or not csrf_header:
			raise HTTPException(status_code=403)

		if csrf_session != csrf_header:
			raise HTTPException(status_code=403)


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
	
	@router.get("/")
	def root(request: Request):
		require_admin(request)
		return serve_file("index.html", "text/html")

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
	# Login / Logout
	# -----------------------------------------------------------------------------

	@router.post("/login")
	async def login(request: Request):
		data = await request.json()
		token = data.get("token")

		if not User.exists(token):
			raise HTTPException(status_code=403, detail="Ungültiger Token")

		session_token = secrets.token_urlsafe(32)
		csrf_token = secrets.token_urlsafe(32)
		session_store[session_token] = {
			"user": token,
			"created": datetime.utcnow().isoformat(),
			"csrf": csrf_token
		}
		response = JSONResponse({"status": "ok"})
		response.set_cookie(
			key="session_token",
			value=session_token,
			httponly=True,
			samesite="lax",
			max_age=7200,         # 2 hours
			path="/"
		)
		response.set_cookie(
			key="csrf_token",
			value=csrf_token,
			secure=True,
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
	# Users (Admin)
	# -----------------------------------------------------------------------------

	@router.get("/users/data")
	def users_data(user: User = Depends(require_admin)):
		return User.load_from_file()
	

	@router.post("/users/save")
	async def users_save(request: Request, user: User = Depends(require_admin)):
		require_csrf(request)
		data = await request.json()
		User.save_to_file(data)
		return {"status": "ok"}
	

	@router.get("/users/export")
	def users_export(user: User = Depends(require_admin)):
		filename, content = User.export()
		
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

	@router.get("/eval")
	def eval_uvi(
		start: str = "2024-01-31",
		end: str = "2026-01-31",
		current_user: User = Depends(require_login)
	):
		return uvi_service.evaluate_for_user(current_user, start, end)
	
	# -----------------------------------------------------------------------------
	# Import Upload (Admin only)
	# -----------------------------------------------------------------------------

	@router.post("/import/upload")
	async def import_upload(
		request: Request,
		file: UploadFile = File(...),
		password: str = Form(...),
		user=Depends(require_admin)
	):
		require_csrf(request)
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
