"""Shared SMTP helper.

Used by both `email_sender.py` (monthly tenant mail) and the in-process
health-alert scheduler in `app.py`. Keeping it in one place means changes
to SMTP host, error handling, or message construction stay consistent.
"""

import smtplib
import logging
from email.mime.text import MIMEText
from typing import Iterable

logger = logging.getLogger(__name__)


def send_mail(sender: str, password: str, recipients: Iterable[str],
              subject: str, body: str, smtp_host: str = "mail.gmx.net",
              smtp_port: int = 465) -> int:
	"""Send one plain-text mail to each recipient. Returns the number sent OK.
	Caller is responsible for validating that sender/password/recipients exist.
	"""
	sent = 0
	try:
		with smtplib.SMTP_SSL(smtp_host, smtp_port) as server:
			server.login(sender, password)
			for recipient in recipients:
				msg = MIMEText(body, "plain", "utf-8")
				msg["From"] = sender
				msg["To"] = recipient
				msg["Subject"] = subject
				server.sendmail(sender, recipient, msg.as_string())
				logger.info("Mail sent to: %s (subject=%r)", recipient, subject)
				sent += 1
	except Exception as e:
		logger.error("Failed to send mail (subject=%r): %s", subject, e)
	return sent
