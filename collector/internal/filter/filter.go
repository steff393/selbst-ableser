// Package filter discards telegrams matching operator-configured rules
// before they ever reach the buffer, the console output, or a report to
// the evaluator — for meters that alternate between telegram formats
// where only one is evaluable.
package filter

import (
	"strings"

	"selbst-ableser/collector/internal/settings"
)

// AnyMeter matches every meter in a FilterRule.
const AnyMeter = "*"

// Filter applies a set of settings.FilterRule.
type Filter struct {
	rules []settings.FilterRule
}

// New builds a Filter from a set of rules.
func New(rules []settings.FilterRule) *Filter {
	return &Filter{rules: rules}
}

// Keep reports whether a telegram should be kept, given its meter ID and
// raw hex encoding. A nil *Filter keeps everything.
//
// The comparison is case-insensitive on both sides: an operator-entered
// BlockedPrefixes value and rawHex are each lowercased before comparing,
// so this does not depend on the caller happening to supply lowercase hex
// (hex.EncodeToString does, but nothing enforces that at this boundary).
func (f *Filter) Keep(meterID, rawHex string) bool {
	if f == nil {
		return true
	}
	rawHex = strings.ToLower(rawHex)
	for _, r := range f.rules {
		if r.MeterID != AnyMeter && r.MeterID != meterID {
			continue
		}
		for _, prefix := range r.BlockedPrefixes {
			if strings.HasPrefix(rawHex, strings.ToLower(prefix)) {
				return false
			}
		}
	}
	return true
}
