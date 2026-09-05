package access

import (
	"errors"
	"fmt"
	"os"

	"selbst-ableser/internal/masterdata"
)

// ErrAlreadyBootstrapped is returned by Bootstrap when a master data file
// already exists at the given path.
var ErrAlreadyBootstrapped = errors.New("access: an installation already exists at this path")

// Bootstrap creates the very first master-data file for a new
// installation (ZUGANG-09): an empty MasterData, encrypted with password.
//
// There is deliberately no separate, system-generated operator token to
// misplace: the operator role is simply "knows the current master-data
// password" (see docs/architektur.md, access control). That makes ZUGANG-09's
// two guarantees hold trivially — the installation can never end up
// without an operator, and that one "access" can never be deleted —
// because the password is required for the file to be readable at all.
//
// Bootstrap refuses to run if a file already exists at path, so it can
// never accidentally discard an existing installation.
//
// It also refuses a password that LooksLikeAccessToken would classify as a
// tenant token: the single login field decides which check to run from the
// credential's shape alone (see token.go), so a same-shaped password would
// make the operator's own password permanently unable to log in — not a
// one-off failure to retry, but a standing lockout until the password is
// changed to something differently shaped. GeneratePassword's own output
// (24 symbols) never collides; this only matters for -password.
func Bootstrap(path, password string) error {
	if _, err := os.Stat(path); err == nil {
		return ErrAlreadyBootstrapped
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if LooksLikeAccessToken(password) {
		return fmt.Errorf("access: this password has the same shape as a tenant access token " +
			"(exactly 12 characters from the restricted token alphabet) — the login form would " +
			"never be able to tell the two apart; choose one that doesn't")
	}
	return masterdata.Save(path, masterdata.MasterData{}, password)
}
