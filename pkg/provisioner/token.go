package provisioner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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

// LoadToken reads the shared secret from path without ever generating one.
// This is the client-side counterpart to GenerateOrLoadToken: lfr-tunneld is
// not the owner of this token (the sidecar is), so it must never silently
// create a mismatched one -- a missing file here means the sidecar hasn't
// started yet, or edge_provisioner_token_file is misconfigured, and either
// way that's a real error to surface, not something to paper over.
func LoadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading edge-provisioner token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("edge-provisioner token file %s is empty", path)
	}
	return token, nil
}
