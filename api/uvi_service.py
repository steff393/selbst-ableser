from collections import defaultdict
from typing import List, Dict, Any
from .users import User
from meterReader import evaluate_uvi, serialize_monthly_results

class UviService:
	def __init__(self, snapshot_dir: str, registry):
		self.snapshot_dir = snapshot_dir
		self.registry = registry
	
	def evaluate_for_user(self, user: User, start: str, end: str) -> Dict[str, Any]:
		restricted_start, restricted_end = user.get_accessible_period(start, end)
		
		if not restricted_start:
			return {"details": [], "house_norm": [], "area": {}}

		all_results = evaluate_uvi(
			json_path=self.snapshot_dir,
			registry=self.registry,
			start_date=restricted_start,
			end_date=restricted_end,
			flat=None
		)

		if user.is_admin:
			details = all_results
			area = User.get_flat_area_mapping()
		else:
			details = [r for r in all_results if r.flat == user.flat]
			area = {user.flat: user.area}
		
		house_norm = self._calculate_house_norm(all_results)
		
		return {
			"details": serialize_monthly_results(details),
			"house_norm": house_norm,
			"area": area
		}
	
	def _calculate_house_norm(self, results: List) -> List[Dict[str, Any]]:
		sums = defaultdict(int)
		for r in results:
			sums[(r.month, r.type)] += r.consumption
		
		total_area = User.get_total_area()
		
		house_norm = []
		for (month, type_), consumption in sorted(sums.items()):
			if total_area > 0:
				normalized = consumption / total_area
			else:
				normalized = 0
			
			house_norm.append({
				"month": month,
				"type": type_,
				"consumption": round(normalized, 2)
			})
		
		return house_norm
	