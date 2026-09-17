package ops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Preflight: the checks a release needs, each carrying its own remedy (#1978).
//
// Cutting v1.48.35 stalled three times, none of them a code defect: an unresolved op://
// credential, an expired 1Password session, and an expired AWS SSO token -- the last of which
// stopped `deploy` AFTER `deploy-clients` had already published. Every one was knowledge held
// in a person's head or a notes file rather than in the repo.
//
// So the rule this encodes: anything an operator has to REMEMBER becomes a check that fails
// with the fix in its message. A preflight that merely says "not ok" moves the knowledge, it
// does not remove the need for it.
//
// Read-only and side-effect free, so it is safe to run at any time and cheap enough to run
// before every release.

// CheckStatus is the outcome of one preflight check.
type CheckStatus int

const (
	CheckOK CheckStatus = iota
	CheckFail
	// CheckWarn is for something that will not stop the release but that the operator should
	// see -- kept distinct so a warning can never be mistaken for a pass at a glance.
	CheckWarn
)

// PreflightResult is one check's outcome. Remedy is the command to run, and is required on a
// failure: a check that cannot say what to do about itself is the thing being fixed here.
type PreflightResult struct {
	Name   string
	Status CheckStatus
	Detail string
	Remedy string
}

// PreflightExit is returned when any check fails.
const PreflightExit = 1

// checkOnePassword names the 1Password session check in every branch that reports it.
const checkOnePassword = "1Password session"

// EvaluatePreflight runs the checks and returns their results.
//
// The lookups are injected so the tests can drive every branch without a 1Password session, an
// AWS account or the signing tools installed -- and, more importantly, so there is a control:
// a test can assert the all-clear path really reports OK, which stops the failure cases passing
// on a checker that simply reports failure for everything.
type PreflightDeps struct {
	Getenv     func(string) string
	LookPath   func(string) (string, error)
	RunCapture func(name string, args ...string) (string, error)
}

func EvaluatePreflight(d PreflightDeps) []PreflightResult {
	var out []PreflightResult

	// 1Password session. An expired one reports "authorization timeout" only after a 60s wait,
	// which reads like an unanswered biometric prompt and is not -- `op whoami` says so at once.
	if refs := UnresolvedOpRefs(SigningCredentialVars, d.Getenv); len(refs) > 0 {
		if _, err := d.LookPath("op"); err != nil {
			out = append(out, PreflightResult{
				Name: "1Password CLI", Status: CheckFail,
				Detail: fmt.Sprintf("credentials are op:// references (%s) but `op` is not installed", strings.Join(refs, ", ")),
				Remedy: "brew install 1password-cli",
			})
		} else if _, err := d.RunCapture("op", "whoami"); err != nil {
			out = append(out, PreflightResult{
				Name: checkOnePassword, Status: CheckFail,
				Detail: fmt.Sprintf("not signed in, and these need resolving: %s", strings.Join(refs, ", ")),
				Remedy: "op signin --account " + GetEnvOrDefaultFn(d.Getenv, "LFT_OP_ACCOUNT", defaultOpAccount),
			})
		} else {
			out = append(out, PreflightResult{
				Name: checkOnePassword, Status: CheckOK,
				Detail: fmt.Sprintf("signed in; %d credential(s) will resolve via `op run`", len(refs)),
			})
		}
	} else {
		out = append(out, PreflightResult{
			Name: checkOnePassword, Status: CheckOK,
			Detail: "no op:// references; credentials are already resolved",
		})
	}

	// AWS. Checked before a release rather than discovered between deploy-clients and deploy,
	// which is the order that leaves clients published against gateways that were not updated.
	profile := GetEnvOrDefaultFn(d.Getenv, "AWS_PROFILE", "")
	if profile == "" {
		out = append(out, PreflightResult{
			Name: "AWS profile", Status: CheckFail,
			Detail: "AWS_PROFILE is unset and the default profile has no credentials, which `deploy` reports as \"no EC2 instance found ... check the region list\"",
			Remedy: "export AWS_PROFILE=lfr-tunnel",
		})
	} else if _, err := d.RunCapture("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text"); err != nil {
		out = append(out, PreflightResult{
			Name: "AWS session", Status: CheckFail,
			Detail: fmt.Sprintf("profile %q has no usable credentials (SSO tokens expire silently)", profile),
			Remedy: "aws sso login --profile " + profile,
		})
	} else {
		out = append(out, PreflightResult{
			Name: "AWS session", Status: CheckOK,
			Detail: fmt.Sprintf("profile %q authenticated", profile),
		})
	}

	// Signing tools. Missing here is exit 2 during sign, at the end of a release.
	for _, t := range []struct{ bin, why, remedy string }{
		{"codesign", "macOS signing", "install Xcode command line tools"},
		{"osslsigncode", "Windows signing", "brew install osslsigncode"},
		{"gpg", "Linux detached signatures", "brew install gnupg"},
	} {
		if _, err := d.LookPath(t.bin); err != nil {
			out = append(out, PreflightResult{
				Name: t.bin, Status: CheckFail,
				Detail: t.bin + " is not on PATH, needed for " + t.why,
				Remedy: t.remedy,
			})
		} else {
			out = append(out, PreflightResult{Name: t.bin, Status: CheckOK, Detail: "present"})
		}
	}

	return out
}

// GetEnvOrDefaultFn is GetEnvOrDefault against an injected lookup, so preflight stays testable.
func GetEnvOrDefaultFn(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

// PreflightCommand prints the results and exits non-zero if any check failed.
func PreflightCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops preflight")
		fmt.Println("\nChecks everything a release needs -- 1Password session, AWS credentials and")
		fmt.Println("the signing tools -- and prints the command to fix anything that is missing.")
		fmt.Println("Read-only. Exits 1 if any check failed.")
		return
	}

	results := EvaluatePreflight(PreflightDeps{
		Getenv:     os.Getenv,
		LookPath:   exec.LookPath,
		RunCapture: RunCommandCaptureOutput,
	})

	fmt.Println("=== Release preflight ===")
	failed := 0
	for _, r := range results {
		switch r.Status {
		case CheckOK:
			fmt.Printf("  OK    %-22s %s\n", r.Name, r.Detail)
		case CheckWarn:
			fmt.Printf("  WARN  %-22s %s\n", r.Name, r.Detail)
		case CheckFail:
			failed++
			fmt.Printf("  FAIL  %-22s %s\n", r.Name, r.Detail)
			if r.Remedy != "" {
				fmt.Printf("        %-22s run: %s\n", "", r.Remedy)
			}
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Printf("%d check(s) failed. Fix them before cutting the release.\n", failed)
		os.Exit(PreflightExit)
	}
	fmt.Println("All checks passed.")
}
