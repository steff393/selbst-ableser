import json
import smtplib
from email.mime.text import MIMEText
import logging
import schedule
import time
from pathlib import Path

# Logging configuration - file and console
log_file = "email.log"
logger = logging.getLogger()
logger.setLevel(logging.INFO)

# Logging to file
file_handler = logging.FileHandler(log_file)
file_handler.setLevel(logging.INFO)
file_formatter = logging.Formatter("%(asctime)s - %(levelname)s - %(message)s", datefmt="%Y-%m-%d %H:%M:%S")
file_handler.setFormatter(file_formatter)
logger.addHandler(file_handler)

# Logging to console
console_handler = logging.StreamHandler()
console_handler.setLevel(logging.INFO)
console_formatter = logging.Formatter("%(asctime)s - %(levelname)s - %(message)s", datefmt="%Y-%m-%d %H:%M:%S")
console_handler.setFormatter(console_formatter)
logger.addHandler(console_handler)

# Load configuration
def load_email_config():
	config_path = Path(__file__).resolve().parent / "email.json"
	if not config_path.is_file():
		logger.info("email.json not found")
		return None

	try:
		with open(config_path, "r", encoding="utf-8") as f:
			config = json.load(f)
	except Exception as e:
		logger.error(f"Failed to read email.json: {e}")
		return None

	if not isinstance(config, dict):
		logger.error("email.json has invalid format: expected an object")
		return None

	if not config.get("sender") or not config.get("password") or not config.get("to"):
		logger.error("email.json missing required mail settings")
		return None

	if isinstance(config["to"], str):
		config["to"] = [config["to"]]

	return config


def build_email_body(config):
	link = config.get("link", "")
	return f"""\
Sehr geehrter Mieter,

Ihre unterjährige Verbrauchsinformation für den letzten Monat steht Ihnen unter folgendem Link zur Verfügung:
{link}

Viele Grüße
selbst-ableser - weil deine Daten dir gehören
"""


def send_mail(config):
	body = build_email_body(config)
	subject = config.get("subject", "Unterjährige Verbrauchsinformation")
	to = config.get("to", [])
	if isinstance(to, str):
		to = [to]

	try:
		with smtplib.SMTP_SSL("mail.gmx.net", 465) as server:
			server.login(config["sender"], config["password"])
			for recipient in to:
				msg = MIMEText(body, "plain", "utf-8")
				msg["From"] = config["sender"]
				msg["To"] = recipient
				msg["Subject"] = subject
				server.sendmail(config["sender"], recipient, msg.as_string())
				logger.info(f"Mail sent to: {recipient}")

	except Exception as e:
		logger.error(f"Failed to send mail: {e}")


def send_monthly_report():
	if time.localtime().tm_mday != 1:
		return

	config = load_email_config()
	if config:
		send_mail(config)

# every 1. of month at 08:00
schedule.every().day.at("08:00").do(send_monthly_report)

# optional startup notification only if config exists
startup_config = load_email_config()
if startup_config:
	send_mail({
		"sender": startup_config["sender"],
		"password": startup_config["password"],
		"to": [startup_config["to"][0]] if isinstance(startup_config["to"], list) and startup_config["to"] else [startup_config["sender"]],
		"subject": "E-Mail-Service gestartet",
		"link": startup_config.get("link", "")
	})

while True:
	schedule.run_pending()
	time.sleep(60)   # check every minute
