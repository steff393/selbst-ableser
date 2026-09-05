package collector_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoEvaluatorModuleDependency documents and double-checks ARCH-01 for
// this module: the collector must never depend on the evaluator module at
// all, so it structurally cannot reach internal/crypto, internal/
// masterdata, internal/billing, or internal/access — those packages live
// under a different module's internal/ tree, which Go's own visibility
// rule already makes unimportable from here. This test is defense in
// depth (e.g. against a stray "replace" directive quietly reintroducing
// the dependency), not the primary enforcement mechanism.
func TestNoEvaluatorModuleDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "selbst-ableser/collector/...").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	for _, dep := range deps {
		if strings.HasPrefix(dep, "selbst-ableser/internal/") || dep == "selbst-ableser" {
			t.Errorf("collector module depends on the evaluator module (%s) — this must never happen", dep)
		}
	}
}
