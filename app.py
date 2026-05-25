"""Unified entry point for selbst-ableser.

One FastAPI app, one uvicorn command (`uvicorn app:app --port 8282`).
Mode in cfg.ini decides which sub-systems and routes are wired up:

  local      = admin UI + USB receiver + decryption (single host)
  evaluator  = admin UI + decryption; receives snapshots from a remote collector
  collector  = USB receiver + (optional) upload; no admin UI, no AES keys
"""

import os
import json
import secrets
import threading
import time
import logging
import configparser
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import RedirectResponse, JSONResponse
from fastapi.middleware.trustedhost import TrustedHostMiddleware
from slowapi import Limiter, _rate_limit_exceeded_handler
from slowapi.util import get_remote_address
from slowapi.errors import RateLimitExceeded
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request as StarletteRequest
from uvicorn.middleware.proxy_headers import ProxyHeadersMiddleware


# ============================================================================
# 1. Configuration
# ============================================================================
BASE_DIR = Path(__file__).parent
cfg_file = BASE_DIR / 'cfg.ini'
if not cfg_file.exists():
	raise FileNotFoundError(f"cfg.ini nicht gefunden in {BASE_DIR}")

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read(cfg_file)
cfg = config['Configuration']

VALID_MODES = ('local', 'collector', 'evaluator')
MODE_DESCRIPTIONS = {
	'local':     'Einzelplatz – alle Komponenten in einem Prozess',
	'collector': 'Nur Sammler – Telegramme empfangen und (optional) hochladen',
	'evaluator': 'Nur Auswertung – Snapshots empfangen, entschlüsseln, Web-UI',
}
mode = cfg.get('Mode', 'local').strip().lower()
if mode not in VALID_MODES:
	raise ValueError(f"Ungültiger Mode '{mode}' in cfg.ini. Erlaubt: {', '.join(VALID_MODES)}")
cfg['Mode'] = mode
print(f"[selbst-ableser] Mode={mode} — {MODE_DESCRIPTIONS[mode]}")

FEATURE_ADMIN = mode in ('local', 'evaluator')   # admin UI + decryption + UVI
FEATURE_WMBUS = mode in ('local', 'collector')   # USB receiver + live dashboard

# Resolve relative paths to absolute so downstream modules don't have to care
cfg['SnapshotDir'] = str(BASE_DIR / cfg['SnapshotDir'])
Path(cfg['SnapshotDir']).mkdir(parents=True, exist_ok=True)
cfg['Userfile']     = str(BASE_DIR / cfg['Userfile']) if FEATURE_ADMIN else ''
cfg['Locationfile'] = str(BASE_DIR / cfg['Locationfile']) if cfg.get('Locationfile') else ''
cfg['Blocklistfile'] = str(BASE_DIR / cfg['Blocklistfile']) if cfg.get('Blocklistfile') else ''


# ============================================================================
# 2. Admin / evaluator subsystem (users + decryption registry)
# ============================================================================
registry = None
if FEATURE_ADMIN:
	if not Path(cfg['Userfile']).exists():
		print("Userfile nicht gefunden – erstelle neue Datei...")
		token = secrets.token_hex(8)
		with open(cfg['Userfile'], "w", encoding="utf-8") as f:
			json.dump({token: {"flat": None}}, f, indent=2)
		print("========================================")
		print("Admin-Token erzeugt:")
		print(token)
		print("Bitte sicher notieren bzw. speichern!")
		print("========================================")

	from meterRegistry import MeterRegistry
	registry = MeterRegistry(
		cfg['Locationfile'],
		password=os.getenv("LOCATION_PW"),
		key_port=int(cfg.get('KeyPortMain', 0)) or None)

	# Weekly health-alert mailer (Monday 08:00 local). Reads SMTP creds from
	# email.json. Silently no-ops if no creds configured or registry is locked.
	from api.health import MeterHealthService
	from api.health_mailer import start_background as start_health_mailer
	start_health_mailer(MeterHealthService(cfg, registry))


# ============================================================================
# 3. Collector subsystem (USB receiver + FrameStore + snapshot scheduler)
# ============================================================================
frame_store = None
if FEATURE_WMBUS:
	from wmBus import WMBusReceiver
	from frame_store import FrameStore
	from wmbusBlocklist import BlockList

	frame_store = FrameStore(cfg)
	blocklist = BlockList(cfg['Blocklistfile'])
	frame_store.start_scheduler()

	def wmbusComm():
		port = cfg.get('Port', '').strip()
		if not port:
			print("Kein Port konfiguriert")
			while True:
				try:
					time.sleep(30)
				except KeyboardInterrupt:
					return
		while True:
			try:
				iu891a = WMBusReceiver(port)
				iu891a.init_stick()
				iu891a.get_device_info()
				iu891a.set_config()
				for meter_id, rssi, wmbus in iu891a.frames():
					if blocklist.is_blocked(meter_id, wmbus):
						print(f"Telegramm von {meter_id} blockiert")
						continue
					frame_store.update(meter_id, rssi, wmbus)
			except KeyboardInterrupt:
				print("Beendet.")
				break
			except Exception as e:
				print(f"Fehler: {e}")
				time.sleep(30)

	threading.Thread(target=wmbusComm, daemon=True).start()


# ============================================================================
# 4. Logging
# ============================================================================
handlers = [logging.StreamHandler()]
if os.getenv("DEPLOYMENT_ENV", "development") == "production":
	handlers.insert(0, logging.FileHandler("audit.log"))

logging.basicConfig(
	level=logging.INFO,
	format="%(asctime)s  %(levelname)-8s  %(name)s  %(message)s",
	datefmt="%Y-%m-%d %H:%M:%S",
	handlers=handlers
)


# ============================================================================
# 5. Middleware
# ============================================================================
class SecurityHeadersMiddleware(BaseHTTPMiddleware):
	async def dispatch(self, request, call_next):
		response = await call_next(request)
		response.headers["X-Content-Type-Options"] = "nosniff"
		response.headers["X-Frame-Options"] = "DENY"
		response.headers["X-XSS-Protection"] = "1; mode=block"
		response.headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
		response.headers["Content-Security-Policy"] = (
			"default-src 'self'; "
			"script-src 'self' 'unsafe-inline'; "
			"style-src 'self' 'unsafe-inline'; "
			"img-src 'self' data:; "
			"object-src 'none'; "
			"frame-ancestors 'none'"
		)
		return response


class RequestSizeLimitMiddleware(BaseHTTPMiddleware):
	def __init__(self, app, max_request_size: int):
		super().__init__(app)
		self.max_request_size = max_request_size

	async def dispatch(self, request: StarletteRequest, call_next):
		content_length = request.headers.get("content-length")
		if content_length and int(content_length) > self.max_request_size:
			return JSONResponse(status_code=413, content={"detail": "File too large"})
		return await call_next(request)


# ============================================================================
# 6. FastAPI app
# ============================================================================
app = FastAPI(
	title="selbst-ableser",
	docs_url=None,
	redoc_url=None,
	openapi_url=None,
)

if os.getenv("DEPLOYMENT_ENV", "development") == "production":
	app.add_middleware(
		TrustedHostMiddleware,
		allowed_hosts=[
			"selbst-ableser.de",
			"app.selbst-ableser.de",
			"www.selbst-ableser.de",
		]
	) # else: don't use TrustedHost during development

# Global upload limit
app.add_middleware(RequestSizeLimitMiddleware, max_request_size=500 * 1024)  # 500kB
# Security Headers Middleware
app.add_middleware(SecurityHeadersMiddleware)
# Proxy Headers Middleware (for correct IP-detection (rate limits) behind Reverse Proxy)
app.add_middleware(ProxyHeadersMiddleware, trusted_hosts=["127.0.0.1"])

# SlowAPI Limiter
limiter = Limiter(key_func=get_remote_address)
app.state.limiter = limiter
app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)


# Single mode endpoint exposed at root for the UI to discover what this
# server can do. Public on purpose — same information as the page title.
@app.get("/mode")
@limiter.limit("60/minute")
def get_mode(request: Request):
	return {
		"mode": mode,
		"features": {
			"admin": FEATURE_ADMIN,
			"wmbus": FEATURE_WMBUS,
		}
	}


if FEATURE_ADMIN:
	from api.routes import get_router as get_admin_router
	app.include_router(get_admin_router(cfg, registry, limiter))

if FEATURE_WMBUS:
	from wmbusServer.routes import get_router as get_wmbus_router
	app.include_router(get_wmbus_router(cfg, registry, frame_store, limiter), prefix="/wmbus")

# In collector-only mode there's no admin landing page — redirect root.
if FEATURE_WMBUS and not FEATURE_ADMIN:
	@app.get("/", include_in_schema=False)
	def _collector_root():
		return RedirectResponse(url="/wmbus/", status_code=302)


@app.exception_handler(HTTPException)
async def custom_http_exception_handler(request, exc):
	if exc.status_code == 401:
		accept = request.headers.get("accept", "")
		if "text/html" in accept:
			return RedirectResponse("/login.html", status_code=302)
		return JSONResponse(status_code=401, content={"detail": "Unauthorized"})
	return JSONResponse(status_code=exc.status_code, content={"detail": exc.detail})
