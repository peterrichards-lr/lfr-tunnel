package ops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Resolving 1Password references so `sign` works when run the obvious way (#1978).
//
// Every signing credential in this project's shell profile is an `op://` reference, so a bare
// `lfr-tunnel-ops sign` cannot work: the values arrive as the literal string "op://Employee/...".
// The tool used to refuse with exit 3 naming the variable, which is true but not the reason --
// the variable IS set, it just has not been resolved. Cutting v1.48.35 lost a cycle to exactly
// that, and then a second one to a half-fix: materialising LFT_SIGN_KEY/LFT_SIGN_CRT by hand
// left LFT_SIGN_PASS as a raw reference, which surfaced as osslsigncode's "maybe wrong password".
//
// `op run --` resolves all of them, including writing the key and certificate into temp .pem
// files and substituting their paths -- which is why it is the whole answer and not part of one.
//
// So rather than document this, the tool does it: if any credential still holds an op://
// reference, re-exec under `op run --`.

// opRunMarker stops the re-exec recursing.
//
// If `op run` substitutes correctly the child sees real values and never re-execs. If it does
// NOT -- no session, a reference to an item that does not exist -- the child would see the same
// op:// values and exec again forever. The marker turns that into one clear failure instead.
const opRunMarker = "LFT_OPS_UNDER_OP_RUN"

// defaultOpAccount is the account holding the Employee vault the signing items live in.
//
// This machine has a personal account registered as well, and `op` will otherwise search it and
// report the item as missing -- which reads as a broken reference rather than the wrong account.
// LFT_OP_ACCOUNT overrides it for anyone whose 1Password is arranged differently.
const defaultOpAccount = "liferayinc.1password.com"

// SigningCredentialVars is every environment variable `sign` reads a secret from.
//
// Deliberately a list rather than a prefix scan of the environment: an unrelated op:// reference
// in the shell (a database URL, say) must not drag signing under `op run`, and a scan would also
// leak which unrelated secrets exist into this tool's error messages.
var SigningCredentialVars = []string{
	"LFT_MACOS_IDENTITY",
	"LFT_SIGN_P12",
	"LFT_SIGN_KEY",
	"LFT_SIGN_CRT",
	"LFT_SIGN_PASS",
	"LFT_GPG_KEY",
	"LFT_GPG_SECRET",
	"LFT_GPG_PASS",
	"MINISIGN_SECRET_KEY",
	"MINISIGN_KEY_PASSWORD",
}

// UnresolvedOpRefs returns the names of vars still holding an op:// reference.
//
// Names only, never values: the names are what the operator needs and the values are secrets
// (or references that identify a vault item), so they must not reach a log or a CI transcript.
func UnresolvedOpRefs(vars []string, getenv func(string) string) []string {
	var found []string
	for _, name := range vars {
		if strings.HasPrefix(getenv(name), "op://") {
			found = append(found, name)
		}
	}
	return found
}

// UnderOpRun reports whether this process was already re-executed under `op run`.
func UnderOpRun(getenv func(string) string) bool {
	return getenv(opRunMarker) == "1"
}

// OpRunCommand builds the argv that re-runs this binary under `op run`.
//
// `--account` is passed when one is known, because this machine has two 1Password accounts and
// the signing items live in the Employee vault of the work one. Without it `op` may resolve
// against the personal account and report the item as missing, which reads like a broken
// reference rather than the wrong account.
func OpRunCommand(self string, args []string, account string) []string {
	cmd := []string{"op", "run"}
	if account != "" {
		cmd = append(cmd, "--account", account)
	}
	cmd = append(cmd, "--")
	return append(append(cmd, self), args...)
}

// ResolveOpRefsOrReexec re-execs under `op run` when credentials are unresolved, and returns
// normally when there is nothing to do.
//
// It never returns after a successful re-exec: the child's exit code becomes this process's.
func ResolveOpRefsOrReexec(args []string) {
	refs := UnresolvedOpRefs(SigningCredentialVars, os.Getenv)
	if len(refs) == 0 {
		return
	}

	if UnderOpRun(os.Getenv) {
		fmt.Fprintln(os.Stderr, "=== Signing REFUSED ===")
		fmt.Fprintf(os.Stderr, "  - Already running under `op run`, but these are still op:// references: %s\n",
			strings.Join(refs, ", "))
		fmt.Fprintln(os.Stderr, "    op did not substitute them. Check the reference points at an item that")
		fmt.Fprintln(os.Stderr, "    exists in an account you are signed in to: `op whoami`, then")
		fmt.Fprintln(os.Stderr, "    `op read \"<the op:// value>\"` to see the actual error.")
		os.Exit(SignExitCredentials)
	}

	if _, err := exec.LookPath("op"); err != nil {
		fmt.Fprintln(os.Stderr, "=== Signing REFUSED ===")
		fmt.Fprintf(os.Stderr, "  - These credentials are op:// references but the 1Password CLI is not on PATH: %s\n",
			strings.Join(refs, ", "))
		fmt.Fprintln(os.Stderr, "    Install `op`, or export the resolved values directly.")
		os.Exit(SignExitCredentials)
	}

	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}

	account := GetEnvOrDefault("LFT_OP_ACCOUNT", defaultOpAccount)
	argv := OpRunCommand(self, args, account)

	fmt.Printf("Credentials are 1Password references (%s).\n", strings.Join(refs, ", "))
	fmt.Printf("Re-running under: %s\n", strings.Join(argv[:len(argv)-len(args)], " "))

	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is built from this binary's own path and args
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), opRunMarker+"=1")

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Failed to run under `op run`: %v\n", err)
		os.Exit(SignExitSetup)
	}
	os.Exit(SignExitOK)
}
