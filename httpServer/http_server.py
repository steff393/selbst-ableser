import socket
from http.server import ThreadingHTTPServer
from functools import partial
from .handler import Handler

def get_local_ip():
	s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
	try:
		s.connect(("8.8.8.8", 80))
		ip = s.getsockname()[0]
	except Exception:
		ip = "127.0.0.1"
	finally:
		s.close()
	return ip


def start_http(cfg, frame_store, registry):
	handler_factory = partial(
		Handler,
		cfg=cfg,
		frame_store=frame_store,
		registry=registry
	)

	server = ThreadingHTTPServer(
		("0.0.0.0", int(cfg['HttpPort'])),
		handler_factory
	)

	print(f"HTTP-Server aktiv: http://{get_local_ip()}:{cfg['HttpPort']}")
	server.serve_forever()
