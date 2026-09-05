package main

import (
	"os"
	"sync"
	"time"

	"selbst-ableser/collector/internal/removable"
	"selbst-ableser/collector/internal/settings"
)

// collectorStatus is what this collector tells the evaluator about itself
// on every settings poll (see settings.Report). All of it is knowledge the
// evaluator cannot obtain on its own: it sees telegrams arrive or not, and
// from that alone cannot distinguish a quiet building from an unplugged
// antenna, nor tell two collectors apart, nor notice that only one of the
// two machines got the new binary.
//
// Deliberately no consumption data and no meter identities — this carries
// facts about the machine, nothing about what it received.
type collectorStatus struct {
	name      string
	version   string
	startedAt time.Time

	mu sync.Mutex
	// Receiver state is event-driven: SerialSource reports connects and
	// disconnects as they happen (see withReceiverLogging), so this is
	// always current rather than polled.
	receiverIsConnected bool
	receiverPort        string
	receiverSince       time.Time
}

// newCollectorStatus fixes the values that cannot change while the process
// runs. An empty name falls back to the hostname, which is right often
// enough to be a useful default and wrong in exactly one case worth naming
// in the docs: two Raspberry Pis, both freshly imaged, both called
// "raspberrypi" — hence -name.
func newCollectorStatus(name, version string, startedAt time.Time) *collectorStatus {
	if name == "" {
		if host, err := os.Hostname(); err == nil {
			name = host
		}
	}
	if name == "" {
		name = "saCollector"
	}
	return &collectorStatus{name: name, version: version, startedAt: startedAt}
}

func (s *collectorStatus) receiverConnected(port string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Only the first connect of an unbroken run sets the timestamp, so
	// "connected since" answers "how long has reception been stable",
	// which is the operationally interesting question.
	if !s.receiverIsConnected {
		s.receiverSince = time.Now()
	}
	s.receiverIsConnected = true
	s.receiverPort = port
}

func (s *collectorStatus) receiverLost() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiverIsConnected = false
	s.receiverPort = ""
	s.receiverSince = time.Time{}
}

// snapshot assembles the report sent with the next poll. The backup medium
// is looked up here rather than tracked, because nothing notifies this
// process when a stick is plugged in — removable.AutoDetect reads the
// current mount table, which costs one syscall-level lookup a minute.
//
// With more than one removable medium attached, this reports the same one
// the daily backup would write to — the first (see removable.AutoDetect).
// The two must not disagree: a status that named a different stick than
// the backup uses would be worse than no status at all.
func (s *collectorStatus) snapshot() settings.Report {
	s.mu.Lock()
	report := settings.Report{
		Name:      s.name,
		Version:   s.version,
		StartedAt: s.startedAt,
		Receiver: settings.ReceiverStatus{
			Connected: s.receiverIsConnected,
			Port:      s.receiverPort,
			Since:     s.receiverSince,
		},
	}
	s.mu.Unlock()

	if mount, found, err := removable.AutoDetect(); err == nil && found {
		report.BackupMedium = settings.BackupMediumStatus{Connected: true, Path: mount}
	}
	return report
}
