def serialize_monthly_results(results):
		return [
			{
				"month": r.month,
				"location_id": r.location_id,
				"flat": r.flat,
				"room": r.room,
				"type": r.type,
				"meter_id": r.meter_id,
				"found_date": r.found_date,
				"meter_value": r.meter_value,
				"consumption": r.consumption
			}
			for r in results
		]


def serialize_monthly_aggregates(results):
		return [
			{
				"month": r.month,
				"type": r.type,
				"consumption": r.consumption
			}
			for r in results
		]
