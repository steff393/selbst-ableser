import threading
import datetime
from datetime import timedelta
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
			print(f"Snapshot nicht gefunden: {path}")
			return
		
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

		# backup if configured and directory exists
		if 'BackupDir' in self.cfg and self.cfg['BackupDir'] and os.path.isdir(self.cfg['BackupDir']):
			backup_dir = self.cfg['BackupDir']
			backup_filename = os.path.join(backup_dir, f"{key}.json")
			try:
				with open(backup_filename, "w", encoding="utf-8") as f:
					json.dump(payload, f, indent=2)
			except Exception as e:
				print(f"[SNAPSHOT] Fehler beim Speichern im Backup: {e}")

		return key


	def time_for_snapshot(self, now):
		if self.cfg['SnapshotMode'] == "daily":
			trigger = (now.hour == 23)
		elif self.cfg['SnapshotMode'] == "monthly":
			tomorrow = now + timedelta(days=1)
			last_day = (tomorrow.month != now.month) # check if tomorrow is a different month, which means today is the last day of the month
			trigger = (last_day and now.hour >= 23)
		elif self.cfg['SnapshotMode'] == "monthly_safe":
			plusDays = now + timedelta(days=5)
			last_days = (plusDays.month != now.month) # check if in 5 days it's a different month, which means we are in the last 5 days of the month
			trigger = (last_days and now.hour >= 23)
		else:
			trigger = False
		return trigger
