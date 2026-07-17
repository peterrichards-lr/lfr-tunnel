package ops

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunCommand executes a local command and prints output to stdout/stderr.
func RunCommand(name string, args ...string) error {
	fmt.Printf("==> Executing: %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunCommandWithEnv executes a command with additional environment variables.
func RunCommandWithEnv(env []string, name string, args ...string) error {
	fmt.Printf("==> Executing (with env): %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
