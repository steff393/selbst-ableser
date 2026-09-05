package masterdata

import (
	"path/filepath"
	"testing"
)

// TestVaultChangePasswordReKeysInPlace covers the whole point of putting
// the re-key inside the vault: the caller never needs, and never gets,
// the current password. The vault must end up usable under the new one,
// with the old one dead.
func TestVaultChangePasswordReKeysInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	const oldPassword = "the original long password"
	const newPassword = "an equally long replacement"

	md := MasterData{Units: []Unit{{ID: "u1", Name: "Wohnung 1", AreaM2: 50}}}
	if err := Save(path, md, oldPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var v Vault
	if err := v.Unlock(path, oldPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := v.ChangePassword(path, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if v.Locked() {
		t.Error("re-keying must not lock the vault")
	}
	// Still able to write, which it can only do with the password it now
	// holds — the proof that it adopted the new one.
	if err := v.Save(path, md); err != nil {
		t.Fatalf("Save after re-key: %v", err)
	}
	if _, err := Load(path, newPassword); err != nil {
		t.Errorf("the file should open with the new password: %v", err)
	}
	if _, err := Load(path, oldPassword); err == nil {
		t.Error("the old password must stop working")
	}
}

func TestVaultChangePasswordRequiresUnlocked(t *testing.T) {
	var v Vault
	if err := v.ChangePassword(filepath.Join(t.TempDir(), "x.enc"), "a long enough password"); err == nil {
		t.Error("a locked vault has nothing to re-key and must refuse")
	}
}
