package access

import (
	"errors"
	"path/filepath"
	"testing"

	"selbst-ableser/internal/masterdata"
)

const testPassword = "correct horse battery staple"

func TestBootstrapCreatesEmptyInstallation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Bootstrap(path, testPassword); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	md, err := masterdata.Load(path, testPassword)
	if err != nil {
		t.Fatalf("Load after Bootstrap: %v", err)
	}
	if len(md.Units) != 0 || len(md.Meters) != 0 {
		t.Errorf("a freshly bootstrapped installation should be empty, got %+v", md)
	}
}

// TestBootstrapRejectsTokenShapedPassword: a password LooksLikeAccessToken
// would classify as a tenant token could never log in — the login form's
// credential-shape dispatch would route it to the token check, which would
// never succeed for it, and it would never reach the check that could.
func TestBootstrapRejectsTokenShapedPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	tokenShaped := "23456789ABCD" // 12 chars, all in tokenAlphabet
	if !LooksLikeAccessToken(tokenShaped) {
		t.Fatalf("test fixture %q does not actually look like a token — fix the fixture", tokenShaped)
	}

	if err := Bootstrap(path, tokenShaped); err == nil {
		t.Fatal("expected Bootstrap to reject a token-shaped password")
	}
	if _, err := masterdata.Load(path, tokenShaped); err == nil {
		t.Error("no file should have been created for the rejected password")
	}
}

func TestBootstrapRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Bootstrap(path, testPassword); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	err := Bootstrap(path, "a different password entirely")
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Fatalf("second Bootstrap: err = %v, want ErrAlreadyBootstrapped", err)
	}

	// And the original password must still work — the failed second
	// attempt must not have touched the file.
	if _, err := masterdata.Load(path, testPassword); err != nil {
		t.Fatalf("original password should still work: %v", err)
	}
}
