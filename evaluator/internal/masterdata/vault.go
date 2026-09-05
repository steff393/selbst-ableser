package masterdata

import (
	"errors"
	"sync"
)

// ErrLocked is returned by Vault.Save when called on a locked vault.
var ErrLocked = errors.New("masterdata: vault is locked")

// Vault holds decrypted master data — and, for as long as it stays
// unlocked, the password that decrypted it — in memory only (STAMM-04).
// "Ausschließlich im Arbeitsspeicher" (exclusively in memory) names the
// one place the password may live; it does not require discarding it the
// instant Unlock returns; it is used again for its own Save so that
// changes made while unlocked (a new access grant, an edited meter) don't
// each demand the password again — the operator entered it once for this
// session. Lock discards both the data and the password
// together, and is the only way to make them unavailable again.
type Vault struct {
	mu       sync.RWMutex
	data     *MasterData
	password string
}

// Unlock decrypts the master data file at path with password and holds
// both it and the password in memory until Lock.
func (v *Vault) Unlock(path, password string) error {
	md, err := Load(path, password)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data = &md
	v.password = password
	return nil
}

// Lock discards the in-memory master data and password together. After
// Lock, Get and Save report locked until Unlock succeeds again.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data = nil
	v.password = ""
}

// Locked reports whether the vault currently holds no data.
func (v *Vault) Locked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.data == nil
}

// Get returns the current master data, and false if the vault is locked.
// Callers evaluating a request while locked must return a clear "locked"
// message, never a partial result or an error that hints at plaintext
// (STAMM-04).
func (v *Vault) Get() (MasterData, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.data == nil {
		return MasterData{}, false
	}
	return *v.data, true
}

// ChangePassword re-encrypts the store under newPassword and adopts it
// for this vault.
//
// Deliberately shaped as "tell the vault to re-key itself" rather than
// "read the current password and re-key from outside": the password this
// vault holds has no reason to ever leave it, so there is no accessor for
// it at all. The caller therefore needs no knowledge of the old password
// — which is also why the operator is only ever asked for the new one.
//
// The in-memory copy is what gets re-encrypted, not a fresh read of the
// file: Unlock and Save keep the two identical, so re-reading would only
// add a way for them to disagree.
//
// This re-keys the current file only — it does not touch path's existing
// .bak history (see Save's backup), which stays readable under the old
// password. If that password may itself have been compromised, changing
// it does not retroactively protect what was already backed up under it;
// delete those .bak files by hand afterward if that matters here.
func (v *Vault) ChangePassword(path, newPassword string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.data == nil {
		return ErrLocked
	}
	if err := Save(path, *v.data, newPassword); err != nil {
		return err
	}
	v.password = newPassword
	return nil
}

// Save re-encrypts and persists md to path using this vault's own
// password, and updates the in-memory copy to match. It fails with
// ErrLocked if the vault is not currently unlocked — an operator cannot
// write master data they have no key to.
func (v *Vault) Save(path string, md MasterData) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.data == nil {
		return ErrLocked
	}
	if err := Save(path, md, v.password); err != nil {
		return err
	}
	v.data = &md
	return nil
}
