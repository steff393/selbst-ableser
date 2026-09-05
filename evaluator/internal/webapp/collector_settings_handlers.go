package webapp

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/config"
	"selbst-ableser/internal/livepush"
)

// This file is the operator's control surface for everything a
// saCollector fetches over GET /collector/config, plus the shared secret
// both collector-facing endpoints authenticate with (ARCH-04) — the
// evaluator's counterpart to the old collector's own local settings page,
// now centralized here since a saCollector has no local config file to
// edit at all.

type collectorFilterRuleView struct {
	Index           int
	MeterID         string
	BlockedPrefixes string
}

type collectorSettingsPageData struct {
	Base
	FilterRules []collectorFilterRuleView
	SecretSet   bool
	Secret      string
	Error       string

	// Collectors is one row per reporting collector (see collectorRow).
	// Empty until the first poll arrives, which takes at most one poll
	// interval after either side starts.
	Collectors []collectorRow

	// Advanced tuning values, shown in a deliberately unobtrusive section
	// (see collector.html) — values an operator normally never touches.
	ReportIntervalSeconds int
	DailyPushHour         int
	IdleReconnectSeconds  int
	ConfigPollSeconds     int
}

func (a *App) handleCollectorSettingsView(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "collector.html", a.collectorSettingsPageData(sess, ""))
}

func (a *App) collectorSettingsPageData(sess *access.Session, errMsg string) collectorSettingsPageData {
	rules := make([]collectorFilterRuleView, len(a.CollectorConfig.FilterRules))
	for i, r := range a.CollectorConfig.FilterRules {
		rules[i] = collectorFilterRuleView{Index: i, MeterID: r.MeterID, BlockedPrefixes: strings.Join(r.BlockedPrefixes, ", ")}
	}
	return collectorSettingsPageData{
		Base:                  a.base("Collector-Einstellungen", sess),
		FilterRules:           rules,
		SecretSet:             a.PushSecret != "",
		Secret:                a.PushSecret,
		Error:                 errMsg,
		Collectors:            a.collectorRows(),
		ReportIntervalSeconds: defaultInt(a.CollectorConfig.ReportIntervalSeconds, 1),
		DailyPushHour:         defaultInt(a.CollectorConfig.DailyPushHour, 3),
		IdleReconnectSeconds:  defaultInt(a.CollectorConfig.IdleReconnectSeconds, 120),
		ConfigPollSeconds:     defaultInt(a.CollectorConfig.ConfigPollSeconds, 60),
	}
}

// defaultInt is v unless it's the zero value, in which case it's
// fallback — used so the advanced-settings form shows the collector's
// own built-in default pre-filled rather than a bare 0 the first time it
// is ever opened, before anything has been explicitly saved.
func defaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// setLiveView switches the live-view push on for one window, or off, and
// persists it. Called from handleLiveViewToggle — the Live-Ansicht page's
// own control, the only place this is exposed now that the Collector
// tab's identical checkbox has moved there.
func (a *App) setLiveView(on bool) error {
	if on {
		a.CollectorConfig = a.CollectorConfig.WithLiveViewUntil(a.now().Add(config.LiveViewWindow))
		if a.CollectorConfig.ReportIntervalSeconds <= 0 {
			a.CollectorConfig.ReportIntervalSeconds = 5 // first-ever activation: seed the advanced value too
		}
	} else {
		a.CollectorConfig = a.CollectorConfig.WithLiveViewUntil(time.Time{})
	}
	return a.persistCollectorConfig()
}

// handleCollectorAdvancedSave is the deliberately unobtrusive section for
// values an operator normally never has a reason to touch — see
// collector.html for why it's kept visually secondary.
func (a *App) handleCollectorAdvancedSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	reportInterval, err1 := strconv.Atoi(r.PostFormValue("report_interval_seconds"))
	dailyHour, err2 := strconv.Atoi(r.PostFormValue("daily_push_hour"))
	idleReconnect, err3 := strconv.Atoi(r.PostFormValue("idle_reconnect_seconds"))
	configPoll, err4 := strconv.Atoi(r.PostFormValue("config_poll_seconds"))

	switch {
	case err1 != nil || reportInterval < 1 || reportInterval > 300:
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "LiveView-Intervall muss zwischen 1 und 300 Sekunden liegen."))
		return
	case err2 != nil || dailyHour < 0 || dailyHour > 23:
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Stunde für den täglichen Push muss zwischen 0 und 23 liegen."))
		return
	case err3 != nil || idleReconnect < 10 || idleReconnect > 3600:
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Retry-Zeit muss zwischen 10 und 3600 Sekunden liegen."))
		return
	case err4 != nil || configPoll < 5 || configPoll > 3600:
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Abfrage-Intervall muss zwischen 5 und 3600 Sekunden liegen."))
		return
	}

	a.CollectorConfig.ReportIntervalSeconds = reportInterval
	a.CollectorConfig.DailyPushHour = dailyHour
	a.CollectorConfig.IdleReconnectSeconds = idleReconnect
	a.CollectorConfig.ConfigPollSeconds = configPoll
	if err := a.persistCollectorConfig(); err != nil {
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Speichern fehlgeschlagen."))
		return
	}
	http.Redirect(w, r, "/operator/collector", http.StatusSeeOther)
}

// handleCollectorTriggerPush is "Push jetzt auslösen", living on the
// Live-Ansicht page next to the live-view start/stop control — the two
// are both about "make a collector talk to me right now", so they belong
// together rather than on the settings-oriented Collector tab. It does
// not itself contact any collector (there is no channel to do that — a
// collector only ever polls this evaluator, never the other way around)
// but sets a flag every collector's next GET /collector/config will see
// and act on, at most ConfigPollSeconds later (see
// config.Collector.TriggerPush). Never persisted — a restart before it's
// picked up simply drops it.
func (a *App) handleCollectorTriggerPush(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a.CollectorConfig.TriggerPush = true
	http.Redirect(w, r, "/operator/live", http.StatusSeeOther)
}

func (a *App) handleCollectorFilterRuleAdd(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	meterID := r.PostFormValue("meter_id")
	if meterID == "" {
		meterID = "*"
	}
	var prefixes []string
	for _, p := range strings.Split(r.PostFormValue("blocked_prefixes"), ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			prefixes = append(prefixes, p)
		}
	}
	if len(prefixes) > 0 {
		a.CollectorConfig.FilterRules = append(a.CollectorConfig.FilterRules, config.FilterRuleConfig{MeterID: meterID, BlockedPrefixes: prefixes})
		if err := a.persistCollectorConfig(); err != nil {
			a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Speichern fehlgeschlagen."))
			return
		}
	}
	http.Redirect(w, r, "/operator/collector", http.StatusSeeOther)
}

func (a *App) handleCollectorFilterRuleRemove(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if idx, err := strconv.Atoi(r.PostFormValue("index")); err == nil && idx >= 0 && idx < len(a.CollectorConfig.FilterRules) {
		a.CollectorConfig.FilterRules = append(a.CollectorConfig.FilterRules[:idx], a.CollectorConfig.FilterRules[idx+1:]...)
		if err := a.persistCollectorConfig(); err != nil {
			a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Speichern fehlgeschlagen."))
			return
		}
	}
	http.Redirect(w, r, "/operator/collector", http.StatusSeeOther)
}

func (a *App) handleCollectorSecretSave(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a.setCollectorSecret(r.PostFormValue("secret"))
	if err := a.persistPushSecret(); err != nil {
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Speichern fehlgeschlagen."))
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "changed collector transfer secret")
	http.Redirect(w, r, "/operator/collector", http.StatusSeeOther)
}

func (a *App) handleCollectorSecretGenerate(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !requireCSRF(sess, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	generated, err := access.GeneratePassword()
	if err != nil {
		a.renderTechnicalError(w, sess, err)
		return
	}
	a.setCollectorSecret(generated)
	if err := a.persistPushSecret(); err != nil {
		a.render(w, "collector.html", a.collectorSettingsPageData(sess, "Speichern fehlgeschlagen."))
		return
	}
	a.audit(access.EventMasterDataChange, sess.AuditActor(), "generated a new collector transfer secret")
	http.Redirect(w, r, "/operator/collector", http.StatusSeeOther)
}

// setCollectorSecret updates PushSecret and, the first time it ever
// becomes non-empty, brings LiveBuffer up if it somehow isn't already
// (it is always initialized at startup in practice — see cmd/saEvaluator
// — this is just a defensive fallback so the live view can never be
// stranded without a buffer).
func (a *App) setCollectorSecret(secret string) {
	a.PushSecret = secret
	if a.LiveBuffer == nil {
		a.LiveBuffer = livepush.NewBuffer(200)
	}
}

// persistCollectorConfig writes CollectorConfig back to ConfigPath's
// collector section, preserving everything else already in the file
// (the evaluator's own section, notify, logging) — the same
// read-modify-write pattern saCollector's own settings package no
// longer needs on its side, now living here instead.
func (a *App) persistCollectorConfig() error {
	if a.ConfigPath == "" {
		return nil
	}
	cfg, err := config.LoadOrEmpty(a.ConfigPath)
	if err != nil {
		return err
	}
	cfg.Collector = a.CollectorConfig
	return config.Save(a.ConfigPath, cfg)
}

// persistPushSecret writes PushSecret back to SecretsPath, preserving any
// SMTP credentials already stored there.
func (a *App) persistPushSecret() error {
	if a.SecretsPath == "" {
		return nil
	}
	secrets, err := config.LoadSecretsOrEmpty(a.SecretsPath)
	if err != nil {
		return err
	}
	secrets.PushSecret = a.PushSecret
	return config.SaveSecrets(a.SecretsPath, secrets)
}

// liveViewState is the live view as the interface should describe it:
// whether it is running right now, and until when. Shared by every page
// that shows or changes it, so they cannot describe it differently.
func (a *App) liveViewState() (active bool, until string) {
	on, deadline := a.CollectorConfig.LiveViewActiveAt(a.now())
	if !on {
		return false, ""
	}
	return true, deadline.Format("15:04")
}
