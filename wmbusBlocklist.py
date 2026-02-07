import json
import threading
from pathlib import Path
from typing import Dict, List

# Example for blocklist.json
#{
#	"*": ["2F"],
#	"12345678": ["49AB", "34"]
#}


class BlockList:
	def __init__(self, filename: str):
		self._lock = threading.Lock()
		self._rules: Dict[str, List[str]] = {}
		if filename != "":
			self._load(filename)

	def _load(self, filename: str):
		path = Path(filename)
		if not path.exists():
			return

		with path.open("r", encoding="utf-8") as f:
			raw = json.load(f)

		with self._lock:
			self._rules.clear()
			for meter_id, prefixes in raw.items():
				self._rules[meter_id] = [
					p.upper() for p in prefixes
				]

	def is_blocked(self, meter_id: str, wmbus: bytes) -> bool:
		with self._lock:
			global_rules = self._rules.get("*", [])
			meter_rules  = self._rules.get(meter_id, [])

		if not global_rules and not meter_rules:
			return False

		hexdata = wmbus.hex().upper()

		for prefix in global_rules:
			if hexdata.startswith(prefix):
				return True

		for prefix in meter_rules:
			if hexdata.startswith(prefix):
				return True

		return False
