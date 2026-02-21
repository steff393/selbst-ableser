import os
import json
from datetime import date, datetime

from fastapi import APIRouter, Request, HTTPException
from fastapi.responses import FileResponse, RedirectResponse

# to be removed ??
from meterRegistry import MeterRegistry
from meterReader import decrypt
# ----------------

def get_router(cfg, registry: MeterRegistry, store, limiter):
	router = APIRouter()	

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

	def loadSnapshot(snapDate: str):
		filename = os.path.join(cfg['SnapshotDir'], snapDate.isoformat() + ".json")
		if not os.path.isfile(filename):
			raise HTTPException(status_code=404, detail="Snapshot not found")

		with open(filename, "r", encoding="utf-8") as f:
			data = json.load(f)
		for v in data.values():
			if isinstance(v.get("wmbus"), str):    # convert wmbus from hex string to bytes (to match live data format)
				v["wmbus"] = bytes.fromhex(v["wmbus"])
		return data

	def build_payload(source_data):
		payload = {}
		for meter_nr, data in source_data.items():
			meterCfg = registry.get_meter(meter_nr)
			aes_key = meterCfg.aes_key if meterCfg else None
			if aes_key:
				wmbus = decrypt(data["wmbus"].hex(), aes_key)
			else: 
				wmbus = data["wmbus"].hex()
			payload[meter_nr] = {
				"timestamp": data["timestamp"],
				"rssi": data["rssi"],
				"wmbus": wmbus,
			}
		return payload

	# -----------------------------------------------------------------------------
	# Static files
	# -----------------------------------------------------------------------------

	@router.get("/favicon.ico")
	@limiter.limit("60/minute")
	def favicon(request: Request):
		return serve_file("favicon.ico", "image/svg+xml")
	
	@router.get("/")
	@limiter.limit("60/minute")
	def root(request: Request):
		return serve_file("wmbus.html", "text/html")

	# -----------------------------------------------------------------------------
	# API
	# -----------------------------------------------------------------------------
	
	@router.get("/data")
	@router.get("/data/{snapDate}")
	@limiter.limit("60/minute")
	def data(request: Request, snapDate: date | None = None):
		if snapDate is None:
			if store is None:
				return {}
			source_data = store.get_all()
		else:
			source_data = loadSnapshot(snapDate)
		return build_payload(source_data)
	
	@router.get("/load/{snapDate}")
	@limiter.limit("60/minute")
	def load(request: Request, snapDate: date | None = None):
		store.load_snapshot_file(snapDate.isoformat() + ".json")
		return RedirectResponse(url="/", status_code=303)
	
	@router.get("/list")
	@limiter.limit("60/minute")
	def list(request: Request):
		files = []
		if os.path.isdir(cfg['SnapshotDir']):
			for f in sorted(os.listdir(cfg['SnapshotDir'])):
				if f.endswith(".json"):
					files.append(f[:-5])
		return files
	
	@router.get("/snapshot")
	@limiter.limit("2/minute")
	def snap(request: Request, snapDate: date | None = None):
		store.make_snapshot(force=True)
		return ({
				"status": "ok",
				"snapshot": datetime.now().strftime("%Y-%m-%d")
			})

	# -----------------------------------------------------------------------------
	# Return the complete router
	# -----------------------------------------------------------------------------

	return(router)
