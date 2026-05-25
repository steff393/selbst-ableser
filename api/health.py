"""Meter health: which configured meters haven't appeared in recent snapshots.

Snapshots-only — works in `local` and `evaluator` mode. For each configured
location we look at the currently-active meter and walk snapshot files
newest-first until we find one that contains its ID. The date of that
snapshot is the meter's `last_seen`.
"""

import os
import json
import re
from datetime import date, timedelta
from typing import List


SNAPSHOT_NAME = re.compile(r"^(\d{4})-(\d{2})-(\d{2})\.json$")


class MeterHealthService:
	def __init__(self, cfg, registry, max_lookback_days: int = 90):
		self.cfg = cfg
		self.registry = registry
		self.max_lookback = max_lookback_days

	def _stale_after_days(self) -> int:
		try:
			return max(1, int(self.cfg.get("MeterStaleAfterDays", "7")))
		except (TypeError, ValueError):
			return 7

	def _configured_meters(self) -> List[dict]:
		"""One row per location, for the meter currently active there.
		Replaced meters are not expected to send, so they're skipped."""
		out = []
		today = date.today()
		for location in self.registry.all_locations():
			meter = self.registry.active_meter(location, today)
			if not meter:
				continue
			out.append({
				"meter_id": meter.id,
				"location": meter.location,
				"flat": meter.flat,
				"room": meter.room,
				"type": meter.type,
			})
		return out

	def _snapshots_newest_first(self):
		snapshot_dir = self.cfg.get("SnapshotDir", "")
		if not snapshot_dir or not os.path.isdir(snapshot_dir):
			return
		cutoff = date.today() - timedelta(days=self.max_lookback)
		files = []
		for name in os.listdir(snapshot_dir):
			m = SNAPSHOT_NAME.match(name)
			if not m:
				continue
			try:
				d = date(int(m.group(1)), int(m.group(2)), int(m.group(3)))
			except ValueError:
				continue
			if d < cutoff:
				continue
			files.append((d, os.path.join(snapshot_dir, name)))
		files.sort(key=lambda x: x[0], reverse=True)
		for d, path in files:
			yield d, path

	def check_meters(self) -> dict:
		if not self.registry or not self.registry.is_unlocked():
			return {
				"status": "locked",
				"missing": [],
				"never_seen": [],
				"healthy": [],
				"threshold_days": self._stale_after_days(),
			}

		threshold = self._stale_after_days()
		today = date.today()
		configured = self._configured_meters()
		needed = {m["meter_id"] for m in configured}

		last_seen: dict[str, date] = {}
		for snap_date, path in self._snapshots_newest_first():
			remaining = needed - last_seen.keys()
			if not remaining:
				break
			try:
				with open(path, "r", encoding="utf-8") as f:
					data = json.load(f)
			except (OSError, json.JSONDecodeError):
				continue
			for meter_id in remaining:
				if meter_id in data:
					last_seen[meter_id] = snap_date

		missing, never_seen, healthy = [], [], []
		for m in configured:
			seen = last_seen.get(m["meter_id"])
			if seen is None:
				never_seen.append({**m, "last_seen": None, "days_since": None})
				continue
			days = (today - seen).days
			row = {**m, "last_seen": seen.isoformat(), "days_since": days}
			if days >= threshold:
				missing.append(row)
			else:
				healthy.append(row)

		missing.sort(key=lambda r: -r["days_since"])
		never_seen.sort(key=lambda r: (r["flat"] or "", r["location"]))
		healthy.sort(key=lambda r: (r["flat"] or "", r["location"]))

		return {
			"status": "ok",
			"missing": missing,
			"never_seen": never_seen,
			"healthy": healthy,
			"threshold_days": threshold,
		}
