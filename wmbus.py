import configparser
import threading
import json
import os
import sys
from wmBus import WMBusReceiver
from frame_store import FrameStore
from meterRegistry import MeterRegistry
from httpServer.http_server import start_http


config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

frame_store = FrameStore(cfg)
registry = MeterRegistry(cfg['Locationfile'])
#registry.print()


def main():
	# Load existing data
	if len(sys.argv) == 2:
		frame_store.load_snapshot_file(sys.argv[1])

	# Snapshot setup
	frame_store.start_scheduler()
	
	# HTTP-Server setup
	threading.Thread(target=start_http, args=(cfg, frame_store, registry), daemon=True).start()
	
	# Serial communication
	iu891a = WMBusReceiver(cfg['Port'])
	try:
		iu891a.init_stick()
		iu891a.get_device_info()
		iu891a.set_config()
		for meter_id, rssi, wmbus in iu891a.frames():
			if registry.is_blocked(meter_id, wmbus):
				print(f"Telegramm von {meter_id} blockiert")
				continue
			frame_store.update(meter_id, rssi, wmbus)
	except KeyboardInterrupt:
		print("Beendet.")
	except Exception as e:
		print(f"Fehler: {e}")


if __name__ == "__main__":
	main()
