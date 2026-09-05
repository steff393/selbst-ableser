package backup

import (
	"os"
	"path/filepath"
	"testing"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/masterdata"
)

// TestRestoreOverLive_ReplacesExistingFiles covers what Restore itself
// deliberately refuses to do: overwriting files that are already there.
// It seeds a "live" installation, backs it up, changes the live files to
// something else entirely, then restores over them and checks the
// backup's content wins — including for archive.db, where a stale -wal
// left over from the pre-restore file must not resurrect old content.
func TestRestoreOverLive_ReplacesExistingFiles(t *testing.T) {
	liveDir := t.TempDir()
	archivePath := filepath.Join(liveDir, "archive.db")
	auditPath := filepath.Join(liveDir, "audit.db")
	masterDataPath := filepath.Join(liveDir, "masterdata.enc")
	configPath := filepath.Join(liveDir, "config.json")
	secretsPath := filepath.Join(liveDir, "secrets.json")

	// The "old" content that must be gone after restoring.
	store, err := archive.OpenStore(archivePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.InsertHistorical(archive.Entry{MeterID: "90000001", Day: "2025-01-01", RawHex: "oldold"}); err != nil {
		t.Fatalf("InsertHistorical: %v", err)
	}
	store.Close() // closed before RestoreOverLive touches the file, like a process that has exited would have it

	auditLog, err := access.OpenAuditLog(auditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog: %v", err)
	}
	auditLog.Close()

	if err := masterdata.Save(masterDataPath, masterdata.MasterData{}, testPassword); err != nil {
		t.Fatalf("Save (old master data): %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatalf("writing old config: %v", err)
	}
	if err := os.WriteFile(secretsPath, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatalf("writing old secrets: %v", err)
	}
	// A stale -wal sidecar, as if the old process had died mid-write —
	// RestoreOverLive must remove this, not leave it to confuse the next
	// open of the freshly restored archive.db.
	if err := os.WriteFile(archivePath+"-wal", []byte("stale wal content"), 0o600); err != nil {
		t.Fatalf("writing stale -wal: %v", err)
	}

	// The "new" content, backed up from a separate, freshly built
	// installation — standing in for a backup taken earlier and now
	// being restored.
	backupSrcDir := t.TempDir()
	newArchivePath := filepath.Join(backupSrcDir, "live-archive.db")
	newAuditPath := filepath.Join(backupSrcDir, "live-audit.db")
	newMasterDataPath := filepath.Join(backupSrcDir, "live-masterdata.enc")

	newStore, err := archive.OpenStore(newArchivePath)
	if err != nil {
		t.Fatalf("OpenStore (new): %v", err)
	}
	if _, err := newStore.InsertHistorical(archive.Entry{MeterID: "90000002", Day: "2025-06-30", RawHex: "newnew"}); err != nil {
		t.Fatalf("InsertHistorical (new): %v", err)
	}
	newAuditLog, err := access.OpenAuditLog(newAuditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog (new): %v", err)
	}
	if err := newAuditLog.Record(access.Event{Type: access.EventLoginSuccess, Detail: "new"}); err != nil {
		t.Fatalf("Record (new): %v", err)
	}
	if err := masterdata.Save(newMasterDataPath, masterdata.MasterData{
		Units: []masterdata.Unit{{ID: "u1", Name: "Neu", AreaM2: 42}},
	}, testPassword); err != nil {
		t.Fatalf("Save (new master data): %v", err)
	}

	backupDir := t.TempDir()
	if err := Run(newStore, newAuditLog, newMasterDataPath, "", "", backupDir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	newStore.Close()
	newAuditLog.Close()

	if err := RestoreOverLive(backupDir, archivePath, auditPath, masterDataPath, configPath, secretsPath); err != nil {
		t.Fatalf("RestoreOverLive: %v", err)
	}

	if _, err := os.Stat(archivePath + "-wal"); !os.IsNotExist(err) {
		t.Errorf("stale -wal sidecar should have been removed, stat err = %v", err)
	}

	restoredStore, err := archive.OpenStore(archivePath)
	if err != nil {
		t.Fatalf("OpenStore (restored): %v", err)
	}
	defer restoredStore.Close()
	if _, found, err := restoredStore.Get("90000001", "2025-01-01"); err != nil || found {
		t.Errorf("old entry (90000001) survived the restore: found=%v err=%v", found, err)
	}
	if entry, found, err := restoredStore.Get("90000002", "2025-06-30"); err != nil || !found || entry.RawHex != "newnew" {
		t.Errorf("Get(90000002) = %+v, found=%v err=%v, want the backed-up entry", entry, found, err)
	}

	restoredAudit, err := access.OpenAuditLog(auditPath)
	if err != nil {
		t.Fatalf("OpenAuditLog (restored): %v", err)
	}
	defer restoredAudit.Close()
	events, total, err := restoredAudit.Events(access.Query{Limit: 10})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if total != 1 || len(events) != 1 || events[0].Type != access.EventLoginSuccess {
		t.Errorf("restored audit log = %+v (total %d), want exactly the one backed-up login_success event", events, total)
	}

	restoredMD, err := masterdata.Load(masterDataPath, testPassword)
	if err != nil {
		t.Fatalf("Load (restored master data): %v", err)
	}
	if len(restoredMD.Units) != 1 || restoredMD.Units[0].ID != "u1" {
		t.Errorf("restored master data units = %+v, want the backed-up unit", restoredMD.Units)
	}

	// The backup itself was taken with configPath/secretsPath empty (Run
	// above), so it never carried a config.json — RestoreOverLive must
	// find nothing to restore there and leave the live file exactly as
	// it was, even though a real target path was given for it here.
	gotConfig, err := os.ReadFile(configPath)
	if err != nil || string(gotConfig) != `{"old":true}` {
		t.Errorf("config.json = %q, err=%v, want the untouched old content", gotConfig, err)
	}
}

// TestRestoreOverLive_RequiresMandatoryFiles refuses a backup source
// missing archive.db/audit.db/masterdata.enc outright, the same
// guarantee Restore gives — a partial or unrelated directory must never
// be accepted as if it were a real backup.
func TestRestoreOverLive_RequiresMandatoryFiles(t *testing.T) {
	emptyDir := t.TempDir()
	liveDir := t.TempDir()
	err := RestoreOverLive(emptyDir,
		filepath.Join(liveDir, "archive.db"),
		filepath.Join(liveDir, "audit.db"),
		filepath.Join(liveDir, "masterdata.enc"),
		"", "")
	if err == nil {
		t.Fatal("RestoreOverLive with no source files succeeded, want an error")
	}
}
