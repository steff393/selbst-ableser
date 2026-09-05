package livepush

import "testing"

func TestBufferRecentOrderAndCapacity(t *testing.T) {
	b := NewBuffer(3)
	b.Add(Telegram{MeterID: "1"})
	b.Add(Telegram{MeterID: "2"})
	b.Add(Telegram{MeterID: "3"})
	b.Add(Telegram{MeterID: "4"}) // pushes "1" out

	got := b.Recent(10)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (capacity)", len(got))
	}
	want := []string{"4", "3", "2"} // newest first
	for i, w := range want {
		if got[i].MeterID != w {
			t.Errorf("got[%d].MeterID = %q, want %q", i, got[i].MeterID, w)
		}
	}
}

func TestBufferClear(t *testing.T) {
	b := NewBuffer(10)
	b.Add(Telegram{MeterID: "1"}, Telegram{MeterID: "2"})
	b.Clear()
	if got := b.Recent(10); len(got) != 0 {
		t.Errorf("Recent(10) after Clear() = %+v, want empty", got)
	}
	// Still usable afterward, not left in some broken zero-value state.
	b.Add(Telegram{MeterID: "3"})
	if got := b.Recent(10); len(got) != 1 || got[0].MeterID != "3" {
		t.Errorf("Recent(10) after Clear()+Add() = %+v, want one entry %q", got, "3")
	}
}

func TestBufferRecentLimit(t *testing.T) {
	b := NewBuffer(10)
	b.Add(Telegram{MeterID: "1"}, Telegram{MeterID: "2"}, Telegram{MeterID: "3"})
	got := b.Recent(2)
	if len(got) != 2 || got[0].MeterID != "3" || got[1].MeterID != "2" {
		t.Errorf("Recent(2) = %+v", got)
	}
}
