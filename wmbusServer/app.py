import os
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import RedirectResponse, JSONResponse
from fastapi.middleware.trustedhost import TrustedHostMiddleware
from slowapi import Limiter, _rate_limit_exceeded_handler
from slowapi.util import get_remote_address
from slowapi.errors import RateLimitExceeded
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request as StarletteRequest
from .routes import get_router


class SecurityHeadersMiddleware(BaseHTTPMiddleware):
	async def dispatch(self, request, call_next):
		response = await call_next(request)
		response.headers["X-Content-Type-Options"] = "nosniff"
		response.headers["X-Frame-Options"] = "DENY"
		response.headers["X-XSS-Protection"] = "1; mode=block"
		response.headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
		return response
	

class RequestSizeLimitMiddleware(BaseHTTPMiddleware):
	def __init__(self, app, max_request_size: int):
		super().__init__(app)
		self.max_request_size = max_request_size

	async def dispatch(self, request: StarletteRequest, call_next):
		content_length = request.headers.get("content-length")
		if content_length and int(content_length) > self.max_request_size:
			return JSONResponse(status_code=413, content={"detail": "File too large"}			)
		return await call_next(request)


# main function
def create_app(cfg, registry, store):
	app = FastAPI(
		title="selbst-ableser",
		docs_url=None,
		redoc_url=None,
		openapi_url=None
	)

	if os.getenv("DEPLOYMENT_ENV", "development") == "production":
		app.add_middleware(
			TrustedHostMiddleware,
			allowed_hosts=[
				"selbst-ableser.de",
				"www.selbst-ableser.de",
			]
		) # else: don't use TrustedHost during development
	
	# Global upload limit
	app.add_middleware(RequestSizeLimitMiddleware, max_request_size=500 * 1024)  # 500kB

	# Security Headers Middleware
	app.add_middleware(SecurityHeadersMiddleware)

	# SlowAPI Limiter
	limiter = Limiter(key_func=get_remote_address)
	app.state.limiter = limiter
	app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)

	# create router with context (parameters)
	router = get_router(cfg, registry, store, limiter)
	app.include_router(router)

	@app.exception_handler(HTTPException)
	async def custom_http_exception_handler(request, exc):
		if exc.status_code == 401:
			accept = request.headers.get("accept", "")
			# Browser erwartet HTML → Redirect
			if "text/html" in accept:
				return RedirectResponse("/login.html", status_code=302)
			# API → JSON
			return JSONResponse(
				status_code=401,
				content={"detail": "Unauthorized"}
			)
		return JSONResponse(
			status_code=exc.status_code,
			content={"detail": exc.detail}
		)
	
	return app
