import os
import json
from datetime import datetime, date
from dataclasses import dataclass
from typing import Optional, Dict


@dataclass
class User:
	token: str
	flat: Optional[str] = None
	area: Optional[float] = 0.0
	move_in: Optional[str] = None
	move_out: Optional[str] = None
	
	# Cache
	_all_users: Optional[Dict[str, 'User']] = None
	_userfile: str = "users.json"

	@property
	def is_admin(self) -> bool:
		return self.flat in (None, "")
	
	@property
	def move_in_date(self) -> Optional[datetime]:
		if self.move_in:
			try:
				return datetime.strptime(self.move_in, "%Y-%m-%d")
			except ValueError:
				return None
		return None
	
	@property
	def move_out_date(self) -> Optional[datetime]:
		if self.move_out:
			try:
				return datetime.strptime(self.move_out, "%Y-%m-%d")
			except ValueError:
				return None
		return None
	
	def get_accessible_period(self, start: str, end: str) -> tuple[Optional[str], Optional[str]]:
		try:
			start_date = datetime.strptime(start, "%Y-%m-%d")
			end_date = datetime.strptime(end, "%Y-%m-%d")
		except ValueError:
			return None, None
		
		if self.move_in_date:
			start_date = max(start_date, self.move_in_date)
		
		if self.move_out_date:
			end_date = min(end_date, self.move_out_date)
		
		if start_date > end_date:
			return None, None
		
		return (
			start_date.strftime("%Y-%m-%d"),
			end_date.strftime("%Y-%m-%d")
		)
	
	def to_dict(self) -> dict:
		result = {}
		if self.flat:
			result["flat"] = self.flat
		if self.area:
			result["area"] = self.area
		if self.move_in:
			result["move_in"] = self.move_in
		if self.move_out:
			result["move_out"] = self.move_out
		return result
	
	# =========================================================================
	# service methods
	# =========================================================================
	
	@classmethod
	def set_userfile(cls, path: str):
		cls._userfile = path
		cls._all_users = None  # delete cache
	
	@classmethod
	def load_from_file(cls) -> Dict[str, dict]:
		try:
			with open(cls._userfile, "r", encoding="utf-8") as f:
				return json.load(f)
		except Exception:
			return {}
	
	@classmethod
	def save_to_file(cls, data: Dict[str, dict]):
		with open(cls._userfile, "w", encoding="utf-8") as f:
			json.dump(data, f, indent=2, ensure_ascii=False)
		cls._all_users = None   # delete cache
	
	@classmethod
	def get_all(cls) -> Dict[str, 'User']:
		"""Returns: Dictionary with  token -> User"""
		if cls._all_users is None:
			raw_data = cls.load_from_file()
			cls._all_users = {
				token: cls.from_dict(token, data)
				for token, data in raw_data.items()
			}
		return cls._all_users
	
	@classmethod
	def get(cls, token: str) -> Optional['User']:
		"""
		Return one user
		user = User.get("token123")
		"""
		return cls.get_all().get(token)
	
	@classmethod
	def exists(cls, token: str) -> bool:
		return token in cls.get_all()
	
	@classmethod
	def from_dict(cls, token: str, data: dict) -> 'User':
		"""Create user from Dictionary"""
		return cls(
			token=token,
			flat=data.get("flat"),
			area=float(data.get("area") or 0),
			move_in=data.get("move_in"),
			move_out=data.get("move_out"),
		)
	
	@classmethod
	def get_flat_area_mapping(cls) -> Dict[str, float]:
		"""return flat -> area mapping"""
		return {u.flat: u.area for u in cls.get_all().values() if u.flat}
	
	@classmethod
	def get_total_area(cls) -> float:
		return sum(cls.get_flat_area_mapping().values())
	
	@classmethod
	def export(cls) -> tuple[str, bytes]:
		"""Create export for backup"""
		raw_data = cls.load_from_file()
		filename = f"users-backup-{date.today()}.json"
		content = json.dumps(raw_data, indent=2, ensure_ascii=False).encode("utf-8")
		return filename, content
