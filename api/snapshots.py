import os
import re
import zipfile
import json
import secrets
from datetime import datetime, timezone
from io import BytesIO

from fastapi import HTTPException
from fastapi.responses import FileResponse, StreamingResponse


class SnapshotService:
	UPLOAD_TOKEN_LENGTH = 32
	
	def __init__(self, cfg):
		self.cfg = cfg
		self.max_file_size = 512 * 1024  # 512KB per file
		self.max_total_size = 10 * 1024 * 1024  # 10MB total for ZIP
		self.token_file = "upload-token.txt"

	def _normalize_snapshot_name(self, name: str) -> str:
		if not isinstance(name, str) or not re.match(r'^[\w\-\.]+\.json$', name):
			raise HTTPException(status_code=400, detail="Invalid filename")
		return name

	def _is_path_safe(self, filepath: str, allowed_dir: str) -> bool:
		"""Check if filepath is within allowed_dir to prevent path traversal."""
		try:
			resolved_path = os.path.abspath(filepath)
			resolved_dir = os.path.abspath(allowed_dir)
			return resolved_path.startswith(resolved_dir + os.sep) or resolved_path == resolved_dir
		except (OSError, ValueError):
			return False

	def _snapshot_dir_for_source(self, source: str) -> str:
		source = source.lower()
		if source == "snapshot":
			return self.cfg.get("SnapshotDir", "")
		if source == "backup":
			return self.cfg.get("BackupDir", "")
		raise HTTPException(status_code=400, detail="Invalid snapshot source")

	def _list_files_from_dir(self, path: str, source: str) -> list[dict]:
		items = []
		if not path or not os.path.isdir(path):
			return items

		for filename in sorted(os.listdir(path)):
			if not filename.endswith(".json"):
				continue
			filepath = os.path.join(path, filename)
			if not os.path.isfile(filepath):
				continue
			stat = os.stat(filepath)
			items.append({
				"name": filename,
				"source": source,
				"size": stat.st_size,
				"modified": datetime.fromtimestamp(stat.st_mtime, timezone.utc).isoformat(),
			})
		return items

	def list_snapshots(self):
		snapshot_files = self._list_files_from_dir(self.cfg.get("SnapshotDir", ""), "snapshot")
		backup_files = self._list_files_from_dir(self.cfg.get("BackupDir", ""), "backup")
		return {
			"files": snapshot_files + backup_files,
			"counts": {
				"snapshot": len(snapshot_files),
				"backup": len(backup_files),
			}
		}

	def read_token(self) -> str:
		"""Read the upload token from file."""
		if not os.path.isfile(self.token_file):
			return ""
		try:
			with open(self.token_file, 'r', encoding='utf-8') as f:
				token = f.read().strip()
				return token if token and len(token) >= self.UPLOAD_TOKEN_LENGTH else ""
		except (OSError, IOError):
			return ""

	def create_token(self) -> str:
		"""Generate a new upload token and save it to file."""
		token = secrets.token_urlsafe(self.UPLOAD_TOKEN_LENGTH)
		try:
			with open(self.token_file, 'w', encoding='utf-8') as f:
				f.write(token)
		except (OSError, IOError) as e:
			raise HTTPException(status_code=500, detail=f"Failed to save token: {str(e)}")
		return token

	def _validate_json(self, content: bytes) -> dict:
		"""Validate that content is valid JSON."""
		try:
			return json.loads(content.decode('utf-8'))
		except (json.JSONDecodeError, UnicodeDecodeError) as e:
			raise HTTPException(status_code=400, detail=f"Invalid JSON format: {str(e)}")

	def handle_upload(self, content: bytes, upload_token: str, filename: str) -> dict:
		"""Handle snapshot upload with validation and security checks."""
		# Validate upload token
		if not upload_token or len(upload_token) < self.UPLOAD_TOKEN_LENGTH:
			raise HTTPException(status_code=401, detail="Invalid upload token")
		
		# Validate token against stored token:
		valid_token = self.read_token()
		if not valid_token or upload_token != valid_token:
			raise HTTPException(status_code=401, detail="Invalid or missing upload token")
		
		# Check file size
		if len(content) > self.max_file_size:
			raise HTTPException(status_code=413, detail=f"File too large (max {self.max_file_size} bytes)")
		
		# Validate JSON content
		json_data = self._validate_json(content)
		
		# Generate expected filename for today and verify it matches
		today = datetime.now(timezone.utc).date()
		expected_filename = f"{today.isoformat()}.json"
		if filename != expected_filename:
			raise HTTPException(status_code=400, detail=f"Filename must be {expected_filename}")
		
		# Check for path traversal
		snapshot_dir = self.cfg.get("SnapshotDir", ".")
		os.makedirs(snapshot_dir, exist_ok=True)
		filepath = os.path.join(snapshot_dir, filename)
		if not self._is_path_safe(filepath, snapshot_dir):
			raise HTTPException(status_code=403, detail="Access rejected")
		
		# Prevent overwriting existing files
		if os.path.isfile(filepath):
			raise HTTPException(status_code=409, detail="File already exists")
		
		# Write the file
		try:
			with open(filepath, 'wb') as f:
				f.write(content)
		except (OSError, IOError) as e:
			raise HTTPException(status_code=500, detail=f"Failed to save file: {str(e)}")
		
		return {
			"status": "ok",
			"filename": filename,
			"size": len(content)
		}

	def download_snapshots(self, files):
		if not isinstance(files, list) or not files:
			raise HTTPException(status_code=400, detail="No files selected")

		entries = []
		total_size = 0

		for item in files:
			if isinstance(item, dict):
				name = item.get("name")
				source = item.get("source")
			else:
				raise HTTPException(status_code=400, detail="Invalid file selection")

			name = self._normalize_snapshot_name(name)
			path_dir = self._snapshot_dir_for_source(source)
			if not path_dir or not os.path.isdir(path_dir):
				raise HTTPException(status_code=404, detail=f"Source directory not available: {source}")
			filepath = os.path.join(path_dir, name)
			if not os.path.isfile(filepath) or not self._is_path_safe(filepath, path_dir):
				raise HTTPException(status_code=404, detail=f"File not found: {name}")

			# Quick validation that it's a JSON file
			try:
				with open(filepath, 'r', encoding='utf-8') as f:
					f.read(1024)  # Read first 1KB to check if it's readable text
			except (UnicodeDecodeError, IOError):
				raise HTTPException(status_code=400, detail=f"Invalid file format: {name}")

			file_size = os.path.getsize(filepath)
			if file_size > self.max_file_size:
				raise HTTPException(status_code=413, detail=f"File too large: {name}")

			total_size += file_size
			if total_size > self.max_total_size:
				raise HTTPException(status_code=413, detail="Total size too large (max 10MB)")

			entries.append((filepath, source, name))

		if len(entries) == 1:
			filepath, source, name = entries[0]
			return FileResponse(
				filepath,
				media_type="application/json",
				headers={"Content-Disposition": f'attachment; filename="{name}"'}
			)

		zip_buffer = BytesIO()
		with zipfile.ZipFile(zip_buffer, mode="w", compression=zipfile.ZIP_DEFLATED) as zip_out:
			for filepath, source, name in entries:
				arc_name = f"{source}/{name}"
				zip_out.write(filepath, arcname=arc_name)
		zip_buffer.seek(0)

		return StreamingResponse(
			zip_buffer,
			media_type="application/zip",
			headers={"Content-Disposition": 'attachment; filename="snapshots.zip"'}
		)
	