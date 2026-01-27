import os
import json
import datetime


def make_snapshot(frameList, data_lock, cfg, last_snapshot_key, force=False):
	with data_lock:
		if not frameList:
			return(last_snapshot_key)

	os.makedirs(cfg['SnapshotDir'], exist_ok=True)

	key = datetime.datetime.now().strftime("%Y-%m-%d")
	if key == last_snapshot_key and not force:
		return(last_snapshot_key)  # snapshot already taken for this period
	
	filename = os.path.join(cfg['SnapshotDir'], f"{key}.json")

	with open(filename, "w", encoding="utf-8") as f:
		payload = {
			str(k): {
				"timestamp": v["timestamp"],
				"rssi": v["rssi"],
				"wmbus": v["wmbus"].hex()
			}
			for k, v in frameList.items()
		}
		json.dump(payload, f, indent=2)

	print(f"[SNAPSHOT] Gespeichert: {filename}")
	return(key)


def time_for_snapshot(now, cfg):
	if cfg['SnapshotMode'] == "daily":
		trigger = (now.hour == 0)

	if cfg['SnapshotMode'] == "monthly":
		trigger = (now.day == 1 and now.hour == 0)

	if not trigger:  # "none" or not trigger time
		return False

	return True


def load_last_snapshot_key(cfg):
	if not os.path.isdir(cfg['SnapshotDir']):
		return None

	files = sorted(f for f in os.listdir(cfg['SnapshotDir']) if f.endswith(".json"))
	return files[-1][:-5] if files else None
