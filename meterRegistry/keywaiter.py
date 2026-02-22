import socket


def wait_for_key_secure(port: int = 53165) -> str:
	host = "127.0.0.1"
	with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
		s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
		s.bind((host, port))
		s.listen(1)

		print(f"Warte auf Key auf Port {port} …")
		conn, _ = s.accept()
		with conn:
			data = conn.recv(4096).decode("utf-8")

			if "\r\n\r\n" in data:
				data = data.split("\r\n\r\n", 1)[1]

			secret = data.strip()
			conn.sendall(
				b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nKey erhalten\n"
			)
			return secret if len(secret) < 100 else ""
