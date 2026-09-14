package ops

import (
	"fmt"
	"os/exec"
)

// Exit codes for `sign` (#1906).
//
// Restored from scripts/sign-client-binaries.sh, which #612/#615 replaced with this CLI. The
// script documented these six and used them; the Go port collapsed everything to 0 or 1, so a
// caller could not tell a missing tool from a bad credential from a failed signature -- and,
// worse, could not tell any of them from success, because a platform nobody configured was
// skipped silently and still exited 0.
//
// `sign` runs behind a biometric prompt at the end of a release, which is when output is least
// likely to be read. The exit code is the only signal most callers check, so it has to mean
// something.
const (
	SignExitOK          = 0 // everything asked for was signed
	SignExitSetup       = 1 // general setup error: stale dist/, bad flags, unreadable files
	SignExitToolMissing = 2 // a required tool is not on PATH (codesign, osslsigncode, gpg)
	SignExitCredentials = 3 // a step was neither configured nor explicitly skipped
	SignExitSigning     = 4 // a signing command was attempted and failed
	SignExitChecksums   = 5 // checksums or the minisign signature could not be produced
)

// skipSentinel is the value that turns "not configured" into "deliberately not signing this".
//
// The distinction is the whole point of #1906: an absent credential is a mistake and now fails,
// while signing a subset on purpose stays possible for anyone who says so explicitly.
const skipSentinel = "skip"

// signEnv is the signing configuration read from the environment.
//
// A struct rather than a dozen parameters so the policy below can be exercised directly. The
// old behaviour could not be tested at all: every refusal was an inline os.Exit.
type signEnv struct {
	MacOSIdentity string
	SignP12       string
	SignKey       string
	SignCrt       string
	GPGKey        string
	GPGSecret     string
	SkipGPG       string
	SkipMinisign  string
	MinisignKey   string
}

// signPlan is what will actually be attempted.
type signPlan struct {
	MacOS    bool
	Windows  bool
	Linux    bool
	Minisign bool
}

// planSigning decides what to sign, and refuses when a step was neither configured nor
// explicitly skipped.
//
// Returns the plan, an exit code (SignExitOK when the run may proceed) and the operator-facing
// reasons. Pure: it consults lookPath for tool availability rather than the real PATH, so a
// missing tool is testable without uninstalling one.
//
// The ORDER matters. Tools are checked only for steps that are actually wanted, so a machine
// without osslsigncode can still sign macOS builds if Windows was explicitly skipped -- and
// credentials are checked before tools, because "you forgot to run under `op run`" is far more
// often the real problem than a missing binary, and it is the more useful thing to be told.
// Each step decides for itself whether it is wanted, in the same shape: planned, or a reason it
// cannot be. Split out so planSigning stays readable as the policy it is, rather than a wall of
// branches -- and so a new platform is added by writing one of these, not by editing a
// conditional four other platforms share.

func planMacOS(env signEnv) (bool, string) {
	switch env.MacOSIdentity {
	case skipSentinel:
		return false, ""
	case "":
		return false, "macOS: LFT_MACOS_IDENTITY is unset. Set it, or set it to \"skip\" to sign without macOS."
	default:
		return true, ""
	}
}

func planWindows(env signEnv) (bool, string) {
	// Either a PKCS#12 bundle or a key/cert pair; "skip" on any of them is the opt-out,
	// matching how the signing code reads them.
	if env.SignP12 == skipSentinel || env.SignKey == skipSentinel || env.SignCrt == skipSentinel {
		return false, ""
	}
	if env.SignP12 != "" || (env.SignKey != "" && env.SignCrt != "") {
		return true, ""
	}
	return false, "Windows: neither LFT_SIGN_P12 nor both of LFT_SIGN_KEY and LFT_SIGN_CRT are set. " +
		"Set them, or set one to \"skip\" to sign without Windows."
}

func planLinux(env signEnv) (bool, string) {
	if env.SkipGPG == "true" || env.GPGKey == skipSentinel {
		return false, ""
	}
	if env.GPGKey == "" && env.GPGSecret == "" {
		return false, "Linux: neither LFT_GPG_KEY nor LFT_GPG_SECRET is set. Set one, or set LFT_SKIP_GPG=true."
	}
	return true, ""
}

func planMinisign(env signEnv) (bool, string) {
	// Covers `lfr-tunnel --upgrade`, so a release without it tells every upgrading client the
	// download cannot be verified. That used to be a warning; it is a refusal now for the same
	// reason as the others -- nobody reads a warning at the end of a release.
	if env.SkipMinisign == "true" || env.MinisignKey == skipSentinel {
		return false, ""
	}
	if env.MinisignKey == "" {
		return false, "minisign: MINISIGN_SECRET_KEY is unset, so upgrading clients would be told the " +
			"download cannot be verified. Set it, or set LFT_SKIP_MINISIGN=true."
	}
	return true, ""
}

// planSigning decides what to sign, and refuses when a step was neither configured nor
// explicitly skipped.
//
// Returns the plan, an exit code (SignExitOK when the run may proceed) and the operator-facing
// reasons. Pure: it consults lookPath for tool availability rather than the real PATH, so a
// missing tool is testable without uninstalling one.
//
// The ORDER matters. Tools are checked only for steps that are actually wanted, so a machine
// without osslsigncode can still sign macOS builds if Windows was explicitly skipped -- and
// credentials are checked before tools, because "you forgot to run under `op run`" is far more
// often the real problem than a missing binary, and it is the more useful thing to be told.
func planSigning(env signEnv, lookPath func(string) (string, error)) (signPlan, int, []string) {
	var plan signPlan
	var problems []string

	for _, step := range []struct {
		into *bool
		plan func(signEnv) (bool, string)
	}{
		{&plan.MacOS, planMacOS},
		{&plan.Windows, planWindows},
		{&plan.Linux, planLinux},
		{&plan.Minisign, planMinisign},
	} {
		wanted, problem := step.plan(env)
		*step.into = wanted
		if problem != "" {
			problems = append(problems, problem)
		}
	}

	if len(problems) > 0 {
		return plan, SignExitCredentials, problems
	}

	// Only now, and only for what is actually wanted. Listed rather than ranged over a map so
	// the same broken machine reports the same tool first every time.
	for _, tool := range []struct {
		name   string
		needed bool
	}{
		{"codesign", plan.MacOS},
		{"osslsigncode", plan.Windows},
		{"gpg", plan.Linux},
	} {
		if !tool.needed {
			continue
		}
		if _, err := lookPath(tool.name); err != nil {
			problems = append(problems, fmt.Sprintf("%s is required but not on PATH: %v", tool.name, err))
		}
	}
	if len(problems) > 0 {
		return plan, SignExitToolMissing, problems
	}

	return plan, SignExitOK, nil
}

// describeSkips lists what a valid plan is deliberately NOT signing.
//
// Printed up front rather than as each step is reached: "Skipping Windows signing" buried in
// the middle of a successful-looking run is exactly how unsigned binaries reached a release.
func describeSkips(plan signPlan) []string {
	var out []string
	if !plan.MacOS {
		out = append(out, "macOS")
	}
	if !plan.Windows {
		out = append(out, "Windows")
	}
	if !plan.Linux {
		out = append(out, "Linux GPG")
	}
	if !plan.Minisign {
		out = append(out, "minisign")
	}
	return out
}

// lookPathFn is exec.LookPath, indirected so tests can simulate a machine without a tool.
var lookPathFn = exec.LookPath

// wouldSign renders one line of the dry-run report.
func wouldSign(planned bool) string {
	if planned {
		return "will be signed"
	}
	return "SKIPPED (explicitly)"
}
