package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RestoreOverLive replaces an already-running installation's own files
// with the contents of a backup produced by Run — the web-triggered
// counterpart to Restore, for recovering a live installation in place
// rather than populating a fresh one. Restore refuses to overwrite
// anything, by design, for its own use case (never silently discard a
// live installation while pointed at the wrong target); RestoreOverLive
// is the opposite by design, since overwriting the current files with
// the backup's is the entire point here.
//
// Every source is checked before anything is written, so a backup
// missing a mandatory file is rejected without having partially
// replaced the others first. Each present target is then replaced
// atomically (written next to it, then renamed over it) — the same
// rename-based replacement Save/SaveSecrets already use elsewhere in
// this codebase, so a failure partway through never leaves a target
// half-written. archive.db/audit.db's stale -wal/-shm sidecar files, if
// any, are removed too: SQLite would otherwise notice the salt in a
// leftover -wal doesn't match the just-replaced main file and ignore it
// on its own, but removing it outright leaves nothing to reason about.
//
// The running process itself must still exit afterward for any of this
// to take effect — it already holds the old archive.db/audit.db open,
// and whatever master data it decrypted at Unlock time stays in memory
// regardless of what is now on disk. Triggering that restart is the
// caller's job (see webapp's handleRestoreUpload), not this function's.
func RestoreOverLive(srcDir, archivePath, auditPath, masterDataPath, configPath, secretsPath string) error {
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

	var toReplace []restoreFile
	for _, f := range files {
		if _, err := os.Stat(f.src); err != nil {
			if os.IsNotExist(err) && f.optional {
				continue // wasn't part of this backup — Run skips it the same way
			}
			return fmt.Errorf("backup: source %s: %w", f.src, err)
		}
		toReplace = append(toReplace, f)
	}

	for _, f := range toReplace {
		tmp := f.dest + ".restoring"
		os.Remove(tmp) // clear a stale leftover from an interrupted earlier attempt, if any
		if err := copyFileOverwrite(f.src, tmp); err != nil {
			return fmt.Errorf("backup: staging %s: %w", f.dest, err)
		}
		if err := os.Rename(tmp, f.dest); err != nil {
			return fmt.Errorf("backup: replacing %s: %w", f.dest, err)
		}
	}

	for _, f := range toReplace {
		if f.dest == archivePath || f.dest == auditPath {
			os.Remove(f.dest + "-wal")
			os.Remove(f.dest + "-shm")
		}
	}
	return nil
}

// copyFileOverwrite is copyFile without the O_EXCL guard: the caller
// always writes to a ".restoring" staging path it just cleared itself,
// so there is nothing here that should ever refuse to overwrite.
func copyFileOverwrite(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
