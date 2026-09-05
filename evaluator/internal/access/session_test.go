package access

import (
	"strings"
	"testing"
	"time"
)

func TestSessionCreateAndLookup(t *testing.T) {
	store := NewSessionStore(time.Hour)
	sess, err := store.Create(RoleTenant, "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" || sess.CSRFToken == "" {
		t.Fatal("session should have a non-empty ID and CSRF token")
	}

	got, ok := store.Lookup(sess.ID)
	if !ok || got.UnitID != "u1" {
		t.Fatalf("Lookup: ok=%v, got=%+v", ok, got)
	}
}

func TestSessionTwoCreatesDifferentIDs(t *testing.T) {
	store := NewSessionStore(time.Hour)
	a, _ := store.Create(RoleOperator, "")
	b, _ := store.Create(RoleOperator, "")
	if a.ID == b.ID {
		t.Fatal("two sessions must not share an ID")
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	now := time.Now()
	store := NewSessionStore(time.Minute)
	store.now = func() time.Time { return now }

	sess, _ := store.Create(RoleTenant, "u1")
	now = now.Add(2 * time.Minute) // past the idle timeout, no activity in between

	if _, ok := store.Lookup(sess.ID); ok {
		t.Fatal("an idle session should have expired")
	}
	// It must also be gone for good, not just reported expired once.
	if _, ok := store.Lookup(sess.ID); ok {
		t.Fatal("an expired session must not come back")
	}
}

func TestSessionActivityRefreshesTimeout(t *testing.T) {
	now := time.Now()
	store := NewSessionStore(time.Minute)
	store.now = func() time.Time { return now }

	sess, _ := store.Create(RoleTenant, "u1")
	now = now.Add(30 * time.Second)
	if _, ok := store.Lookup(sess.ID); !ok {
		t.Fatal("session should still be alive")
	}
	now = now.Add(45 * time.Second) // 75s since creation, but only 45s since the last lookup
	if _, ok := store.Lookup(sess.ID); !ok {
		t.Fatal("activity should have refreshed the idle timer")
	}
}

func TestSessionRevoke(t *testing.T) {
	store := NewSessionStore(time.Hour)
	sess, _ := store.Create(RoleOperator, "")
	store.Revoke(sess.ID)
	if _, ok := store.Lookup(sess.ID); ok {
		t.Fatal("a revoked session should not be found")
	}
}

func TestSessionRevokeByUnit(t *testing.T) {
	store := NewSessionStore(time.Hour)
	a, _ := store.Create(RoleTenant, "u1")
	b, _ := store.Create(RoleTenant, "u2")
	op, _ := store.Create(RoleOperator, "")

	store.RevokeByUnit("u1")

	if _, ok := store.Lookup(a.ID); ok {
		t.Error("session for the revoked unit should be gone")
	}
	if _, ok := store.Lookup(b.ID); !ok {
		t.Error("session for a different unit should be unaffected")
	}
	if _, ok := store.Lookup(op.ID); !ok {
		t.Error("operator session should be unaffected by a unit revocation")
	}
}

func TestSessionRevokeAll(t *testing.T) {
	store := NewSessionStore(time.Hour)
	a, _ := store.Create(RoleOperator, "")
	b, _ := store.Create(RoleTenant, "u1")
	store.RevokeAll()
	if _, ok := store.Lookup(a.ID); ok {
		t.Error("RevokeAll should have ended the operator session")
	}
	if _, ok := store.Lookup(b.ID); ok {
		t.Error("RevokeAll should have ended the tenant session")
	}
}

func TestSessionRevokeAllExcept(t *testing.T) {
	store := NewSessionStore(time.Hour)
	keep, _ := store.Create(RoleOperator, "")
	otherOp, _ := store.Create(RoleOperator, "")
	tenant, _ := store.Create(RoleTenant, "u1")

	store.RevokeAllExcept(keep.ID)

	if _, ok := store.Lookup(keep.ID); !ok {
		t.Error("the kept session should still be found")
	}
	if _, ok := store.Lookup(otherOp.ID); ok {
		t.Error("the other operator session should have been revoked")
	}
	if _, ok := store.Lookup(tenant.ID); ok {
		t.Error("the tenant session should have been revoked too")
	}
}

func TestVerifyCSRF(t *testing.T) {
	store := NewSessionStore(time.Hour)
	sess, _ := store.Create(RoleOperator, "")

	if !VerifyCSRF(sess, sess.CSRFToken) {
		t.Error("the session's own CSRF token should verify")
	}
	if VerifyCSRF(sess, "wrong") {
		t.Error("a wrong CSRF token should not verify")
	}
	if VerifyCSRF(nil, sess.CSRFToken) {
		t.Error("a nil session should never verify")
	}
}

// TestAuditActorNeverLeaksSessionID pins ZUGANG-08's "no full tokens in
// the log" for the one value most easily leaked into it by accident: the
// session ID is the session cookie, so an audit log carrying it verbatim
// would hand a live credential to anyone who reads the (unencrypted, and
// backed-up) log file.
func TestAuditActorNeverLeaksSessionID(t *testing.T) {
	store := NewSessionStore(time.Hour)
	op, _ := store.Create(RoleOperator, "")
	tenant, _ := store.Create(RoleTenant, "u3")

	for _, sess := range []*Session{op, tenant} {
		actor := sess.AuditActor()
		if strings.Contains(actor, sess.ID) {
			t.Errorf("AuditActor() = %q, must not contain the session ID", actor)
		}
		if strings.Contains(actor, sess.CSRFToken) {
			t.Errorf("AuditActor() = %q, must not contain the CSRF token", actor)
		}
	}

	if got := op.AuditActor(); !strings.HasPrefix(got, "operator/") {
		t.Errorf("operator AuditActor() = %q, want an operator/ prefix", got)
	}
	if got := tenant.AuditActor(); !strings.HasPrefix(got, "tenant u3/") {
		t.Errorf("tenant AuditActor() = %q, want a tenant u3/ prefix", got)
	}

	// Stable per session (so one session's events stay correlatable) and
	// different between sessions (so two logins are told apart).
	if op.AuditActor() != op.AuditActor() {
		t.Error("AuditActor() must be stable for the same session")
	}
	if op.AuditActor() == tenant.AuditActor() {
		t.Error("two different sessions must not share an audit actor")
	}
	if (*Session)(nil).AuditActor() != "unauthenticated" {
		t.Error("a nil session should audit as unauthenticated")
	}
}
