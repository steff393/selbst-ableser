import json
from datetime import date
from dataclasses import dataclass
from typing import Dict


@dataclass
class Email:
	_file: str = "email.json"

	# =========================================================================
	# service methods
	# =========================================================================
	
	@classmethod
	def load_from_file(cls) -> Dict[str, dict]:
		try:
			with open(cls._file, "r", encoding="utf-8") as f:
				return json.load(f)
		except Exception:
			return {}
	
	@classmethod
	def save_to_file(cls, data: Dict[str, dict]):
		with open(cls._file, "w", encoding="utf-8") as f:
			json.dump(data, f, indent=2, ensure_ascii=False)
	
	@classmethod
	def export(cls) -> tuple[str, bytes]:
		"""Create export for backup"""
		raw_data = cls.load_from_file()
		filename = f"email-backup-{date.today()}.json"
		content = json.dumps(raw_data, indent=2, ensure_ascii=False).encode("utf-8")
		return filename, content
