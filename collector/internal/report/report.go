// Package report sends telegram entries to the evaluator's single,
// unified reporting endpoint. What the evaluator does with a given entry
// — refresh the live view, or also commit it durably — is its own
// decision based on the entry's day, not something the caller here
// chooses; see cmd/saCollector for how often this is called and with what
// (recent activity vs. a full closed day).
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"selbst-ableser/collector/internal/store"
)

// Result is what the evaluator reports back for one POST.
type Result struct {
	Accepted  int
	Conflicts int
}

// Send posts entries to baseURL+"/collector/report", authenticated the
// same way as Fetch in the settings package. An empty entries slice is a
// no-op that still returns cleanly, so callers can invoke Send freely.
//
// final tells the evaluator whether to treat every entry in this call as
// the day's finished record, worth committing durably, or only as a
// live-view refresh — see cmd/saCollector for which loop sets which.
func Send(ctx context.Context, client *http.Client, baseURL, secret string, entries []store.Entry, final bool) (Result, error) {
	if len(entries) == 0 {
		return Result{}, nil
	}

	body, err := json.Marshal(struct {
		Final   bool          `json:"final"`
		Entries []store.Entry `json:"entries"`
	}{Final: final, Entries: entries})
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/collector/report", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		// http.Client wraps every failure in a *url.Error that repeats the
		// full request URL — the caller already says what this request was
		// for (see cmd/saCollector's "live report"/"daily report" log
		// lines), so unwrap down to the actual cause instead of repeating it.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return Result{}, uerr.Err
		}
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("evaluator returned %s", resp.Status)
	}

	var result struct {
		Accepted  int `json:"accepted"`
		Conflicts int `json:"conflicts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Result{}, err
	}
	return Result{Accepted: result.Accepted, Conflicts: result.Conflicts}, nil
}

// NewClient builds an *http.Client with a sensible timeout for report/
// settings calls — small payloads on a local or otherwise fast network,
// so a generous-looking timeout still fails fast on a genuine outage.
//
// This collector identifies itself in the body of its settings poll (see
// settings.Report), not in a header: what the evaluator shows is a whole
// status block — name, receiver, backup medium — and a version alone in
// a User-Agent would have been a second, redundant channel for one field
// of it.
func NewClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}
