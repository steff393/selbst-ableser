import os
import json
import datetime
from typing import Optional
from .model import MeterConfig, MeterReading, MonthlyResult
from .decrypt import decryptTelegram, getEncrMode
from .logger import dbg
from .cleanup import move_unused_files
from meterRegistry import MeterRegistry


_daily_cache = {}
_JSON_PATH = "."  # default


# ============================================================
# Helper functions
# ============================================================

def month_ends(start_date, end_date):
	# return list of last day of each month between start_date and end_date
	result = []
	# special case: if start_date is first of month, include last day of previous month
	if start_date.day == 1:
		prev_month_last = start_date - datetime.timedelta(days=1)
		if prev_month_last <= end_date:
			result.append(prev_month_last)
	# normal cases
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
		if wmbus_hex[0:2] == "32":
			# Byte 18/19, Little Endian
			return int(wmbus_hex[38:40] + wmbus_hex[36:38], 16)

		if wmbus_hex[0:2] == "38":
			# Byte 19/20/21, BCD
			return int(wmbus_hex[38:39]) * 100000 + \
			       int(wmbus_hex[39:40]) * 10000 + \
			       int(wmbus_hex[40:41]) * 1000 + \
			       int(wmbus_hex[41:42]) * 100 + \
			       int(wmbus_hex[42:43]) * 10 + \
			       int(wmbus_hex[43:44])

		if wmbus_hex[0:2] == "9e":
			# Byte 25-28, Little Endian
			return int(wmbus_hex[56:58] + wmbus_hex[54:56] + wmbus_hex[52:54] + wmbus_hex[50:52], 16)
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


def search_meter_reading(date: datetime.date, meter_id: str, aes_key: Optional[str] = None, max_back: int = 5) -> Optional[MeterReading]:
	# search up to max_back days before date for the meter reading
	for delta in range(max_back + 1):
		d = date - datetime.timedelta(days=delta)
		data = load_daily_file(d)
		if data and meter_id in data:
			if aes_key and getEncrMode(data["wmbus"].hex()) is not None:
				wmbus = decryptTelegram(data[meter_id]["wmbus"], aes_key)
				if wmbus:
					val = get_currHCA_from_wmbus(wmbus)
			else:
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
# Core logic
# ============================================================

def evaluate_uvi(json_path: str, registry: MeterRegistry, start_date=None, end_date=None, flat=None) -> list[MonthlyResult]:
	global _daily_cache
	_daily_cache = {}

	global _JSON_PATH
	_JSON_PATH = json_path

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
		for location in registry.all_locations(flat):
			meterCfg = registry.active_meter(location, month_end)
			if not meterCfg:
				continue

			reading = search_meter_reading(month_end, meterCfg.id, meterCfg.aes_key)
			if not reading: # no reading found
				continue
			
			oldValue    = lastMonths_reading.get(location)
			oldMeterCfg = lastMonths_meterCfg.get(location)
			if meterCfg.type == "HKV" and month_end.month == 1 and oldValue:
				oldValue.value = 0 # reset HKV value at cutoff date
			consumption = calculate_monthly_consumption(oldValue, reading, oldMeterCfg, meterCfg)
			lastMonths_reading[location] = reading
			lastMonths_meterCfg[location] = meterCfg

			if consumption is not None:
				results.append(
					MonthlyResult(
						location_id = location,
						flat        = meterCfg.flat,
						room        = meterCfg.room,
						type        = meterCfg.type,
						month       = month_end.strftime("%Y-%m"),
						consumption = consumption,
						meter_id    = meterCfg.id,
						meter_value = reading.value,
						found_date  = reading.found_date.strftime("%Y-%m-%d")
					)
				)
	
	# Move unused snapshot files to subfolder within the requested time range
	#if flat is None:  # only when evaluating all locations
	#	move_unused_files(results, json_path, start_date=start_date, end_date=end_date)
	
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
