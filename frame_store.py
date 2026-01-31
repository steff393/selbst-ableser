import threading
import datetime
import time
import os
import json


class FrameStore:
	def __init__(self, cfg):
		self._frames = {}
		self._lock = threading.Lock()
		self.cfg = cfg

		# load last snapshot
		if not os.path.isdir(cfg['SnapshotDir']):
			return None
		files = sorted(f for f in os.listdir(cfg['SnapshotDir']) if f.endswith(".json"))
		self.last_snapshot_key =  files[-1][:-5] if files else None


	def start_scheduler(self, interval=30):
		def run():
			while True:
				if self.time_for_snapshot(datetime.datetime.now()):
					self.make_snapshot()
				time.sleep(interval)
		threading.Thread(target=run, daemon=True).start()


	def update(self, meter_id, rssi, wmbus, timestamp=None):
		if timestamp is None:
			timestamp = datetime.datetime.now().strftime("%d.%m.%Y %H:%M:%S")
		with self._lock:
			self._frames[meter_id] = {
				"timestamp": timestamp,
				"rssi": rssi,
				"wmbus": wmbus
			}


	def load_snapshot_file(self, filename):
		"""Liest eine Snapshot-Datei aus SnapshotDir und lädt die Frames in den Store."""
		path = os.path.join(self.cfg['SnapshotDir'], filename)
		if not os.path.isfile(path):
			raise FileNotFoundError(f"Snapshot nicht gefunden: {path}")

		count = 0
		with self._lock:
			with open(path, "r", encoding="utf-8") as f:
				data = json.load(f)
			for meter_id, entry in data.items():
				self._frames[meter_id] = {
					"timestamp": entry.get("timestamp"),
					"rssi": entry.get("rssi"),
					"wmbus": bytes.fromhex(entry.get("wmbus", ""))
				}
				count += 1

		print(f"{count} Telegramme importiert")
		return count


	def get_all(self):
		with self._lock:
			return dict(self._frames)


	def make_snapshot(self, force=False):
		with self._lock:
			frame_copy = dict(self._frames)
			if not frame_copy:
				return self.last_snapshot_key

		os.makedirs(self.cfg['SnapshotDir'], exist_ok=True)
		key = datetime.datetime.now().strftime("%Y-%m-%d")
		if key == self.last_snapshot_key and not force:
			return self.last_snapshot_key

		filename = os.path.join(self.cfg['SnapshotDir'], f"{key}.json")
		payload = {
			meter_id: {
				"timestamp": data["timestamp"],
				"rssi": data["rssi"],
				"wmbus": data["wmbus"].hex()
			}
			for meter_id, data in frame_copy.items()
		}

		with open(filename, "w", encoding="utf-8") as f:
			json.dump(payload, f, indent=2)

		self.last_snapshot_key = key
		print(f"[SNAPSHOT] Gespeichert: {filename}")
		return key


	def time_for_snapshot(self, now):
		if self.cfg['SnapshotMode'] == "daily":
			trigger = (now.hour == 0)
		elif self.cfg['SnapshotMode'] == "monthly":
			trigger = (now.day == 1 and now.hour == 0)
		else:
			trigger = False
		return trigger
