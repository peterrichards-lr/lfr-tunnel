package ops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunCommand executes a local command and prints output to stdout/stderr.
// secretFlags names flags whose *following* argument is a credential. The echo below prints
// every argument it is given, so without this a caller that passes a password inline leaks it
// to stdout -- and from there to scrollback, a piped log, or a CI transcript (#1555).
//
// This lives in the runner rather than in each caller deliberately. Redacting at the call site
// only fixes the callers that exist today; the next one to pass a credential would reintroduce
// the leak silently, which is exactly how this one survived.
//
// Redaction is defence in depth, NOT the primary fix. An argument is visible in the process
// table to any local user for as long as the command runs, and no amount of not-printing it
// changes that. Prefer the file-based option a tool offers (osslsigncode -readpass, gpg
// --passphrase-file) so the secret never reaches argv at all.
var secretFlags = map[string]struct{}{
	"-pass":        {},
	"-p":           {},
	"--password":   {},
	"--passphrase": {},
	"--token":      {},
	"--secret":     {},
	"--access-key": {},
}

// redactArgs renders a command's arguments for display, masking the value after any flag in
// secretFlags. It never mutates the arguments actually passed to the process.
func redactArgs(args []string) string {
	shown := make([]string, len(args))
	maskNext := false
	for i, a := range args {
		if maskNext {
			shown[i] = "<redacted>"
			maskNext = false
			continue
		}
		shown[i] = a
		if _, ok := secretFlags[a]; ok {
			maskNext = true
		}
	}
	return strings.Join(shown, " ")
}

func RunCommand(name string, args ...string) error {
	fmt.Printf("==> Executing: %s %s\n", name, redactArgs(args))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunCommandWithEnv executes a command with additional environment variables.
func RunCommandWithEnv(env []string, name string, args ...string) error {
	fmt.Printf("==> Executing (with env): %s %s\n", name, redactArgs(args))
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunCommandCaptureOutput executes a local command and returns its stdout as a string,
// for callers that need to parse the result (e.g. `aws ec2 describe-instances --output
// json`) rather than just check pass/fail. Stderr still streams to the terminal so
// failures remain visible.
func RunCommandCaptureOutput(name string, args ...string) (string, error) {
	fmt.Printf("==> Executing: %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

// CheckFatal exits the program if err is not nil.
func CheckFatal(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %s: %v\n", msg, err)
		os.Exit(1)
	}
}

// GetEnvOrDefault returns an environment variable or a default value.
func GetEnvOrDefault(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

// IsHelpRequest reports whether args contains -h, -help, or --help anywhere. Every
// Command function with side effects (builds, signs, or touches the network/VPS) must
// check this FIRST and print usage instead of running -- main.go's top-level usage text
// promises `lfr-tunnel-ops <command> -help` works, but that's only true if each command
// actually honors it; args isn't parsed for a help flag automatically.
func IsHelpRequest(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}
