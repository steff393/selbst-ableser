# Usage:
# in main project folder run:
# python -m tests.test_meterRegistry

from meterRegistry import MeterRegistry

print("[TEST] MeterRegistry Konsolen-Test gestartet")

registry = MeterRegistry("locations.json")
registry.print()

print("Alle Zählerplätze:")
print(registry.all_locations())

print("Zählerplätze in Wohnung 1:")
print(registry.all_locations("1"))

print("Zählerplätze in Wohnung 9:")
print(registry.all_locations("9"))

