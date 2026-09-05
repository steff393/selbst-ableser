// Command saCollector receives wM-Bus telegrams from a radio receiver,
// validates their protocol-level structure, and reports them to an
// evaluator. It never touches key material, master data, or access
// tokens — it cannot even import a package that provides them, since it
// lives in its own Go module (see internal/telegram/doc.go).
//
// Almost everything about how it behaves — how often it reports, which
// telegrams to filter out — is not a flag here at all: it is fetched from
// the evaluator (see internal/settings) and cached locally so a restart,
// or a brief loss of contact, does not interrupt reception. The only
// things a flag can never replace are physically local to this machine:
// which receiver to talk to, and the one bootstrap secret that lets this
// collector authenticate to its evaluator in the first place.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"selbst-ableser/collector/internal/buildinfo"
	"selbst-ableser/collector/internal/filter"
	"selbst-ableser/collector/internal/receiver"
	"selbst-ableser/collector/internal/removable"
	"selbst-ableser/collector/internal/report"
	"selbst-ableser/collector/internal/settings"
	"selbst-ableser/collector/internal/store"
	"selbst-ableser/collector/internal/telegram"
)

// The daily push (see deliverDue) never sends a day until it is fully
// over — see settings.Settings.DailyHour for the configured hour
// (evaluator-configured, defaulting to 3): a smaller-hours default,
// deliberately not late evening, so a day that just ended reaches the
// archive soon after, rather than only on the following night's run.

// stdout and stderr are where logLine and errLine write; package-level
// variables (rather than the real os.Stdout/os.Stderr used directly) so a
// test can swap in a buffer and check what got printed.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// logLine and errLine print a uniformly timestamped line — every line
// saCollector prints, status and errors alike, should read the same way
// in the console. The one exception is the per-telegram receipt line,
// which carries the telegram's own received-at time instead (see
// runReceiveLoop): that timestamp is meaningful data, not just a log
// prefix, so it is not routed through here.
func logLine(format string, args ...any) {
	fmt.Fprintf(stdout, "%s  "+format+"\n", timestampedArgs(args)...)
}

func errLine(format string, args ...any) {
	fmt.Fprintf(stderr, "%s  "+format+"\n", timestampedArgs(args)...)
}

func timestampedArgs(args []any) []any {
	return append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		errLine("%v", err)
		os.Exit(1)
	}
}

// The two files this collector keeps locally, under fixed names in -path.
// There is deliberately no way to redirect one of them on its own: both
// exist for the same situation — the evaluator being unreachable — and
// are only useful together, so splitting them across directories would
// buy nothing and cost an explanation everywhere the layout is described.
// The evaluator's five files follow the same rule (see config's file name
// constants in the other module).
const (
	settingsCacheFileName = "settings-cache.json"
	backupFileName        = "backup.db"
)

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("saCollector", flag.ExitOnError)
	evaluator := fs.String("evaluator", "http://localhost:8226", "evaluator base URL")
	secret := fs.String("secret", "", "shared transfer secret (ARCH-04); empty relies on loopback trust, only accepted if the evaluator also has none configured")
	name := fs.String("name", "", "name this collector reports to the evaluator, shown on its Collector page; empty uses the hostname — set it when more than one collector reports to the same evaluator, since two freshly imaged Pis are both called raspberrypi")
	port := fs.String("port", "", "serial port device path of the receiver, e.g. COM3 or /dev/ttyUSB0; omitted auto-detects an IMST iU891A-XL by USB ID — only needed as a manual override (e.g. more than one receiver on this machine)")
	dataPath := fs.String("path", "data", "directory for this collector's own two local files (settings-cache.json, backup.db) — a working directory for files that regrow on their own, not an installation like the evaluator's")
	showVersion := fs.Bool("version", false, "print the build identifier and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedCachePath := filepath.Join(*dataPath, settingsCacheFileName)
	resolvedBackupPath := filepath.Join(*dataPath, backupFileName)

	if *showVersion {
		fmt.Println("saCollector", versionLabel())
		return nil
	}
	logLine("saCollector %s starting", versionLabel())

	// Everything the evaluator gets to know about this machine, assembled
	// here so the receiver callbacks below can feed it as events happen.
	status := newCollectorStatus(*name, versionLabel(), time.Now())
	logLine("reporting as %q", status.name)

	// A receiver missing right now — at startup, or later after being
	// unplugged — is not fatal: SerialSource's own reconnect-with-backoff
	// loop (see OpenAutoDetectedPort) keeps watching for one to appear
	// (withReceiverLogging reports the first failed attempt and every
	// later (re)connect, so this stays visible without repeating on every
	// retry), so the process stays up and starts receiving as soon as one
	// is plugged in. Only an unambiguous setup problem (more than one
	// receiver found, needing an explicit -port) still stops the process
	// at startup.
	var source receiver.Source
	if *port != "" {
		source = withReceiverLogging(receiver.NewSerialSource(receiver.OpenSerialPort(*port)), status)
		logLine("connecting to %s", *port)
	} else {
		if _, _, err := receiver.FindReceiverPort(); err != nil {
			return fmt.Errorf("saCollector: detecting receiver: %w", err)
		}
		source = withReceiverLogging(receiver.NewSerialSource(receiver.OpenAutoDetectedPort()), status)
	}
	defer source.Close()

	buffer, err := store.Open(":memory:")
	if err != nil {
		return fmt.Errorf("saCollector: opening buffer: %w", err)
	}
	defer buffer.Close()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient := report.NewClient()
	live := newLiveSettings(resolvedCachePath, *evaluator, *secret, httpClient, status)
	live.start(ctx)

	lastSent := &lastSentTime{t: time.Now()}
	reportErr := &reportErrorTracker{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runReportLoop(ctx, live, buffer, *evaluator, *secret, httpClient, lastSent, reportErr)
	}()
	go func() {
		defer wg.Done()
		runDailyLoop(ctx, live, buffer, *evaluator, *secret, httpClient, resolvedBackupPath)
	}()

	err = runReceiveLoop(ctx, source, buffer, live)
	stop()
	wg.Wait()

	// A final, best-effort flush of whatever arrived since the report
	// loop's last tick: without this, a clean shutdown could happen to
	// land between two ticks of the periodic loop and leave freshly
	// received data unsent until the process starts again. This never
	// marks anything final — only the daily loop does that, strictly on
	// its own schedule — so it cannot wrongly commit a day that only
	// looks done because the process happened to be stopping.
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sendPending(flushCtx, buffer, *evaluator, *secret, httpClient, lastSent, reportErr)

	return err
}

// lastSentTime is the report loop's watermark, shared with the
// shutdown-time final flush so it does not resend what the loop's last
// successful tick already delivered.
type lastSentTime struct {
	mu sync.Mutex
	t  time.Time
}

func (l *lastSentTime) get() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t
}

func (l *lastSentTime) set(t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.t = t
}

// withReceiverLogging wires a short, operator-facing console message to
// s's connect, disconnect, and connect-failure events (see
// SerialSource.OnConnect/OnDisconnect/OnConnectError) — a receiver being
// unplugged and later replugged, or simply not present yet, should be
// visible in the log, not silent, even though SerialSource itself handles
// the actual reconnecting without any help from here. OnConnectError
// already suppresses repeats of an unchanging failure, so this never
// prints on every backoff retry against a still-absent receiver.
// It also feeds the same events into status, which is what the evaluator
// shows: whether a receiver is attached at all is the difference between
// "no meter is transmitting" and "the antenna is unplugged", and from the
// evaluator's side those look identical — both are simply no telegrams.
func withReceiverLogging(s *receiver.SerialSource, status *collectorStatus) *receiver.SerialSource {
	s.OnConnect = func(port string) {
		logLine("receiver connected on %s", port)
		status.receiverConnected(port)
	}
	s.OnDisconnect = func(err error) {
		logLine("receiver lost: %v", err)
		status.receiverLost()
	}
	s.OnConnectError = func(err error) {
		logLine("receiver unavailable: %v", err)
		status.receiverLost()
	}
	s.OnDeviceInfo = func(step string, raw []byte) { logLine("%s response: % X", step, raw) }
	return s
}

// runReceiveLoop is the collector's core job: read telegrams as fast as
// they arrive, apply the current filter rules, and keep the buffer's
// per-meter-per-day row up to date. It never blocks on network I/O — the
// report and daily loops read the buffer independently, on their own
// schedules.
func runReceiveLoop(ctx context.Context, source receiver.Source, buffer *store.Store, live *liveSettings) error {
	logLine("receiving (Ctrl-C to stop)")
	for {
		tel, err := source.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				logLine("stopped")
				return nil
			}
			return fmt.Errorf("saCollector: receiving: %w", err)
		}

		rawHex := hex.EncodeToString(tel.Raw)
		if !live.filter().Keep(tel.MeterID, rawHex) {
			continue // FUNK-05-style filtering: discarded as if never received
		}

		entry := store.Entry{
			MeterID:    tel.MeterID,
			Day:        telegram.DayOf(tel.ReceivedAt),
			ReceivedAt: tel.ReceivedAt,
			RSSI:       tel.RSSI,
			RawHex:     rawHex,
		}
		if err := buffer.Upsert(entry); err != nil {
			errLine("buffering meter %s: %v", entry.MeterID, err)
			continue
		}

		fmt.Fprintf(stdout, "%s  meter %-10s  %4d dBm (%-4s)  %s  %d bytes\n",
			tel.ReceivedAt.Format("2006-01-02 15:04:05"),
			tel.MeterID, tel.RSSI, telegram.ClassifyRSSI(tel.RSSI), describeTelegram(tel.Raw), len(tel.Raw))
	}
}

// describeTelegram gives a short manufacturer/device-type label for
// diagnostic console output, without needing any key material.
func describeTelegram(raw []byte) string {
	if len(raw) < 10 {
		return "malformed"
	}
	manufacturer := telegram.Manufacturer(binary.LittleEndian.Uint16(raw[2:4]))
	deviceType, ok := telegram.IdentifyDeviceType(raw[9])
	if !ok {
		return manufacturer
	}
	return manufacturer + " " + deviceType.Abbr
}

// inactiveRecheckInterval is how often runReportLoop re-checks whether
// the live view has been switched on while it's off — a cheap, purely
// local check, deliberately not tied to Settings.ConfigPollInterval
// (which could default to a full 60s, or even be its unfetched zero
// value this early, if read right at startup): activating the live view
// should never risk waiting out a stale, unrelated interval first.
const inactiveRecheckInterval = time.Second

// runReportLoop sends whatever has arrived since the last successful send
// to the evaluator, on the interval the evaluator itself last supplied
// (see liveSettings) — but only while the live view is switched on at
// all (Settings.LiveViewActive, off by default); while it's off, this
// just waits and rechecks rather than sending on some fallback interval.
// This loop never marks anything final and never prunes the buffer, so a
// delivery failure here costs nothing beyond a delayed live view; the
// daily loop is what actually commits and clears.
func runReportLoop(ctx context.Context, live *liveSettings, buffer *store.Store, evaluatorURL, secret string, client *http.Client, lastSent *lastSentTime, reportErr *reportErrorTracker) {
	for {
		active, interval := live.confirmedActive()
		wait := inactiveRecheckInterval
		if active {
			wait = interval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if active, _ := live.confirmedActive(); active {
			sendPending(ctx, buffer, evaluatorURL, secret, client, lastSent, reportErr)
		}
	}
}

// reportErrorTracker suppresses repeats of the exact same live-report
// error so an extended outage (the evaluator being unreachable, say) logs
// it once instead of once per retry — the report interval can be as short
// as a few seconds, so without this an outage floods the console with an
// identical line over and over.
type reportErrorTracker struct {
	mu   sync.Mutex
	last string
}

// note reports whether msg is new since the last call (and should
// therefore be logged), and remembers it either way.
func (t *reportErrorTracker) note(msg string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if msg == t.last {
		return false
	}
	t.last = msg
	return true
}

// clear resets the tracker once a send succeeds, so a later recurrence of
// the same error is reported again rather than assumed already known.
func (t *reportErrorTracker) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = ""
}

// sendPending sends everything received since lastSent's current value,
// marked not final, and advances it only on success — a failed send
// leaves lastSent untouched, so the same entries (plus whatever else
// arrived meanwhile) are simply retried on the next call.
func sendPending(ctx context.Context, buffer *store.Store, evaluatorURL, secret string, client *http.Client, lastSent *lastSentTime, reportErr *reportErrorTracker) {
	since := lastSent.get()
	now := time.Now()
	entries, err := buffer.Since(since)
	if err != nil {
		errLine("reading recent entries: %v", err)
		return
	}
	if len(entries) == 0 {
		lastSent.set(now)
		return
	}
	if _, err := report.Send(ctx, client, evaluatorURL, secret, entries, false); err != nil {
		if reportErr.note(err.Error()) {
			errLine("live report: %v", err)
		}
		return
	}
	reportErr.clear()
	lastSent.set(now)
}

// runDailyLoop wakes once a day at the configured daily-push hour
// (Settings.DailyHour, default 3 — see deliverDue for why a small-hours
// default, not late evening) — or immediately, whenever the evaluator's
// "Push jetzt auslösen" button requests it (live.triggerRequested) —
// and tries to deliver every day the buffer holds that deliverDue
// considers fully over, durably: a network report marked final, and a
// write to backup.db — on a USB stick if one is attached at that
// moment, otherwise at backupPath. A day is only removed from the
// buffer once at least one of the two succeeded; both failing simply
// means it stays and is retried the next time this runs, scheduled or
// manually triggered, until one works. A manual trigger clears the
// buffer on success exactly like the real daily run would, so firing it
// several times a day and still letting the scheduled run happen is
// harmless — each just delivers whatever is new since the last one, and
// the evaluator's own commit is idempotent per meter and day.
// dailyHourRecheckInterval bounds how long a change to the configured
// daily-push hour can take to reach an already-running collector. The
// wait below is armed once, from whatever live.settings().DailyHour()
// returns at that moment, and — being a plain time.After — has no way to
// notice a later change on its own; live.settings() is updated
// independently and asynchronously by the settings-poll loop (see
// liveSettings.poll), with nothing to wake this select when it does.
// Abandoning the current wait this often and re-arming it from whatever
// DailyHour() returns now closes that gap without needing a direct
// signal from the poller — matching the "wirkt spätestens beim nächsten
// Abholen der Einstellungen (innerhalb einer Minute)" already promised
// for every other collector setting. Re-arming when nothing actually
// changed costs one nextDailyTrigger call, so this can safely be shorter
// than actually necessary rather than tuned finely.
// A var, not a const, so a test can shrink it instead of running for
// real minutes.
var dailyHourRecheckInterval = time.Minute

func runDailyLoop(ctx context.Context, live *liveSettings, buffer *store.Store, evaluatorURL, secret string, client *http.Client, backupPath string) {
	for {
		includeToday := false
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextDailyTrigger(time.Now(), live.settings().DailyHour()))):
		case <-live.triggerRequested():
			logLine("manual push requested")
			includeToday = true
		case <-time.After(dailyHourRecheckInterval):
			continue // re-derive the wait above from whatever DailyHour() is now, without treating this as a due delivery
		}
		if ctx.Err() != nil {
			return
		}
		deliverDue(ctx, buffer, evaluatorURL, secret, client, backupPath, includeToday)
	}
}

// deliverDue sends every buffered day whose calendar day (telegram.Day,
// in telegram.Local — see DayOf) is fully in the past. The scheduled
// run never sends the day still in progress, even once today's own
// trigger hour has already been reached: a day is not actually over
// until midnight, and committing it before then durably archives a
// reading that is still missing whatever arrives later that same day.
// Left in the buffer, a day still in progress is picked up
// automatically once a later scheduled call finds it has finally
// become the past — this is the entire reason the default trigger hour
// is set to the small hours rather than late evening (see
// runDailyLoop): the closer to midnight the buffer is actually checked,
// the sooner a day that just ended gets delivered.
//
// includeToday lifts that restriction for the one caller that has a
// reason to override it: a manual "Push jetzt auslösen" is a deliberate
// request for whatever the current reading is, right now, not a claim
// that the day is actually finished — the operator asking for it is
// what makes an admittedly-incomplete commit acceptable here, the same
// way it never is for the unattended scheduled run. Sending today's
// still-open reading this way can still conflict with a later,
// more-complete automatic delivery of that same day once it does end —
// see the Live-Ansicht's "Heutige Werte löschen" for recovering from
// exactly that.
func deliverDue(ctx context.Context, buffer *store.Store, evaluatorURL, secret string, client *http.Client, backupPath string, includeToday bool) {
	today := telegram.DayOf(time.Now())
	days, err := buffer.Days()
	if err != nil {
		errLine("listing pending days: %v", err)
		return
	}
	for _, day := range days {
		if !includeToday && !day.Before(today) {
			continue // not over yet — leave it buffered for a later (scheduled) run
		}
		entries, err := buffer.ForDay(day)
		if err != nil {
			errLine("reading day %s: %v", day, err)
			continue
		}

		networkOK := true
		if _, err := report.Send(ctx, client, evaluatorURL, secret, entries, true); err != nil {
			networkOK = false
			errLine("daily report for %s: %v", day, err)
		}

		backupOK := writeBackup(entries, backupPath)

		if networkOK || backupOK {
			if err := buffer.DeleteDay(day); err != nil {
				errLine("clearing delivered day %s: %v", day, err)
			} else {
				logLine("day %s delivered (network=%v, backup=%v)", day, networkOK, backupOK)
			}
		} else {
			logLine("day %s not delivered, retrying later", day)
		}
	}
}

// writeBackup writes entries into backup.db on a currently attached USB
// stick, or into the fixed internal backupPath if none is attached right
// now. Either way it is the same schema-compatible store the evaluator
// reads back with its own archive import path.
func writeBackup(entries []store.Entry, backupPath string) bool {
	dest := backupPath
	if mount, found, err := removable.AutoDetect(); err == nil && found {
		dest = filepath.Join(mount, "backup.db")
	}

	db, err := store.Open(dest)
	if err != nil {
		errLine("opening backup at %s: %v", dest, err)
		return false
	}
	defer db.Close()

	for _, e := range entries {
		if err := db.Upsert(e); err != nil {
			errLine("writing backup at %s: %v", dest, err)
			return false
		}
	}
	return true
}

// nextDailyTrigger returns the next occurrence of hour:00 strictly after
// now.
func nextDailyTrigger(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// liveSettings holds the collector's current operating parameters —
// fetched from the evaluator, cached locally, and updated in the
// background — behind a mutex so the receive loop (reading the filter on
// every telegram) and the report loop (reading the interval on every
// tick) never race with the poll loop replacing them.
type liveSettings struct {
	cache     *settings.Cache
	evaluator string
	secret    string
	client    *http.Client
	// status is asked for a fresh snapshot on every poll, so what the
	// evaluator shows is the state at that moment rather than at startup.
	status *collectorStatus

	mu        sync.Mutex
	cur       settings.Settings
	confirmed bool // set once this run's own poll has succeeded at least once; see confirmedActive

	// startedAt anchors the startup live window (see confirmedActive).
	startedAt time.Time

	// triggerCh receives a value exactly when a poll notices TriggerPush
	// newly become true (an edge, not a level) — see poll and
	// triggerRequested. Buffered by 1 so a poll never blocks waiting for
	// a slow consumer.
	triggerCh chan struct{}
}

func newLiveSettings(cachePath, evaluator, secret string, client *http.Client, status *collectorStatus) *liveSettings {
	return &liveSettings{
		startedAt: time.Now(),
		cache:     settings.NewCache(cachePath),
		evaluator: evaluator,
		secret:    secret,
		client:    client,
		status:    status,
		triggerCh: make(chan struct{}, 1),
	}
}

// start loads whatever was last cached (if anything) so the collector has
// a sensible starting point immediately, then begins polling the
// evaluator in the background for as long as ctx is alive.
func (l *liveSettings) start(ctx context.Context) {
	if cached, found, err := l.cache.Load(); err == nil && found {
		l.mu.Lock()
		l.cur = cached
		l.mu.Unlock()
		receiver.SetIdleTimeout(cached.IdleReconnect())
	}

	go func() {
		l.poll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(l.settings().ConfigPollInterval()):
				l.poll(ctx)
			}
		}
	}()
}

func (l *liveSettings) poll(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	fetched, err := settings.Fetch(fetchCtx, l.client, l.evaluator, l.secret, l.status.snapshot())
	if err != nil {
		// The evaluator is unreachable right now; keep operating on
		// whatever is already cached/current, silently, exactly as
		// intended — this is not an operator-facing error.
		return
	}

	l.mu.Lock()
	triggered := fetched.TriggerPush && !l.cur.TriggerPush
	l.cur = fetched
	l.confirmed = true
	l.mu.Unlock()

	receiver.SetIdleTimeout(fetched.IdleReconnect())

	if triggered {
		select {
		case l.triggerCh <- struct{}{}:
		default: // already one pending; runDailyLoop hasn't consumed it yet
		}
	}

	if err := l.cache.Save(fetched); err != nil {
		errLine("caching settings: %v", err)
	}
}

func (l *liveSettings) settings() settings.Settings {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}

// confirmedActive reports whether the live view should actually push right
// now, and if so, at what interval. It requires confirmed — a cached
// LiveViewActive loaded at startup from a previous run is not enough on
// its own: a restarted collector must not resume frequent pushing purely
// on the cache's say-so before this run's own first poll has heard the
// same from the evaluator. Once confirmed becomes true it stays true for
// the rest of the process's life, even through a later failed poll — an
// outage after a confirmed "active" should not silently stop the live
// view either.
// startupLiveWindow is how long after start a collector reports at the
// live interval regardless of what the evaluator has configured. Bringing
// a receiver up — plugging it in, checking that telegrams arrive, seeing
// which meters are in range — is exactly when someone is watching the
// Live-Ansicht, and having to switch it on first (from the other machine,
// in the deployment where they are separate) just to see whether the
// thing works at all is the wrong way round.
const startupLiveWindow = 5 * time.Minute

// startupLiveInterval paces that window when the evaluator has no live
// interval configured at all. Frequent enough to watch, not so frequent
// that a five-minute burst is a meaningful amount of traffic.
const startupLiveInterval = 10 * time.Second

// confirmedActive reports whether the live push should run right now, and
// how often.
//
// Two independent reasons for it to be on: the evaluator asked for it, or
// this collector started less than startupLiveWindow ago. The startup
// window deliberately does not wait for l.confirmed — a collector that
// cannot reach its evaluator at all is precisely the case someone is
// standing in front of the receiver trying to diagnose.
func (l *liveSettings) confirmedActive() (active bool, interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.confirmed && l.cur.LiveViewActive() {
		return true, l.cur.ReportInterval()
	}
	if time.Since(l.startedAt) < startupLiveWindow {
		if l.confirmed && l.cur.ReportIntervalSeconds > 0 {
			return true, l.cur.ReportInterval()
		}
		return true, startupLiveInterval
	}
	return false, 0
}

func (l *liveSettings) filter() *filter.Filter {
	return filter.New(l.settings().FilterRules)
}

// triggerRequested fires once each time the evaluator's "Push jetzt
// auslösen" button sets TriggerPush — see poll.
func (l *liveSettings) triggerRequested() <-chan struct{} {
	return l.triggerCh
}

// versionLabel is buildinfo.Version with a stand-in for the one case that
// carries no identification at all (a `go run`, which stamps no revision).
func versionLabel() string {
	if v := buildinfo.Version(); v != "" {
		return v
	}
	return "unbekannt"
}
