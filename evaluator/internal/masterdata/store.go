package masterdata

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen  = 16
	nonceLen = 12
	keyLen   = 32 // AES-256

	argon2Time      = 3
	argon2MemoryKiB = 64 * 1024
	argon2Threads   = 4

	// MinPasswordLength is enforced by Save (STAMM-03). This one secret
	// decrypts every meter's AES key in the installation and is the only
	// thing standing between a copied file and all of them, so the floor
	// is set well above what a password policy would otherwise call
	// acceptable.
	MinPasswordLength = 12
)

// ErrWrongPasswordOrCorrupt is returned by Load when the file cannot be
// decrypted. A wrong password and a corrupted or tampered-with file are
// indistinguishable at the cryptographic level — both fail the same
// authenticated-encryption check — and are deliberately reported the same
// way: AES-GCM's authentication tag is what satisfies STAMM-03's
// requirement to detect tampering, not a separate integrity check.
var ErrWrongPasswordOrCorrupt = errors.New("masterdata: wrong password, or the file is corrupted or was tampered with")

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryKiB, argon2Threads, keyLen)
}

// Save encrypts md and writes it to path, first moving any existing file
// aside as a dated backup (STAMM-08) so that a bad write, or a save of
// data that turns out to be wrong, never destroys the only copy of the
// installation's AES keys.
//
// File layout: salt(16) || nonce(12) || ciphertext, following the
// established convention for this kind of file — see docs/architektur.md.
func Save(path string, md MasterData, password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("masterdata: password must be at least %d characters", MinPasswordLength)
	}
	if d := Validate(md); !d.OK() {
		return fmt.Errorf("masterdata: refusing to save invalid data: %v", d.Errors)
	}

	plaintext, err := json.Marshal(md)
	if err != nil {
		return fmt.Errorf("masterdata: encoding: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("masterdata: generating salt: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("masterdata: generating nonce: %w", err)
	}

	gcm, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("masterdata: creating %s: %w", dir, err)
		}
	}

	if err := backup(path); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("masterdata: writing: %w", err)
	}
	return os.Rename(tmp, path)
}

// maxDatedBackups bounds how many of backup's timestamped copies are kept
// per master-data path — STAMM-08's safety net (a bad write, or a save of
// data that turns out to be wrong, never destroys the only copy) needs a
// handful of recent snapshots to be useful, not an ever-growing archive
// that a busy installation (a Save per master-data edit) would otherwise
// accumulate without limit. Pruning happens by count, not age: an
// installation edited rarely should still keep a real history, not just
// whatever fits in the last N days.
const maxDatedBackups = 10

// backup copies an existing file aside with a timestamp suffix before it
// is overwritten, then prunes older dated backups beyond maxDatedBackups.
// A missing file (first save) is not an error.
//
// A backup taken before a password change is still only readable with
// that old password — Vault.ChangePassword re-keys the current file, not
// its history. If the old password may itself have been compromised
// (often the reason for changing it in the first place), pruning by
// count alone does not remove that exposure; see DeleteDatedBackups for
// the operator-facing way to clear it.
func backup(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("masterdata: reading existing file for backup: %w", err)
	}
	backupPath := fmt.Sprintf("%s.%s.bak", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return err
	}
	// Best-effort: the backup that actually matters for this Save (the
	// one just written above) already succeeded. A stray old .bak file
	// this couldn't remove — a permissions hiccup, an open handle on some
	// platform — is a disk-hygiene issue to retry on the next Save, not a
	// reason to fail an otherwise-good save of real master data; this
	// package has no logger to report it to either way.
	_ = pruneBackups(path)
	return nil
}

// pruneBackups removes the oldest dated backups for path beyond
// maxDatedBackups. The timestamp suffix (backup's "20060102-150405"
// format) sorts lexically in chronological order, so no parsing is needed
// to tell oldest from newest.
func pruneBackups(path string) error {
	matches, err := filepath.Glob(path + ".*.bak")
	if err != nil {
		return fmt.Errorf("masterdata: listing backups: %w", err)
	}
	if len(matches) <= maxDatedBackups {
		return nil
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-maxDatedBackups] {
		if err := os.Remove(old); err != nil {
			return fmt.Errorf("masterdata: removing old backup %s: %w", old, err)
		}
	}
	return nil
}

// DeleteDatedBackups removes every dated backup for path (see backup and
// pruneBackups above) and reports how many were removed. Unlike
// pruneBackups, which only trims down to maxDatedBackups, this removes all
// of them — the operator-triggered answer to backup's own documented gap:
// ChangePassword re-keys only the current file, so a .bak written before a
// password change stays readable with the old password unless someone
// removes it explicitly.
func DeleteDatedBackups(path string) (int, error) {
	matches, err := filepath.Glob(path + ".*.bak")
	if err != nil {
		return 0, fmt.Errorf("masterdata: listing backups: %w", err)
	}
	removed := 0
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			return removed, fmt.Errorf("masterdata: removing backup %s: %w", m, err)
		}
		removed++
	}
	return removed, nil
}

// Load decrypts the master data at path.
func Load(path string, password string) (MasterData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MasterData{}, err
	}
	if len(raw) < saltLen+nonceLen {
		return MasterData{}, ErrWrongPasswordOrCorrupt
	}
	salt := raw[:saltLen]
	nonce := raw[saltLen : saltLen+nonceLen]
	ciphertext := raw[saltLen+nonceLen:]

	gcm, err := newGCM(deriveKey(password, salt))
	if err != nil {
		return MasterData{}, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return MasterData{}, ErrWrongPasswordOrCorrupt
	}

	var md MasterData
	if err := json.Unmarshal(plaintext, &md); err != nil {
		return MasterData{}, fmt.Errorf("masterdata: decoding: %w", err)
	}
	return md, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("masterdata: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("masterdata: %w", err)
	}
	return gcm, nil
}
