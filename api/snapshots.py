import os
import re
import zipfile
from datetime import datetime, timezone
from io import BytesIO

from fastapi import HTTPException
from fastapi.responses import FileResponse, StreamingResponse


class SnapshotService:
	def __init__(self, cfg):
		self.cfg = cfg
		self.max_file_size = 512 * 1024  # 512KB per file
		self.max_total_size = 10 * 1024 * 1024  # 10MB total for ZIP

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
	