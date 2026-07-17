package ops

import (
	"fmt"
	"os"
	"regexp"
)

// BuildCommand handles the cross-compilation of client binaries.
func BuildCommand(args []string) {
	fmt.Println("Starting cross-platform build...")

	version := os.Getenv("VERSION")
	if version == "" {
		version = extractVersion()
	}

	targets := []struct {
		GOOS   string
		GOARCH string
		Output string
	}{
		{"linux", "amd64", "dist/lfr-tunnel-linux-amd64"},
		{"linux", "arm64", "dist/lfr-tunnel-linux-arm64"},
		{"darwin", "amd64", "dist/lfr-tunnel-darwin-amd64"},
		{"darwin", "arm64", "dist/lfr-tunnel-darwin-arm64"},
		{"windows", "amd64", "dist/lfr-tunnel-windows-amd64.exe"},
	}

	for _, target := range targets {
		env := []string{
			fmt.Sprintf("GOOS=%s", target.GOOS),
			fmt.Sprintf("GOARCH=%s", target.GOARCH),
		}

		ldflags := fmt.Sprintf("-s -w -X lfr-tunnel/pkg/config.Version=%s", version)

		err := RunCommandWithEnv(env, "go", "build", "-ldflags", ldflags, "-trimpath", "-o", target.Output, "./cmd/lfr-tunnel")
		CheckFatal(err, fmt.Sprintf("Failed to build for %s/%s", target.GOOS, target.GOARCH))
	}

	fmt.Println("Build complete!")
}

func extractVersion() string {
	content, err := os.ReadFile("pkg/config/version.go")
	if err != nil {
		fmt.Println("Warning: Could not read version.go, defaulting to unknown")
		return "unknown"
	}
	re := regexp.MustCompile(`Version\s*=\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(string(content))
	if len(matches) > 1 {
		return matches[1]
	}
	return "unknown"
}
