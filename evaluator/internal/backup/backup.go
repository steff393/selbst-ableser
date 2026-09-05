// Package backup implements BETRIEB-07's full-system backup and restore —
// the archive, the master data, and the audit log, as a single operation —
// and DATEN-06's automatic detection of a connected removable medium
// (Linux and Windows; see AutoDetectDest and the platform-specific
// removable_*.go files). Run and Restore themselves work with any
// filesystem path, whether it came from AutoDetectDest or was given
// explicitly (a mounted USB drive, a network share, ...).
package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/telegram"
)

const (
	archiveFileName = "archive.db"
	auditFileName   = "audit.db"
	masterDataName  = "masterdata.enc"
	configFileName  = "config.json"
	secretsFileName = "secrets.json"

	// markerFileName records that a backup happened at all (DATEN-08/
	// UI-08's "Vorhandensein einer Sicherung"), next to the master data
	// file rather than in destDir — destDir is often a USB stick that
	// isn't reliably readable back (unmounted, swapped), while the
	// evaluator always has its own master data path at hand.
	markerFileName = "last-backup.json"
)

// AutoDetectDest picks exactly one connected removable medium's mount
// point to back up to (DATEN-06). It refuses to guess: no medium found,
// or more than one, is reported clearly rather than either doing nothing
// silently or picking one at random — "Fehlschlagen sichtbar machen statt
// still zu ignorieren" applies to detection itself, not just to the
// backup operation.
func AutoDetectDest() (string, error) {
	mounts, err := ListRemovableMountPoints()
	if err != nil {
		return "", err
	}
	switch len(mounts) {
	case 0:
		return "", fmt.Errorf("backup: no removable medium found; connect one, or name a destination directory explicitly")
	case 1:
		return mounts[0], nil
	default:
		return "", fmt.Errorf("backup: multiple removable media found (%s); name the destination directory explicitly to pick one", strings.Join(mounts, ", "))
	}
}

// Run writes a complete, consistent backup of the archive, the audit log,
// the master data file, and — where they exist — config.json and
// secrets.json into destDir (created if needed). The archive and audit log
// use their own online-backup mechanism (safe with a concurrently running
// collector or evaluator); the other files are plain copies, safe because
// Save/SaveSecrets only ever replace them via a rename of a fully-written
// temporary file (see internal/masterdata and internal/config).
//
// configPath and secretsPath are optional (pass "" to skip either): a
// bare-minimum installation may not have customized config.json yet, and
// secrets.json only exists at all once notify or push_secret settings have
// been saved once. Skipping an absent file is not an error — only a
// present-but-unreadable one is.
func Run(store *archive.Store, auditLog *access.AuditLog, masterDataPath, configPath, secretsPath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("backup: creating %s: %w", destDir, err)
	}

	if err := store.BackupTo(filepath.Join(destDir, archiveFileName)); err != nil {
		return fmt.Errorf("backup: archive: %w", err)
	}
	if err := auditLog.BackupTo(filepath.Join(destDir, auditFileName)); err != nil {
		return fmt.Errorf("backup: audit log: %w", err)
	}
	if err := copyFile(masterDataPath, filepath.Join(destDir, masterDataName)); err != nil {
		return fmt.Errorf("backup: master data: %w", err)
	}
	if configPath != "" {
		if err := copyFileIfExists(configPath, filepath.Join(destDir, configFileName)); err != nil {
			return fmt.Errorf("backup: config: %w", err)
		}
	}
	if secretsPath != "" {
		if err := copyFileIfExists(secretsPath, filepath.Join(destDir, secretsFileName)); err != nil {
			return fmt.Errorf("backup: secrets: %w", err)
		}
	}
	if err := writeMarker(masterDataPath, destDir); err != nil {
		return fmt.Errorf("backup: recording marker: %w", err)
	}
	return nil
}

// LastBackupInfo is what LastBackup reports.
type LastBackupInfo struct {
	At   time.Time
	Dest string
}

func markerPath(masterDataPath string) string {
	return filepath.Join(filepath.Dir(masterDataPath), markerFileName)
}

func writeMarker(masterDataPath, dest string) error {
	// In telegram.Local, not the process's own zone: the evaluator's host
	// (a VPS) commonly runs its OS clock in UTC, and LastBackupInfo.At is
	// displayed as a plain wall-clock string with no zone indicator (see
	// the operator pages that read LastBackup back) — it needs to already
	// be correct before formatting, the same reasoning as audit.go's At.
	data, err := json.Marshal(LastBackupInfo{At: time.Now().In(telegram.Local), Dest: dest})
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(masterDataPath), data, 0o600)
}

// LastBackup reports the most recent successful backup recorded next to
// masterDataPath, if any (DATEN-08/UI-08). found is false if no backup
// has ever been run against this installation.
func LastBackup(masterDataPath string) (info LastBackupInfo, found bool, err error) {
	data, err := os.ReadFile(markerPath(masterDataPath))
	if os.IsNotExist(err) {
		return LastBackupInfo{}, false, nil
	}
	if err != nil {
		return LastBackupInfo{}, false, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return LastBackupInfo{}, false, err
	}
	return info, true, nil
}

// restoreFile is one file Restore considers. optional files (config.json,
// secrets.json) are skipped without error if missing from srcDir — Run
// itself skips them the same way when they didn't exist at backup time.
type restoreFile struct {
	src, dest string
	optional  bool
}

// Restore copies a backup produced by Run into place at the given target
// paths. configPath/secretsPath may be "" to skip restoring those (e.g.
// restoring onto a machine that keeps its own local config).
//
// Every source and destination is checked *before* anything is copied, so
// a problem with any one file — a missing mandatory source, an already-
// occupied destination — is reported without having partially restored
// the others first. Restore refuses to overwrite an existing file at any
// target, so it can never silently discard a live installation's data —
// move or remove the target first if replacing it is really intended.
func Restore(srcDir, archivePath, auditPath, masterDataPath, configPath, secretsPath string) error {
	files := []restoreFile{
		{filepath.Join(srcDir, archiveFileName), archivePath, false},
		{filepath.Join(srcDir, auditFileName), auditPath, false},
		{filepath.Join(srcDir, masterDataName), masterDataPath, false},
	}
	if configPath != "" {
		files = append(files, restoreFile{filepath.Join(srcDir, configFileName), configPath, true})
	}
	if secretsPath != "" {
		files = append(files, restoreFile{filepath.Join(srcDir, secretsFileName), secretsPath, true})
	}

	var toCopy []restoreFile
	for _, f := range files {
		if _, err := os.Stat(f.dest); err == nil {
			return fmt.Errorf("backup: restore target %s already exists, refusing to overwrite", f.dest)
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Stat(f.src); err != nil {
			if os.IsNotExist(err) && f.optional {
				continue // wasn't part of this backup — Run skips it the same way
			}
			return fmt.Errorf("backup: source %s: %w", f.src, err)
		}
		toCopy = append(toCopy, f)
	}
	for _, f := range toCopy {
		if err := copyFile(f.src, f.dest); err != nil {
			return fmt.Errorf("backup: restoring %s: %w", f.dest, err)
		}
	}
	return nil
}

func copyFileIfExists(src, dest string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return copyFile(src, dest)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return err
	}
	return out.Close()
}
