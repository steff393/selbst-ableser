import os
import json
import datetime
from typing import Optional
from .model import MeterConfig, MeterReading, MonthlyResult
from .logger import dbg



_daily_cache = {}
_JSON_PATH = "."  # default


# ============================================================
# Helper functions
# ============================================================

def month_ends(start_date, end_date):
	# return list of last day of each month between start_date and end_date
	result = []
	current = start_date.replace(day=1) # first day of start month
	while current <= end_date:
		next_month = (current.replace(day=28) + datetime.timedelta(days=4)).replace(day=1) # jump to day 28, add 4 days, go to first of next month
		last_day = next_month - datetime.timedelta(days=1)
		if start_date <= last_day <= end_date:
			result.append(last_day)
		current = next_month
	return result


def get_currHCA_from_wmbus(wmbus_hex: str) -> Optional[int]:
	try:
		# Byte 18/19, Little Endian
		return int(wmbus_hex[38:40] + wmbus_hex[36:38], 16)
	except Exception:
		return None


def load_daily_file(date: datetime.date):
	# load daily JSON file and cache it
	key = date.strftime("%Y-%m-%d")
	if key in _daily_cache:
		return _daily_cache[key]

	filename = f"{_JSON_PATH}/{key}.json"
	if not os.path.exists(filename):
		_daily_cache[key] = None
		return None

	with open(filename, "r") as f:
		data = json.load(f)

	_daily_cache[key] = data
	return data


def search_meter_reading(meter_id: str, date: datetime.date, max_back: int = 5) -> Optional[MeterReading]:
	# search up to max_back days before date for the meter reading
	for delta in range(max_back + 1):
		d = date - datetime.timedelta(days=delta)
		data = load_daily_file(d)
		if data and meter_id in data:
			val = get_currHCA_from_wmbus(data[meter_id]["wmbus"])
			if val is not None:
				return MeterReading(meter_id=meter_id, value=val, found_date=d)
	return None


def calculate_monthly_consumption(prev: Optional[MeterReading], curr: MeterReading, 
                                  prevMeterCfg: Optional[MeterConfig], currMeterCfg: MeterConfig) -> Optional[int]:
	# calculate the difference between two meter readings
	if prev is None:
		return None
	
	consumption = curr.value - prev.value

	# in case of counter exchange, apply correction
	# consumption = consumption + (prev_meter.endstand - curr_meter.anfangsstand)
	if prevMeterCfg and prevMeterCfg.id != currMeterCfg.id:
		if prevMeterCfg.finalValue is not None:
			correction = prevMeterCfg.finalValue - currMeterCfg.startValue
			dbg(f"Zählerwechsel: {prevMeterCfg.id} | {prevMeterCfg.finalValue} → {currMeterCfg.id} | {currMeterCfg.startValue}")
			if correction >= 0:
				dbg(f"=>Korrektur: +{correction}")
			else:
				dbg(f"=>Korrektur: {correction}")
			consumption += correction

	return consumption


# ============================================================
# Load meter location configuration
# ============================================================

def load_locations(filename: str):
	with open(filename, "r") as f:
		locCfg = json.load(f)["zaehlerplaetze"]

	locationList = []
	# append locations
	for loc in locCfg:
		meterList = []
		# append meters
		for meter in loc["zaehler"]:
			meterList.append(
				MeterConfig(
					id=meter["id"],
					startDate  = datetime.datetime.strptime(meter["start"], "%Y-%m-%d").date(),
					startValue = meter.get("anfangsstand", 0),
					finalValue = meter.get("endstand")
				)
			)
		locationList.append({
			"locId": loc["platz"],
			"flat": loc["whg"],
			"meters": meterList
		})
	return locationList


# ============================================================
# Core logic
# ============================================================

def evaluate_uvi(json_path: str, locations_path: str, start_date=None, end_date=None) -> list[MonthlyResult]:
	global _daily_cache
	_daily_cache = {}

	global _JSON_PATH
	_JSON_PATH = json_path

	locCfg = load_locations(locations_path)

	results: list[MonthlyResult] = []
	lastMonths_reading = {}
	lastMonths_meterCfg = {}

	# if dates are strings, convert to date objects
	if isinstance(start_date, str):
		start_date = datetime.datetime.strptime(start_date, "%Y-%m-%d").date()
	if isinstance(end_date, str):
		end_date = datetime.datetime.strptime(end_date, "%Y-%m-%d").date()

	# loop over each month
	for month_end in month_ends(start_date, end_date):		
		# loop over each location
		for location in locCfg:
			locId = location["locId"]

			# find the FIRST valid meter at this location and this month
			for meterCfg in location["meters"]:
				if meterCfg.startDate <= month_end:
					reading = search_meter_reading(meterCfg.id, month_end)
					if not reading: # no reading found
						break
					
					# calculate consumption based on last month's reading
					oldValue    = lastMonths_reading.get(locId)
					oldMeterCfg = lastMonths_meterCfg.get(locId)
					consumption = calculate_monthly_consumption(oldValue, reading, oldMeterCfg, meterCfg)
					# store current reading for next month
					lastMonths_reading[locId] = reading
					lastMonths_meterCfg[locId] = meterCfg

					if consumption is not None:
						results.append(
							MonthlyResult(
								location_id = locId,
								flat        = location["flat"],
								month       = month_end.strftime("%Y-%m"),
								consumption = consumption,
								meter_id    = meterCfg.id,
								meter_value = reading.value,
								found_date  = reading.found_date.strftime("%Y-%m-%d")
							)
						)
					
					# only FIRST valid meter will be used, the next ones are too old and ignored
					break
	return results


# ============================================================
# Output
# ============================================================

def print_results(results: list[MonthlyResult]):
	print("\nMonatlicher Verbrauch:\n")
	for r in results:
		print(
			f"{r.month} | Platz {r.location_id} | Whg {r.flat} | "
			f"Zähler: {r.meter_id} | {r.found_date} = {r.meter_value:>6} | Verbrauch: {r.consumption:>6} "
		)
