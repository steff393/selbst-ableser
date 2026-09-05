package access

import (
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/telegram"
)

func openTestAuditLog(t *testing.T) *AuditLog {
	t.Helper()
	a, err := OpenAuditLog(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestAuditLogRecordAndQuery(t *testing.T) {
	a := openTestAuditLog(t)

	if err := a.Record(Event{Type: EventLoginFailure, Actor: "unauthenticated", Detail: "unit u3"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := a.Record(Event{Type: EventLoginSuccess, Actor: "operator/3f9a2c11", Detail: "unit u3"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	events, total, err := a.Events(Query{})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 || total != 2 {
		t.Fatalf("got %d events (total %d), want 2/2", len(events), total)
	}
	// Newest first.
	if events[0].Type != EventLoginSuccess || events[1].Type != EventLoginFailure {
		t.Errorf("unexpected order: %+v", events)
	}
	if events[0].At.IsZero() {
		t.Error("recorded event should have a non-zero timestamp")
	}
}

func TestAuditLogQueryRespectsLimitButReportsTotal(t *testing.T) {
	a := openTestAuditLog(t)
	for i := 0; i < 5; i++ {
		if err := a.Record(Event{Type: EventDataIngested, Actor: "collector-report"}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	events, total, err := a.Events(Query{Limit: 2})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	// The total is what makes "showing 2 of 5" possible without a second query.
	if total != 5 {
		t.Errorf("total = %d, want 5 (every match, not just the returned page)", total)
	}
}

func TestAuditLogQueryFiltersByTypeAndDay(t *testing.T) {
	a := openTestAuditLog(t)

	record := func(typ EventType, month time.Month, day int) {
		t.Helper()
		at := time.Date(2026, month, day, 12, 0, 0, 0, telegram.Local)
		if err := a.Record(Event{Type: typ, At: at, Actor: "operator/aabbccdd"}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	// These deliberately straddle the daylight-saving change, whose
	// shifting UTC offset would break a naive lexical timestamp compare.
	record(EventLoginSuccess, time.January, 15)
	record(EventLoginFailure, time.January, 20)
	record(EventLoginSuccess, time.July, 10)
	record(EventMasterDataChange, time.July, 11)

	events, total, err := a.Events(Query{Types: []EventType{EventLoginSuccess}})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 || total != 2 {
		t.Fatalf("type filter returned %d events (total %d), want 2/2", len(events), total)
	}
	for _, e := range events {
		if e.Type != EventLoginSuccess {
			t.Errorf("type filter leaked a %s event", e.Type)
		}
	}

	events, _, err = a.Events(Query{From: "2026-07-01"})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("From filter returned %d events, want the 2 from July", len(events))
	}

	events, _, err = a.Events(Query{From: "2026-01-16", To: "2026-07-10"})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("day range returned %d events, want 2 (both bounds inclusive, across the DST change)", len(events))
	}
}

func TestAuditLogHasEvent(t *testing.T) {
	a := openTestAuditLog(t)

	has, err := a.HasEvent(EventNotificationSent, "unit u1 month 2025-06")
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if has {
		t.Fatal("HasEvent should be false before anything was recorded")
	}

	if err := a.Record(Event{Type: EventNotificationSent, Detail: "unit u1 month 2025-06"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	has, err = a.HasEvent(EventNotificationSent, "unit u1 month 2025-06")
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if !has {
		t.Error("HasEvent should be true after a matching event was recorded")
	}

	has, err = a.HasEvent(EventNotificationSent, "unit u1 month 2025-07")
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if has {
		t.Error("HasEvent should not match a different month")
	}
}
