# Usage:
# in main project folder run:
# python -m tests.test_meterReader

from meterReader import evaluate_uvi, print_results

print("[TEST] Evaluator Konsolen-Test gestartet")

results = evaluate_uvi(
	json_path      = "testData",   # folder with daily JSON files YYYY-MM-DD.json
	locations_path = "locations.json",
	start_date     = "2024-01-01",
	end_date       = "2025-12-31"
)

print_results(results)
