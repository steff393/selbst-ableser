package webapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/backup"
	"selbst-ableser/internal/correction"
	"selbst-ableser/internal/telegram"
)

// maxArchiveImportUpload bounds an uploaded archive-import file: generous
// for what this schema actually needs (a full multi-year, multi-meter
// archive is still a small SQLite file), just enough to stop an obviously
// wrong upload from being accepted silently.
const maxArchiveImportUpload = 200 << 20 // 200 MiB

type archivePageData struct {
	Base
	Stats          archive.Stats
	Error          string
	StorageBytes   int64
	StorageKnown   bool
	LastBackupAt   string
	HasLastBackup  bool
	ImportReport   *archiveImportReportView
	CorrectionRSSI int // the sentinel correction.RSSI, for the correction form's own explanation
}

type archiveImportReportView struct {
	EntriesInserted  int
	EntriesUnchanged int
	Issues           []archiveImportIssueView
}

type archiveImportIssueView struct {
	Context  string
	Conflict bool
}

// handleArchiveOverview is UI-08 (DATEN-08): the archive's extent, its
// storage footprint, whether a backup has ever been recorded for this
// installation (see internal/backup's marker file — written next to the
// master data path, since the backup destination itself, often a USB
// stick, isn't reliably readable back), and a form to pick a range to
// download.
func (a *App) handleArchiveOverview(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data, err := a.archivePageData(sess, "")
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.render(w, "archive.html", data)
}

func (a *App) archivePageData(sess *access.Session, errMsg string) (archivePageData, error) {
	stats, err := a.Store.Stats(a.today())
	if err != nil {
		return archivePageData{}, err
	}

	data := archivePageData{Base: a.base("Archiv", sess), Stats: stats, Error: errMsg, CorrectionRSSI: correction.RSSI}
	if a.ArchivePath != "" {
		if fi, err := os.Stat(a.ArchivePath); err == nil {
			data.StorageBytes = fi.Size()
			data.StorageKnown = true
		}
	}
	if info, found, err := backup.LastBackup(a.MasterDataPath); err == nil && found {
		data.HasLastBackup = true
		data.LastBackupAt = info.At.Format("2006-01-02 15:04:05")
	}
	return data, nil
}

// handleArchiveImport merges a previously exported archive-schema SQLite
// file (a saCollector backup.db, or another installation's archive.db — the
// schema is identical either way) into this installation's
// live archive. Uploaded content is only ever read from a throwaway copy;
// the operator's own original file is never touched by this. Conflicting
// entries (DATEN-03: an existing entry for a closed day that differs from
// the one being imported) are reported, never silently overwritten — see
// archive.ImportFile.
func (a *App) handleArchiveImport(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveImportUpload)
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	upload, _, err := r.FormFile("file")
	if err != nil {
		a.renderArchivePageWithError(w, sess, "Keine Datei ausgewählt, oder die Datei ist zu groß.")
		return
	}
	defer upload.Close()

	tmpDir, err := os.MkdirTemp("", "sa-archive-import-*")
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "upload.db")
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	_, copyErr := io.Copy(tmpFile, upload)
	closeErr := tmpFile.Close()
	if copyErr != nil {
		a.renderTechnicalError(w, sess, copyErr)
		return
	}
	if closeErr != nil {
		a.renderTechnicalError(w, sess, closeErr)
		return
	}

	report, err := archive.ImportFile(a.Store, tmpPath)
	if err != nil {
		a.renderArchivePageWithError(w, sess, "Import fehlgeschlagen: die Datei ist keine gültige Archiv-Datenbank.")
		return
	}

	view := archiveImportReportView{EntriesInserted: report.EntriesInserted, EntriesUnchanged: report.EntriesUnchanged}
	for _, issue := range report.Issues {
		view.Issues = append(view.Issues, archiveImportIssueView{
			Context:  issue.File,
			Conflict: errors.Is(issue.Err, archive.ErrConflict),
		})
	}
	a.audit(access.EventDataIngested, sess.AuditActor(), fmt.Sprintf("imported archive file: %d inserted, %d unchanged, %d issues", report.EntriesInserted, report.EntriesUnchanged, len(report.Issues)))

	data, dataErr := a.archivePageData(sess, "")
	if dataErr != nil {
		a.renderTechnicalError(w, sess, dataErr)
		return
	}
	data.ImportReport = &view
	a.render(w, "archive.html", data)
}

// handleArchiveCompress is the operator-only, manual shrink path for a
// range of full calendar months (DATEN-06, like handleArchiveDelete): for
// each such month it keeps only the entry each meter needs for FACH-01's
// monthly reading — the one nearest month-end within the configured
// lookback window — and deletes the rest of that meter's entries for the
// month. A month only partially covered by the requested range is left
// alone, and a month with no qualifying entry for a meter is left alone
// too, so this can never remove data a monthly reading still depends on.
// Nothing is decrypted here; the operator is expected to have already
// confirmed the affected months' readings are plausible and gap-free.
func (a *App) handleArchiveCompress(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	from, err1 := telegram.ParseDay(r.PostFormValue("from"))
	to, err2 := telegram.ParseDay(r.PostFormValue("to"))
	if err1 != nil || err2 != nil {
		a.renderArchivePageWithError(w, sess, "Ungültiger Zeitraum.")
		return
	}

	deleted, err := a.Store.CompressRange(from, to, a.lookbackDays())
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.audit(access.EventArchiveCompressed, sess.AuditActor(), fmt.Sprintf("%d entries, %s to %s", deleted, from, to))

	data, dataErr := a.archivePageData(sess, "")
	if dataErr != nil {
		a.renderTechnicalError(w, sess, dataErr)
		return
	}
	data.Flash = fmt.Sprintf("%d Einträge komprimiert (nur vollständige Monate zwischen %s und %s berücksichtigt).", deleted, formatDay(from), formatDay(to))
	a.render(w, "archive.html", data)
}

// handleArchiveCorrect is DATEN-09's operator-facing correction path: the
// evaluator itself builds and re-encrypts a corrected telegram (see
// internal/correction) from the value the operator supplies — only the
// meter reading changes; header, manufacturer/version/medium, and
// encryption are all copied from a genuine telegram this exact meter
// previously sent. If day itself has no archived entry (a gap being
// backfilled, not a wrong value being fixed), the nearest other archived
// day for the same meter is used as that template instead. Requires the
// vault unlocked, since the meter's AES key is needed either way — unlike
// handleArchiveDelete/Compress, which only ever touch still-encrypted
// bytes.
func (a *App) handleArchiveCorrect(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	meterID := r.PostFormValue("meter")
	day, err := telegram.ParseDay(r.PostFormValue("day"))
	if err != nil {
		a.renderArchivePageWithError(w, sess, "Ungültiger Tag.")
		return
	}
	newValue, err := strconv.ParseInt(r.PostFormValue("value"), 10, 64)
	if err != nil || newValue < 0 {
		a.renderArchivePageWithError(w, sess, "Ungültiger Zählerstand.")
		return
	}

	meter, found := md.MeterByNumber(meterID, day)
	if !found {
		a.renderArchivePageWithError(w, sess, fmt.Sprintf("Kein Zähler %s an diesem Tag in den Stammdaten aktiv.", meterID))
		return
	}

	template, exact, err := a.Store.Get(meterID, day)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	templateDay := day
	if !exact {
		nearest, found, err := a.Store.NearestDay(meterID, day)
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
		if !found {
			a.renderArchivePageWithError(w, sess, fmt.Sprintf("Für Zähler %s ist kein einziges Telegramm archiviert — es gibt keine Vorlage für eine Korrektur.", meterID))
			return
		}
		templateDay = nearest
		template, _, err = a.Store.Get(meterID, nearest)
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
	}

	newHex, oldValue, err := correction.Build(template, meter.AESKey, newValue)
	if err != nil {
		a.renderArchivePageWithError(w, sess, "Korrektur fehlgeschlagen: "+err.Error())
		return
	}

	receivedAt := template.ReceivedAt
	if !exact {
		// No genuine telegram exists for day itself — nothing to preserve a
		// receive time from, so this uses the same fixed end-of-day
		// convention every other synthetic archive entry in this codebase
		// uses, purely cosmetic (nothing reads ReceivedAt's time-of-day).
		dayStart, err := time.ParseInLocation("2006-01-02", string(day), telegram.Local)
		if err != nil {
			a.renderTechnicalError(w, sess, err)
			return
		}
		receivedAt = dayStart.Add(23*time.Hour + 55*time.Minute)
	}

	entry := archive.Entry{MeterID: meterID, Day: day, ReceivedAt: receivedAt, RSSI: correction.RSSI, RawHex: newHex}
	if err := a.Store.Correct(entry); err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.audit(access.EventArchiveCorrected, sess.AuditActor(), fmt.Sprintf("meter %s, day %s: %d -> %d", meterID, day, oldValue, newValue))

	data, dataErr := a.archivePageData(sess, "")
	if dataErr != nil {
		a.renderTechnicalError(w, sess, dataErr)
		return
	}
	if exact {
		data.Flash = fmt.Sprintf("Zähler %s, Tag %s: %d → %d korrigiert.", meterID, formatDay(day), oldValue, newValue)
	} else {
		data.Flash = fmt.Sprintf("Zähler %s, Tag %s: neu angelegt mit Wert %d (Vorlage vom %s).", meterID, formatDay(day), newValue, formatDay(templateDay))
	}
	a.render(w, "archive.html", data)
}

func (a *App) renderArchivePageWithError(w http.ResponseWriter, sess *access.Session, errMsg string) {
	data, err := a.archivePageData(sess, errMsg)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.render(w, "archive.html", data)
}

// handleArchiveDownload streams a day-range excerpt of the archive as
// JSON — still fully encrypted (A.3), so this is safe to hand over for
// backup or for a third party's review without decrypting anything.
func (a *App) handleArchiveDownload(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	from, err1 := telegram.ParseDay(r.URL.Query().Get("from"))
	to, err2 := telegram.ParseDay(r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil {
		http.Error(w, "ungültiger Zeitraum", http.StatusBadRequest)
		return
	}

	entries, err := a.Store.Range(from, to)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=archive-%s-bis-%s.json", from, to))
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		a.logger().Error("streaming archive download", "err", err)
	}
}

// handleArchiveDelete is DATEN-06's required manual-cleanup path: the
// archive is otherwise kept indefinitely, nothing is ever removed
// automatically, so a deliberate operator action is the only way to
// shrink it — e.g. clearing out data loaded while testing, or old
// telegrams whose retention is no longer needed. Irreversible, and
// deliberately separate from DATEN-09's per-entry correction mechanism.
func (a *App) handleArchiveDelete(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	from, err1 := telegram.ParseDay(r.PostFormValue("from"))
	to, err2 := telegram.ParseDay(r.PostFormValue("to"))
	if err1 != nil || err2 != nil {
		a.renderArchivePageWithError(w, sess, "Ungültiger Zeitraum.")
		return
	}

	deleted, err := a.Store.DeleteRange(from, to)
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.audit(access.EventArchiveDeleted, sess.AuditActor(), fmt.Sprintf("%d entries, %s to %s", deleted, from, to))

	data, dataErr := a.archivePageData(sess, "")
	if dataErr != nil {
		a.renderTechnicalError(w, sess, dataErr)
		return
	}
	data.Flash = fmt.Sprintf("%d Einträge gelöscht (%s bis %s).", deleted, formatDay(from), formatDay(to))
	a.render(w, "archive.html", data)
}
