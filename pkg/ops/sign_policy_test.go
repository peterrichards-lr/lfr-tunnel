package ops

import (
	"errors"
	"strings"
	"testing"
)

// #1906. `sign` skipped any platform whose env vars were unset and exited 0, reporting
// "=== Client Signing Complete! ===" over binaries it had never signed. These assert the two
// halves of the fix: an unconfigured step is refused, and an explicitly skipped one is allowed.
//
// The old behaviour is the control -- every "refused" case below returned SignExitOK before.

func alwaysFound(string) (string, error) { return "/usr/bin/stub", nil }

// fullEnv is a complete, valid configuration. Each test below breaks exactly one thing, so a
// failure names the field responsible rather than a generally-unhappy fixture.
func fullEnv() signEnv {
	return signEnv{
		MacOSIdentity: "ABC123",
		SignKey:       "/tmp/k.pem",
		SignCrt:       "/tmp/c.pem",
		GPGKey:        "DEADBEEF",
		MinisignKey:   "untrusted comment: minisign key",
	}
}

func TestACompleteConfigurationSignsEverything(t *testing.T) {
	// PREMISE. Without this, every refusal below could be a policy that refuses everything.
	plan, code, problems := planSigning(fullEnv(), alwaysFound)
	if code != SignExitOK {
		t.Fatalf("a complete configuration must be accepted, got exit %d: %v", code, problems)
	}
	if !plan.MacOS || !plan.Windows || !plan.Linux || !plan.Minisign {
		t.Errorf("every step should be planned, got %+v", plan)
	}
}

func TestAnUnconfiguredStepIsRefusedRatherThanSkipped(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*signEnv)
		expect string
	}{
		{"no macOS identity", func(e *signEnv) { e.MacOSIdentity = "" }, "LFT_MACOS_IDENTITY"},
		{"no Windows credentials", func(e *signEnv) { e.SignKey = ""; e.SignCrt = "" }, "LFT_SIGN_P12"},
		{"Windows cert without key", func(e *signEnv) { e.SignKey = "" }, "LFT_SIGN_KEY"},
		{"no GPG key or secret", func(e *signEnv) { e.GPGKey = ""; e.GPGSecret = "" }, "LFT_GPG_KEY"},
		{"no minisign key", func(e *signEnv) { e.MinisignKey = "" }, "MINISIGN_SECRET_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fullEnv()
			tc.mutate(&env)
			_, code, problems := planSigning(env, alwaysFound)
			if code != SignExitCredentials {
				t.Fatalf("want exit %d (missing credential), got %d -- this is the silent skip that shipped unsigned binaries",
					SignExitCredentials, code)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.expect) {
				t.Errorf("the refusal should name %s so the operator knows what to set; got %v", tc.expect, problems)
			}
		})
	}
}

func TestAnExplicitSkipIsStillAllowed(t *testing.T) {
	// Signing a subset on purpose has to stay possible, or the refusal above just blocks
	// legitimate work. This is the boundary between the two.
	cases := []struct {
		name   string
		mutate func(*signEnv)
		check  func(signPlan) bool
	}{
		{"macOS skipped", func(e *signEnv) { e.MacOSIdentity = "skip" }, func(p signPlan) bool { return !p.MacOS && p.Windows }},
		{"Windows skipped", func(e *signEnv) { e.SignKey = "skip" }, func(p signPlan) bool { return !p.Windows && p.MacOS }},
		{"GPG skipped by flag", func(e *signEnv) { e.SkipGPG = "true" }, func(p signPlan) bool { return !p.Linux && p.MacOS }},
		{"GPG skipped by sentinel", func(e *signEnv) { e.GPGKey = "skip" }, func(p signPlan) bool { return !p.Linux }},
		{"minisign skipped", func(e *signEnv) { e.SkipMinisign = "true" }, func(p signPlan) bool { return !p.Minisign && p.MacOS }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fullEnv()
			tc.mutate(&env)
			plan, code, problems := planSigning(env, alwaysFound)
			if code != SignExitOK {
				t.Fatalf("an explicit skip must be allowed, got exit %d: %v", code, problems)
			}
			if !tc.check(plan) {
				t.Errorf("wrong step skipped: %+v", plan)
			}
		})
	}
}

func TestAMissingToolIsItsOwnExitCode(t *testing.T) {
	missing := func(name string) func(string) (string, error) {
		return func(n string) (string, error) {
			if n == name {
				return "", errors.New("executable file not found in $PATH")
			}
			return "/usr/bin/stub", nil
		}
	}
	for _, tool := range []string{"codesign", "osslsigncode", "gpg"} {
		t.Run(tool, func(t *testing.T) {
			_, code, problems := planSigning(fullEnv(), missing(tool))
			if code != SignExitToolMissing {
				t.Fatalf("want exit %d (tool missing), got %d", SignExitToolMissing, code)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tool) {
				t.Errorf("the refusal should name %s; got %v", tool, problems)
			}
		})
	}
}

func TestAToolIsOnlyRequiredForAStepThatWillRun(t *testing.T) {
	// BOUNDING. A machine with no osslsigncode must still be able to sign macOS builds when
	// Windows was skipped deliberately -- otherwise the tool check becomes a reason to stop
	// using the explicit skips, and people go back to unset variables.
	env := fullEnv()
	env.SignKey = "skip"
	noOsslsigncode := func(n string) (string, error) {
		if n == "osslsigncode" {
			return "", errors.New("not found")
		}
		return "/usr/bin/stub", nil
	}
	if _, code, problems := planSigning(env, noOsslsigncode); code != SignExitOK {
		t.Fatalf("a skipped platform must not require its tool, got exit %d: %v", code, problems)
	}
}

func TestACredentialProblemIsReportedBeforeAToolProblem(t *testing.T) {
	// Both are wrong here. The credential is reported because it is far more often the real
	// cause -- a run that was not under `op run --` -- and naming the tool first sends someone
	// installing software they already have.
	env := fullEnv()
	env.MacOSIdentity = ""
	neverFound := func(string) (string, error) { return "", errors.New("not found") }
	_, code, _ := planSigning(env, neverFound)
	if code != SignExitCredentials {
		t.Errorf("want exit %d (credentials) to win over %d (tool), got %d",
			SignExitCredentials, SignExitToolMissing, code)
	}
}

func TestEveryExitCodeIsDistinct(t *testing.T) {
	// The contract restored from scripts/sign-client-binaries.sh. Two codes colliding would
	// silently merge two causes back into one, which is the defect being fixed.
	seen := map[int]string{}
	for name, code := range map[string]int{
		"OK": SignExitOK, "Setup": SignExitSetup, "ToolMissing": SignExitToolMissing,
		"Credentials": SignExitCredentials, "Signing": SignExitSigning, "Checksums": SignExitChecksums,
	} {
		if prev, dup := seen[code]; dup {
			t.Errorf("exit code %d is used by both %s and %s", code, prev, name)
		}
		seen[code] = name
	}
	if len(seen) != 6 {
		t.Errorf("want 6 distinct exit codes, got %d", len(seen))
	}
}

func TestDescribeSkipsNamesWhatWillNotBeSigned(t *testing.T) {
	// The summary is printed up front; "Skipping Windows signing" buried mid-run is how
	// unsigned binaries reached a release in the first place.
	got := strings.Join(describeSkips(signPlan{MacOS: true, Minisign: true}), ", ")
	if !strings.Contains(got, "Windows") || !strings.Contains(got, "Linux GPG") {
		t.Errorf("want the unsigned platforms named, got %q", got)
	}
	if strings.Contains(got, "macOS") {
		t.Errorf("a platform that WILL be signed must not be listed as skipped: %q", got)
	}
	if len(describeSkips(signPlan{MacOS: true, Windows: true, Linux: true, Minisign: true})) != 0 {
		t.Error("a complete plan skips nothing")
	}
}
