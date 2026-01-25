import datetime
from dataclasses import dataclass
from typing import Optional

@dataclass
class MeterConfig:
	id: str
	startDate: datetime.date
	startValue: int
	finalValue: Optional[int]

@dataclass
class MeterReading:
	meter_id: str
	value: int
	found_date: datetime.date

@dataclass
class MonthlyResult:
	location_id: str
	flat: str
	month: str
	consumption: int
	meter_id: str
	meter_value: int
	found_date: str
