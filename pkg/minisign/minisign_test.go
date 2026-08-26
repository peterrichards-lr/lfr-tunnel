package minisign

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The error paths need no valid key, so they run everywhere. The happy path needs a real
// minisign secret key -- the library offers no key generation and hand-assembling the binary
// key format in a test would be testing the fixture, not the code -- so it follows the existing
// precedent in pkg/client/upgrade_test.go and uses TEST_MINISIGN_SECRET_KEY, which CI supplies.

func TestSignFileNoSecretKey(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(in, []byte("abc  file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SignFile("", "", in, filepath.Join(dir, "checksums.txt.minisig"))
	if !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("expected ErrNoSecretKey, got %v", err)
	}

	// Distinguishable on purpose: pkg/ops/sign.go reports "no key configured" differently from
	// "signing failed", because they call for different actions from whoever is releasing.
	if errors.Is(err, os.ErrNotExist) {
		t.Error("ErrNoSecretKey must not be confusable with a missing file")
	}
}

func TestSignFileUnreadableInput(t *testing.T) {
	dir := t.TempDir()
	err := SignFile("untrusted comment: x\nnonsense", "", filepath.Join(dir, "absent.txt"),
		filepath.Join(dir, "out.minisig"))
	if err == nil {
		t.Fatal("expected an error for a missing input file")
	}
	if errors.Is(err, ErrNoSecretKey) {
		t.Errorf("wrong error for a missing input: %v", err)
	}
	// The input is read before the key is decoded, so the message should name the file rather
	// than blaming the key.
	if !strings.Contains(err.Error(), "absent.txt") {
		t.Errorf("error %q does not name the unreadable file", err)
	}
}

func TestSignFileInvalidKey(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(in, []byte("abc  file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SignFile("not a minisign key at all", "", in, filepath.Join(dir, "out.minisig"))
	if err == nil {
		t.Fatal("expected an error for an undecodable key")
	}
	if !strings.Contains(err.Error(), "secret key") {
		t.Errorf("error %q does not point at the key", err)
	}
	// A failed signing must not leave a partial signature that a client would then try to
	// verify against.
	if _, statErr := os.Stat(filepath.Join(dir, "out.minisig")); statErr == nil {
		t.Error("a signature file was written despite the failure")
	}
}

// TestSignFileFromEnv covers the env-reading wrapper, which is what both callers actually use.
func TestSignFileFromEnv(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(in, []byte("abc  file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "checksums.txt.minisig")

	t.Setenv(SecretKeyEnv, "")
	if err := SignFileFromEnv(in, out); !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("unset key: expected ErrNoSecretKey, got %v", err)
	}

	sk := os.Getenv("TEST_MINISIGN_SECRET_KEY")
	if sk == "" {
		t.Skip("TEST_MINISIGN_SECRET_KEY not set -- skipping the signing path")
	}

	t.Setenv(SecretKeyEnv, sk)
	t.Setenv(PasswordEnv, os.Getenv("TEST_MINISIGN_KEY_PASSWORD"))
	if err := SignFileFromEnv(in, out); err != nil {
		t.Fatalf("signing with the test key failed: %v", err)
	}

	sig, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no signature written: %v", err)
	}
	// Shape only. That the signature actually verifies is already covered end to end by
	// pkg/client/upgrade_test.go against the same key.
	if !strings.HasPrefix(string(sig), "untrusted comment:") {
		t.Errorf("signature does not look like minisign output: %.40q", sig)
	}
}
