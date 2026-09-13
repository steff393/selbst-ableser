package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

// realTelegram is an actual received wM-Bus telegram (the same one the
// evaluator's live-view tests use). L=0x38, C=0x44 — the shape
// findTelegram is meant to lock onto.
const realTelegram = "384493445805744036087ae11820252f2f0b6e4500004b6e000000426c3f3ccb086e000000c2086c5f312f2f2f2f2f326c5e31046d12144132"

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	return b
}

// TestFindTelegramInNoisyCapture is the case the whole tool turns on: a
// capture at the right baud rate, with the frame sitting somewhere in the
// middle of whatever else the port delivered. If this works, the report
// answers "which baud rate, and what does a frame look like" on its own.
func TestFindTelegramInNoisyCapture(t *testing.T) {
	tele := mustHex(t, realTelegram)
	capture := append([]byte{0x11, 0x22, 0x33}, tele...)
	capture = append(capture, 0xAB, 0xCD)

	var report strings.Builder
	if !findTelegram(&report, capture) {
		t.Fatalf("expected to find the telegram, got report:\n%s", report.String())
	}
	out := report.String()
	if !strings.Contains(out, "Offset 3") {
		t.Errorf("expected the offset of the telegram, got:\n%s", out)
	}
	if !strings.Contains(out, "40740558") {
		t.Errorf("expected the decoded meter number, got:\n%s", out)
	}
	// The bytes that follow are what tells us whether the device appends
	// something of its own (an RSSI byte, say) — so they have to be in
	// the report, not just the frame itself.
	if !strings.Contains(out, "AB CD") {
		t.Errorf("expected the trailing bytes after the frame, got:\n%s", out)
	}
}

func TestFindTelegramReportsNothingForNoise(t *testing.T) {
	noise := make([]byte, 512)
	for i := range noise {
		noise[i] = byte(i % 7) // never a plausible length/C-field pairing
	}
	var report strings.Builder
	if findTelegram(&report, noise) {
		t.Errorf("noise must not be reported as a telegram:\n%s", report.String())
	}
}

func TestFindTelegramStopsAfterAFewExamples(t *testing.T) {
	tele := mustHex(t, realTelegram)
	var capture []byte
	for i := 0; i < maxTelegramExamples+3; i++ {
		capture = append(capture, tele...)
	}
	var report strings.Builder
	findTelegram(&report, capture)
	if got := strings.Count(report.String(), "gültiges wM-Bus-Telegramm"); got != maxTelegramExamples {
		t.Errorf("reported %d examples, want the cap of %d", got, maxTelegramExamples)
	}
}

// TestChainLengthDistinguishesStrides is the other half of the analysis:
// with no valid telegram to anchor on (an encrypted-only capture that
// ParseWMBus rejects, say), the framing can still be guessed from whether
// length bytes chain up — and whether they do so with or without an extra
// trailing byte tells us if the device appends an RSSI.
func TestChainLengthDistinguishesStrides(t *testing.T) {
	// Three frames of 12 payload bytes each, no trailing byte.
	var plain []byte
	for i := 0; i < 3; i++ {
		plain = append(plain, 12)
		plain = append(plain, make([]byte, 12)...)
	}
	if n, ok := chainLength(plain, 0); !ok || n != 3 {
		t.Errorf("L+1 stride on a clean chain = (%d, %v), want (3, true)", n, ok)
	}

	// The same, but each frame followed by one extra byte.
	var withExtra []byte
	for i := 0; i < 3; i++ {
		withExtra = append(withExtra, 12)
		withExtra = append(withExtra, make([]byte, 12)...)
		withExtra = append(withExtra, 0x9C)
	}
	if n, ok := chainLength(withExtra, 1); !ok || n != 3 {
		t.Errorf("L+2 stride on a chain with a trailing byte = (%d, %v), want (3, true)", n, ok)
	}
	// And the wrong assumption must not look convincing on that same data.
	if n, ok := chainLength(withExtra, 0); ok && n >= 3 {
		t.Errorf("L+1 stride should not chain cleanly through trailing bytes, got %d frames", n)
	}
}

func TestIsRadiocraftsPrompt(t *testing.T) {
	if !isRadiocraftsPrompt([]byte{'>'}) {
		t.Error("a bare prompt should be recognized")
	}
	// A module mid-transmission can put frame bytes ahead of the prompt.
	if !isRadiocraftsPrompt([]byte{0x38, 0x44, '>'}) {
		t.Error("a prompt after other bytes should still be recognized")
	}
	if isRadiocraftsPrompt([]byte{0x38, 0x44, 0x93}) {
		t.Error("binary frame data must not be mistaken for a prompt")
	}
	if isRadiocraftsPrompt(nil) {
		t.Error("no answer is not a prompt")
	}
}

func TestParseBauds(t *testing.T) {
	got, err := parseBauds("19200, 9600 ,115200")
	if err != nil {
		t.Fatalf("parseBauds: %v", err)
	}
	want := []int{19200, 9600, 115200}
	if len(got) != len(want) {
		t.Fatalf("parseBauds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseBauds[%d] = %d, want %d — the order is the order they get tried in", i, got[i], want[i])
		}
	}

	for _, bad := range []string{"", "0", "-1", "abc"} {
		if _, err := parseBauds(bad); err == nil {
			t.Errorf("parseBauds(%q) should fail", bad)
		}
	}
}
