package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

const testPassword = "correct horse battery staple"

// TestRunAndRestore_RoundTrip is the "geprüft" half of BETRIEB-07's
// requirement: an untested backup is not a backup. It builds a small but
// non-trivial installation, backs it up, restores it under fresh paths,
// and checks that every piece — archive entry, audit entry, and master
// data (including the AES key, which is the one thing that can never be
// regenerated if lost) — comes back exactly.
func TestRunAndRestore_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	archivePath := filepath.Join(srcDir, "archive.db")
	auditPath := filepath.Join(srcDir, "audit.db")
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")

	store, err := archive.OpenStore(archivePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: "2025-06-30", RawHex: "aabbcc"}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}

	auditLog, err := access.OpenAuditLog(auditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer auditLog.Close()
	if err := auditLog.Record(access.Event{Type: access.EventDataIngested, Detail: "test"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	key := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	md := masterdata.MasterData{
		Units:       []masterdata.Unit{{ID: "u1", Name: "Unit 1", AreaM2: 60}},
		MeterPoints: []masterdata.MeterPoint{{ID: "mp1", UnitID: "u1", Kind: masterdata.KindHeating}},
		Meters:      []masterdata.Meter{{Number: "90000001", MeterPointID: "mp1", AESKey: key, InstalledAt: "2024-01-01"}},
	}
	if err := masterdata.Save(masterDataPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	if err := Run(store, auditLog, masterDataPath, "", "", destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	restoreDir := t.TempDir()
	restoredArchive := filepath.Join(restoreDir, "archive.db")
	restoredAudit := filepath.Join(restoreDir, "audit.db")
	restoredMasterData := filepath.Join(restoreDir, "masterdata.enc")
	if err := Restore(destDir, restoredArchive, restoredAudit, restoredMasterData, "", ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restoredStore, err := archive.OpenStore(restoredArchive)
	if err != nil {
		t.Fatalf("OpenStore(restored archive): %v", err)
	}
	defer restoredStore.Close()
	entry, found, err := restoredStore.Get("90000001", "2025-06-30")
	if err != nil || !found || entry.RawHex != "aabbcc" {
		t.Errorf("restored archive entry = %+v, found=%v, err=%v", entry, found, err)
	}

	restoredAuditLog, err := access.OpenAuditLog(restoredAudit)
	if err != nil {
		t.Fatalf("OpenAuditLog(restored): %v", err)
	}
	defer restoredAuditLog.Close()
	events, _, err := restoredAuditLog.Events(access.Query{})
	if err != nil || len(events) != 1 {
		t.Errorf("restored audit events = %+v, err=%v", events, err)
	}

	restoredMD, err := masterdata.Load(restoredMasterData, testPassword)
	if err != nil {
		t.Fatalf("Load(restored masterdata): %v", err)
	}
	if len(restoredMD.Meters) != 1 || restoredMD.Meters[0].AESKey != key {
		t.Errorf("restored master data lost or corrupted the AES key: %+v", restoredMD.Meters)
	}
}

func TestRunRecordsLastBackupMarker(t *testing.T) {
	srcDir := t.TempDir()
	archivePath := filepath.Join(srcDir, "archive.db")
	auditPath := filepath.Join(srcDir, "audit.db")
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")

	store, err := archive.OpenStore(archivePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	auditLog, err := access.OpenAuditLog(auditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer auditLog.Close()
	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, found, err := LastBackup(masterDataPath); err != nil || found {
		t.Fatalf("LastBackup before any run: found=%v, err=%v, want not found", found, err)
	}

	destDir := t.TempDir()
	if err := Run(store, auditLog, masterDataPath, "", "", destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, found, err := LastBackup(masterDataPath)
	if err != nil || !found {
		t.Fatalf("LastBackup after Run: found=%v, err=%v", found, err)
	}
	if info.Dest != destDir {
		t.Errorf("recorded Dest = %q, want %q", info.Dest, destDir)
	}
	if time.Since(info.At) > time.Minute {
		t.Errorf("recorded At = %v, want close to now", info.At)
	}
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	srcDir := t.TempDir()
	store, _ := archive.OpenStore(filepath.Join(srcDir, "archive.db"))
	defer store.Close()
	auditLog, _ := access.OpenAuditLog(filepath.Join(srcDir, "audit.db"))
	defer auditLog.Close()
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")
	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	if err := Run(store, auditLog, masterDataPath, "", "", destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	restoreDir := t.TempDir()
	target := filepath.Join(restoreDir, "archive.db")
	if err := masterdata.Save(target, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("seeding an existing file at the restore target: %v", err)
	}

	err := Restore(destDir, target, filepath.Join(restoreDir, "audit.db"), filepath.Join(restoreDir, "masterdata.enc"), "", "")
	if err == nil {
		t.Fatal("expected Restore to refuse overwriting an existing file")
	}
}

// TestRestoreChecksEverythingBeforeCopyingAnything covers Prüfpunkt 8.C: a
// backup directory missing one of the mandatory files must fail without
// restoring the others first, so a failed restore never leaves a mixed
// state (e.g. a restored archive with no master data next to it).
func TestRestoreChecksEverythingBeforeCopyingAnything(t *testing.T) {
	srcDir := t.TempDir()
	store, err := archive.OpenStore(filepath.Join(srcDir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	auditLog, err := access.OpenAuditLog(filepath.Join(srcDir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer auditLog.Close()
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")
	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	if err := Run(store, auditLog, masterDataPath, "", "", destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Simulate an incomplete backup directory (e.g. copied off a USB stick
	// that dropped a file): the master data half is missing.
	if err := os.Remove(filepath.Join(destDir, "masterdata.enc")); err != nil {
		t.Fatalf("removing masterdata.enc from the backup: %v", err)
	}

	restoreDir := t.TempDir()
	restoredArchive := filepath.Join(restoreDir, "archive.db")
	err = Restore(destDir, restoredArchive, filepath.Join(restoreDir, "audit.db"), filepath.Join(restoreDir, "masterdata.enc"), "", "")
	if err == nil {
		t.Fatal("expected Restore to fail: the backup is missing masterdata.enc")
	}
	if _, statErr := os.Stat(restoredArchive); !os.IsNotExist(statErr) {
		t.Error("archive.db should not have been restored either — a failed restore must not leave a mixed state")
	}
}

// TestRunAndRestoreIncludeConfigAndSecretsWhenPresent covers the backup
// scope decided in 08-betrieb.md: config.json and secrets.json travel with
// the rest of the installation when they exist, so a restore does not
// leave the collector's push_secret or the SMTP credentials to be
// re-entered by hand.
func TestRunAndRestoreIncludeConfigAndSecretsWhenPresent(t *testing.T) {
	srcDir := t.TempDir()
	archivePath := filepath.Join(srcDir, "archive.db")
	auditPath := filepath.Join(srcDir, "audit.db")
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")
	configPath := filepath.Join(srcDir, "config.json")
	secretsPath := filepath.Join(srcDir, "secrets.json")

	store, err := archive.OpenStore(archivePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	auditLog, err := access.OpenAuditLog(auditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer auditLog.Close()
	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"evaluator":{"addr":"127.0.0.1:8080"}}`), 0o600); err != nil {
		t.Fatalf("writing config.json: %v", err)
	}
	if err := os.WriteFile(secretsPath, []byte(`{"push_secret":"s3cr3t"}`), 0o600); err != nil {
		t.Fatalf("writing secrets.json: %v", err)
	}

	destDir := t.TempDir()
	if err := Run(store, auditLog, masterDataPath, configPath, secretsPath, destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "config.json")); err != nil {
		t.Errorf("config.json missing from backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "secrets.json")); err != nil {
		t.Errorf("secrets.json missing from backup: %v", err)
	}

	restoreDir := t.TempDir()
	restoredConfig := filepath.Join(restoreDir, "config.json")
	restoredSecrets := filepath.Join(restoreDir, "secrets.json")
	err = Restore(destDir,
		filepath.Join(restoreDir, "archive.db"), filepath.Join(restoreDir, "audit.db"), filepath.Join(restoreDir, "masterdata.enc"),
		restoredConfig, restoredSecrets)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := os.ReadFile(restoredConfig)
	if err != nil || string(got) != `{"evaluator":{"addr":"127.0.0.1:8080"}}` {
		t.Errorf("restored config.json = %q, err=%v", got, err)
	}
	got, err = os.ReadFile(restoredSecrets)
	if err != nil || string(got) != `{"push_secret":"s3cr3t"}` {
		t.Errorf("restored secrets.json = %q, err=%v", got, err)
	}
}

// TestRunAndRestoreSkipAbsentConfigAndSecrets covers the common case of an
// installation that has never customized config.json/secrets.json (or a
// backup taken before either was passed to Run): Restore must not fail
// just because the optional files never existed to restore.
func TestRunAndRestoreSkipAbsentConfigAndSecrets(t *testing.T) {
	srcDir := t.TempDir()
	store, err := archive.OpenStore(filepath.Join(srcDir, "archive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	auditLog, err := access.OpenAuditLog(filepath.Join(srcDir, "audit.db"))
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	defer auditLog.Close()
	masterDataPath := filepath.Join(srcDir, "masterdata.enc")
	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	// configPath/secretsPath point at files that don't exist — Run must
	// not error, and must simply not write them into destDir.
	if err := Run(store, auditLog, masterDataPath, filepath.Join(srcDir, "config.json"), filepath.Join(srcDir, "secrets.json"), destDir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("expected no config.json in the backup, got err=%v", err)
	}

	restoreDir := t.TempDir()
	err = Restore(destDir,
		filepath.Join(restoreDir, "archive.db"), filepath.Join(restoreDir, "audit.db"), filepath.Join(restoreDir, "masterdata.enc"),
		filepath.Join(restoreDir, "config.json"), filepath.Join(restoreDir, "secrets.json"))
	if err != nil {
		t.Fatalf("Restore should succeed even though config.json/secrets.json were never backed up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("expected no config.json restored, got err=%v", err)
	}
}
