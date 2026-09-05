package webapp

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/billing"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/livepush"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/notify"
	"selbst-ableser/internal/telegram"
)

// App holds everything the HTTP handlers need. It contains no business
// logic of its own (UI-13): every handler calls into internal/billing,
// internal/masterdata, or internal/access and renders the result.
type App struct {
	Store          *archive.Store
	ArchivePath    string // for UI-08's storage-usage display; Store itself has no path
	Vault          *masterdata.Vault
	MasterDataPath string

	Sessions *access.SessionStore
	Audit    *access.AuditLog
	// AuditPath, like ArchivePath, exists only so the overview's
	// storage-usage figure can stat the file; AuditLog has no path.
	AuditPath     string
	LoginLimiter  *access.Limiter
	UnlockLimiter *access.Limiter

	LookbackDays int // FACH-01; 0 means billing.DefaultLookbackDays

	// Version identifies the running build for the operator overview
	// (BETRIEB-05: updating means swapping the binary, so the operator
	// needs a way to see which one is actually running). Deliberately not
	// exposed on the unauthenticated status endpoint — see
	// statusResponse's own note on staying uninteresting to attack.
	Version string

	Templates map[string]*template.Template
	StaticFS  fs.FS

	// TrustProxy must only be set when a reverse proxy actually sits in
	// front of this process and strips any client-supplied
	// X-Forwarded-For before setting its own (BETRIEB-06). See
	// clientKey in session.go for what goes wrong if this is set without
	// that being true.
	TrustProxy bool

	// AllowedHosts restricts which Host header values are answered at all
	// (BETRIEB-06's "die zulässigen Hostnamen einschränken") — a request
	// for a Host outside this list gets a generic rejection before
	// reaching any handler. Empty means unrestricted, appropriate for
	// local-only use; set it once the evaluator is reachable from the
	// internet, alongside TrustProxy.
	AllowedHosts []string

	// Logger receives operational and error log entries (BETRIEB-04); its
	// level is where "sparse by default" is controlled. Left nil, it
	// falls back to slog.Default().
	Logger *slog.Logger

	// LiveBuffer holds telegrams reported by a collector (UI-06's live
	// view); always non-nil, so the default single-machine deployment
	// (no secret configured, collector trusted over loopback — see
	// validCollectorAuth) has somewhere to land without extra setup.
	LiveBuffer *livepush.Buffer
	// PushSecret authenticates every collector-to-evaluator call
	// (ARCH-04): GET /collector/config and POST /collector/report alike.
	// Empty is a valid, deliberate state (see validCollectorAuth), not
	// "disabled".
	PushSecret string

	// NotifyConfig and SMTP are the two halves of BENACHR-04's mail
	// setup, edited together on one page but stored apart: what to send
	// in the config file, how to reach the server in the secrets file
	// (BETRIEB-02). Zero values mean nothing is sent.
	NotifyConfig config.Notify
	SMTP         config.SMTPCredentials

	// LegalConfig is UI-12's two public notices (Impressum,
	// Datenschutzerklärung) — see config.Legal's doc comment for why
	// these, unlike the rest of what an operator enters, are not vault
	// data. Persisted the same read-modify-write way as NotifyConfig/
	// CollectorConfig, via ConfigPath below.
	LegalConfig config.Legal

	// CollectorConfig is served back to any saCollector that asks (GET
	// /collector/config): report interval and telegram filter rules,
	// changed through the operator area (see collector_settings_handlers.go),
	// not something saCollector reads from its own file. Zero value is
	// fine; the config handler and saCollector's own settings package
	// both apply sensible defaults.
	CollectorConfig config.Collector

	// ConfigPath/SecretsPath are where a change to CollectorConfig or
	// PushSecret made through the operator area is written back to, so it
	// survives a restart — the same read-modify-write pattern saCollector
	// itself no longer needs (it has no local config file at all), now
	// applied on the evaluator side instead. Either left empty means that
	// half of the settings only ever applies to the running process.
	ConfigPath  string
	SecretsPath string

	Now func() time.Time

	// Mailer sends the Benachrichtigungen page's manual "Test" actions
	// (handleNotifyTestMonthly/Weekly/Startup). Left nil, it falls back to
	// a real notify.Mailer built from the current SMTP field — the same
	// "override for tests, real thing otherwise" shape as Now/Logger.
	// Kept as the small Sender interface, not *notify.Mailer, so a test
	// can substitute a fake without touching a real mail server.
	Mailer notify.Sender

	// Restart is called once, after a successful restore, once the HTTP
	// response confirming it has already been sent — see
	// handleRestoreUpload. Restoring writes fresh files to disk but
	// cannot make this process pick them up: it already holds
	// archive.db/audit.db open and whatever master data it decrypted at
	// Unlock time stays decrypted in memory regardless of what a restore
	// just replaced on disk, so nothing takes effect until the process
	// itself is replaced. Left nil, it falls back to actually exiting
	// (see restart()) — deploy/systemd/selbst-ableser-evaluator.service
	// sets Restart=always, which is what brings a fresh process back up
	// against the newly restored files. Overridable so a test can
	// observe that a restart was requested without killing the test
	// binary itself.
	Restart func()

	// collectorsMu/collectors hold what each collector last said about
	// itself on POST /collector/config (see markCollectorSeen) — keyed by
	// the name it reports, so several collectors are several rows rather
	// than one overwriting the next. Everything in here is knowledge this
	// process cannot obtain on its own: whether a receiver is attached at
	// all (from here, an unplugged antenna and a quiet building are both
	// simply no telegrams), which build is running on the other machine
	// (BETRIEB-05), and whether the daily backup would find a stick.
	//
	// Memory only, deliberately, and for the whole life of the process: a
	// collector that stops reporting keeps its row and turns red, because
	// "was here this morning and has been silent since" is the single most
	// useful thing this table can say. A row that quietly vanished would
	// look like a collector that was never configured. Restarting the
	// evaluator is what clears the table — that is also how a collector
	// taken out of service disappears for good, and it costs the surviving
	// ones one poll interval to reappear.
	collectorsMu sync.Mutex
	collectors   map[string]collectorState
}

func (a *App) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) mailer() notify.Sender {
	if a.Mailer != nil {
		return a.Mailer
	}
	return notify.NewMailer(a.SMTP)
}

// restart triggers a.Restart if set (tests), otherwise exits the process
// shortly after returning — long enough, in practice, for the response
// that is already being written when this is called to reach the client
// first. Not a graceful shutdown: this process has none (see main.go),
// and none is needed here — an abrupt exit is exactly how this process
// already ends whenever systemd stops it, and archive.db/audit.db's own
// WAL-mode recovery already has to handle that.
func (a *App) restart() {
	if a.Restart != nil {
		a.Restart()
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

func (a *App) today() telegram.Day {
	return telegram.DayOf(a.now())
}

// collectorState is one row of the Collector page: what a collector said
// about itself, plus when it said it.
type collectorState struct {
	Name      string
	Version   string
	StartedAt time.Time

	ReceiverConnected bool
	ReceiverPort      string
	ReceiverSince     time.Time

	BackupConnected bool
	BackupPath      string

	LastSeen time.Time
}

// maxCollectors bounds the table. Rows are never dropped for being old
// (see App.collectors), so the only thing left to guard against is a
// caller inventing names: the endpoint is reachable by anyone holding the
// transfer secret, and on a single-machine installation by anything on
// loopback. Well above any real installation — reaching it at all means
// something other than a collector is talking to this endpoint, and then
// the least recently seen row is the right one to lose.
const maxCollectors = 10

// markCollectorSeen records what a collector just said about itself on a
// successfully authenticated POST /collector/config (handleCollectorConfig,
// the only caller). The reported name is the key, so two collectors are two
// rows and a restarted one reclaims its own.
func (a *App) markCollectorSeen(report collectorReport) {
	a.collectorsMu.Lock()
	defer a.collectorsMu.Unlock()

	now := a.now()
	if a.collectors == nil {
		a.collectors = make(map[string]collectorState)
	}
	name := sanitizeCollectorText(report.Name, 40)
	if name == "" {
		name = "unbenannt"
	}
	a.collectors[name] = collectorState{
		Name:              name,
		Version:           sanitizeCollectorText(report.Version, 40),
		StartedAt:         report.StartedAt,
		ReceiverConnected: report.Receiver.Connected,
		ReceiverPort:      sanitizeCollectorText(report.Receiver.Port, 60),
		ReceiverSince:     report.Receiver.Since,
		BackupConnected:   report.BackupMedium.Connected,
		BackupPath:        sanitizeCollectorText(report.BackupMedium.Path, 60),
		LastSeen:          now,
	}
	for len(a.collectors) > maxCollectors {
		oldestKey, oldest := "", time.Time{}
		for key, state := range a.collectors {
			if oldest.IsZero() || state.LastSeen.Before(oldest) {
				oldestKey, oldest = key, state.LastSeen
			}
		}
		delete(a.collectors, oldestKey)
	}
}

// knownCollectors returns the reporting collectors, ordered by name so the
// table does not reshuffle between page loads.
func (a *App) knownCollectors() []collectorState {
	a.collectorsMu.Lock()
	defer a.collectorsMu.Unlock()

	out := make([]collectorState, 0, len(a.collectors))
	for _, state := range a.collectors {
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// collectorLastSeenAt is the most recent contact from any collector — the
// overview's single "has anything reported at all" signal, kept because
// that card answers a different question than the per-collector table.
func (a *App) collectorLastSeenAt() time.Time {
	a.collectorsMu.Lock()
	defer a.collectorsMu.Unlock()

	var newest time.Time
	for _, state := range a.collectors {
		if state.LastSeen.After(newest) {
			newest = state.LastSeen
		}
	}
	return newest
}

// sanitizeCollectorText keeps a reported string displayable. Everything a
// collector sends is attacker-controlled in principle — the endpoint is
// reachable by anyone holding the transfer secret, and on a
// single-machine installation by anything on loopback — so nothing goes
// into an operator's page unbounded or with control characters in it.
// html/template already escapes; this is about not rendering a megabyte
// of junk into a table cell.
func sanitizeCollectorText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func (a *App) lookbackDays() int {
	if a.LookbackDays > 0 {
		return a.LookbackDays
	}
	return billing.DefaultLookbackDays
}

// Routes builds the complete handler tree.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /login", a.handleLoginForm)
	mux.HandleFunc("POST /login", a.handleLogin)
	mux.HandleFunc("POST /logout", a.handleLogout)

	mux.HandleFunc("GET /uvi", a.handleUVI)

	mux.HandleFunc("GET /operator", a.handleOperatorOverview)
	mux.HandleFunc("POST /operator/unlock", a.handleUnlock)
	mux.HandleFunc("POST /operator/lock", a.handleLock)

	mux.HandleFunc("GET /operator/access", a.handleAccessList)
	mux.HandleFunc("POST /operator/access", a.handleAccessSave)
	mux.HandleFunc("POST /operator/access/password", a.handleChangePassword)
	mux.HandleFunc("POST /operator/access/delete-backups", a.handleDeleteBackups)

	mux.HandleFunc("GET /operator/masterdata", a.handleMasterDataView)
	mux.HandleFunc("POST /operator/masterdata", a.handleMasterDataSave)
	mux.HandleFunc("POST /operator/masterdata/building", a.handleBuildingSave)
	mux.HandleFunc("GET /operator/masterdata/export", a.handleMasterDataExport)

	mux.HandleFunc("GET /operator/meterstatus", a.handleMeterStatus)

	mux.HandleFunc("GET /operator/live", a.handleLiveView)
	mux.HandleFunc("POST /operator/live/toggle", a.handleLiveViewToggle)
	mux.HandleFunc("POST /operator/live/delete-today", a.handleLiveDeleteToday)
	mux.HandleFunc("POST /operator/live/clear-buffer", a.handleLiveBufferClear)
	mux.HandleFunc("GET /operator/collector", a.handleCollectorSettingsView)
	mux.HandleFunc("POST /operator/collector/advanced", a.handleCollectorAdvancedSave)
	mux.HandleFunc("POST /operator/collector/trigger-push", a.handleCollectorTriggerPush)
	mux.HandleFunc("POST /operator/collector/filter-rules", a.handleCollectorFilterRuleAdd)
	mux.HandleFunc("POST /operator/collector/filter-rules/remove", a.handleCollectorFilterRuleRemove)
	mux.HandleFunc("POST /operator/collector/secret", a.handleCollectorSecretSave)
	mux.HandleFunc("POST /operator/collector/secret/generate", a.handleCollectorSecretGenerate)
	mux.HandleFunc("POST /collector/config", a.handleCollectorConfig)
	mux.HandleFunc("POST /collector/report", a.handleCollectorReport)

	mux.HandleFunc("GET /operator/archive", a.handleArchiveOverview)
	mux.HandleFunc("GET /operator/archive/download", a.handleArchiveDownload)
	mux.HandleFunc("POST /operator/archive/delete", a.handleArchiveDelete)
	mux.HandleFunc("POST /operator/archive/compress", a.handleArchiveCompress)
	mux.HandleFunc("POST /operator/archive/correct", a.handleArchiveCorrect)
	mux.HandleFunc("POST /operator/archive/import", a.handleArchiveImport)

	mux.HandleFunc("GET /operator/backup", a.handleBackupPage)
	mux.HandleFunc("POST /operator/backup/download", a.handleBackupDownload)
	mux.HandleFunc("POST /operator/restore", a.handleRestoreUpload)
	mux.HandleFunc("POST /operator/restart", a.handleRestartRequest)

	mux.HandleFunc("GET /operator/readings", a.handleReadings)
	mux.HandleFunc("GET /operator/readings/export", a.handleReadingsExport)

	mux.HandleFunc("GET /operator/audit", a.handleAuditLog)

	mux.HandleFunc("GET /operator/security", a.handleSecuritySettingsView)
	mux.HandleFunc("POST /operator/security", a.handleSecuritySettingsSave)

	mux.HandleFunc("GET /operator/notify", a.handleNotifySettingsView)
	mux.HandleFunc("POST /operator/notify", a.handleNotifySettingsSave)
	mux.HandleFunc("POST /operator/notify/test/monthly", a.handleNotifyTestMonthly)
	mux.HandleFunc("POST /operator/notify/test/weekly", a.handleNotifyTestWeekly)
	mux.HandleFunc("POST /operator/notify/test/startup", a.handleNotifyTestStartup)
	mux.HandleFunc("POST /operator/legal", a.handleLegalSave)

	mux.HandleFunc("GET /impressum", a.handleImprint)
	mux.HandleFunc("GET /datenschutz", a.handlePrivacyPolicy)

	mux.HandleFunc("GET /status", a.handleStatus)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(a.StaticFS)))

	return securityHeaders(a.restrictHosts(mux))
}

// contentSecurityPolicy is deliberately not configurable: every source
// this interface ever loads from is fixed at build time (its own
// same-origin templates/scripts/styles, and the data: URI favicon), never
// operator- or installation-specific, so there is nothing a stricter or
// looser policy per deployment would even mean. script-src and style-src
// keep 'unsafe-inline' — every page here uses inline <script> for the
// small amount of page-specific behavior (nav toggle, chart data, the
// live-view poll) rather than a bundler, in keeping with QUAL-09's "no
// unnecessary build tooling" — but every other directive is as tight as
// the app actually needs: no framing, no plugins, no form submission or
// navigation to another origin, no fetches anywhere but back to itself.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders sets the response headers a browser otherwise has no
// way to be told about: that this HTML must never sniff into something
// else (nosniff), never be framed by another origin (frame-ancestors in
// the CSP above covers this for CSP-aware browsers; X-Frame-Options
// repeats it for the handful that only understand the older header),
// never be reached over plain HTTP again once it has been reached over
// TLS once (HSTS — harmless to send unconditionally, since browsers only
// ever honor it when it actually arrived over a secure connection in the
// first place), and never leak the page URL as a Referer to another
// origin. None of this replaces html/template's own escaping (still the
// actual XSS defense); it bounds what a script could do if that escaping
// were ever defeated by a bug.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Strict-Transport-Security", "max-age=15552000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// restrictHosts rejects any request whose Host header isn't in
// AllowedHosts, before it reaches routing — a caller cannot distinguish
// "wrong host" from any other rejection reason (ZUGANG-07 also applies
// here: no information leak via a distinctive response). A nil/empty
// AllowedHosts is unrestricted, matching TrustProxy's "off unless
// explicitly configured for a public deployment" default.
// The list is taken once, here, rather than read per request: it is the
// one setting whose wrong value locks the operator out of the page that
// could correct it, so it changes only at a restart, giving a mistake a
// bounded blast radius (see handleSecuritySettingsSave, which refuses to
// save a list excluding the caller's own host in the first place).
func (a *App) restrictHosts(next http.Handler) http.Handler {
	allowed := a.AllowedHosts
	if len(allowed) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostListAllows(allowed, r.Host) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostListAllows reports whether host — a Host header, so possibly with
// a port — is in allowed. An empty list allows everything.
func hostListAllows(allowed []string, host string) bool {
	if len(allowed) == 0 {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, h := range allowed {
		if h == host {
			return true
		}
	}
	return false
}
