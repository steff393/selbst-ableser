package billing

import "testing"

func TestClipToAccess_AcceptanceCriterion(t *testing.T) {
	// FACH-10's own acceptance criterion: a tenant who moved in on
	// 2025-06-01 and requests 2024-01-01..2025-12-31 gets exactly
	// 2025-06-01..2025-12-31.
	moveIn := mustDay(t, "2025-06-01")
	start, end, empty := ClipToAccess(mustDay(t, "2024-01-01"), mustDay(t, "2025-12-31"), moveIn, nil)
	if empty {
		t.Fatal("expected a non-empty range")
	}
	if start != moveIn {
		t.Errorf("start = %s, want %s", start, moveIn)
	}
	if end != mustDay(t, "2025-12-31") {
		t.Errorf("end = %s, want 2025-12-31", end)
	}
}

func TestClipToAccess_MoveOutLimitsEnd(t *testing.T) {
	moveOut := mustDay(t, "2025-03-31")
	_, end, empty := ClipToAccess(mustDay(t, "2024-01-01"), mustDay(t, "2025-12-31"), mustDay(t, "2020-01-01"), &moveOut)
	if empty {
		t.Fatal("expected a non-empty range")
	}
	if end != moveOut {
		t.Errorf("end = %s, want %s", end, moveOut)
	}
}

func TestClipToAccess_EmptyIntersection(t *testing.T) {
	moveIn := mustDay(t, "2026-01-01")
	_, _, empty := ClipToAccess(mustDay(t, "2024-01-01"), mustDay(t, "2025-12-31"), moveIn, nil)
	if !empty {
		t.Error("expected an empty range when the access period starts after the request ends")
	}
}
