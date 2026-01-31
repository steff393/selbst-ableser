import json
import datetime
from dataclasses import dataclass
from typing import Dict, List, Optional


@dataclass(frozen=True)
class MeterConfig:
	id:         str
	location:   str
	flat:       str
	room:       str
	#type:       str
	startDate:  datetime.date
	startValue: int
	finalValue: Optional[int] = None
	cutoffDate: Optional[str] = None
	aes_key:    Optional[str] = None
	kc_factor:  Optional[int] = None
	blockMsg:   Optional[str] = None


class MeterRegistry:
	def __init__(self, json_path: str):
		# flache Ablage aller Zähler
		self._meters: List[MeterConfig] = []
		# Cache: platz -> sortierte Zählerliste
		self._by_loc: Dict[str, List[MeterConfig]] = {}

		with open(json_path, "r", encoding="utf-8") as f:
			cfg = json.load(f)

		for loc in cfg.get("zaehlerplaetze", []):
			for meter in loc.get("zaehler", []):
				self.add_meter(
					MeterConfig(
						id         = meter["id"],
						location   = loc["platz"],
						flat       = loc["whg"],
						room       = loc["raum"],
						#type     = loc["type"],
						startDate  = datetime.date.fromisoformat(meter["start"]),
						startValue = meter.get("anfangsstand", 0),
						finalValue = meter.get("endstand"),
						cutoffDate = meter.get("stichtag"),
						aes_key    = meter.get("aes_key"),
						kc_factor  = meter.get("kcfaktor"),
						blockMsg   = meter.get("blockMsg"),
					)
				)


	def add_meter(self, meter: MeterConfig):
		self._meters.append(meter)
		self._by_loc.clear()  # Cache invalidieren


	def get_meter(self, meter_id: str) -> Optional[MeterConfig]:
		for m in self._meters:
			if m.id == meter_id:
				return m
		return None


	# ------------------------------------------------------------------
	# Öffentliche API
	# ------------------------------------------------------------------

	def all_locations(self) -> List[str]:
		"""
		Gibt alle Zählerplätze zurück (ohne weitere Infos)
		"""
		return sorted({m.location for m in self._meters})


	def meters_for_location(self, location: str) -> List[MeterConfig]:
		"""
		Alle Zähler eines Platzes, nach Startdatum aufsteigend sortiert
		"""
		if location not in self._by_loc:
			meters = [m for m in self._meters if m.location == location]
			meters.sort(key=lambda m: m.startDate)
			self._by_loc[location] = meters
		return self._by_loc[location]


	def active_meter(self, location: str, date: datetime.date) -> Optional[MeterConfig]:
		"""
		Gibt den Zähler zurück, der an diesem Datum aktiv war
		"""
		active = None
		for meter in self.meters_for_location(location):
			if meter.startDate <= date:
				active = meter
			else:
				break
		return active


	def previous_meter(self, location: str, date: datetime.date) -> Optional[MeterConfig]:
		"""
		Gibt den Zähler zurück, der VOR dem aktuell aktiven eingebaut war
		(nützlich bei Zählerwechseln)
		"""
		previous = None
		for meter in self.meters_for_location(location):
			if meter.startDate < date:
				previous = meter
			else:
				break
		return previous


	def is_blocked(self, meter_id: str, wmbus: bytes) -> bool:
		meter = self.get_meter(meter_id)
		if not meter or not meter.blockMsg:
			return False
		return f"{wmbus[0]:02X}" == meter.blockMsg.upper()
