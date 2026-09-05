package webapp

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/notify"
)

// gmxDefaults pre-fills the SMTP form for the provider this installation
// actually uses, so the common case is one field (the password) rather
// than five. Any other provider is entered by hand — the settings stay
// fully general (BENACHR-04); this is a starting point, not a special
// case in the sending code.
var gmxDefaults = config.SMTPCredentials{
	Host:       "mail.gmx.net",
	Port:       587,
	Encryption: "starttls",
}

type notifySettingsPageData struct {
	Base

	Enabled             bool
	StartupNotification bool
	OperatorEmail       string
	BaseURL             string

	Host        string
	Port        string
	Encryption  string
	Username    string
	From        string
	PasswordSet bool

	SendHour   int
	Error      string
	TestResult string
	TestFailed bool

	legalEditData // ImprintText, PrivacyPolicyText, ImprintDefault, PrivacyPolicyDefault, LegalDraft — see legal_handlers.go
}

func (a *App) notifySettingsPageData(sess *access.Session, errMsg string) notifySettingsPageData {
	smtp := a.SMTP
	if smtp.Host == "" {
		smtp = gmxDefaults
	}
	port := ""
	if smtp.Port > 0 {
		port = strconv.Itoa(smtp.Port)
	}
	encryption := smtp.Encryption
	if encryption == "" {
		encryption = "starttls"
	}

	return notifySettingsPageData{
		Base:                a.base("Benachrichtigungen", sess),
		Enabled:             a.NotifyConfig.Enabled,
		StartupNotification: a.NotifyConfig.StartupNotification,
		OperatorEmail:       a.NotifyConfig.OperatorEmail,
		BaseURL:             a.NotifyConfig.BaseURL,
		Host:                smtp.Host,
		Port:                port,
		Encryption:          encryption,
		Username:            smtp.Username,
		From:                smtp.From,
		PasswordSet:         a.SMTP.Password != "",
		SendHour:            notify.SendHour,
		Error:               errMsg,
		legalEditData:       a.legalEditData(),
	}
}

// handleNotifySettingsView is BENACHR-04's configuration surface: which
// messages go out, to whom, and over which mail server.
func (a *App) handleNotifySettingsView(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "notify.html", a.notifySettingsPageData(sess, ""))
}

// handleNotifySettingsSave stores both halves at once: what to send (the
// config file) and how to reach the mail server (the secrets file).
// They are separate files on purpose — BETRIEB-02 keeps credentials out
// of the functional configuration — but one form, because to an operator
// they are one setting.
func (a *App) handleNotifySettingsSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	enabled := r.PostFormValue("enabled") != ""
	operatorEmail := strings.TrimSpace(r.PostFormValue("operator_email"))
	if enabled && operatorEmail == "" {
		a.render(w, "notify.html", a.notifySettingsPageData(sess, "Ohne Betreiber-Adresse kann nichts versendet werden."))
		return
	}

	port := 0
	if raw := strings.TrimSpace(r.PostFormValue("port")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			a.render(w, "notify.html", a.notifySettingsPageData(sess, "Ungültiger Port."))
			return
		}
		port = parsed
	}

	encryption := r.PostFormValue("encryption")
	switch encryption {
	case "none", "starttls", "tls":
	default:
		a.render(w, "notify.html", a.notifySettingsPageData(sess, "Ungültige Verschlüsselung."))
		return
	}

	a.NotifyConfig.Enabled = enabled
	a.NotifyConfig.StartupNotification = r.PostFormValue("startup_notification") != ""
	a.NotifyConfig.OperatorEmail = operatorEmail
	a.NotifyConfig.BaseURL = strings.TrimSpace(r.PostFormValue("base_url"))

	a.SMTP.Host = strings.TrimSpace(r.PostFormValue("host"))
	a.SMTP.Port = port
	a.SMTP.Encryption = encryption
	a.SMTP.Username = strings.TrimSpace(r.PostFormValue("username"))
	a.SMTP.From = strings.TrimSpace(r.PostFormValue("from"))
	// An empty password field leaves the stored one alone, so saving an
	// unrelated change does not silently clear it.
	if pw := r.PostFormValue("password"); pw != "" {
		a.SMTP.Password = pw
	}

	if err := a.persistNotifySettings(); err != nil {
		a.render(w, "notify.html", a.notifySettingsPageData(sess, "Speichern fehlgeschlagen: "+err.Error()))
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "notification settings")
	http.Redirect(w, r, "/operator/notify", http.StatusSeeOther)
}

// handleNotifyTestMonthly, handleNotifyTestWeekly, and
// handleNotifyTestStartup are the "Test" buttons on the Benachrichtigungen
// page (UI-05): one per mail kind, each sending the real message with the
// real recipients, right now, regardless of the schedule that normally
// gates it — the same manual-override idea as the Collector page's "Push
// jetzt auslösen". Mail configuration is the kind of thing that looks
// right and silently does not work; forcing an actual send is the only way
// to know that it does. It also makes this the way to hand-deliver a
// month or week whose scheduled send was missed entirely (device off
// during its one-hour window) — see notify/schedule.go on why that window
// is narrow rather than caught up automatically, and weekly.go's
// monthlyReminderConfirmed for how a missed month surfaces as a hint in
// the weekly mail instead.
//
// All three still record to the audit log exactly as a scheduled run
// would — that log is only ever a record here, never something a send
// decision reads back (see notify/monthly.go), so recording is safe
// regardless of how often this is clicked.

// handleNotifyTestMonthly sends this month's tenant reminder (BENACHR-01)
// to every current access grant with an email on file, plus the usual
// operator summary — the same content the scheduled run sends, just not
// waiting for the 1st of the month.
func (a *App) handleNotifyTestMonthly(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	data := a.notifySettingsPageData(sess, "")
	md, unlocked := a.Vault.Get()
	if !unlocked {
		data.TestFailed = true
		data.TestResult = "Stammdaten sind gesperrt — ohne sie ist nicht bekannt, wer eine Mieter-Adresse hinterlegt hat."
		a.render(w, "notify.html", data)
		return
	}

	today := a.today()
	month := string(today)[:7]
	mailer := a.mailer()
	result, err := notify.SendMonthlyReminders(mailer, a.Audit, md, month, today, a.NotifyConfig.BaseURL, a.NotifyConfig.OperatorEmail)
	if err != nil {
		data.TestFailed = true
		data.TestResult = "Versand fehlgeschlagen: " + err.Error()
		a.render(w, "notify.html", data)
		return
	}

	a.audit(access.EventNotificationSent, sess.AuditActor(), fmt.Sprintf("manual monthly reminder run: %d versendet, %d fehlgeschlagen", result.Sent, len(result.Failed)))
	if len(result.Failed) > 0 {
		data.TestFailed = true
		data.TestResult = fmt.Sprintf("%d versendet, %d fehlgeschlagen (Wohnungen: %s).", result.Sent, len(result.Failed), strings.Join(result.Failed, ", "))
	} else if result.Sent == 0 {
		data.TestResult = "Keine Wohnung mit hinterlegter E-Mail-Adresse und laufendem Zugang gefunden — nichts versendet."
	} else {
		data.TestResult = fmt.Sprintf("Monatserinnerung an %d Wohnung(en) versendet.", result.Sent)
	}
	a.render(w, "notify.html", data)
}

// handleNotifyTestWeekly sends the operator's own weekly status now
// (BENACHR-03): silent meters, the locked state, or — the common case —
// confirmation that nothing needs attention.
func (a *App) handleNotifyTestWeekly(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	data := a.notifySettingsPageData(sess, "")
	if a.NotifyConfig.OperatorEmail == "" {
		data.TestFailed = true
		data.TestResult = "Keine Betreiber-Adresse hinterlegt."
		a.render(w, "notify.html", data)
		return
	}

	mailer := a.mailer()
	sent, err := notify.SendWeeklyStatus(mailer, a.Audit, a.Store, a.Vault, a.now(), silentThresholdDays, a.NotifyConfig.OperatorEmail, true)
	if err != nil {
		data.TestFailed = true
		data.TestResult = "Versand fehlgeschlagen: " + err.Error()
		a.render(w, "notify.html", data)
		return
	}

	a.audit(access.EventNotificationSent, sess.AuditActor(), "manual weekly status run")
	if sent {
		data.TestResult = "Wochenstatus an " + a.NotifyConfig.OperatorEmail + " versendet."
	} else {
		// Only reachable if OperatorEmail became empty between the check
		// above and here, which requireCSRF's single-threaded handling of
		// this request makes impossible in practice — kept as a clear
		// message rather than silently rendering an empty result.
		data.TestFailed = true
		data.TestResult = "Nicht versendet."
	}
	a.render(w, "notify.html", data)
}

// handleNotifyTestStartup sends the "system is running again" notice now,
// regardless of whether the startup-notification toggle is currently on —
// the toggle governs whether it fires automatically on every real restart,
// not whether the operator can ask for one by hand.
func (a *App) handleNotifyTestStartup(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	data := a.notifySettingsPageData(sess, "")
	if a.NotifyConfig.OperatorEmail == "" {
		data.TestFailed = true
		data.TestResult = "Keine Betreiber-Adresse hinterlegt."
		a.render(w, "notify.html", data)
		return
	}

	mailer := a.mailer()
	if err := notify.SendStartupNotification(mailer, a.Audit, true, a.NotifyConfig.OperatorEmail, a.NotifyConfig.BaseURL); err != nil {
		data.TestFailed = true
		data.TestResult = "Versand fehlgeschlagen: " + err.Error()
		a.render(w, "notify.html", data)
		return
	}

	a.audit(access.EventNotificationSent, sess.AuditActor(), "manual startup notification")
	data.TestResult = "Start-Nachricht an " + a.NotifyConfig.OperatorEmail + " versendet."
	a.render(w, "notify.html", data)
}

// persistNotifySettings writes both halves back to where they belong: the
// message settings to the config file, the mail-server credentials to the
// secrets file (BETRIEB-02 keeps the two apart). Either path being unset
// means that half applies to the running process only.
func (a *App) persistNotifySettings() error {
	if a.ConfigPath != "" {
		cfg, err := config.LoadOrEmpty(a.ConfigPath)
		if err != nil {
			return err
		}
		cfg.Notify = a.NotifyConfig
		if err := config.Save(a.ConfigPath, cfg); err != nil {
			return err
		}
	}
	if a.SecretsPath != "" {
		secrets, err := config.LoadSecretsOrEmpty(a.SecretsPath)
		if err != nil {
			return err
		}
		secrets.SMTP = a.SMTP
		if err := config.SaveSecrets(a.SecretsPath, secrets); err != nil {
			return err
		}
	}
	return nil
}
