import serial
import serial.tools.list_ports
import time
from .slip import encode
from .dongle import check_crc, check_wmbus_len, extract_wmbus_data, read_frame_timeout, listen_frames

BAUDRATE = 115200
TIMEOUT  = 0.2
IMST_VID = "04B4" # Cypress Semiconductor
IMST_PID = "0003" # Product ID


class WMBusReceiver:
	def __init__(self, port):
		if port == "auto":
			port = self.find_iU891A()
		if port is None:
			raise Exception("iU891A-XL Stick nicht gefunden. Bitte korrekten Port in cfg.ini angeben.")
		self.ser = serial.Serial(port, BAUDRATE, timeout=TIMEOUT)
		print(f"Verbinde mit iU891A-XL an {port}...")

	def find_iU891A(self):
		# Automatically find the iU891A-XL stick by checking connected serial ports
		for port in serial.tools.list_ports.comports():
			if "IMST" in (port.manufacturer or "") or \
				 "iU891A" in (port.description or ""):
				return port.device
			# Alternative: check VID/PID
			hwid = port.hwid.upper()
			if f"VID_{IMST_VID}" in hwid and f"PID_{IMST_PID}" in hwid:
				return port.device
		print("IMST iU891A nicht gefunden. Verfügbare serielle Ports:")
		for port in serial.tools.list_ports.comports():
			print(f"  {port.device} - {port.description}")
		return None

	def init_stick(self):
		# Wakeup
		self.ser.write(b'\xC0' * 30)
		time.sleep(0.1)
		self.ser.reset_input_buffer()


	def get_device_info(self):
		# Get Device Info
		self.ser.write(encode(0x01, 0x03, text="Get Device Info"))
		resp = read_frame_timeout(self.ser)
		if resp:
			if resp.startswith(b'\xC0\x01\x04\x00') and check_crc(resp):
				print(f"<- {resp.hex().upper()} Device Info OK")
			else:
				print(f"<- {resp.hex().upper()} Device Info FAILED")
				return
		else:
			print("<- Keine Antwort!")
			return


	def set_config(self):
		# Set Config C1/T1: Mode(03) Options(0E00) UI(0000) LED(3200) Recalib(A0BB0D00)
		self.ser.write(encode(0x09, 0x03, bytes.fromhex("030E0000003200A0BB0D00"), "Set Configuration C1/T1"))
		resp = read_frame_timeout(self.ser)
		if resp:
			if resp.startswith(b'\xC0\x09\x04\x00') and check_crc(resp):
				print(f"<- {resp.hex().upper()} Configuration OK")
			else:
				print(f"<- {resp.hex().upper()} Configuration FAILED")
				return
		else:
			print("<- Keine Antwort!")
			return
		print("Stick ist bereit und empfängt... (Strg+C zum Beenden)")


	def frames(self):
		for frame in listen_frames(self.ser):
			if frame is None:
				continue
			if not check_crc(frame):
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültiger Frame (CRC Fehler): {frame.hex().upper()}")
				continue
			if not check_wmbus_len(frame):
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültiger Frame (Längenfehler): {frame.hex().upper()}")
				continue
			meter_id, rssi, wmbus = extract_wmbus_data(frame)
			if meter_id is None:
				print(f"{time.strftime('%H:%M:%S')} ⚠ Ungültige Meter ID im Frame: {frame.hex().upper()}")
				continue
			else:
				print(f"{time.strftime('%H:%M:%S')} ✔ Zähler {meter_id} | RSSI {rssi} dBm | wmBus {wmbus.hex().upper()}")
				yield(meter_id, rssi, wmbus)
