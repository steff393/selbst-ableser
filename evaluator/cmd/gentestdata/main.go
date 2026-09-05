// Command gentestdata extends an archive-schema SQLite database (see
// test/archive.db, the demo dataset docs/quickstart.md points at) by one or more
// additional month-end periods, one new entry per meter already present.
// Each meter continues its own trend rather than a fixed global one: a
// heat-cost allocator's own recently implied yearly pace, redistributed
// across the year with a winter-heavy seasonal curve and reset each January
// (FACH-01/07's annual-reset billing model); a water meter's own recent
// average monthly rate, steadily increasing. Both get a bit of per-run
// randomness so repeated runs don't retrace an identical line.
//
// Every new telegram is built via internal/correction.Build from the
// meter's own last telegram as a template — same manufacturer, medium, and
// encoding, only the value itself changes — so this tool never needs its
// own copy of the wM-Bus encoding rules.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/correction"
	"selbst-ableser/internal/decode"
	"selbst-ableser/internal/telegram"
)

func main() {
	dbPath := flag.String("db", "test/archive.db", "path to the archive.db to extend")
	months := flag.Int("months", 1, "number of additional month-end periods to generate")
	flag.Parse()

	if err := run(*dbPath, *months); err != nil {
		fmt.Fprintln(os.Stderr, "gentestdata:", err)
		os.Exit(1)
	}
}

func run(dbPath string, months int) error {
	if months < 1 {
		return fmt.Errorf("-months must be at least 1")
	}

	store, err := archive.OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	entries, err := store.AllEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("archive at %s is empty; nothing to extend", dbPath)
	}

	byMeter := make(map[string][]archive.Entry)
	for _, e := range entries {
		byMeter[e.MeterID] = append(byMeter[e.MeterID], e)
	}
	meterIDs := make([]string, 0, len(byMeter))
	for id := range byMeter {
		meterIDs = append(meterIDs, id)
	}
	sort.Strings(meterIDs)

	inserted := 0
	for period := 0; period < months; period++ {
		for _, id := range meterIDs {
			hist := byMeter[id]
			next, err := nextEntry(hist)
			if err != nil {
				return fmt.Errorf("meter %s: %w", id, err)
			}
			changed, err := store.InsertHistorical(next)
			if err != nil {
				return fmt.Errorf("meter %s, day %s: %w", id, next.Day, err)
			}
			if changed {
				inserted++
			}
			byMeter[id] = append(hist, next)
		}
	}

	fmt.Printf("gentestdata: extended %s by %d month(s) across %d meters (%d entries inserted)\n",
		dbPath, months, len(meterIDs), inserted)
	return nil
}

// zeroKey is safe here because every telegram in a gentestdata-built
// archive is cleartext (CI 0x78, no config word — see the old CSV-based
// generator this replaced): internal/crypto.Decrypt never looks at the key
// for those, so correction.Build never actually needs a real one.
var zeroKey [16]byte

// monthValue is one meter's decoded reading for one archived month, used
// to reconstruct its recent trend.
type monthValue struct {
	day   time.Time
	value int64
}

// nextEntry builds hist's meter's next month-end entry from its own
// history: the last entry is the template telegram (same manufacturer,
// medium, and value encoding), and the new value comes from either
// nextHKVValue or nextWaterValue depending on what unit the template's own
// current-value record decodes to.
func nextEntry(hist []archive.Entry) (archive.Entry, error) {
	last := hist[len(hist)-1]
	reading, err := decodedValue(last)
	if err != nil {
		return archive.Entry{}, fmt.Errorf("decoding last telegram: %w", err)
	}

	lastDay, err := time.ParseInLocation("2006-01-02", string(last.Day), telegram.Local)
	if err != nil {
		return archive.Entry{}, err
	}
	nextDay := lastDayOfNextMonth(lastDay)

	months, err := monthlyHistory(hist)
	if err != nil {
		return archive.Entry{}, fmt.Errorf("decoding history: %w", err)
	}

	var newValue int64
	switch reading.Unit {
	case decode.UnitHeatCostAllocator:
		newValue = nextHKVValue(months, nextDay)
	case decode.UnitLiters:
		newValue = nextWaterValue(months)
	default:
		return archive.Entry{}, fmt.Errorf("unsupported unit %v", reading.Unit)
	}

	rawHex, _, err := correction.Build(last, zeroKey, newValue)
	if err != nil {
		return archive.Entry{}, fmt.Errorf("building telegram: %w", err)
	}

	ts := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 23, 55, 0, 0, telegram.Local)
	return archive.Entry{
		MeterID:    last.MeterID,
		Day:        telegram.DayOf(ts),
		ReceivedAt: ts,
		RSSI:       -70,
		RawHex:     rawHex,
	}, nil
}

func decodedValue(e archive.Entry) (decode.Reading, error) {
	raw, err := hex.DecodeString(e.RawHex)
	if err != nil {
		return decode.Reading{}, err
	}
	frame, err := telegram.ParseWMBus(raw)
	if err != nil {
		return decode.Reading{}, err
	}
	records, ok, err := decode.Standard(frame.CI, raw)
	if err != nil {
		return decode.Reading{}, err
	}
	if !ok {
		return decode.Reading{}, fmt.Errorf("no standard-compliant records in CI 0x%02X", frame.CI)
	}
	reading, found, err := decode.CurrentValue(records)
	if err != nil {
		return decode.Reading{}, err
	}
	if !found {
		return decode.Reading{}, fmt.Errorf("no current-value record")
	}
	return reading, nil
}

func monthlyHistory(hist []archive.Entry) ([]monthValue, error) {
	out := make([]monthValue, 0, len(hist))
	for _, e := range hist {
		reading, err := decodedValue(e)
		if err != nil {
			return nil, err
		}
		day, err := time.ParseInLocation("2006-01-02", string(e.Day), telegram.Local)
		if err != nil {
			return nil, err
		}
		out = append(out, monthValue{day: day, value: reading.Value})
	}
	return out, nil
}

// lastDayOfNextMonth returns the last calendar day of the month after d's —
// day 0 of (month+2) is the day before that month starts, i.e. the last day
// of (month+1); time.Date normalizes the month overflow, including a year
// rollover from December.
func lastDayOfNextMonth(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month()+2, 0, 0, 0, 0, 0, telegram.Local)
}

// seasonalFraction is a heat cost allocator's typical share of its annual
// total added in each month following the January reset (FACH-01's
// heating-season model: near-zero in summer, heaviest in mid-winter),
// derived from this project's own synthetic reference data and reused here
// so freshly generated months follow the same shape. Fractions sum to 1;
// January itself has none, since that month is the reset instead.
var seasonalFraction = map[time.Month]float64{
	time.February:  0.179,
	time.March:     0.159,
	time.April:     0.0945,
	time.May:       0.048,
	time.June:      0.0115,
	time.July:      0.013,
	time.August:    0.024,
	time.September: 0.0365,
	time.October:   0.0975,
	time.November:  0.1425,
	time.December:  0.195,
}

// nextHKVValue continues a heat-cost allocator's own trend. Crossing into
// January means a fresh annual cycle: a new, small starting value scaled to
// a random fraction of the meter's last completed cycle. Any other month
// adds this cycle's own implied annual total times that month's seasonal
// share, both with a bit of jitter so repeated runs (and different meters)
// don't all move in lockstep.
func nextHKVValue(months []monthValue, nextDay time.Time) int64 {
	last := months[len(months)-1]

	if nextDay.Month() == time.January {
		total := lastCycleTotal(months)
		if total <= 0 {
			total = 200 // no full cycle in history yet; a plausible fallback scale
		}
		v := int64(float64(total) * (0.05 + rand.Float64()*0.15)) // 5-20% of last cycle
		return clampMin(v, 10)
	}

	total := impliedCycleTotal(months)
	frac := seasonalFraction[nextDay.Month()]
	increment := int64(total * frac * (0.85 + rand.Float64()*0.30)) // +/-15%
	return last.value + clampMin(increment, 1)
}

// lastCycleTotal finds the most recently completed January-to-December
// cycle in months and returns its total (December's value minus that same
// January's) — 0 if no such pair exists yet.
func lastCycleTotal(months []monthValue) int64 {
	var dec, jan monthValue
	haveDec := false
	for i := len(months) - 1; i >= 0; i-- {
		m := months[i]
		if !haveDec {
			if m.day.Month() == time.December {
				dec = m
				haveDec = true
			}
			continue
		}
		if m.day.Month() == time.January && m.day.Year() == dec.day.Year() {
			jan = m
			return dec.value - jan.value
		}
	}
	return 0
}

// impliedCycleTotal estimates the current annual cycle's total from the
// most recent real month-over-month increase and that month's own
// seasonal share — self-correcting, so it needs no separate stored state
// across repeated gentestdata runs. Falls back to lastCycleTotal if the
// history ends exactly on a January reset (no increase within the current
// cycle to measure yet). Assumes the last two entries are one calendar
// month apart, true for the uniform month-end cadence every entry in this
// archive was generated with; a gappier history would misattribute a
// multi-month gap's increase to a single month's seasonal share.
func impliedCycleTotal(months []monthValue) float64 {
	last := months[len(months)-1]
	if last.day.Month() == time.January {
		if total := lastCycleTotal(months); total > 0 {
			return float64(total)
		}
		return 200
	}
	if len(months) < 2 {
		return 200
	}
	prev := months[len(months)-2]
	frac := seasonalFraction[last.day.Month()]
	if frac <= 0 {
		return 200
	}
	return float64(last.value-prev.value) / frac
}

// nextWaterValue continues a water meter's own recent average monthly
// growth rate — no seasonal reset, just a steadily increasing total, with
// jitter so it doesn't repeat the exact same increment every call. The rate
// is measured over actual elapsed calendar months between the first and
// last entries, not the entry count, so a gappy history still yields a
// correct average rather than an inflated one.
func nextWaterValue(months []monthValue) int64 {
	last := months[len(months)-1]
	first := months[0]
	elapsed := monthsBetween(first.day, last.day)
	rate := 50.0 // fallback for a meter with only one reading so far
	if elapsed > 0 {
		rate = float64(last.value-first.value) / float64(elapsed)
	}
	increment := int64(rate * (0.8 + rand.Float64()*0.4)) // +/-20%
	return last.value + clampMin(increment, 1)
}

// monthsBetween returns the number of calendar months from a to b (b
// assumed not before a), independent of the day-of-month component.
func monthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func clampMin(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}
