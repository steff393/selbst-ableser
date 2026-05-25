"""Weekly meter-health email alert.

Runs in-process (admin/evaluator role) because the health check needs the
unlocked `MeterRegistry`. Uses `email.json` for SMTP credentials — same
file the monthly tenant mail uses — but sends to the admin (the configured
`sender`), not to the tenant `to` list. Tenants don't need to hear about
which meters in their building are silent.
"""

import json
import time
import logging
import threading
from datetime import datetime
from pathlib import Path

from mailer import send_mail

logger = logging.getLogger("selbst_ableser.audit")

_DEFAULT_INTERVAL_SECONDS = 60


def _load_email_config(email_file: str = "email.json"):
	path = Path(email_file)
	if not path.is_file():
		return None
	try:
		with path.open("r", encoding="utf-8") as f:
			cfg = json.load(f)
	except Exception as e:
		logger.warning("HEALTH MAIL skip — could not read %s: %s", email_file, e)
		return None
	if not isinstance(cfg, dict):
		return None
	if not cfg.get("sender") or not cfg.get("password"):
		return None
	return cfg


def _compose_body(report: dict) -> str:
	never = report.get("never_seen", [])
	missing = report.get("missing", [])
	healthy = report.get("healthy", [])
	threshold = report.get("threshold_days", 7)

	lines = ["Wöchentliche Übersicht der Zähler-Gesundheit:", ""]
	lines.append(f"  Nie empfangen:             {len(never)}")
	lines.append(f"  Seit ≥ {threshold} Tagen stumm:   {len(missing)}")
	lines.append(f"  OK:                        {len(healthy)}")
	lines.append("")

	if never:
		lines.append("Nie empfangen:")
		for r in never:
			lines.append(f"  - Wohnung {r['flat'] or '–'} / {r['location']} / {r['type']} (Zähler {r['meter_id']})")
		lines.append("")

	if missing:
		lines.append(f"Seit ≥ {threshold} Tagen stumm:")
		for r in missing:
			lines.append(
				f"  - Wohnung {r['flat'] or '–'} / {r['location']} / {r['type']} "
				f"(Zähler {r['meter_id']}, zuletzt {r['last_seen']}, vor {r['days_since']} Tagen)"
			)
		lines.append("")

	if not never and not missing:
		lines.append("Alle konfigurierten Zähler haben innerhalb des Grenzwerts gesendet.")
		lines.append("")

	lines.append("--")
	lines.append("selbst-ableser")
	return "\n".join(lines)


def _should_fire_now(now: datetime, last_sent: datetime | None) -> bool:
	# Weekly, Monday 08:00 local. Allow the hour-bucket (08:xx) so we don't
	# miss the slot if the process started mid-minute.
	if now.weekday() != 0 or now.hour != 8:
		return False
	if last_sent is None:
		return True
	# Don't re-fire within the same hour bucket
	return (now - last_sent).total_seconds() >= 3600


def run_loop(health_service, email_file: str = "email.json",
             tick_seconds: int = _DEFAULT_INTERVAL_SECONDS,
             send_on_start: bool = False):
	"""Blocking loop. Intended to be run as a daemon thread."""
	last_sent: datetime | None = None
	if send_on_start:
		_send_once(health_service, email_file)
		last_sent = datetime.now()
	while True:
		now = datetime.now()
		if _should_fire_now(now, last_sent):
			if _send_once(health_service, email_file):
				last_sent = now
		time.sleep(tick_seconds)


def _send_once(health_service, email_file: str) -> bool:
	cfg = _load_email_config(email_file)
	if not cfg:
		return False  # silently skip — operator hasn't set up email
	report = health_service.check_meters()
	if report.get("status") == "locked":
		logger.info("HEALTH MAIL skip — registry locked")
		return False
	recipients = [cfg["to"][0]] if isinstance(cfg["to"], list) and cfg["to"] else [cfg["sender"]]

	# Special case: Registry locked
	if report.get("status") == "locked":
		subject = "[selbst-ableser] Wöchentlicher Zähler-Status - Gesperrt!"
		body = "Der Zähler-Status konnte nicht überprüft werden, da das Zählerregister gesperrt ist."
		sent = send_mail(cfg["sender"], cfg["password"], recipients, subject, body)
		logger.info("HEALTH MAIL — registry locked")
		return False
	
	# Compose and send the report email
	subject = "[selbst-ableser] Wöchentlicher Zähler-Status"
	body = _compose_body(report)
	sent = send_mail(cfg["sender"], cfg["password"], recipients, subject, body)
	if sent:
		logger.info(
			"HEALTH MAIL sent — never_seen=%d missing=%d healthy=%d",
			len(report["never_seen"]), len(report["missing"]), len(report["healthy"])
		)
	return sent > 0


def start_background(health_service, email_file: str = "email.json"):
	t = threading.Thread(
		target=run_loop,
		args=(health_service, email_file),
		daemon=True,
		name="health-mailer",
	)
	t.start()
	return t
