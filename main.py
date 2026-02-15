import configparser
import os
from meterRegistry import MeterRegistry
from api.app import create_app

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

registry = MeterRegistry(
	cfg['Locationfile'],
	password=os.getenv("LOCATION_PW"),
	key_port=int(cfg.get('KeyPort', 0)) or None)
#registry.print()

app = create_app(cfg, registry)
