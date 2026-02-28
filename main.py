import configparser
import os
import json
import secrets
from meterRegistry import MeterRegistry
from api.app import create_app
from pathlib import Path

# Load and validate the configuration
BASE_DIR = Path(__file__).parent
cfg_file = BASE_DIR / 'cfg.ini'
if not cfg_file.exists():
	raise FileNotFoundError(f"cfg.ini nicht gefunden in {BASE_DIR}")

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read(cfg_file)
cfg = config['Configuration']

cfg['SnapshotDir']  = str(BASE_DIR / cfg['SnapshotDir'])
Path(cfg['SnapshotDir']).mkdir(parents=True, exist_ok=True)

cfg['Userfile']     = str(BASE_DIR / cfg['Userfile'])

if cfg.get('Locationfile'):
    cfg['Locationfile'] = str(BASE_DIR / cfg['Locationfile'])
else:
    cfg['Locationfile'] = ''

# Setup
if not Path(cfg['Userfile']).exists():
	print("Userfile nicht gefunden – erstelle neue Datei...")
	token = secrets.token_hex(8)

	initial_data = {
		token: {
			"flat": None,
		}
	}
	with open(cfg['Userfile'], "w", encoding="utf-8") as f:
		json.dump(initial_data, f, indent=2)
	print("========================================")
	print("Admin-Token erzeugt:")
	print(token)
	print("Bitte sicher notieren bzw. speichern!")
	print("========================================")


registry = MeterRegistry(
	cfg['Locationfile'],
	password=os.getenv("LOCATION_PW"),
	key_port=int(cfg.get('KeyPortMain', 0)) or None)
#registry.print()

app = create_app(cfg, registry)
