import datetime
from dataclasses import dataclass
from typing import Optional

@dataclass
class MeterConfig:
	id: str
	startDate: datetime.date
	startValue: int
	finalValue: Optional[int]
	aes_key: Optional[str]

@dataclass
class MeterReading:
	meter_id: str
	value: int
	found_date: datetime.date

@dataclass
class MonthlyResult:
	location_id: str
	flat: str
	room: str
	type: str
	month: str
	consumption: int
	meter_id: str
	meter_value: int
	found_date: str

@dataclass
class MonthlyAggregateResult:
	month: str
	type: str
	consumption: int
