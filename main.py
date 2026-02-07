import configparser
import argparse
from frame_store import FrameStore
from meterRegistry import MeterRegistry
from httpServer.http_server import start_http

parser = argparse.ArgumentParser()
parser.add_argument("-pw",   help="Passwort für Locationfile (optional)")
args = parser.parse_args()

config = configparser.ConfigParser(inline_comment_prefixes='#')
config.read('cfg.ini')
cfg = config['Configuration']

frame_store = FrameStore(cfg)
registry    = MeterRegistry(
	cfg['Locationfile'], 
	password = args.pw if not "" else None, 
	key_port=int(cfg.get('KeyPort', 0)) or None)
#registry.print()


def main():
	try:
		# HTTP-Server setup
		start_http(cfg, frame_store, registry)
	except KeyboardInterrupt:
		print("Server beendet.")


if __name__ == "__main__":
	main()
