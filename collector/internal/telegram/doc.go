// Package telegram defines the shared wire format types for radio telegrams
// (frame envelope, checksums) used by the collector.
//
// This is a deliberate copy of the evaluator module's own internal/telegram,
// not an import: the collector lives in its own Go module specifically so
// Go's internal/ visibility rule makes the evaluator's internal/crypto,
// internal/masterdata, internal/billing, and internal/access packages
// impossible to import from here, not just discouraged by convention.
// Depending on the evaluator module for this one small, non-sensitive,
// rarely-changing package would reintroduce exactly the coupling that
// split is meant to avoid, so it is duplicated instead.
package telegram
