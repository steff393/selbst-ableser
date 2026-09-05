package access

import "errors"

// ErrUnauthorized is returned uniformly for any access-control failure:
// no session, wrong role, wrong unit. ZUGANG-07 requires that a caller
// cannot distinguish these cases (or infer whether a unit or a piece of
// data exists) from the error alone.
var ErrUnauthorized = errors.New("access: not authorized")

// RequireOperator authorizes an action restricted to the operator role
// (ZUGANG-01): master data changes, access management, archive access,
// unlocking, and the evaluator's live view (ZUGANG-05).
func RequireOperator(sess *Session) error {
	if sess == nil || sess.Role != RoleOperator {
		return ErrUnauthorized
	}
	return nil
}

// RequireTenant authorizes an action restricted to the tenant of one
// specific unit (FACH-10): a session must exist, hold the tenant role, and
// be scoped to exactly that unit.
func RequireTenant(sess *Session, unitID string) error {
	if sess == nil || sess.Role != RoleTenant || sess.UnitID != unitID {
		return ErrUnauthorized
	}
	return nil
}

// RequireUVIAccess authorizes viewing one unit's UVI (UI-02): either the
// tenant of exactly that unit, or an operator, who may view any unit's
// UVI ("der Mieter aus Wohnung 4 fragt nach" — control and support, not
// something a tenant may do for another tenant's unit).
func RequireUVIAccess(sess *Session, unitID string) error {
	if sess == nil {
		return ErrUnauthorized
	}
	if sess.Role == RoleOperator {
		return nil
	}
	return RequireTenant(sess, unitID)
}
