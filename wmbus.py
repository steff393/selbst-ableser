import configparser
import threading
import os
import time
from wmBus import WMBusReceiver
from frame_store import FrameStore
from wmbusBlocklist import BlockList
from meterRegistry import MeterRegistry
from wmbusServer.app import create_app
from pathlib import Path


def wmbusComm():
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


# Load and validate the configuration
BASE_DIR = Path(__file__).parent
cfg_file = BASE_DIR / 'cfg.ini'
if not cfg_file.exists():
	raise FileNotFoundError(f"cfg.ini nicht gefunden in {BASE_DIR}")

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read(cfg_file)
cfg = config['Configuration']

cfg['SnapshotDir']   = str(BASE_DIR / cfg['SnapshotDir'])
Path(cfg['SnapshotDir']).mkdir(parents=True, exist_ok=True)

if cfg.get('Locationfile'):
    cfg['Locationfile'] = str(BASE_DIR / cfg['Locationfile'])
else:
    cfg['Locationfile'] = ''

if cfg.get('Blocklistfile'):
    cfg['Blocklistfile'] = str(BASE_DIR / cfg['Blocklistfile'])
else:
    cfg['Blocklistfile'] = ''


frame_store = FrameStore(cfg)
blocklist = BlockList(cfg['Blocklistfile'])

# Snapshot setup
frame_store.start_scheduler()

# to be removed ??
registry = MeterRegistry(
	cfg['Locationfile'],
	password=os.getenv("LOCATION_PW"),
	key_port=int(cfg.get('KeyPortWmbus', 0)) or None)
#registry.print()
#-----------------

# Serial communication
threading.Thread(target=wmbusComm, daemon=True).start()

# Server
app = create_app(cfg, registry, frame_store)
