import os
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import RedirectResponse, JSONResponse
from fastapi.middleware.trustedhost import TrustedHostMiddleware
from slowapi import Limiter, _rate_limit_exceeded_handler
from slowapi.util import get_remote_address
from slowapi.errors import RateLimitExceeded
from .routes import get_router


def create_app(cfg, registry):
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

	# SlowAPI Limiter
	limiter = Limiter(key_func=get_remote_address)
	app.state.limiter = limiter
	app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)

	# create router with context (parameters)
	router = get_router(cfg, registry, limiter)
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
