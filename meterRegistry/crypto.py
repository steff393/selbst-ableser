from cryptography.hazmat.primitives.kdf.scrypt import Scrypt
from cryptography.hazmat.primitives.ciphers.aead import AESGCM


class WrongPassword(Exception):
	pass


def derive_key(password: str, salt: bytes) -> bytes:
	kdf = Scrypt(
		salt=salt,
		length=32,
		n=2**14,
		r=8,
		p=1,
	)
	return kdf.derive(password.encode("utf-8"))


def decrypt(data: bytes, password: str) -> bytes:
	try:
		if len(data) < 28:
			raise ValueError("Datei zu kurz oder beschädigt")

		salt = data[:16]
		nonce = data[16:28]
		ciphertext = data[28:]

		key = derive_key(password, salt)
		aesgcm = AESGCM(key)
		return aesgcm.decrypt(nonce, ciphertext, None)
	except Exception:
		raise WrongPassword("Falsches Passwort oder beschädigte Datei")
