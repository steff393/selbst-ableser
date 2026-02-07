import configparser
import threading
import time
import argparse
from wmBus import WMBusReceiver
from frame_store import FrameStore
from wmbusBlocklist import BlockList
from wmbusServer.http_server import start_http

parser = argparse.ArgumentParser()
parser.add_argument("-snap", help="Laden von Daten aus Snapshot-Datei (optional)")
args = parser.parse_args()

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

frame_store = FrameStore(cfg)
blocklist = BlockList(cfg.get('Blocklist', ''))

def main():
	# Load existing data
	if (args.snap):
		frame_store.load_snapshot_file(args.snap)

	# Snapshot setup
	frame_store.start_scheduler()
	
	# HTTP-Server setup
	threading.Thread(target=start_http, args=(cfg, frame_store), daemon=True).start()
	
	# Serial communication
	port = cfg.get('Port', '').strip()
	if port:
		while True:
			try:
				iu891a = WMBusReceiver(port)
				iu891a.init_stick()
				iu891a.get_device_info()
				iu891a.set_config()
				for meter_id, rssi, wmbus in iu891a.frames():
					if blocklist.is_blocked(meter_id, wmbus):
						print(f"Telegramm von {meter_id} blockiert")
						continue
					frame_store.update(meter_id, rssi, wmbus)
			except KeyboardInterrupt:
				print("Beendet.")
				break
			except Exception as e:
				print(f"Fehler: {e}")
				time.sleep(30)
	else: 
		print("Kein Port konfiguriert")
		while True:
			try:
				time.sleep(30)
			except KeyboardInterrupt:
				print("Beendet.")
				break


if __name__ == "__main__":
	main()
