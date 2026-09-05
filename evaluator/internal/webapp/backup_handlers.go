package webapp

import (
	"archive/zip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/backup"
)

// maxRestoreUpload bounds an uploaded backup archive: generous enough
// for a real multi-year archive.db plus audit.db together, just enough
// to stop an obviously wrong upload from being accepted silently — the
// same reasoning as maxArchiveImportUpload, sized up since this bundles
// several files (including the two databases) into one.
const maxRestoreUpload = 1 << 30 // 1 GiB

// backupFileNames is the fixed set of files a backup ever contains — the
// same five backup.Run itself knows about. Used on the restore side as
// an allowlist: any zip entry whose base name is not exactly one of
// these is ignored outright, entry path (directories, "../", an
// absolute path) included. This is what makes extractBackupZip safe
// against a malicious or merely corrupted archive claiming an entry
// name that would otherwise escape the extraction directory — the
// actual write target is always <dir>/<matched name>, never anything
// derived from the untrusted name beyond that match.
var backupFileNames = map[string]bool{
	"archive.db":     true,
	"audit.db":       true,
	"masterdata.enc": true,
	"config.json":    true,
	"secrets.json":   true,
}

type backupPageData struct {
	Base
	HasLastBackup bool
	LastBackupAt  string
	Error         string
	Restored      bool
	Restarting    bool
}

// handleBackupPage is UI-08's web counterpart to `saEvaluator
// backup`/`restore`: the same complete, five-file backup, downloadable
// with one click instead of an SSH session, and — the harder half —
// restorable back into this same running installation. See
// handleRestoreUpload's doc comment for why this cannot help with every
// disaster it might sound like it can.
func (a *App) handleBackupPage(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.renderBackupPage(w, sess, "")
}

func (a *App) renderBackupPage(w http.ResponseWriter, sess *access.Session, errMsg string) {
	data := backupPageData{Base: a.base("Backup", sess), Error: errMsg}
	if info, found, err := backup.LastBackup(a.MasterDataPath); err == nil && found {
		data.HasLastBackup = true
		data.LastBackupAt = info.At.Format("2006-01-02 15:04:05")
	}
	a.render(w, "backup.html", data)
}

// handleRestartRequest is a plain, no-file-involved counterpart to
// handleRestoreUpload's restart step — for recovering from a stuck or
// misbehaving process, or for picking up a binary already swapped in
// via scp, without needing an SSH session just to run `systemctl
// restart`. It never touches what code is on disk; only a separate,
// SSH-authenticated channel (scp, or the CLI backup/restore) can change
// that — this button restarts whatever is already there.
func (a *App) handleRestartRequest(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a.audit(access.EventServiceRestart, sess.AuditActor(), "requested from the Backup page")
	data := backupPageData{Base: a.base("Backup", sess), Restarting: true}
	if info, found, err := backup.LastBackup(a.MasterDataPath); err == nil && found {
		data.HasLastBackup = true
		data.LastBackupAt = info.At.Format("2006-01-02 15:04:05")
	}
	a.render(w, "backup.html", data)
	a.restart()
}

// handleBackupDownload builds a fresh backup.Run snapshot into a
// scratch directory, zips it, and streams the zip straight back as the
// response — nothing written by this request outlives it except the
// same last-backup.json marker the CLI's own `backup` subcommand
// writes, next to master data.
func (a *App) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	tmpDir, err := os.MkdirTemp("", "sa-backup-*")
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	if err := backup.Run(a.Store, a.Audit, a.MasterDataPath, a.ConfigPath, a.SecretsPath, tmpDir); err != nil {
		a.renderBackupPage(w, sess, "Sicherung fehlgeschlagen: "+err.Error())
		return
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	filename := fmt.Sprintf("selbst-ableser-backup-%s.zip", a.now().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	zw := zip.NewWriter(w)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := addFileToZip(zw, filepath.Join(tmpDir, e.Name()), e.Name()); err != nil {
			// Headers and part of the body are already on the wire; there
			// is no clean error response left to send at this point, only
			// stopping here instead of shipping a truncated zip silently.
			zw.Close()
			return
		}
	}
	zw.Close()

	a.audit(access.EventDataIngested, sess.AuditActor(), "backup downloaded")
}

func addFileToZip(zw *zip.Writer, srcPath, nameInZip string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	fw, err := zw.Create(nameInZip)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}

// handleRestoreUpload accepts a backup produced by handleBackupDownload
// (or `saEvaluator backup`) and replaces this installation's own files
// with it, then restarts the process so the replacement actually takes
// effect (see backup.RestoreOverLive and App.restart).
//
// This cannot recover an installation whose master data is itself the
// thing that is lost or corrupted: reaching this page at all requires
// an operator session, and the operator login path requires decrypting
// the current master data to establish one in the first place (see
// handleLogin). For that disaster, the CLI `restore` subcommand against
// a stopped process remains the only way in. What this does genuinely
// help with: recovering archive.db or audit.db specifically, or rolling
// the whole installation back to an earlier state, whenever the current
// operator password still unlocks it.
func (a *App) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUpload)
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	upload, _, err := r.FormFile("file")
	if err != nil {
		a.renderBackupPage(w, sess, "Keine Datei ausgewählt, oder die Datei ist zu groß.")
		return
	}
	defer upload.Close()

	tmpDir, err := os.MkdirTemp("", "sa-restore-*")
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	if err := extractBackupZip(upload, tmpDir); err != nil {
		a.renderBackupPage(w, sess, "Wiederherstellung fehlgeschlagen: die Datei ist keine gültige Sicherung ("+err.Error()+").")
		return
	}
	if err := backup.RestoreOverLive(tmpDir, a.ArchivePath, a.AuditPath, a.MasterDataPath, a.ConfigPath, a.SecretsPath); err != nil {
		a.renderBackupPage(w, sess, "Wiederherstellung fehlgeschlagen: "+err.Error())
		return
	}

	a.audit(access.EventDataIngested, sess.AuditActor(), "restore uploaded, restarting")
	data := backupPageData{Base: a.base("Backup", sess), Restored: true}
	if info, found, err := backup.LastBackup(a.MasterDataPath); err == nil && found {
		data.HasLastBackup = true
		data.LastBackupAt = info.At.Format("2006-01-02 15:04:05")
	}
	a.render(w, "backup.html", data)
	a.restart()
}

// extractBackupZip reads upload (an entire zip, buffered to a scratch
// file first since a multipart upload is not guaranteed to already be
// one — the same approach handleArchiveImport already uses for its own
// upload) and writes out only the entries matching backupFileNames,
// under their own fixed names in destDir. Returns an error if the zip
// cannot be read at all, or contains none of the expected files —
// anything else inside it is ignored rather than rejected, the same
// leniency Restore/RestoreOverLive already have toward a backup that
// simply never had a config.json or secrets.json.
func extractBackupZip(upload multipart.File, destDir string) error {
	tmpZip, err := os.CreateTemp(destDir, "upload-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpZip.Name())
	defer tmpZip.Close()

	size, err := io.Copy(tmpZip, upload)
	if err != nil {
		return err
	}
	if _, err := tmpZip.Seek(0, io.SeekStart); err != nil {
		return err
	}

	zr, err := zip.NewReader(tmpZip, size)
	if err != nil {
		return fmt.Errorf("keine gültige ZIP-Datei: %w", err)
	}

	found := false
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if !backupFileNames[name] {
			continue
		}
		if err := extractZipEntry(f, filepath.Join(destDir, name)); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("enthält keine der erwarteten Dateien (archive.db, masterdata.enc, audit.db, config.json, secrets.json)")
	}
	return nil
}

func extractZipEntry(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
