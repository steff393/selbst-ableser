package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"selbst-ableser/internal/config"
)

// TestPushDisabledBehindProxy covers K1: a reverse proxy makes a
// collector request's loopback-looking address meaningless, so an empty
// secret in that case means the push endpoint is disabled — never a
// reason to refuse to start (validCollectorAuth enforces the actual
// rejection independently) — while the zero-config single-machine
// default (no proxy, no secret) is never flagged as disabled at all.
func TestPushDisabledBehindProxy(t *testing.T) {
	if pushDisabledBehindProxy(false, "") {
		t.Error("no proxy, no secret: the zero-config default must not be reported as disabled")
	}
	if pushDisabledBehindProxy(true, "a-real-secret") {
		t.Error("proxy with a secret configured: must not be reported as disabled")
	}
	if !pushDisabledBehindProxy(true, "") {
		t.Error("proxy without a secret: push must be reported as disabled")
	}
	if pushDisabledBehindProxy(false, "a-real-secret") {
		t.Error("no proxy but a secret set anyway: must not be reported as disabled")
	}
}

// TestIsYesOrEmpty: a bare Enter confirms, since the prompt it answers
// only appears because the operator named this directory in the first
// place; anything not recognizably "yes" aborts.
func TestIsYesOrEmpty(t *testing.T) {
	for _, s := range []string{"", "  ", "j", "J", "ja", "Ja", "JA", "y", "Y", "yes", "Yes", "  j  "} {
		if !isYesOrEmpty(s) {
			t.Errorf("isYesOrEmpty(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"n", "nein", "no", "N", "vielleicht", "j?"} {
		if isYesOrEmpty(s) {
			t.Errorf("isYesOrEmpty(%q) = true, want false", s)
		}
	}
}

// TestTakeDirArg covers the one shape that has to be rejected rather than
// quietly misread: a flag where the directory belongs, which Go's flag
// package would otherwise leave unparsed (see takeDirArg).
func TestTakeDirArg(t *testing.T) {
	dir, rest, err := takeDirArg([]string{"data", "-addr", ":8227"}, "evaluator")
	if err != nil {
		t.Fatalf("takeDirArg: %v", err)
	}
	if dir != "data" {
		t.Errorf("dir = %q, want data", dir)
	}
	if len(rest) != 2 || rest[0] != "-addr" {
		t.Errorf("rest = %v, want [-addr :8227]", rest)
	}

	if _, _, err := takeDirArg(nil, "evaluator"); err == nil {
		t.Error("no arguments at all must be an error, not an implicit default directory")
	}
	if _, _, err := takeDirArg([]string{"-addr", ":8227"}, "evaluator"); err == nil {
		t.Error("a flag in the directory's place must be refused, not taken as a directory name")
	}
}

// TestSetUpInstallationNeedsATerminal is the one invariant the folded-in
// setup must never lose: without a terminal — a systemd unit, a pipe —
// it refuses instead of generating an operator password that would be
// shown once, into a log nobody is reading at that moment.
func TestSetUpInstallationNeedsATerminal(t *testing.T) {
	original := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = original })

	dir := filepath.Join(t.TempDir(), "new-installation")

	stdinIsTerminal = func() bool { return false }
	err := setUpInstallation(dir, bufio.NewReader(strings.NewReader("j\n")))
	if err == nil {
		t.Fatal("without a terminal, setting up an installation must be refused")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("the refused run must not have created anything")
	}

	stdinIsTerminal = func() bool { return true }
	if err := setUpInstallation(dir, bufio.NewReader(strings.NewReader("j\n"))); err != nil {
		t.Fatalf("with a terminal and a yes: %v", err)
	}
	if _, err := os.Stat(config.MasterDataPath(dir)); err != nil {
		t.Errorf("master data was not created: %v", err)
	}
}

// TestSetUpInstallationAborts: a "no" leaves the directory untouched.
func TestSetUpInstallationAborts(t *testing.T) {
	original := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = original })
	stdinIsTerminal = func() bool { return true }

	dir := filepath.Join(t.TempDir(), "declined")
	if err := setUpInstallation(dir, bufio.NewReader(strings.NewReader("n\n"))); err == nil {
		t.Fatal("a declined setup must return an error rather than starting an empty installation")
	}
	if _, err := os.Stat(config.MasterDataPath(dir)); !os.IsNotExist(err) {
		t.Error("a declined setup must not create master data")
	}
}

// TestListenURLs: the bind address and the address somebody types are not
// the same thing, and the wildcard forms are exactly where they differ.
func TestListenURLs(t *testing.T) {
	fixedIPs := func() []string { return []string{"192.168.1.42"} }

	cases := []struct {
		name string
		addr string
		want []string
	}{
		{"port only means every interface", ":8226",
			[]string{"http://localhost:8226", "http://192.168.1.42:8226"}},
		{"explicit wildcard", "0.0.0.0:8226",
			[]string{"http://localhost:8226", "http://192.168.1.42:8226"}},
		{"loopback stays loopback", "127.0.0.1:8226",
			[]string{"http://localhost:8226"}},
		{"a concrete host answers only there", "10.0.0.5:9000",
			[]string{"http://10.0.0.5:9000"}},
		{"non-default port is carried through", ":8227",
			[]string{"http://localhost:8227", "http://192.168.1.42:8227"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := listenURLs(c.addr, fixedIPs)
			if len(got) != len(c.want) {
				t.Fatalf("listenURLs(%q) = %v, want %v", c.addr, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("listenURLs(%q)[%d] = %q, want %q", c.addr, i, got[i], c.want[i])
				}
			}
		})
	}

	// A bind address without a port is not something to guess at.
	if got := listenURLs("nonsense", fixedIPs); got != nil {
		t.Errorf("listenURLs on an unparseable address = %v, want nil", got)
	}
}

func TestReadLine(t *testing.T) {
	got, err := readLine(bufio.NewReader(strings.NewReader("  hello world  \nnext line\n")))
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got != "hello world" {
		t.Errorf("readLine = %q, want %q (trimmed)", got, "hello world")
	}

	// A prompt with no trailing newline (e.g. piped input ending right
	// after the answer) is a valid empty-or-not answer, not an error.
	got, err = readLine(bufio.NewReader(strings.NewReader("no newline here")))
	if err != nil {
		t.Fatalf("readLine at EOF: %v", err)
	}
	if got != "no newline here" {
		t.Errorf("readLine at EOF = %q, want %q", got, "no newline here")
	}
}
