import threading
import datetime

class FrameStore:
	def __init__(self):
		self._frames = {}
		self._lock = threading.Lock()

	def update(self, meter_id, rssi, wmbus, timestamp=None):
		if timestamp is None:
			timestamp = datetime.datetime.now().strftime("%d.%m.%Y %H:%M:%S")

		with self._lock:
			self._frames[meter_id] = {
				"timestamp": timestamp,
				"rssi": rssi,
				"wmbus": wmbus
			}

	def bulk_import(self, frames):
		with self._lock:
			for f in frames:
				self._frames[f.meter_id] = {
					"timestamp": f.timestamp,
					"rssi": f.rssi,
					"wmbus": f.wmbus
				}

	def snapshot(self):
		with self._lock:
			# wichtig: copy, nicht Referenz
			return dict(self._frames)

	def get_all(self):
		with self._lock:
			return dict(self._frames)

	def get_lock(self):
		# für Legacy-Code (make_snapshot etc.)
		return self._lock
