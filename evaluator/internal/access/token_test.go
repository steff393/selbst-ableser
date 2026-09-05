package access

import (
	"strings"
	"testing"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

func mustDay(t *testing.T, s string) telegram.Day {
	t.Helper()
	d, err := telegram.ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func TestGenerateAccessTokenFormat(t *testing.T) {
	tok, err := GenerateAccessToken()
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	parts := strings.Split(tok, "-")
	if len(parts) != groupCount {
		t.Fatalf("token %q has %d groups, want %d", tok, len(parts), groupCount)
	}
	for _, p := range parts {
		if len(p) != groupSize {
			t.Errorf("group %q has length %d, want %d", p, len(p), groupSize)
		}
		for _, c := range p {
			if !strings.ContainsRune(tokenAlphabet, c) {
				t.Errorf("token %q contains a character outside the alphabet: %q", tok, c)
			}
		}
	}
}

func TestGenerateAccessTokenIsRandom(t *testing.T) {
	a, _ := GenerateAccessToken()
	b, _ := GenerateAccessToken()
	if a == b {
		t.Fatal("two generated tokens should not collide (statistically)")
	}
}

func TestVerifyAccessTokenFindsCurrentGrant(t *testing.T) {
	accesses := []masterdata.Access{
		{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDay(t, "2025-01-01")},
	}
	got, ok := VerifyAccessToken(accesses, "aaaa-bbbb-cccc", mustDay(t, "2025-06-01"))
	if !ok || got.UnitID != "u1" {
		t.Fatalf("VerifyAccessToken: ok=%v, got=%+v", ok, got)
	}
}

func TestVerifyAccessTokenIsFormatForgiving(t *testing.T) {
	accesses := []masterdata.Access{
		{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDay(t, "2025-01-01")},
	}
	_, ok := VerifyAccessToken(accesses, " aaaabbbbcccc ", mustDay(t, "2025-06-01"))
	if !ok {
		t.Fatal("expected a match despite missing dashes, extra spaces, and lowercase input")
	}
}

func TestVerifyAccessTokenRejectsUnknown(t *testing.T) {
	accesses := []masterdata.Access{
		{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDay(t, "2025-01-01")},
	}
	if _, ok := VerifyAccessToken(accesses, "ZZZZ-ZZZZ-ZZZZ", mustDay(t, "2025-06-01")); ok {
		t.Fatal("an unknown token should not match")
	}
}

func TestVerifyAccessTokenRejectsExpired(t *testing.T) {
	end := mustDay(t, "2025-05-31")
	accesses := []masterdata.Access{
		{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDay(t, "2025-01-01"), End: &end},
	}
	if _, ok := VerifyAccessToken(accesses, "AAAA-BBBB-CCCC", mustDay(t, "2025-06-01")); ok {
		t.Fatal("an expired grant should not match, same as a nonexistent token")
	}
}

func TestVerifyAccessTokenRejectsBeforeStart(t *testing.T) {
	accesses := []masterdata.Access{
		{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: mustDay(t, "2025-06-01")},
	}
	if _, ok := VerifyAccessToken(accesses, "AAAA-BBBB-CCCC", mustDay(t, "2025-01-01")); ok {
		t.Fatal("a grant that has not started yet should not match")
	}
}

func TestLooksLikeAccessToken(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"AAAA-BBBB-CCCC", true},
		{"aaaa-bbbb-cccc", true},
		{"AAAABBBBCCCC", true},
		{" aaaa-bbbb-cccc ", true},
		{"correct horse battery staple", false}, // a real operator password
		{"short-pass", false},
		{"AAAA-BBBB-CCC0", false}, // '0' is outside tokenAlphabet
		{"", false},
	}
	for _, c := range cases {
		if got := LooksLikeAccessToken(c.in); got != c.want {
			t.Errorf("LooksLikeAccessToken(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestGeneratePasswordMeetsMinimumLength(t *testing.T) {
	pw, err := GeneratePassword()
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	if len(pw) < masterdata.MinPasswordLength {
		t.Fatalf("generated password %q has length %d, want at least %d", pw, len(pw), masterdata.MinPasswordLength)
	}
	for _, c := range pw {
		if c != '-' && !strings.ContainsRune(tokenAlphabet, c) {
			t.Errorf("generated password %q contains an unexpected character %q", pw, c)
		}
	}
}

func TestGeneratePasswordIsRandom(t *testing.T) {
	a, err := GeneratePassword()
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	b, err := GeneratePassword()
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	if a == b {
		t.Fatal("two generated passwords should not collide (statistically)")
	}
}

func TestRedactToken(t *testing.T) {
	got := RedactToken("AAAA-BBBB-CCCC")
	if !strings.HasSuffix(got, "CCCC") {
		t.Errorf("RedactToken = %q, want a suffix of CCCC", got)
	}
	if strings.Contains(got, "AAAA") || strings.Contains(got, "BBBB") {
		t.Errorf("RedactToken = %q, should not contain the earlier groups", got)
	}
}
