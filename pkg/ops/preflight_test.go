package ops

import (
	"errors"
	"strings"
	"testing"
)

// Preflight and op:// resolution (#1978).
//
// The defect being guarded is not a crash: it is an operator having to KNOW that `op run` is
// mandatory, that the Employee vault is on the work account, and that an AWS SSO token expires
// silently between deploy-clients and deploy. So the assertions are about what the output TELLS
// someone, not merely that a failure was detected.

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func found(string) (string, error) { return "/usr/bin/x", nil }

func okRun(string, ...string) (string, error) { return "ok", nil }

func resultsByName(rs []PreflightResult) map[string]PreflightResult {
	m := map[string]PreflightResult{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

// CONTROL. Without this, every case below could pass on a checker that fails everything.
func TestPreflightPassesWhenEverythingIsInPlace(t *testing.T) {
	rs := EvaluatePreflight(PreflightDeps{
		Getenv:     env(map[string]string{"AWS_PROFILE": "lfr-tunnel"}),
		LookPath:   found,
		RunCapture: okRun,
	})
	for _, r := range rs {
		if r.Status != CheckOK {
			t.Fatalf("CONTROL: %q reported %v (%s) on a healthy environment -- the failure cases below prove nothing",
				r.Name, r.Status, r.Detail)
		}
	}
	if len(rs) < 4 {
		t.Fatalf("expected the tool and session checks to run, got %d results", len(rs))
	}
}

// FIRING. The v1.48.35 stall: credentials are op:// references and there is no op session.
func TestPreflightSaysHowToSignIntoTheRightAccount(t *testing.T) {
	rs := resultsByName(EvaluatePreflight(PreflightDeps{
		Getenv: env(map[string]string{
			"AWS_PROFILE":   "lfr-tunnel",
			"LFT_SIGN_KEY":  "op://Employee/x/key",
			"LFT_SIGN_PASS": "op://Employee/x/password",
		}),
		LookPath: found,
		RunCapture: func(name string, args ...string) (string, error) {
			if name == "op" {
				return "", errors.New("account is not signed in")
			}
			return "ok", nil
		},
	}))

	r, ok := rs[checkOnePassword]
	if !ok || r.Status != CheckFail {
		t.Fatalf("an absent op session was not reported as a failure: %+v", r)
	}
	// The account matters: this machine has a personal one too, and `op` resolving against it
	// reports the item as missing, which reads as a broken reference rather than a wrong account.
	if !strings.Contains(r.Remedy, "op signin") || !strings.Contains(r.Remedy, "liferayinc") {
		t.Fatalf("the remedy does not name the sign-in command and the work account: %q", r.Remedy)
	}
	if !strings.Contains(r.Detail, "LFT_SIGN_KEY") {
		t.Fatalf("the failure does not say which credentials need resolving: %q", r.Detail)
	}
}

// FIRING. An expired SSO token, which stopped deploy AFTER deploy-clients had published.
func TestPreflightSaysHowToRenewAws(t *testing.T) {
	rs := resultsByName(EvaluatePreflight(PreflightDeps{
		Getenv:   env(map[string]string{"AWS_PROFILE": "lfr-tunnel"}),
		LookPath: found,
		RunCapture: func(name string, args ...string) (string, error) {
			if name == "aws" {
				return "", errors.New("Token has expired and refresh failed")
			}
			return "ok", nil
		},
	}))

	r := rs["AWS session"]
	if r.Status != CheckFail {
		t.Fatalf("an expired AWS session was not reported as a failure: %+v", r)
	}
	if !strings.Contains(r.Remedy, "aws sso login --profile lfr-tunnel") {
		t.Fatalf("the remedy does not name the profile to renew: %q", r.Remedy)
	}
}

// FIRING. An unset AWS_PROFILE fails as a misleading "no EC2 instance found", so preflight has
// to name the real cause before a release goes anywhere near it.
func TestPreflightExplainsTheMisleadingUnsetProfileError(t *testing.T) {
	rs := resultsByName(EvaluatePreflight(PreflightDeps{
		Getenv: env(map[string]string{}), LookPath: found, RunCapture: okRun,
	}))
	r := rs["AWS profile"]
	if r.Status != CheckFail {
		t.Fatalf("an unset AWS_PROFILE was not reported: %+v", r)
	}
	if !strings.Contains(r.Detail, "no EC2 instance found") {
		t.Fatalf("the detail does not connect this to the error the operator will actually see: %q", r.Detail)
	}
}

// FIRING. A missing signing tool is exit 2 at the END of a release, behind the biometric prompt.
func TestPreflightCatchesAMissingSigningTool(t *testing.T) {
	rs := resultsByName(EvaluatePreflight(PreflightDeps{
		Getenv: env(map[string]string{"AWS_PROFILE": "lfr-tunnel"}),
		LookPath: func(bin string) (string, error) {
			if bin == "osslsigncode" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + bin, nil
		},
		RunCapture: okRun,
	}))
	r := rs["osslsigncode"]
	if r.Status != CheckFail || !strings.Contains(r.Remedy, "brew install osslsigncode") {
		t.Fatalf("a missing Windows signing tool was not reported with its remedy: %+v", r)
	}
	if rs["gpg"].Status != CheckOK {
		t.Fatal("an unrelated tool was also reported missing -- the check is not specific")
	}
}

// --- op:// resolution ---

// FIRING. The exact shape that cost a cycle: the variable IS set, so every presence check
// passes, but it holds a reference rather than a secret.
func TestAnOpReferenceIsNotAResolvedCredential(t *testing.T) {
	got := UnresolvedOpRefs(SigningCredentialVars, env(map[string]string{
		"LFT_SIGN_KEY":  "op://Employee/item/key",
		"LFT_SIGN_PASS": "op://Employee/item/password",
		"LFT_GPG_KEY":   "E8E407B5D8F557B3F0749EE676219C6F0B96C87B",
	}))
	if len(got) != 2 {
		t.Fatalf("expected the two op:// references, got %v", got)
	}
	// BOUNDING: a real value is not a reference. Without this the detector could report
	// everything and still pass the case above.
	for _, name := range got {
		if name == "LFT_GPG_KEY" {
			t.Fatal("a resolved literal was reported as an unresolved reference")
		}
	}
}

// BOUNDING. A fully resolved environment must re-exec nothing: re-running under `op run` when
// it is not needed would raise a biometric prompt no one asked for.
func TestAFullyResolvedEnvironmentNeedsNoOpRun(t *testing.T) {
	if got := UnresolvedOpRefs(SigningCredentialVars, env(map[string]string{
		"LFT_SIGN_KEY":  "/tmp/win.key",
		"LFT_SIGN_PASS": "hunter2",
	})); len(got) != 0 {
		t.Fatalf("resolved credentials were reported as needing op run: %v", got)
	}
}

// An op:// reference in an UNRELATED variable must not drag signing under `op run`.
func TestOnlySigningCredentialsTriggerOpRun(t *testing.T) {
	if got := UnresolvedOpRefs(SigningCredentialVars, env(map[string]string{
		"DATABASE_URL": "op://Personal/db/url",
	})); len(got) != 0 {
		t.Fatalf("an unrelated op:// reference triggered the signing re-exec: %v", got)
	}
}

func TestOpRunCommandTargetsTheWorkAccount(t *testing.T) {
	argv := OpRunCommand("/usr/local/bin/lfr-tunnel-ops", []string{"sign", "-dry-run"}, "liferayinc.1password.com")
	joined := strings.Join(argv, " ")
	if !strings.HasPrefix(joined, "op run --account liferayinc.1password.com -- ") {
		t.Fatalf("op run invocation is wrong: %q", joined)
	}
	if !strings.HasSuffix(joined, "lfr-tunnel-ops sign -dry-run") {
		t.Fatalf("the original arguments were not preserved: %q", joined)
	}
}

// The marker is what stops the re-exec recursing when op cannot substitute.
func TestTheReexecMarkerIsHonoured(t *testing.T) {
	if UnderOpRun(env(map[string]string{})) {
		t.Fatal("reported as running under op run with no marker set")
	}
	if !UnderOpRun(env(map[string]string{opRunMarker: "1"})) {
		t.Fatal("the marker was not honoured, so a failed substitution would loop forever")
	}
}
