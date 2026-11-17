package billing

import (
	"path/filepath"
	"testing"

	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// hcaPayloadWithReset builds a standard record set carrying both the live
// value (storage 0) and the value frozen at the last billing-period reset
// (storage 1, DIF bit 6 set), plus that reset's date.
func hcaPayloadWithReset(current, frozen [3]byte, resetDate [2]byte) []byte {
	p := []byte{0x2F, 0x2F, 0x0B, 0x6E}
	p = append(p, current[:]...)
	p = append(p, 0x4B, 0x6E) // the same record, storage number 1
	p = append(p, frozen[:]...)
	p = append(p, 0x42, 0x6C) // 16-bit type G date, storage number 1
	p = append(p, resetDate[:]...)
	for len(p) < 32 {
		p = append(p, 0x2F)
	}
	return p
}

// bcd3 encodes a value the way hcaPayload's three BCD bytes are read.
func bcd3(v int) [3]byte {
	var out [3]byte
	for i := 0; i < 3; i++ {
		out[i] = byte(v%10) | byte((v/10)%10)<<4
		v /= 100
	}
	return out
}

// dateG20251231 is 31 December 2025 in the type G encoding decodeDateG reads.
var dateG20251231 = [2]byte{0x3F, 0x3C}

func testStore(t *testing.T) *archive.Store {
	t.Helper()
	store, err := archive.OpenStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func insertEntry(t *testing.T, store *archive.Store, meter string, day telegram.Day, rawHex string) {
	t.Helper()
	if _, err := store.InsertHistorical(archive.Entry{
		MeterID: meter, Day: day, ReceivedAt: dayTime(t, day), RSSI: -80, RawHex: rawHex,
	}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}
}

func heatingMasterData(t *testing.T, key [16]byte, freezeMonth int) masterdata.MasterData {
	t.Helper()
	return masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "1", Name: "EG", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{{ID: "101", UnitID: "1", Room: "WOH", Kind: masterdata.KindHeatCostAllocator}},
		Meters: []masterdata.Meter{{
			Number: "90000001", MeterPointID: "101", AESKey: key,
			InstalledAt: mustDay(t, "2024-01-01"), FreezeMonth: freezeMonth,
		}},
	}
}

func TestFindBillingResetReadsTheFrozenValueFromALaterTelegram(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)

	// Only a January telegram exists, and it still carries what the device
	// froze on 31 December. That is the whole point of reading it forward.
	insertEntry(t, store, "90000001", mustDay(t, "2026-01-09"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(95), bcd3(313), dateG20251231)))

	got, ok, err := FindBillingReset(store, "90000001", key, mustDay(t, "2025-12-31"), 0)
	if err != nil || !ok {
		t.Fatalf("FindBillingReset: ok=%v err=%v", ok, err)
	}
	if got.Value != 313 {
		t.Errorf("frozen value = %d, want 313", got.Value)
	}
	if !got.HasDate || got.Date.Day != 31 || got.Date.Month != 12 || got.Date.Year != 2025 {
		t.Errorf("date = %+v (has=%v), want 2025-12-31", got.Date, got.HasDate)
	}
}

// TestFindBillingResetRefusesAForeignPeriod guards against a device whose
// frozen value belongs to a different reset day — a replacement whose
// first due date has not been reached, or one configured for another
// month. Reporting nothing found leaves the caller with its previous
// behaviour instead of closing a period on someone elses number.
func TestFindBillingResetRefusesAForeignPeriod(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)
	insertEntry(t, store, "90000001", mustDay(t, "2026-01-09"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(95), bcd3(313), [2]byte{0x3E, 0x36})))

	_, ok, err := FindBillingReset(store, "90000001", key, mustDay(t, "2025-12-31"), 0)
	if err != nil {
		t.Fatalf("FindBillingReset: %v", err)
	}
	if ok {
		t.Error("a frozen value from another reset day must not be used")
	}
}

func TestFindBillingResetSearchesForwardOnly(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)
	// A telegram from before the reset cannot carry this period's frozen
	// value, so it must not be consulted.
	insertEntry(t, store, "90000001", mustDay(t, "2025-12-20"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(300), bcd3(999), dateG20251231)))

	_, ok, err := FindBillingReset(store, "90000001", key, mustDay(t, "2025-12-31"), 0)
	if err != nil {
		t.Fatalf("FindBillingReset: %v", err)
	}
	if ok {
		t.Error("a telegram from before the reset day must not be used")
	}
}

// TestMonthlyReadingsCloseThePeriodOnTheFrozenValue is the case that
// motivated all of this: the meter goes quiet over the turn of the year,
// so December's last telegram is from the 26th. Closing December on that
// telegram loses everything measured between then and the reset — and
// unlike an ordinary month, January cannot make it up, because it starts
// from zero.
func TestMonthlyReadingsCloseThePeriodOnTheFrozenValue(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)

	insertEntry(t, store, "90000001", mustDay(t, "2025-11-30"),
		buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(233))))
	insertEntry(t, store, "90000001", mustDay(t, "2025-12-26"),
		buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(300))))
	insertEntry(t, store, "90000001", mustDay(t, "2026-01-31"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(95), bcd3(313), dateG20251231)))

	md := heatingMasterData(t, key, 12)
	monthEnds := []telegram.Day{mustDay(t, "2025-11-30"), mustDay(t, "2025-12-31"), mustDay(t, "2026-01-31")}

	readings, err := MonthlyReadingsForMeterPoint(store, md, "101", monthEnds, 10)
	if err != nil {
		t.Fatalf("MonthlyReadingsForMeterPoint: %v", err)
	}
	if len(readings) != 3 {
		t.Fatalf("readings = %+v, want three months", readings)
	}
	if readings[1].Value != 313 {
		t.Errorf("December closes on %d, want the frozen 313 (its last telegram said 300)", readings[1].Value)
	}
	if readings[1].Day != mustDay(t, "2025-12-31") {
		t.Errorf("December's day = %s, want the month end", readings[1].Day)
	}

	results, err := CalculateSeries(readings,
		func(string, telegram.Day) int { return 12 },
		func(string, telegram.Day) float64 { return 1 },
		func(string, string) (SwapCorrection, bool) { return SwapCorrection{}, false })
	if err != nil {
		t.Fatalf("CalculateSeries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two months", results)
	}
	if results[0].RawConsumption != 80 { // 313 - 233, not 300 - 233
		t.Errorf("December = %v, want 80", results[0].RawConsumption)
	}
	if results[1].RawConsumption != 95 { // the new period counts from zero
		t.Errorf("January = %v, want 95", results[1].RawConsumption)
	}
	if !results[1].BillingReset {
		t.Error("January must be marked as the start of a new billing period")
	}
}

// TestMonthlyReadingsCloseAPeriodWithoutAnyTelegramOfItsOwn goes one step
// further: the device was silent for all of December. The period still
// closes exactly, because the frozen value travels in January's telegrams.
func TestMonthlyReadingsCloseAPeriodWithoutAnyTelegramOfItsOwn(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)

	insertEntry(t, store, "90000001", mustDay(t, "2025-11-30"),
		buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(233))))
	insertEntry(t, store, "90000001", mustDay(t, "2026-01-05"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(8), bcd3(313), dateG20251231)))

	readings, err := MonthlyReadingsForMeterPoint(store, heatingMasterData(t, key, 12),
		"101", []telegram.Day{mustDay(t, "2025-11-30"), mustDay(t, "2025-12-31")}, 5)
	if err != nil {
		t.Fatalf("MonthlyReadingsForMeterPoint: %v", err)
	}
	if len(readings) != 2 || readings[1].Value != 313 {
		t.Errorf("readings = %+v, want December closed on the frozen 313", readings)
	}
}

// TestSeriesAcrossASwapInTheFirstMonthOfAPeriod runs a replacement in
// January through master data and archive, the way a report does: the
// reset is decided by the outgoing device's Stichtag as resolved from
// master data, whatever the incoming one says.
func TestSeriesAcrossASwapInTheFirstMonthOfAPeriod(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	swapDay := mustDay(t, "2026-01-15")

	cases := []struct {
		name                 string
		oldFreeze, newFreeze int
		oldDecember          []byte // the outgoing device's telegram around the turn of the year
		oldDecemberDay       string
		endReading           int64
		want                 []float64 // December, January, February
		wantJanuaryReset     bool
	}{
		{
			// The old device froze 313 on 31 December and counted 30 more
			// until it came out; the new one counts straight through.
			name: "outgoing resets, incoming has no Stichtag", oldFreeze: 12, newFreeze: 0,
			oldDecember:      hcaPayloadWithReset(bcd3(20), bcd3(313), dateG20251231),
			oldDecemberDay:   "2026-01-09",
			endReading:       30,
			want:             []float64{80, 50, 35}, // 313-233, 30+(25-5), 60-25
			wantJanuaryReset: true,
		},
		{
			// The old device counted straight through from 313 to 343; the
			// new one has a Stichtag of its own that plays no part yet.
			name: "outgoing has no Stichtag, incoming resets", oldFreeze: 0, newFreeze: 12,
			oldDecember:    hcaPayload(bcd3(313)),
			oldDecemberDay: "2025-12-31",
			endReading:     343,
			want:           []float64{80, 50, 35}, // 313-233, (343-313)+(25-5), 60-25
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := testStore(t)
			insertEntry(t, store, "90000001", mustDay(t, "2025-11-30"), buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(233))))
			insertEntry(t, store, "90000001", mustDay(t, c.oldDecemberDay), buildEncryptedTelegramHex(t, key, c.oldDecember))
			insertEntry(t, store, "90000002", mustDay(t, "2026-01-31"), buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(25))))
			insertEntry(t, store, "90000002", mustDay(t, "2026-02-28"), buildEncryptedTelegramHex(t, key, hcaPayload(bcd3(60))))

			md := heatingMasterData(t, key, c.oldFreeze)
			md.Meters[0].RemovedAt = &swapDay
			md.Meters[0].EndReading = &c.endReading
			md.Meters = append(md.Meters, masterdata.Meter{
				Number: "90000002", MeterPointID: "101", AESKey: key,
				InstalledAt: swapDay, StartReading: 5, FreezeMonth: c.newFreeze,
			})

			results, err := seriesForMeterPoint(store, md, md.MeterPoints[0], mustDay(t, "2025-11-01"), mustDay(t, "2026-02-28"), 0)
			if err != nil {
				t.Fatalf("seriesForMeterPoint: %v", err)
			}
			if len(results) != len(c.want) {
				t.Fatalf("results = %+v, want %d months", results, len(c.want))
			}
			for i, want := range c.want {
				if results[i].RawConsumption != want {
					t.Errorf("%s = %v, want %v", results[i].Month, results[i].RawConsumption, want)
				}
			}
			if results[1].Swap == nil || results[1].BillingReset != c.wantJanuaryReset {
				t.Errorf("January = %+v, want a swap with reset=%v", results[1], c.wantJanuaryReset)
			}
			if results[0].BillingReset || results[2].BillingReset {
				t.Errorf("December and February must not be reset months: %+v", results)
			}
		})
	}
}

// TestMonthlyReadingsLeaveMetersWithoutAStichtagAlone: a meter without a
// Stichtag month never resets, so nothing here may touch it even if its
// telegram carried a frozen value.
func TestMonthlyReadingsLeaveMetersWithoutAStichtagAlone(t *testing.T) {
	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store := testStore(t)
	insertEntry(t, store, "90000002", mustDay(t, "2025-12-26"),
		buildEncryptedTelegramHex(t, key, hcaPayloadWithReset(bcd3(300), bcd3(999), dateG20251231)))

	md := masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "1", Name: "EG", AreaM2: 50}},
		MeterPoints: []masterdata.MeterPoint{{ID: "121", UnitID: "1", Room: "BAD", Kind: masterdata.KindHotWater}},
		Meters: []masterdata.Meter{{
			Number: "90000002", MeterPointID: "121", AESKey: key,
			InstalledAt: mustDay(t, "2024-01-01"),
		}},
	}
	readings, err := MonthlyReadingsForMeterPoint(store, md, "121", []telegram.Day{mustDay(t, "2025-12-31")}, 10)
	if err != nil {
		t.Fatalf("MonthlyReadingsForMeterPoint: %v", err)
	}
	if len(readings) != 1 || readings[0].Value != 300 {
		t.Errorf("readings = %+v, want the plain telegram value 300", readings)
	}
}
