package webapp

import (
	"encoding/json"
	"net/http"
)

// statusResponse is deliberately narrow (BETRIEB-08): whether the process
// is working, the locked state, and when the archive last received
// anything — no meter numbers, no consumption values, no configuration
// details, so this endpoint is not itself something worth attacking.
type statusResponse struct {
	Ready            bool   `json:"ready"`
	Locked           bool   `json:"locked"`
	ArchiveEntries   int    `json:"archive_entries"`
	LastArchiveEntry string `json:"last_archive_entry,omitempty"`
}

// handleStatus is BETRIEB-08: a machine-readable status report, reachable
// without a session (an external monitoring check has no login of its
// own to offer).
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.Stats(a.today())
	resp := statusResponse{
		Ready:  err == nil,
		Locked: a.Vault.Locked(),
	}
	if err == nil {
		resp.ArchiveEntries = stats.TotalEntries
		resp.LastArchiveEntry = string(stats.LatestDay)
	}

	w.Header().Set("Content-Type", "application/json")
	if !resp.Ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		a.logger().Error("encoding status response", "err", err)
	}
}
