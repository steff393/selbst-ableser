package webapp

import (
	"time"

	"selbst-ableser/internal/telegram"
)

// collectorRow is one line of the Collector page's table — everything
// already formatted, since a template can neither compare timestamps nor
// decide what "silent" means.
//
// Every time is an absolute timestamp, never "vor 12 Sekunden": the page
// is static once rendered, so an age would silently go stale in front of
// the reader while a clock time stays true no matter how long the tab
// stays open.
type collectorRow struct {
	Name string

	// StatusClass/StatusText are the same badge vocabulary the rest of the
	// operator area uses (badge-success/-warning/-danger).
	StatusClass string
	StatusText  string

	Version string

	ReceiverText  string
	ReceiverClass string

	BackupText  string
	BackupClass string

	StartedAt string
}

// collectorRows renders the reporting collectors for display, applying the
// same tolerance the single-collector badge uses: a collector is late once
// it has missed its poll by half an interval again.
func (a *App) collectorRows() []collectorRow {
	tolerance := a.collectorTolerance()
	now := a.now()

	states := a.knownCollectors()
	rows := make([]collectorRow, 0, len(states))
	for _, s := range states {
		row := collectorRow{
			Name:      s.Name,
			Version:   s.Version,
			StartedAt: a.formatCollectorTime(s.StartedAt),
		}
		if row.Version == "" {
			row.Version = "—"
		}

		// The label names the last contact rather than a duration: it is a
		// heartbeat, and a heartbeat has a time, not an age.
		if now.Sub(s.LastSeen) <= tolerance {
			row.StatusClass = "badge-success"
			row.StatusText = "gemeldet " + a.formatCollectorTime(s.LastSeen)
		} else {
			row.StatusClass = "badge-danger"
			row.StatusText = "zuletzt " + a.formatCollectorTime(s.LastSeen)
		}

		switch {
		case s.ReceiverConnected && s.ReceiverPort != "":
			row.ReceiverClass = "badge-success"
			row.ReceiverText = s.ReceiverPort
		case s.ReceiverConnected:
			row.ReceiverClass = "badge-success"
			row.ReceiverText = "verbunden"
		default:
			row.ReceiverClass = "badge-danger"
			row.ReceiverText = "nicht verbunden"
		}

		switch {
		case s.BackupConnected && s.BackupPath != "":
			row.BackupClass = "badge-success"
			row.BackupText = s.BackupPath
		case s.BackupConnected:
			row.BackupClass = "badge-success"
			row.BackupText = "gesteckt"
		default:
			// Not an error: the daily backup falls back to a file inside
			// the collector's own directory when no stick is attached
			// (DATEN-06), so this is information, not a fault.
			row.BackupClass = "badge-neutral"
			row.BackupText = "keiner"
		}

		rows = append(rows, row)
	}
	return rows
}

// collectorTolerance is how long after its due poll a collector still
// counts as current: one and a half intervals, which absorbs a single
// missed or retried poll without the display flapping.
func (a *App) collectorTolerance() time.Duration {
	pollInterval := time.Duration(defaultInt(a.CollectorConfig.ConfigPollSeconds, 60)) * time.Second
	return pollInterval + pollInterval/2
}

// collectorsReporting counts how many of the known collectors are
// currently on time — the numerator of the overview's "N von M gemeldet".
func (a *App) collectorsReporting() (reporting, known int) {
	tolerance := a.collectorTolerance()
	now := a.now()
	states := a.knownCollectors()
	for _, s := range states {
		if now.Sub(s.LastSeen) <= tolerance {
			reporting++
		}
	}
	return reporting, len(states)
}

// formatCollectorTime prints a wall-clock time: today's as a bare clock
// time, anything older with its date, so a row that has not moved since
// last week cannot be misread as this afternoon.
func (a *App) formatCollectorTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	local := t.In(telegram.Local)
	if local.Format("2006-01-02") == a.now().In(telegram.Local).Format("2006-01-02") {
		return local.Format("15:04:05")
	}
	return local.Format("02.01.2006 15:04:05")
}
