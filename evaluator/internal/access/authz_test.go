package access

import (
	"errors"
	"testing"
)

func TestRequireOperator(t *testing.T) {
	if err := RequireOperator(nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("nil session: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireOperator(&Session{Role: RoleTenant}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("tenant session: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireOperator(&Session{Role: RoleOperator}); err != nil {
		t.Errorf("operator session: err = %v, want nil", err)
	}
}

func TestRequireTenant(t *testing.T) {
	if err := RequireTenant(nil, "u1"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("nil session: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireTenant(&Session{Role: RoleOperator}, "u1"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("operator session: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireTenant(&Session{Role: RoleTenant, UnitID: "u2"}, "u1"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong unit: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireTenant(&Session{Role: RoleTenant, UnitID: "u1"}, "u1"); err != nil {
		t.Errorf("matching tenant: err = %v, want nil", err)
	}
}

func TestRequireUVIAccess(t *testing.T) {
	if err := RequireUVIAccess(nil, "u1"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("nil session: err = %v, want ErrUnauthorized", err)
	}
	if err := RequireUVIAccess(&Session{Role: RoleOperator}, "u1"); err != nil {
		t.Errorf("operator, any unit: err = %v, want nil", err)
	}
	if err := RequireUVIAccess(&Session{Role: RoleOperator}, "u2"); err != nil {
		t.Errorf("operator, another unit: err = %v, want nil", err)
	}
	if err := RequireUVIAccess(&Session{Role: RoleTenant, UnitID: "u1"}, "u1"); err != nil {
		t.Errorf("matching tenant: err = %v, want nil", err)
	}
	if err := RequireUVIAccess(&Session{Role: RoleTenant, UnitID: "u2"}, "u1"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("tenant, wrong unit: err = %v, want ErrUnauthorized", err)
	}
}
