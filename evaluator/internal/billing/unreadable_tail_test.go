package billing

import (
	"testing"

	"selbst-ableser/internal/archive"
)

// A telegram that ends in a byte the record walk cannot follow — a lone
// DIF with no value behind it — still carries its reading in full, ahead
// of the damage. Real devices send these: the Qundis heat meter captured
// on 2026-09-09 ends on a dangling DIF 0x64. Discarding the value over a
// trailing byte loses a reading that was there to be read.
func TestReadValueSurvivesAnUnreadableTail(t *testing.T) {
	const (
		// The length byte is each telegram's own total minus itself.
		headerWithTail = "1144934401020304010778" // CI 0x78: no transport header, no encryption
		headerClean    = "1044934401020304010778"
		volume         = "0413e8030000" // DIF 04 / VIF 13: volume, 1000 l
		tail           = "64"           // a DIF announcing four bytes that never come
	)

	whole, found, err := ReadValue(archive.Entry{RawHex: headerWithTail + volume + tail}, [16]byte{})
	if err != nil {
		t.Fatalf("ReadValue: %v", err)
	}
	if !found {
		t.Fatal("no reading found in a telegram whose only damage is its last byte")
	}
	if whole.Value != 1000 {
		t.Errorf("value = %d, want 1000", whole.Value)
	}

	// The same telegram without the tail must of course still agree —
	// otherwise the test above proves nothing about the tail.
	clean, found, err := ReadValue(archive.Entry{RawHex: headerClean + volume}, [16]byte{})
	if err != nil || !found {
		t.Fatalf("clean telegram: %+v, %v, %v", clean, found, err)
	}
	if clean.Value != whole.Value {
		t.Errorf("tail changed the reading: %d with, %d without", whole.Value, clean.Value)
	}
}

// The error is not swallowed, only deferred: where the walk fails *before*
// anything usable was read, it is still what the caller gets.
func TestReadValueReportsAWalkErrorWhenNothingWasFound(t *testing.T) {
	const header = "0b44934401020304010778"
	_, found, err := ReadValue(archive.Entry{RawHex: header + "64"}, [16]byte{})
	if found {
		t.Fatal("found a reading in a telegram that has none")
	}
	if err == nil {
		t.Error("expected the walk error to be reported when no value was found")
	}
}
