package access

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"strings"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/telegram"
)

// tokenAlphabet excludes visually ambiguous characters (0/O, 1/I/L) since
// this token is meant to be handed to a tenant on paper and typed back in
// — it must be readable and typeable without a mistake, not just
// generated. groupSize and groupCount give 12 symbols from the 31-symbol
// alphabet: ~59 bits of entropy, short enough to write down, long enough
// that guessing it is impractical, especially combined with the rate
// limiting in ratelimit.go (ZUGANG-06).
const (
	tokenAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	groupSize     = 4
	groupCount    = 3
)

// GenerateAccessToken creates a new tenant access token (ZUGANG-02).
// Generating it server-side is what makes its entropy a property of this
// code rather than of whatever the caller happened to supply.
// Format: "XXXX-XXXX-XXXX".
func GenerateAccessToken() (string, error) {
	return randomGroupedSecret(groupCount)
}

// passwordGroupCount gives 24 symbols from the 31-symbol alphabet — ~119
// bits of entropy — for the operator's initial master-data password
// (ZUGANG-09): generated and shown exactly once, rather than the
// operator choosing their own at bootstrap. It can be changed afterward
// (see ChangePassword) to something more memorable; the generated one
// only has to be strong and correctly transcribable, not memorable.
const passwordGroupCount = 6

// GeneratePassword creates a random initial master-data password, in the
// same readable, unambiguous alphabet as an access token, just longer.
func GeneratePassword() (string, error) {
	return randomGroupedSecret(passwordGroupCount)
}

// randomGroupedSecret returns n groups of groupSize characters from
// tokenAlphabet, dash-separated.
func randomGroupedSecret(n int) (string, error) {
	raw := make([]byte, groupSize*n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, r := range raw {
		if i > 0 && i%groupSize == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(tokenAlphabet[int(r)%len(tokenAlphabet)])
	}
	return b.String(), nil
}

// normalizeToken makes token comparison forgiving of how a tenant typed it
// back in (case, stray spaces, missing dashes) without weakening it: only
// formatting is normalized, not the token's actual character content.
func normalizeToken(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// VerifyAccessToken finds which access grant, if any, a presented token
// belongs to. It is deliberately built to give a uniform answer regardless
// of whether the token matches nothing, matches an expired grant, or
// matches a currently valid one (ZUGANG-07: none of these may be
// distinguishable from the outside), and it compares every candidate in
// constant time and without short-circuiting on the first match, so which
// position in the list matched cannot be inferred by timing either.
func VerifyAccessToken(accesses []masterdata.Access, token string, today telegram.Day) (masterdata.Access, bool) {
	presented := []byte(normalizeToken(token))

	var match masterdata.Access
	found := false
	for _, a := range accesses {
		candidate := []byte(normalizeToken(a.Token))
		equal := len(candidate) == len(presented) && subtle.ConstantTimeCompare(candidate, presented) == 1
		valid := equal && accessCurrentOn(a, today)
		if valid {
			match, found = a, true
		}
	}
	return match, found
}

func accessCurrentOn(a masterdata.Access, day telegram.Day) bool {
	if day.Before(a.Start) {
		return false
	}
	if a.End != nil && a.End.Before(day) {
		return false
	}
	return true
}

// LooksLikeAccessToken reports whether s has the shape of a tenant access
// token (12 symbols from tokenAlphabet, dashes/spaces optional) rather than
// an operator password, so the login form can offer a single input field
// and dispatch on the credential's own shape instead of asking the caller
// to pick a role first. A human-chosen operator password matching this
// narrow shape by coincidence is practically impossible (12 symbols from a
// restricted 32-symbol alphabet, no lowercase, no punctuation).
func LooksLikeAccessToken(s string) bool {
	n := normalizeToken(s)
	if len(n) != groupSize*groupCount {
		return false
	}
	for _, c := range n {
		if !strings.ContainsRune(tokenAlphabet, c) {
			return false
		}
	}
	return true
}

// RedactToken returns a form of a token safe to put in a log message
// (ZUGANG-08 forbids logging secrets in full): only its last four
// characters, everything else replaced.
func RedactToken(token string) string {
	n := normalizeToken(token)
	if len(n) <= 4 {
		return "****"
	}
	return fmt.Sprintf("****%s", n[len(n)-4:])
}
