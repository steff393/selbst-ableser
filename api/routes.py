import os
import secrets
from datetime import datetime, timezone
from cachetools import TTLCache

from fastapi import APIRouter, Request, UploadFile, File, Form, Depends, HTTPException
from fastapi.responses import JSONResponse, FileResponse, RedirectResponse, Response

from .users import User
from .email import Email
from .uvi_service import UviService
from .importer import import_and_encrypt


SESSION_LIFETIME_SECONDS = 7200  # 2 hours
session_store = TTLCache(maxsize=50, ttl=SESSION_LIFETIME_SECONDS) # max 50 sessions at same time

def get_router(cfg, registry, limiter):
	router = APIRouter()
	User.set_userfile(cfg["Userfile"])

	uvi_service = UviService(cfg["SnapshotDir"], registry)
	
	def require_login(request: Request) -> User:
		token = request.cookies.get("session_token")
		if not token or token not in session_store:
			raise HTTPException(status_code=401)

		session = session_store[token]
		user = User.get(session["user"])
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
		import re
		if not re.match(r'^[\w\-\.]+$', filename): # accept only alphanumeric, dot and minus
			raise HTTPException(status_code=400, detail="Invalid filename")
		
		base = os.path.dirname(os.path.abspath(__file__))
		path = os.path.join(base, "..", "web", filename)
		web_dir = os.path.abspath(os.path.join(base, "..", "web"))
		resolved_path = os.path.abspath(path)

		if not resolved_path.startswith(web_dir):
			raise HTTPException(status_code=403, detail="Access rejected")
		if not os.path.exists(path):
			raise HTTPException(status_code=404, detail=f"{filename} not found")

		return FileResponse(path, media_type=content_type)


	# -----------------------------------------------------------------------------
	# Static files
	# -----------------------------------------------------------------------------

	@router.get("/favicon.ico")
	@limiter.limit("20/minute")
	def favicon(request: Request):
		return serve_file("favicon.ico", "image/svg+xml")

	@router.get("/chart.umd.min.js")
	@limiter.limit("20/minute")
	def chart_js(request: Request):
		return serve_file("chart.umd.min.js", "text/javascript")
	
	@router.get("/")
	@limiter.limit("60/minute")
	def root(request: Request):
		require_admin(request)
		return serve_file("index.html", "text/html")

	PAGE_CONFIG = {
		"index": "admin",
		"users": "admin",
		"import": "admin",
		"uvi": "login",
		"email": "admin",
	}

	@router.get("/{page_name}.html")
	@limiter.limit("60/minute")
	def serve_html_page(page_name: str,	request: Request):
		# public pages
		if page_name in ["login", "impressum", "datenschutz"]:
				return serve_file(page_name + ".html", "text/html")

		# protected pages
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
	@limiter.limit("10/minute")
	async def login(request: Request, response: Response):
		data = await request.json()
		token = data.get("token")

		if not User.exists(token):
			raise HTTPException(status_code=403, detail="Ungültiger Token")

		session_token = secrets.token_urlsafe(32)
		csrf_token = secrets.token_urlsafe(32)
		session_store[session_token] = {
			"user": token,
			"created": datetime.now(timezone.utc).isoformat(), # not needed (because TTLCache takes care), but maybe useful for debugging
			"csrf": csrf_token
		}
		response = JSONResponse({"status": "ok"})
		response.set_cookie(
			key="session_token",
			value=session_token,
			httponly=True,
			secure=(request.url.scheme == "https"), # similar like below
			samesite="lax",
			max_age=7200,         # 2 hours
			path="/"
		)
		response.set_cookie(
			key="csrf_token",
			value=csrf_token,
			secure=(request.url.scheme == "https"), # CSRF token cookie doesn't need to be HttpOnly so JS can read it but it should only be marked secure when using HTTPS.
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
	@limiter.limit("20/minute")
	def users_data(request: Request, user: User = Depends(require_admin)):
		return User.load_from_file()
	

	@router.post("/users/save")
	@limiter.limit("10/minute")
	async def users_save(request: Request, user: User = Depends(require_admin)):
		require_csrf(request)
		data = await request.json()
		if not isinstance(data, dict):
			raise HTTPException(status_code=400, detail="Invalid format")
		for token, user_data in data.items():
			if not isinstance(user_data, dict):
				raise HTTPException(status_code=400, detail="Invalid user data")
		User.save_to_file(data)
		return {"status": "ok"}
	

	@router.get("/users/export")
	@limiter.limit("5/hour")
	def users_export(request: Request, user: User = Depends(require_admin)):
		filename, content = User.export()
		
		return Response(
			content=content,
			media_type="application/json",
			headers={
				"Content-Disposition": f'attachment; filename="{filename}"'
			}
		)

	# -----------------------------------------------------------------------------
	# Email (Admin)
	# -----------------------------------------------------------------------------

	@router.get("/email/data")
	@limiter.limit("20/minute")
	def email_data(request: Request, user: User = Depends(require_admin)):
		return Email.load_from_file()
	

	@router.post("/email/save")
	@limiter.limit("10/minute")
	async def email_save(request: Request, user: User = Depends(require_admin)):
		require_csrf(request)
		data = await request.json()
		if not isinstance(data, dict):
			raise HTTPException(status_code=400, detail="Invalid format")
		Email.save_to_file(data)
		return {"status": "ok"}
	

	@router.get("/email/export")
	@limiter.limit("5/hour")
	def email_export(request: Request, user: User = Depends(require_admin)):
		filename, content = Email.export()
		
		return Response(
			content=content,
			media_type="application/json",
			headers={
				"Content-Disposition": f'attachment; filename="{filename}"'
			}
		)

	@router.post("/email/test")
	@limiter.limit("10/hour")
	async def email_test(request: Request, user: User = Depends(require_admin)):
		require_csrf(request)
		data = await request.json()
		if not isinstance(data, dict):
			raise HTTPException(status_code=400, detail="Invalid format")
		
		# Validate required fields
		if not data.get("sender") or not data.get("password") or not data.get("to"):
			raise HTTPException(status_code=400, detail="Missing required email settings")
		
		# Send test email using the same logic as the email.py script
		import smtplib
		from email.mime.text import MIMEText
		
		try:
			with smtplib.SMTP_SSL("mail.gmx.net", 465) as server:
				server.login(data["sender"], data["password"])
				for recipient in data["to"]:
					msg = MIMEText("Test-E-Mail zur Überprüfung der E-Mail-Konfiguration", "plain", "utf-8")
					msg["From"] = data["sender"]
					msg["To"] = recipient
					msg["Subject"] = "Test-E-Mail von selbst-ableser"
					server.sendmail(data["sender"], recipient, msg.as_string())
		except Exception as e:
			raise HTTPException(status_code=500, detail=f"Failed to send test email: {str(e)}")
		
		return {"status": "ok"}

	# -----------------------------------------------------------------------------
	# UVI
	# -----------------------------------------------------------------------------

	@router.get("/eval")
	@limiter.limit("20/minute")
	def eval_uvi(
		request: Request,
		start: str = "2024-01-31",
		end: str = "2026-01-31",
		current_user: User = Depends(require_login)
	):
		return uvi_service.evaluate_for_user(current_user, start, end)
	
	# -----------------------------------------------------------------------------
	# Import Upload (Admin only)
	# -----------------------------------------------------------------------------
	
	@router.get("/locations/locked")
	@limiter.limit("60/minute")
	def locations_locked(request: Request, user: User = Depends(require_admin)):
		return ({"status": "unlocked" if registry.is_unlocked() else "locked"})
	
	@router.post("/locations/import")
	@limiter.limit("5/minute")
	async def import_upload(
		request: Request,
		file: UploadFile = File(...),
		password: str = Form(...),
		user=Depends(require_admin)
	):
		require_csrf(request)
		content = await file.read()
		result = import_and_encrypt(content, password, cfg["Locationfile"])
		return {
			"status": "ok",
			"output": cfg["Locationfile"],
			**result
		}
	
	@router.post("/locations/unlock")
	@limiter.limit("10/day")
	async def receive_key(request: Request, user=Depends(require_admin)):
		require_csrf(request)
		try:
			body = await request.json()
			key = body.get("key")
			if not key or len(key) > 100:
				raise HTTPException(status_code=400, detail="Kein gültiger Key übermittelt")
			# unlock the meterRegistry with the provided key
			if registry._unlock(key) == True:
				return {"status": "ok"}
			else:
				return {"status": "wrong password"}
		except Exception as e:
			return {"status": "error"}

	# -----------------------------------------------------------------------------
	# Return the complete router
	# -----------------------------------------------------------------------------

	return(router)
