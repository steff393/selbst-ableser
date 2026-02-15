import os
import json
from datetime import date

class UsersService:
	def __init__(self, userfile="users.json"):
		self.userfile = userfile

	def load_users(self):
		if not os.path.isfile(self.userfile):
			return {}  # Setup-Mode
		try:
			with open(self.userfile, "r", encoding="utf-8") as f:
				return json.load(f)
		except Exception:
			return {}  # Setup-Mode

	def save_users(self, data):
		with open(self.userfile, "w", encoding="utf-8") as f:
			json.dump(data, f, indent=2)
		return True

	def get_export_data(self):
		users = self.load_users()
		filename = f"users-backup-{date.today()}.json"
		content = json.dumps(users, indent=2).encode("utf-8")
		return filename, content


	def is_setup_mode(self):
		return len(self.load_users()) == 0

	def is_admin(self, token):
		users = self.load_users()
		# Admin, wenn "flat" fehlt oder leer
		return users.get(token).get("flat") in (None, "")
