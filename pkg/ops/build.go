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

	const (
		archAmd64 = "amd64"
		archArm64 = "arm64"
	)

	targets := []struct {
		GOOS   string
		GOARCH string
		Output string
	}{
		{"linux", archAmd64, "dist/lfr-tunnel-linux-" + archAmd64},
		{"linux", archArm64, "dist/lfr-tunnel-linux-" + archArm64},
		{"darwin", archAmd64, "dist/lfr-tunnel-darwin-" + archAmd64},
		{"darwin", archArm64, "dist/lfr-tunnel-darwin-" + archArm64},
		{"windows", archAmd64, "dist/lfr-tunnel-windows-" + archAmd64 + ".exe"},
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
