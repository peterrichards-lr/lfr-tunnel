// Package minisign signs a file with minisign, using a key supplied entirely through the
// environment. It never contains or reads a hardcoded key.
//
// This exists as a library rather than only as scripts/minisign_helper.go because the local
// signing path used to reach that helper through `go run` (#1402). The Go toolchain links every
// executable it produces inside GOTMPDIR -- or the system temp dir when that is unset -- and
// `go run` then EXECUTES from there. On the EDR-protected workstation GOTMPDIR is unset for a
// directly-invoked `lfr-tunnel-ops sign`, so that call linked and ran an unsigned binary out of
// /var/folders, which is the pattern CLAUDE.md exists to prevent and that has already cost this
// project three environment reinstalls.
//
// Calling it in-process removes the subprocess entirely: nothing is linked, nothing is written
// to a temp directory, and it is faster. scripts/minisign_helper.go stays as a thin wrapper so
// .github/workflows/release.yml keeps working unchanged -- that path runs on an ephemeral Linux
// runner, where the EDR rule does not apply.
package minisign

import (
	"errors"
	"fmt"
	"os"

	minisign "github.com/jedisct1/go-minisign"
)

// Env var names, kept here so the library, the CLI wrapper and the docs cannot drift.
const (
	SecretKeyEnv = "MINISIGN_SECRET_KEY"
	PasswordEnv  = "MINISIGN_KEY_PASSWORD"
)

// ErrNoSecretKey is returned when MINISIGN_SECRET_KEY is unset or empty. Callers distinguish it
// because "no key configured" is a legitimate state -- a fork cutting a release without this
// project's key -- whereas every other error means signing was attempted and failed.
var ErrNoSecretKey = errors.New(SecretKeyEnv + " is not set")

// SignFileFromEnv signs inPath and writes the detached signature to outPath, taking the key from
// MINISIGN_SECRET_KEY and its passphrase from MINISIGN_KEY_PASSWORD.
func SignFileFromEnv(inPath, outPath string) error {
	return SignFile(os.Getenv(SecretKeyEnv), os.Getenv(PasswordEnv), inPath, outPath)
}

// SignFile is the testable form: the key and passphrase are arguments rather than read from the
// environment, so a test does not have to mutate process state to cover the error paths.
//
// secretKey is the full minisign secret key file content -- the "untrusted comment: ..." line
// plus the base64 payload.
func SignFile(secretKey, password, inPath, outPath string) error {
	if secretKey == "" {
		return ErrNoSecretKey
	}

	content, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inPath, err)
	}

	sk, err := minisign.DecodePrivateKey(secretKey)
	if err != nil {
		return fmt.Errorf("decoding the secret key: %w", err)
	}
	defer sk.Wipe()

	if sk.IsEncrypted() {
		if password == "" {
			return fmt.Errorf("the secret key is password-protected but %s is not set", PasswordEnv)
		}
		if err := sk.Decrypt(password); err != nil {
			return fmt.Errorf("decrypting the secret key: %w", err)
		}
	}

	sig, err := sk.Sign(content, minisign.SignOptions{Hashed: true})
	if err != nil {
		return fmt.Errorf("signing %s: %w", inPath, err)
	}

	if err := os.WriteFile(outPath, sig.Encode(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	return nil
}
