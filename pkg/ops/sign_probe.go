package ops

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Proving a dry run can fail (#1978).
//
// `sign -dry-run` reported "Dry run: configuration is complete / Windows: will be signed" and
// the real run then failed with osslsigncode's "maybe wrong password". It checked the variables
// were SET, never that they RESOLVED or that the password opened the key -- so it produced
// exactly the false assurance it exists to prevent, which is the #1975 shape again.
//
// The cheapest check that could actually have caught it: ask openssl to open the private key
// with the configured password. That exercises the credential, the password and the file format
// together, which is the combination that failed, and costs milliseconds.

// CredentialProbe injects the tool lookup and runner so the probe is testable without openssl
// installed and without a real key -- and so a test can assert the healthy path reports OK,
// which is what stops the failure cases passing on a probe that rejects everything.
type CredentialProbe struct {
	LookPath func(string) (string, error)
	Run      func(name string, args ...string) error
	// WriteTemp materialises inline PEM so openssl can be pointed at a path. Injected for the
	// same reason.
	WriteTemp func(content, pattern string) (string, error)
}

// ErrProbeUnavailable means the check could not be performed, NOT that it passed.
//
// Kept distinct so "openssl is missing" can never be rendered as a tick. A probe that silently
// degrades to success when its tool is absent is the failure this whole issue is about.
var ErrProbeUnavailable = errors.New("probe unavailable")

// ProbeWindowsKey reports whether the Windows signing key can actually be opened with the
// configured password.
//
// Returns ErrProbeUnavailable (wrapped) when it cannot tell, so the caller can say "not checked"
// rather than "fine".
func ProbeWindowsKey(p CredentialProbe, signKey, signPass string) error {
	if signKey == "" || signKey == skipSentinel {
		return fmt.Errorf("%w: no Windows key configured", ErrProbeUnavailable)
	}
	if strings.HasPrefix(signKey, "op://") {
		return fmt.Errorf("%w: LFT_SIGN_KEY is still an op:// reference", ErrProbeUnavailable)
	}
	if _, err := p.LookPath("openssl"); err != nil {
		return fmt.Errorf("%w: openssl is not on PATH", ErrProbeUnavailable)
	}

	keyPath := signKey
	if !fileExists(keyPath) {
		if !strings.Contains(signKey, "-----BEGIN") {
			return fmt.Errorf("LFT_SIGN_KEY is neither a readable file nor inline PEM")
		}
		tmp, err := p.WriteTemp(signKey, "probe-key-*.pem")
		if err != nil {
			return fmt.Errorf("%w: could not materialise the key: %v", ErrProbeUnavailable, err)
		}
		defer func() { _ = os.Remove(tmp) }()
		keyPath = tmp
	}

	// `-noout` so nothing about the key reaches stdout. The password is passed as pass:<value>,
	// which is visible in this process's own argv -- the same exposure the real signing path
	// already has via -readpass, tracked as #1555 and not widened here.
	args := []string{"pkey", "-in", keyPath, "-noout"}
	if signPass != "" && signPass != skipSentinel {
		args = append(args, "-passin", "pass:"+signPass)
	}

	if err := p.Run("openssl", args...); err != nil {
		return fmt.Errorf("the Windows signing key could not be opened with LFT_SIGN_PASS " +
			"(this is what surfaces later as osslsigncode's \"maybe wrong password\")")
	}
	return nil
}

// DescribeProbe renders one probe outcome for the dry-run summary.
//
// An unavailable probe is rendered as "not checked", never as a pass: the operator has to be
// able to tell "proven" from "could not tell", which is the distinction the old dry run erased.
func DescribeProbe(err error) string {
	switch {
	case err == nil:
		return "verified: the key opens with LFT_SIGN_PASS"
	case errors.Is(err, ErrProbeUnavailable):
		return "NOT CHECKED: " + strings.TrimPrefix(err.Error(), "probe unavailable: ")
	default:
		return "FAILED: " + err.Error()
	}
}
