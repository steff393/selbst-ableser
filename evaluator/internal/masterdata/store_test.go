package masterdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testPassword = "correct horse battery staple"

func TestSaveLoadRoundTrip(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].AESKey = [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	path := filepath.Join(t.TempDir(), "masterdata.enc")

	if err := Save(path, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path, testPassword)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Meters[0].AESKey != md.Meters[0].AESKey {
		t.Errorf("AESKey round-trip mismatch: got %x, want %x", got.Meters[0].AESKey, md.Meters[0].AESKey)
	}
	if len(got.Units) != len(md.Units) || got.Units[0].AreaM2 != md.Units[0].AreaM2 {
		t.Errorf("Units round-trip mismatch: %+v", got.Units)
	}
}

func TestLoadWrongPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := Load(path, "some other password entirely")
	if !errors.Is(err, ErrWrongPasswordOrCorrupt) {
		t.Fatalf("Load with wrong password: err = %v, want ErrWrongPasswordOrCorrupt", err)
	}
}

func TestLoadTamperedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF // flip a bit in the ciphertext
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Load(path, testPassword)
	if !errors.Is(err, ErrWrongPasswordOrCorrupt) {
		t.Fatalf("Load of a tampered file: err = %v, want ErrWrongPasswordOrCorrupt", err)
	}
}

func TestSaveRejectsShortPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, wellFormed(t), "short"); err == nil {
		t.Fatal("expected Save to reject a password shorter than MinPasswordLength")
	}
}

func TestSaveRejectsInvalidData(t *testing.T) {
	md := wellFormed(t)
	md.Meters[0].Number = "not-a-number"
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, md, testPassword); err == nil {
		t.Fatal("expected Save to reject data that fails validation")
	}
}

func TestSaveBacksUpPreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "masterdata.enc")

	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			backups++
		}
	}
	if backups == 0 {
		t.Error("expected a .bak file after overwriting an existing master data file")
	}
}

func TestVaultLockUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var v Vault
	if !v.Locked() {
		t.Fatal("a fresh Vault should start locked")
	}
	if _, ok := v.Get(); ok {
		t.Fatal("Get should report ok=false while locked")
	}

	if err := v.Unlock(path, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if v.Locked() {
		t.Fatal("Vault should not be locked after a successful Unlock")
	}
	md, ok := v.Get()
	if !ok || len(md.Units) == 0 {
		t.Fatalf("Get after Unlock: ok=%v, md=%+v", ok, md)
	}

	v.Lock()
	if !v.Locked() {
		t.Fatal("Vault should be locked after Lock")
	}
	if _, ok := v.Get(); ok {
		t.Fatal("Get should report ok=false after Lock")
	}
}

func TestVaultUnlockWrongPasswordStaysLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "masterdata.enc")
	if err := Save(path, wellFormed(t), testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var v Vault
	if err := v.Unlock(path, "wrong password"); err == nil {
		t.Fatal("expected an error for the wrong password")
	}
	if !v.Locked() {
		t.Fatal("a failed Unlock must not leave the vault unlocked")
	}
}

// TestPruneBackupsKeepsOnlyTheNewest covers M5: repeated saves must not
// accumulate .bak files without bound — a busy installation saves on
// every edit. Exercised directly against pruneBackups (rather than
// through repeated real Saves, which would need real time to pass
// between them to get distinct timestamped filenames at all) so the test
// runs instantly regardless of maxDatedBackups' size.
func TestPruneBackupsKeepsOnlyTheNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "masterdata.enc")

	var names []string
	for i := 0; i < maxDatedBackups+5; i++ {
		name := fmt.Sprintf("%s.202601%02d-000000.bak", path, i+1) // distinct, chronologically ordered
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}

	if err := pruneBackups(path); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}

	matches, err := filepath.Glob(path + ".*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != maxDatedBackups {
		t.Errorf("got %d backups, want exactly %d (the cap)", len(matches), maxDatedBackups)
	}
	// The ones pruned must be the oldest, never the newest.
	newest := names[len(names)-1]
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the newest backup was pruned; it must always be the oldest ones that go: %v", err)
	}
	oldest := names[0]
	if _, err := os.Stat(oldest); err == nil {
		t.Error("the oldest backup should have been pruned")
	}
}
