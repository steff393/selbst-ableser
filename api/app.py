from fastapi import FastAPI, HTTPException
from fastapi.responses import RedirectResponse, JSONResponse
from .routes import get_router


def create_app(cfg, registry):
	app = FastAPI(
		title="selbst-ableser",
		docs_url=None,
		redoc_url=None,
		openapi_url=None
	)

	# create router with context (parameters)
	router = get_router(cfg, registry)
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
