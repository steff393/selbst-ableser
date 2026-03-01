import json
import datetime
import threading
from dataclasses import dataclass
from typing import Dict, List, Optional

from .crypto import decrypt, WrongPassword
from .keywaiter import wait_for_key_secure


@dataclass(frozen=True)
class MeterConfig:
	id:         str
	location:   str
	flat:       str
	room:       str
	type:       str
	startDate:  datetime.date
	startValue: int
	finalValue: Optional[int] = None
	cutoffDate: Optional[str] = None
	aes_key:    Optional[str] = None
	kc_factor:  Optional[int] = None
	blockMsg:   Optional[str] = None


class MeterRegistry:
	def __init__(self, json_path: str, password: Optional[str] = None, key_port: Optional[int] = None):
		self._path = json_path
		self._key_port = key_port
		self._unlocked = False
		self._lock = threading.RLock()
		self._meters: List[MeterConfig] = []
		self._by_loc: Dict[str, List[MeterConfig]] = {}  # Cache -> sortierte Zählerliste

		if password:
			self._unlock(password) # Password via command line
		else:
			if not self._try_load_plaintext(): # check if plaintext
				# open socket to receive password
				threading.Thread(target=self.registry_unlock_worker, daemon=True).start()


	def registry_unlock_worker(self):
		while True:
			try:
				password = wait_for_key_secure(self._key_port)
				ok = self._unlock(password)
				if ok:
					break
			except RuntimeError as e:
				print(f"Warnung: {e}")
				break
		
	def _try_load_plaintext(self) -> bool:
		try:
			with open(self._path, "r", encoding="utf-8") as f:
				cfg = json.load(f)
			self._load_from_cfg(cfg)
			return True
		except Exception:
			return False


	def _unlock(self, password: Optional[str] = None):
		try:
			with open(self._path, "rb") as f:
				enc = f.read()

			plain = decrypt(enc, password)
			cfg = json.loads(plain.decode("utf-8"))
			self._load_from_cfg(cfg)
			self._unlocked = True
			print("Registry entschlüsselt")
			return True
		except WrongPassword:
			print("Falsches Passwort – Registry gesperrt")
			return False


	def is_unlocked(self) -> bool:
		return self._unlocked


	def _load_from_cfg(self, cfg: dict):
		with self._lock:
			self._meters.clear()
			self._by_loc.clear()

			for loc in cfg.get("locations", []):
				for meter in loc.get("meter", []):
					self._meters.append(
						MeterConfig(
							id         = meter["id"],
							location   = loc["location"],
							flat       = loc["flat"],
							room       = loc["room"],
							type       = loc["type"],
							startDate  = datetime.date.fromisoformat(meter["startDate"]),
							startValue = meter.get("anfangsstand", 0),
							finalValue = meter.get("endstand"),
							cutoffDate = meter.get("stichtag"),
							aes_key    = meter.get("aes_key"),
							kc_factor  = meter.get("kcfaktor"),
							blockMsg   = meter.get("blockMsg"),
						)
					)


	def get_meter(self, meter_id: str) -> Optional[MeterConfig]:
		with self._lock:
			for m in self._meters:
				if m.id == meter_id:
					return m
			return None


	def all_locations(self, flat = None) -> List[str]:
		"""
		Gibt alle Zählerplätze zurück (ohne weitere Infos)
		"""
		with self._lock:
			return sorted(
				{m.location 
				for m in self._meters
				if flat is None or m.flat == flat # if flat=None => return all, else => return only the locations for the flat
			})


	def meters_for_location(self, location: str) -> List[MeterConfig]:
		"""
		Alle Zähler eines Platzes, nach Startdatum aufsteigend sortiert
		"""
		with self._lock:
			if location not in self._by_loc:
				meters = [m for m in self._meters if m.location == location]
				meters.sort(key=lambda m: m.startDate)
				self._by_loc[location] = meters
			return self._by_loc[location]


	def active_meter(self, location: str, date: datetime.date) -> Optional[MeterConfig]:
		"""
		Gibt den Zähler zurück, der an diesem Datum aktiv war
		"""
		with self._lock:
			active = None
			for meter in self.meters_for_location(location):
				if meter.startDate <= date:
					active = meter
				else:
					break
			return active


	def is_blocked(self, meter_id: str, wmbus: bytes) -> bool:
		with self._lock:
			meter = self.get_meter(meter_id)
			if not meter or not meter.blockMsg:
				return False
			return f"{wmbus[0]:02X}" == meter.blockMsg.upper()


	def print(self):
		with self._lock:
			print("MeterRegistry")
			print("=" * 60)

			for location in self.all_locations():
				print(f"Zählerplatz {location}")
				print("-" * 60)

				for m in self.meters_for_location(location):
					end = m.finalValue if m.finalValue is not None else "—"
					cutoff = m.cutoffDate or "—"

					print(
						f"  ID {m.id:>10} | "
						f"Start: {m.startDate.isoformat()} | "
						f"Anfang: {m.startValue:>6} | "
						f"Ende: {end:>6} | "
						f"Raum: {m.room}"
					)
				print()
