import logging
import re
from datetime import date
from pathlib import Path
from typing import List, Optional

DATE_PATTERN = re.compile(r"^(\d{4})-(\d{2})-(\d{2})\.json$")
logger = logging.getLogger(__name__)


def parse_snapshot_date(filename: str) -> Optional[date]:
	match = DATE_PATTERN.match(filename)
	if not match:
		return None
	year, month, day = match.groups()
	try:
		return date(int(year), int(month), int(day))
	except ValueError:
		return None


def list_snapshot_files(snapshot_dir: str) -> List[Path]:
	base = Path(snapshot_dir)
	if not base.exists() or not base.is_dir():
		raise FileNotFoundError(f"Snapshot directory not found: {snapshot_dir}")

	files = []
	for path in base.iterdir():
		if path.is_file() and path.suffix.lower() == ".json":
			if parse_snapshot_date(path.name) is not None:
				files.append(path)
	return sorted(files, key=lambda p: p.name)


def move_unused_files(
	results: List,
	snapshot_dir: str,
	start_date: Optional[date] = None,
	end_date: Optional[date] = None,
	subfolder: str = "unused",
) -> List[Path]:
	"""
	Move snapshot files not appearing in the found_date of evaluate_uvi results to a subfolder.
	Only files inside the supplied date range are considered for cleanup.
	"""
	found_dates = {result.found_date for result in results}
	keep_files = {f"{date}.json" for date in found_dates}

	files = list_snapshot_files(snapshot_dir)
	unused = []
	for path in files:
		snapshot_date = parse_snapshot_date(path.name)
		if snapshot_date is None:
			continue
		if start_date and snapshot_date < start_date:
			continue
		if end_date and snapshot_date > end_date:
			continue
		if path.name not in keep_files:
			unused.append(path)

	archive_dir = Path(snapshot_dir) / subfolder
	archive_dir.mkdir(parents=True, exist_ok=True)

	for path in unused:
		new_path = archive_dir / path.name
		path.rename(new_path)
		logger.info("Moved %s to %s", path.name, new_path)

	return unused
