from Crypto.Cipher import AES
from typing import Optional

def decryptTelegram(telegram: str, key: str) -> Optional[str]:
	# decrypts wmbus telegram
	try:
		data    = bytes.fromhex(telegram)
		hex_key = bytes.fromhex(key)
		if len(data) < 16 or len(hex_key) != 16:
			raise ValueError
	except Exception:
		print(f"Decrypt: Ungültige Daten '{telegram}' oder AES-Key: '{key}'")
		return(None)

	# Standard IV Konstruktion
	m_field    = data[2:4]       # Manufacturer       --| 
	id_field   = data[4:8]       # Meter ID           --|--> all 4 = unique ID for every meter
	ver_field  = data[8:9]       # Version/Generation --|
	type_field = data[9:10]      # Typ (e.g. HCA)     --|
	ci_field   = data[10:11]     # CI-Field -> skipped
	acc_field  = data[11:12] * 8 # Access Number * 8
	block_cnt  =(data[13] & 0xF0) >> 4 # Size             | confWord, spec ch. 7.2.4.3
	encr_mode  =(data[14] & 0x1F)# Encryption Mode M      |    ""
	offset     = 15
	block_size = 16

	if encr_mode != 5:
		print(f"Decrypt: encr_mode {encr_mode} kann nicht entschlüsselt werden")
		return(None)

	iv = m_field + id_field + ver_field + type_field + acc_field # no sum, but concatenation of bytes
	#print(f"IV: {iv.hex().upper()}")
	#print(f"Size: {block_cnt} Blöcke a {block_size} Bytes = {block_cnt * block_size}dez. Bytes")

	cipher = AES.new(hex_key, AES.MODE_CBC, iv)
	result = cipher.decrypt(data[offset : offset + block_cnt * block_size])
	if result[0] == 0x2F and result[1] == 0x2F:
		# add begin and end of original telegramm
		decrypt = data[:offset] + result + data[offset + block_cnt * block_size :]
		return(decrypt.hex()) # return as string
	else:
		return(None)


def getEncrMode(telegram: str) -> Optional[int]:
	try:
		data = bytes.fromhex(telegram)
	except Exception:
		return None

	if len(data) < 11:
		return None

	ci = data[10]

	if ci == 0x78 or (0xA0 <= ci <= 0xB7):   # CI = 0x78 (no tplh) or 0xA0–0xB7 (Mfct specific) -> never encrypted
		return None

	CI_MAP = {
		0x7A: 14, # CI = 0x7A → short tplh, tpl-cfg high-byte at byte 14
		0x72: 22, # CI = 0x72 → long  tplh, tpl-cfg high-byte at byte 22
		0x8C: 34, # CI = 0x8C → ELL,        tpl-cfg high-byte at byte 34
	}
	
	if ci in CI_MAP:
		if len(data) >= (CI_MAP[ci] + 1):
			return (data[CI_MAP[ci]] & 0x07)
	return None
