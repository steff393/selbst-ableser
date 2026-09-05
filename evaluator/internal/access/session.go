package access

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"

	"selbst-ableser/internal/telegram"
)

// Role is who a session belongs to.
type Role int

const (
	RoleNone Role = iota
	// RoleOperator: full access, established by unlocking the master
	// data (STAMM-04) — see Vault.Unlock and this package's doc comment
	// on why there is no separate operator credential.
	RoleOperator
	// RoleTenant: restricted to one unit, established by presenting a
	// valid access token (ZUGANG-02).
	RoleTenant
)

// Session is a server-side login session (ZUGANG-03). ID is what goes in
// the session cookie; it and CSRFToken are random and unguessable, and
// neither is ever logged (see audit.go).
type Session struct {
	ID        string
	Role      Role
	UnitID    string // set only for RoleTenant
	CSRFToken string
	CreatedAt time.Time
	LastSeen  time.Time

	// AccessStart and AccessEnd are the tenant access grant's own period
	// (set only for RoleTenant), carried on the session so FACH-10's
	// clipping doesn't need to re-look-up which grant a login used.
	AccessStart telegram.Day
	AccessEnd   *telegram.Day
}

// AuditActor is how a session identifies itself in the security log
// (ZUGANG-08). It deliberately never contains Session.ID: that value is
// the session cookie — a live bearer credential — while the audit log is
// stored unencrypted and is carried along in every backup, which is
// exactly why ZUGANG-08 forbids full tokens there. A truncated hash keeps
// one session's events correlatable with each other (the property that
// makes the log worth reading at all) without being usable to impersonate
// it, and the role prefix says who acted without naming a person (SZ-6).
func (s *Session) AuditActor() string {
	if s == nil {
		return "unauthenticated"
	}
	sum := sha256.Sum256([]byte(s.ID))
	ref := hex.EncodeToString(sum[:4])
	switch s.Role {
	case RoleOperator:
		return "operator/" + ref
	case RoleTenant:
		return "tenant " + s.UnitID + "/" + ref
	default:
		return "unknown/" + ref
	}
}

// SessionStore holds live sessions in memory. Sessions are never persisted
// to disk: a restart requires logging in again, which is acceptable and
// keeps FACH-12's "nothing decrypted touches disk" property simple to
// reason about (a session by itself carries no plaintext, but keeping the
// store in-memory-only avoids the question entirely).
type SessionStore struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	idleTimeout time.Duration
	now         func() time.Time
}

// NewSessionStore creates a store that expires a session after it has
// seen no activity for idleTimeout (ZUGANG-03).
func NewSessionStore(idleTimeout time.Duration) *SessionStore {
	return &SessionStore{
		sessions:    make(map[string]*Session),
		idleTimeout: idleTimeout,
		now:         time.Now,
	}
}

// Create starts a new session and returns it. A fresh, random ID is
// generated on every call (never reused, never derived from user input),
// which is what makes session fixation and prediction impractical.
func (s *SessionStore) Create(role Role, unitID string) (*Session, error) {
	id, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{ID: id, Role: role, UnitID: unitID, CSRFToken: csrf, CreatedAt: now, LastSeen: now}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = sess
	return sess, nil
}

// CreateTenant starts a new RoleTenant session for a verified access
// grant, carrying the grant's own period for FACH-10's clipping (see
// VerifyAccessToken).
func (s *SessionStore) CreateTenant(unitID string, accessStart telegram.Day, accessEnd *telegram.Day) (*Session, error) {
	sess, err := s.Create(RoleTenant, unitID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.AccessStart = accessStart
	sess.AccessEnd = accessEnd
	return sess, nil
}

// Lookup returns the session for id, if it exists and has not gone idle
// (in which case it is dropped, not just reported absent — ZUGANG-07: an
// expired session must behave exactly like one that never existed).
// A successful lookup counts as activity and refreshes the idle timer.
func (s *SessionStore) Lookup(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	if s.now().Sub(sess.LastSeen) > s.idleTimeout {
		delete(s.sessions, id)
		return nil, false
	}
	sess.LastSeen = s.now()
	return sess, true
}

// Revoke ends one session immediately (logout, or an operator revoking a
// tenant's access — ZUGANG-04).
func (s *SessionStore) Revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// RevokeByUnit ends every live session for a unit. ZUGANG-04 requires a
// revoked access to stop working immediately, including for sessions that
// were already established before the revocation.
func (s *SessionStore) RevokeByUnit(unitID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.Role == RoleTenant && sess.UnitID == unitID {
			delete(s.sessions, id)
		}
	}
}

// RevokeAll ends every live session.
func (s *SessionStore) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*Session)
}

// RevokeAllExcept ends every live session other than keepID — the operator
// password change's own use: a password change happens precisely when a
// compromise is suspected, so every *other* session (a possible
// eavesdropper) should end immediately, but the operator who just proved
// they know the new password by setting it should not be logged out by
// their own action.
func (s *SessionStore) RevokeAllExcept(keepID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.sessions {
		if id != keepID {
			delete(s.sessions, id)
		}
	}
}

// VerifyCSRF checks a submitted CSRF token against the session's, in
// constant time.
func VerifyCSRF(sess *Session, provided string) bool {
	if sess == nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(provided)) == 1
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
