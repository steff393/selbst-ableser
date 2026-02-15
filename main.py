import configparser
import os
import json
import secrets
from meterRegistry import MeterRegistry
from api.app import create_app

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

# Setup
if not os.path.exists(cfg['Userfile']):
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
	key_port=int(cfg.get('KeyPort', 0)) or None)
#registry.print()

app = create_app(cfg, registry)
