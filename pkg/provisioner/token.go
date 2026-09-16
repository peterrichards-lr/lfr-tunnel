package provisioner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// GenerateOrLoadToken returns the shared secret lfr-tunneld must present (as
// "Authorization: Bearer <token>") on every request to this sidecar.
//
// This sidecar binds to 127.0.0.1 only, so the token is defense-in-depth
// against other local users/processes on the same box, not the primary
// security boundary -- but it's cheap to get right, so we do.
func GenerateOrLoadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("token file %s exists but is empty", path)
		}
		return token, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading token file %s: %w", path, err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	token := hex.EncodeToString(raw)

	// 0600: only the owner (the account this process runs as) can read it.
	// lfr-tunneld must run as the same user, or be granted read access some
	// other way, to pick up the same token.
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing token file %s: %w", path, err)
	}
	return token, nil
}

// ValidToken reports whether presented matches expected, in constant time.
func ValidToken(expected, presented string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

// Why LoadToken failed (#1956). All four are faults -- LoadToken is only reached once
// edge_provisioner_url is set, so there is no "absent is fine" state down here; that one
// lives in the caller, which never calls this at all when the sidecar is not configured.
//
// They are separate sentinels because the operator's next action differs for each: set a
// setting, fix a typo, fix permissions, wait for the sidecar. Collapsing them was how a
// mistyped edge_provisioner_token_file reached the admin panel as "not configured on this
// server" -- an operator error presented as a decision.
//
// NONE of these, and nothing wrapped into them, ever carries the token itself, its length
// or any prefix of it: the file's CONTENT is never read into an error, only its path and
// the filesystem's own complaint about opening it. A diagnosis that narrowed the secret
// would be a worse bug than the one this fixes.
var (
	// ErrTokenPathUnset is edge_provisioner_url set with no edge_provisioner_token_file
	// beside it. Distinct from a missing file: there is no path to check for a typo.
	ErrTokenPathUnset = errors.New("provisioner: no edge-provisioner token file configured")
	// ErrTokenNotFound is a configured path with nothing at it -- a typo, the wrong
	// directory, or a sidecar that has not started and so has not written the file yet.
	ErrTokenNotFound = errors.New("provisioner: configured edge-provisioner token file does not exist")
	// ErrTokenUnreadable is a file that exists and cannot be read: usually 0600 owned by
	// the sidecar's user when lfr-tunneld runs as another.
	ErrTokenUnreadable = errors.New("provisioner: configured edge-provisioner token file could not be read")
	// ErrTokenEmpty is a readable file with nothing in it -- a truncated write, or the
	// sidecar interrupted between creating the file and writing to it.
	ErrTokenEmpty = errors.New("provisioner: configured edge-provisioner token file is empty")
)

// LoadToken reads the shared secret from path without ever generating one.
// This is the client-side counterpart to GenerateOrLoadToken: lfr-tunneld is
// not the owner of this token (the sidecar is), so it must never silently
// create a mismatched one -- a missing file here means the sidecar hasn't
// started yet, or edge_provisioner_token_file is misconfigured, and either
// way that's a real error to surface, not something to paper over.
//
// Every error matches exactly one of the sentinels above and names the path it tried, so
// the caller can tell an operator which of the four states they are in (#1956). The error
// text is shown to admins; it must stay free of the token itself.
func LoadToken(path string) (string, error) {
	if path == "" {
		return "", ErrTokenPathUnset
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("edge-provisioner token file %s: %w", path, ErrTokenNotFound)
		}
		// The filesystem's own message, not the file's contents: os.ReadFile fails before
		// it has read any, so there is nothing of the secret in err to leak.
		return "", fmt.Errorf("reading edge-provisioner token file %s: %v: %w", path, err, ErrTokenUnreadable)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("edge-provisioner token file %s: %w", path, ErrTokenEmpty)
	}
	return token, nil
}
