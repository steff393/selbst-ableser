// Command saEvaluator runs the operator/tenant web application (UVI) and
// its supporting admin subcommands. Telegram reception lives entirely in
// the separate saCollector binary/module now — this process is never
// given a code path to a wM-Bus receiver or a USB backup stick, only to
// the archive, master data, and audit log it evaluates.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/archive"
	"selbst-ableser/internal/backup"
	"selbst-ableser/internal/buildinfo"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/livepush"
	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/notify"
	"selbst-ableser/internal/telegram"
	"selbst-ableser/internal/webapp"
	"selbst-ableser/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "backup":
		err = runBackup(os.Args[2:])
	case "restore":
		err = runRestore(os.Args[2:])
	case "version":
		fmt.Println("saEvaluator", versionLabel())
	case "help", "-h", "--help":
		usage()
	default:
		// Anything else is the directory of an installation to run: the
		// common case gets the short form (`saEvaluator /var/lib/...`)
		// rather than a verb that only ever repeats the program's name.
		err = runEvaluator(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "saEvaluator:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  saEvaluator <directory> [-addr :8226]   run the installation in <directory>")
	fmt.Fprintln(os.Stderr, "  saEvaluator backup <directory> [<destination>]")
	fmt.Fprintln(os.Stderr, "  saEvaluator restore <backup> <directory>")
	fmt.Fprintln(os.Stderr, "  saEvaluator version")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "A directory holding "+config.ConfigFileName+", "+config.SecretsFileName+", "+
		config.ArchiveFileName+", "+config.MasterDataFileName+" and "+config.AuditFileName+" is one")
	fmt.Fprintln(os.Stderr, "installation; running against a different one is the whole of switching data sets.")
	fmt.Fprintln(os.Stderr, "An empty or new directory is set up on the spot when run from a terminal.")
}

// versionLabel is buildinfo.Version with a stand-in for the one case that
// carries no identification at all (a `go run`), so every caller — the
// subcommand, the status endpoint, the overview page — says the same thing.
func versionLabel() string {
	if v := buildinfo.Version(); v != "" {
		return v
	}
	return "unbekannt"
}

// takeDirArg pulls the installation directory off the front of args and
// returns the rest for flag parsing. Deliberately positional and
// deliberately first: Go's flag package stops parsing at the first
// non-flag argument, so accepting it anywhere would make
// `saEvaluator data -addr :8227` silently ignore the flag.
func takeDirArg(args []string, verb string) (dir string, rest []string, err error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("%s: a directory is required — %s", verb, exampleFor(verb))
	}
	if strings.HasPrefix(args[0], "-") {
		return "", nil, fmt.Errorf("%s: expected a directory first, got %q — %s", verb, args[0], exampleFor(verb))
	}
	return args[0], args[1:], nil
}

func exampleFor(verb string) string {
	switch verb {
	case "backup":
		return "for example: saEvaluator backup /var/lib/selbst-ableser /media/usb"
	default:
		return "for example: saEvaluator /var/lib/selbst-ableser"
	}
}

// stdinIsTerminal reports whether a human is watching this process, which
// is the one thing that decides whether a not-yet-set-up directory may be
// set up right here (see setUpInstallation): the generated operator
// password is printed once and never stored, so it must reach a terminal
// somebody is reading — never a systemd journal.
//
// A variable so a test can exercise both branches; nothing but a test
// ever replaces it.
var stdinIsTerminal = func() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// pushDisabledBehindProxy reports whether the live-view push endpoint is
// unreachable in the current configuration — not a reason to refuse to
// start, just worth a clear warning instead of a silent dead end.
//
// Without a push secret, a collector request is trusted whenever it looks
// like it came from loopback (see validCollectorAuth) — correct for the
// zero-config single-machine default, where nothing but the collector
// itself can ever make a connection look that way. Once a reverse proxy
// is in front, though, *it* is what's local to this process: any request
// the proxy forwards — including one from the open internet — arrives
// with the proxy's own loopback address, not the real caller's, so
// validCollectorAuth refuses the loopback fallback once trustProxy is on
// (a proxied RemoteAddr can't stand in for "local" any more) — every push
// is rejected until a real secret is set, independent of this function.
// That rejection already stands on its own: an empty push_secret behind a
// proxy is a fully self-contained "this one feature is off" state, the
// same as it would be without a proxy at all, not a startup-blocking
// problem — the rest of the evaluator (UVI, Stammdaten, everything else)
// has no dependency on the push endpoint working. Set push_secret from
// the Collector page once logged in, or by hand in secrets.json, to
// enable it — no restart forced on the way there.
func pushDisabledBehindProxy(trustProxy bool, pushSecret string) bool {
	return trustProxy && pushSecret == ""
}

// bootstrapMasterData creates mdPath's master data if it doesn't exist yet
// (ZUGANG-09) and prints the generated operator password once, to this
// terminal only — never a command-line argument (so it can never end up
// in shell history), and never logged, so it can only ever reach whatever
// is actually watching this process's stdout right now. That is exactly
// why setUpInstallation refuses to call this without a terminal on stdin:
// a systemd unit's stdout ends up in the journal, a much longer-lived and
// more widely readable home for a secret than one terminal session.
func bootstrapMasterData(mdPath string) error {
	pw, err := access.GeneratePassword()
	if err != nil {
		return fmt.Errorf("evaluator: generating initial password: %w", err)
	}
	switch err := access.Bootstrap(mdPath, pw); {
	case err == nil:
		fmt.Printf("evaluator: created %s\n", mdPath)
		fmt.Println()
		fmt.Println("Operator password (shown once now, write it down):")
		fmt.Println()
		fmt.Println("  " + pw)
		fmt.Println()
		fmt.Println("Change it to something memorable from the operator overview once logged in.")
		return nil
	case errors.Is(err, access.ErrAlreadyBootstrapped):
		// the normal case: an existing installation, nothing to do here.
		return nil
	default:
		return fmt.Errorf("evaluator: creating %s: %w", mdPath, err)
	}
}

// setUpInstallation turns an empty or not-yet-existing directory into an
// installation: it creates the master data and prints the generated
// operator password (see bootstrapMasterData). Nothing else is asked or
// written — the settings that decide how safely this evaluator can be
// exposed (BETRIEB-06) are on the Sicherheit page once it is running,
// where they can also be corrected later, and every other file appears
// on its own as soon as there is something to put in it.
//
// Folded into the normal start rather than kept as its own subcommand:
// the one thing that genuinely cannot happen unattended is the password,
// so that — not a verb the operator has to know about — is what this
// gates on. Without a terminal on stdin (a systemd unit, a pipe) it
// refuses and says so, exactly as the separate subcommand's absence used
// to, instead of generating a password into a service log.
func setUpInstallation(dir string, reader *bufio.Reader) error {
	mdPath := config.MasterDataPath(dir)
	if !stdinIsTerminal() {
		return fmt.Errorf("%s is not an installation yet (no %s) and there is no terminal to set one up from — "+
			"run `saEvaluator %s` once by hand first; the operator password it generates is shown once and "+
			"must not end up in a service log (see docs/install.md)", dir, config.MasterDataFileName, dir)
	}

	fmt.Printf("%s ist noch keine Anlage.\n", dir)
	fmt.Println("------------------------------------")
	fmt.Println("Hier entstehen dann alle Dateien dieser Anlage: die Stammdaten mit den")
	fmt.Println("Zählerschlüsseln, das Rohdaten-Archiv, das Protokoll, Konfiguration und")
	fmt.Println("Zugangsgeheimnisse. Nur die Programmdatei selbst liegt woanders und lässt")
	fmt.Println("sich jederzeit gefahrlos ersetzen, ohne diesen Ordner zu berühren.")
	fmt.Println()
	fmt.Println("Ein zweiter Ordner ist eine zweite, völlig getrennte Anlage — so wechselt")
	fmt.Println("man zwischen Testdaten und echten Daten.")
	fmt.Println()
	fmt.Print("Jetzt hier einrichten? [J/n]: ")
	answer, err := readLine(reader)
	if err != nil {
		return fmt.Errorf("reading answer: %w", err)
	}
	if !isYesOrEmpty(answer) {
		return fmt.Errorf("aborted — nothing was created in %s", dir)
	}
	fmt.Println()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return bootstrapMasterData(mdPath)
}

// readLine reads one line from r with the trailing newline and any
// surrounding whitespace removed. An empty final line with no trailing
// newline (io.EOF right after the prompt, e.g. piped/non-interactive
// input) is a valid empty answer, not an error.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// isYesOrEmpty accepts the German and English short/long forms of "yes",
// and a bare Enter — the prompt it answers ("Jetzt hier einrichten?
// [J/n]") only ever appears because somebody named this directory on the
// command line, so confirming what they already asked for is the
// expected answer, and nothing is at stake in it: a directory created by
// mistake holds one file and can simply be deleted.
func isYesOrEmpty(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "j", "ja", "y", "yes":
		return true
	default:
		return false
	}
}

// runEvaluator starts the web application: the operator's administration
// area and the tenant-facing UVI (UI-01 through UI-13).
func runEvaluator(args []string) error {
	dir, rest, err := takeDirArg(args, "evaluator")
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("saEvaluator", flag.ExitOnError)
	// No backticks in this description: Go's flag package reads the first
	// backticked word as the value placeholder.
	addr := flags.String("addr", "", "address to listen on for this run, overriding addr in config.json; useful for a second installation alongside a running one (default "+config.DefaultAddr+")")
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if extra := flags.Args(); len(extra) > 0 {
		return fmt.Errorf("evaluator: unexpected argument %q — only one directory is taken", extra[0])
	}

	// One reader for every prompt: bufio.Reader fills its buffer from a
	// single Read on stdin, which can pull in more than the line being
	// asked for right now; a second, freshly constructed reader would
	// never see what the first already buffered.
	stdin := bufio.NewReader(os.Stdin)

	// An absent config.json is not a problem: every field it holds has a
	// documented default, and the settings pages write it as soon as
	// there is something to write. Master data is the one file that
	// cannot default into existence — it carries a password nobody can
	// regenerate — so that, and only that, is what "is this directory an
	// installation yet?" comes down to.
	cfg, err := config.LoadOrEmpty(config.ConfigPath(dir))
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mdPath := config.MasterDataPath(dir)
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		if err := setUpInstallation(dir, stdin); err != nil {
			return fmt.Errorf("evaluator: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("evaluator: checking %s: %w", mdPath, err)
	}

	logger := newLogger(cfg.Logging.Level)

	listenAddr := cfg.Evaluator.Addr
	if *addr != "" {
		listenAddr = *addr
	}
	if listenAddr == "" {
		listenAddr = config.DefaultAddr
	}

	store, err := archive.OpenStore(config.ArchivePath(dir))
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer store.Close()

	auditLog, err := access.OpenAuditLog(config.AuditPath(dir))
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditLog.Close()

	templates, err := webapp.LoadTemplates(web.FS)
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("loading static assets: %w", err)
	}

	app := &webapp.App{
		Store:           store,
		ArchivePath:     config.ArchivePath(dir),
		Vault:           &masterdata.Vault{},
		MasterDataPath:  mdPath,
		Sessions:        access.NewSessionStore(2 * time.Hour),
		Audit:           auditLog,
		AuditPath:       config.AuditPath(dir),
		LoginLimiter:    access.NewLimiter(10, time.Minute),
		UnlockLimiter:   access.NewLimiter(5, 5*time.Minute),
		LookbackDays:    cfg.Evaluator.LookbackDays,
		Version:         versionLabel(),
		Templates:       templates,
		StaticFS:        staticFS,
		TrustProxy:      cfg.Evaluator.TrustProxy,
		AllowedHosts:    cfg.Evaluator.AllowedHosts,
		Logger:          logger,
		CollectorConfig: cfg.Collector,
		NotifyConfig:    cfg.Notify,
		LegalConfig:     cfg.Legal,
		ConfigPath:      config.ConfigPath(dir),
		SecretsPath:     config.SecretsPath(dir),
		// Always available, regardless of whether a push_secret ends up
		// configured below: saCollector's default (no secret, loopback
		// trust) needs somewhere to land just as much as a configured
		// one does. An empty buffer costs nothing.
		LiveBuffer: livepush.NewBuffer(200),
	}

	secretsPath := config.SecretsPath(dir)
	secrets, err := config.LoadSecrets(secretsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("loading secrets: %w", err)
		}
		// No secrets file yet: write an empty starter. There is no value
		// in here that could silently be wrong — SMTP and PushSecret
		// simply stay unset, and an empty PushSecret is a fully valid,
		// self-contained state (see pushDisabledBehindProxy) rather than
		// something that needs to block startup.
		if err := config.SaveSecrets(secretsPath, config.Secrets{}); err != nil {
			fmt.Fprintf(os.Stderr, "evaluator: writing starter %s: %v\n", secretsPath, err)
		} else {
			fmt.Printf("evaluator: wrote starter secrets file to %s\n", secretsPath)
		}
	}

	if secrets.PushSecret != "" {
		app.PushSecret = secrets.PushSecret
		logger.Info("evaluator: live-view push endpoint enabled")
	}
	// Carried on the App so the Benachrichtigungen page can show and
	// edit them; the loop below keeps using its own copy, since changing
	// the server mid-run should not silently re-point an in-flight send.
	app.SMTP = secrets.SMTP

	if cfg.Notify.Enabled {
		if err := secrets.RequireSMTP(); err != nil {
			return fmt.Errorf("evaluator: %w", err)
		}
		mailer := notify.NewMailer(secrets.SMTP)
		go runNotificationLoop(app, mailer, auditLog, cfg, logger)
	}

	// Anchored on TrustProxy rather than guessed at from the listen
	// address: TrustProxy is the one signal that already means "a reverse
	// proxy genuinely sits in front of this process" (BETRIEB-06 requires
	// it to be set deliberately, never inferred). A warning rather than a
	// refusal to start, because the push endpoint enforces this on its
	// own anyway (validCollectorAuth) — nothing else about the
	// installation depends on it.
	if pushDisabledBehindProxy(cfg.Evaluator.TrustProxy, app.PushSecret) {
		logger.Warn("evaluator: trust_proxy is on but no push_secret is configured — " +
			"the live-view push endpoint will reject every collector request until one is set " +
			"(Collector page once logged in)")
	}

	// Explicit timeouts rather than the zero-value (no timeout at all)
	// http.ListenAndServe defaults to: without them, a connection that
	// trickles its request headers in one byte at a time, or that simply
	// never finishes sending them, ties up a server goroutine forever —
	// classic Slowloris, and unauthenticated (it happens before any
	// handler, let alone a login, runs). ReadTimeout/WriteTimeout are
	// deliberately generous rather than tight: the archive import path
	// alone accepts uploads up to 200 MiB (see maxArchiveImportUpload),
	// and a large archive/readings export can be substantial too — both
	// are legitimate, if infrequent, operator actions that must not be
	// cut off partway through on a slow connection.
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	logger.Info("evaluator starting", "version", versionLabel(), "dir", dir, "addr", listenAddr, "trust_proxy", cfg.Evaluator.TrustProxy)
	printReachableAt(listenURLs(listenAddr, nonLoopbackIPv4s))
	return server.ListenAndServe()
}

// printReachableAt turns the listen address into something that can be
// typed into a browser. The log line above carries the bind address as
// configured (":8226"), which is precise and useless for that: it names a
// port and every interface, not an address anyone can visit. The addresses
// worth printing are the ones that are hard to look up — a machine's own
// place in the network, needed as soon as the UVI should be opened from a
// phone in the same house (see docs/quickstart-win.md's firewall note).
func printReachableAt(urls []string) {
	if len(urls) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Reachable at:")
	for _, u := range urls {
		fmt.Println("  " + u)
	}
	fmt.Println()
}

// listenURLs expands a bind address into the URLs it answers on. A
// wildcard host means every interface, so the machine's own addresses are
// looked up and listed after localhost; a concrete host answers only
// there and is returned as it stands. localIPs is a parameter so a test
// can supply a fixed set rather than depending on the machine it runs on.
//
// Always http://: this process only ever speaks plain HTTP. Where a
// reverse proxy terminates TLS, the address the outside world uses is the
// proxy's own and is not knowable from here — that one is on the
// Sicherheit page instead, as the Domain-Sperre.
func listenURLs(addr string, localIPs func() []string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	switch host {
	case "", "0.0.0.0", "::":
		urls := []string{"http://localhost:" + port}
		for _, ip := range localIPs() {
			urls = append(urls, "http://"+net.JoinHostPort(ip, port))
		}
		return urls
	case "127.0.0.1", "::1", "localhost":
		return []string{"http://localhost:" + port}
	default:
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
}

// nonLoopbackIPv4s reports this machine's own IPv4 addresses. IPv6 is
// left out on purpose: the point is a line somebody retypes on a phone,
// and every machine that has a v6 address here also has a v4 one on the
// same network. Link-local addresses are skipped for the same reason —
// they are not what anyone will type.
func nonLoopbackIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
			continue
		}
		if v4 := n.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

// runNotificationLoop sends the startup notification once (BENACHR-05),
// then periodically checks for a monthly reminder to send (BENACHR-01,
// catch-up friendly — see config.NotificationCheckInterval) and for
// anything BENACHR-03 says the operator should be alerted about.
func runNotificationLoop(app *webapp.App, mailer *notify.Mailer, auditLog *access.AuditLog, cfg config.Config, logger *slog.Logger) {
	if err := notify.SendStartupNotification(mailer, auditLog, cfg.Notify.StartupNotification, cfg.Notify.OperatorEmail, cfg.Notify.BaseURL); err != nil {
		logger.Error("startup notification failed", "err", err)
	}

	ticker := time.NewTicker(config.NotificationCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		today := telegram.DayOf(now)

		// The tenants' monthly reminder: only within the first of the
		// month's notify.SendHour (BENACHR-01) — a run missed during that
		// hour is not caught up automatically; see notify/schedule.go on
		// why, and notify's monthlyReminderConfirmed on how it surfaces as
		// a hint instead.
		if notify.MonthlyReminderDue(now) {
			if md, unlocked := app.Vault.Get(); unlocked {
				month := string(today)[:7]
				if _, err := notify.SendMonthlyReminders(mailer, auditLog, md, month, today, cfg.Notify.BaseURL, cfg.Notify.OperatorEmail); err != nil {
					logger.Error("monthly reminder run failed", "err", err)
				}
			}
		}

		// The operator's weekly status: Monday from notify.SendHour, once
		// a week, whether or not anything is wrong. This is also where a
		// silent meter or a still-locked vault surfaces by mail — there is
		// deliberately no separate, immediate "Störungsmeldung" alongside
		// it (see notify's computeWeeklyStatus doc comment for why: without
		// a per-day dedup, it re-sent every hour for as long as a problem
		// persisted).
		if _, err := notify.SendWeeklyStatus(mailer, auditLog, app.Store, app.Vault, now, silentMeterDays, cfg.Notify.OperatorEmail, false); err != nil {
			logger.Error("weekly status mail failed", "err", err)
		}
	}
}

// silentMeterDays matches the Zählerstatus page's own threshold, so a
// meter reported as quiet by mail and on screen means the same thing.
const silentMeterDays = 5

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// runBackup implements BETRIEB-07/DATEN-06: a complete, consistent backup
// of an installation — archive, audit log, master data, config.json and
// secrets.json — into one directory. All five, always: a backup that
// silently turns out to be missing a part is worse than an obvious
// failure, since it is only ever discovered when it is already needed.
// There is deliberately no flag to leave anything out.
//
// Which files those are needs no lookup: an installation is one
// directory holding the five under their fixed names (see config's file
// name constants), so a backup covers by construction exactly what the
// running evaluator uses.
func runBackup(args []string) error {
	dir, rest, err := takeDirArg(args, "backup")
	if err != nil {
		return err
	}
	// The destination is optional: without one, a connected removable
	// medium is detected automatically (DATEN-06).
	var destDir string
	switch len(rest) {
	case 0:
	case 1:
		destDir = rest[0]
	default:
		return fmt.Errorf("backup: unexpected argument %q — %s", rest[1], exampleFor("backup"))
	}

	masterDataPath := config.MasterDataPath(dir)
	if _, err := os.Stat(masterDataPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup: %s holds no installation (no %s) — pass the directory the evaluator runs with", dir, config.MasterDataFileName)
		}
		return fmt.Errorf("backup: checking %s: %w", masterDataPath, err)
	}
	archivePath := config.ArchivePath(dir)
	auditPath := config.AuditPath(dir)
	configPath := config.ConfigPath(dir)
	secretsPath := config.SecretsPath(dir)

	if destDir == "" {
		detected, err := backup.AutoDetectDest()
		if err != nil {
			return err
		}
		destDir = detected
		fmt.Printf("backup: detected removable medium at %s\n", destDir)
	}

	store, err := archive.OpenStore(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer store.Close()
	auditLog, err := access.OpenAuditLog(auditPath)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditLog.Close()

	if err := backup.Run(store, auditLog, masterDataPath, configPath, secretsPath, destDir); err != nil {
		return err
	}
	fmt.Printf("backup: wrote a complete backup to %s\n", destDir)
	return nil
}

// runRestore implements the other half of BETRIEB-07: restoring a backup
// produced by the backup subcommand. It never overwrites an existing file
// at any target (see internal/backup.Restore).
//
// The target directory is taken literally, and the five files land in it
// under their fixed names — the same layout every installation has. The
// usual target is a directory that does not exist yet: a fresh machine,
// or the scratch directory docs/install.md recommends rehearsing a
// restore against.
func runRestore(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("restore: expected a backup and a target directory — for example: saEvaluator restore /media/usb /var/lib/selbst-ableser")
	}
	src, dir := args[0], args[1]
	if strings.HasPrefix(src, "-") || strings.HasPrefix(dir, "-") {
		return fmt.Errorf("restore: expected two directories, got %q and %q", src, dir)
	}

	// 0o700 matches what an installation is created with everywhere else.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("restore: creating %s: %w", dir, err)
	}
	if err := backup.Restore(src,
		config.ArchivePath(dir),
		config.AuditPath(dir),
		config.MasterDataPath(dir),
		config.ConfigPath(dir),
		config.SecretsPath(dir),
	); err != nil {
		return err
	}
	fmt.Printf("restore: restored from %s into %s\n", src, dir)
	return nil
}
