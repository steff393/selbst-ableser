// Package buildinfo reports which build of this program is running.
//
// Knowing that is a precondition for the update path this project uses
// (BETRIEB-05): a release is a replaced binary plus a service restart,
// with no update mechanism inside the program itself. Without a version
// visible from the outside, an operator has no way to confirm the swap
// actually took effect — which matters most in the deployment form where
// collector and evaluator sit on different machines and only one of them
// was updated.
package buildinfo

import "runtime/debug"

// release is set at link time for a tagged build:
//
//	go build -ldflags "-X selbst-ableser/collector/internal/buildinfo.release=0.1.0"
//
// Left empty, Version falls back to the revision the Go toolchain stamps
// into any binary built from a work tree, rather than inventing a number
// that would claim more than it knows.
var release string

// Version identifies this build: a release name when one was linked in,
// otherwise a development marker carrying the source revision and whether
// the tree had uncommitted changes at build time. It returns "" when
// neither is available (a `go run`, which stamps no revision at all) —
// callers decide how to present that, since this package has no business
// producing user-facing wording.
func Version() string {
	if release != "" {
		return release
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	var revision string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}

	v := "devel-" + revision
	if modified {
		// An uncommitted build is not reproducible from the revision alone,
		// so say so rather than let it pass for that exact commit.
		v += "+dirty"
	}
	return v
}
