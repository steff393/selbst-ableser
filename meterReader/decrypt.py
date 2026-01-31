from Crypto.Cipher import AES
import binascii

def decrypt(telegram, key):
	data    = binascii.unhexlify(''.join(c for c in telegram.lower() if c in '0123456789abcdef')) # to lower case
	hex_key = binascii.unhexlify(key)
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
		print(f"encr_mode {encr_mode} can't be decoded")
		return(None)

	iv = m_field + id_field + ver_field + type_field + acc_field # no sum, but concatenation of bytes
	#print(f"IV: {iv.hex().upper()}")
	#print(f"Size: {block_cnt} Blöcke a {block_size} Bytes = {block_cnt * block_size}dez. Bytes")

	cipher = AES.new(hex_key, AES.MODE_CBC, iv)
	result = cipher.decrypt(data[offset : offset + block_cnt * block_size])
	if result[0] == 0x2F and result[1] == 0x2F:
		# return begin and end of original telegramm
		return(data[:offset] + result + data[offset + block_cnt * block_size :])
	else:
		return(None)