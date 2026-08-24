package ops

import (
	"fmt"
	"os"
	"regexp"
)

// BuildCommand handles the cross-compilation of client binaries.
func BuildCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops build")
		fmt.Println("\nCross-compiles client binaries for linux/amd64, linux/arm64, darwin/amd64,")
		fmt.Println("darwin/arm64, and windows/amd64 into dist/. Set VERSION to override the")
		fmt.Println("version embedded via ldflags (defaults to pkg/config/version.go's Version).")
		return
	}

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

	// Deployment defaults are baked in at build time, exactly as the Makefile and the
	// release workflow already do (#1188). This path was missed, and it is the one that
	// matters most: `build` populates dist/, which `deploy-clients` publishes to the
	// portal's downloads directory, so a client fetched by `lfr-tunnel --upgrade` had no
	// default gateway even once the release workflow was building correctly (#1256).
	//
	// Unset stays supported and means the same here as everywhere else: the client asks to
	// be pointed at a gateway rather than guessing one.
	serverURL := GetEnvOrDefault("LFT_DEFAULT_SERVER_URL", "")
	statusPageURL := GetEnvOrDefault("LFT_DEFAULT_STATUS_PAGE_URL", "")
	portalURL := GetEnvOrDefault("LFT_DEFAULT_PORTAL_URL", "")

	// Say which defaults are going in before compiling. An empty value is invisible in the
	// finished binary, which is how a release shipped with none and nobody noticed.
	reportDefault("DefaultServerURL", serverURL, "clients will ask to be pointed at a gateway")
	reportDefault("DefaultStatusPageURL", statusPageURL, "no status-page hint when a gateway looks unreachable")
	reportDefault("DefaultPortalURL", portalURL, "browser login falls back to the gateway, which serves the portal too")

	for _, target := range targets {
		env := []string{
			fmt.Sprintf("GOOS=%s", target.GOOS),
			fmt.Sprintf("GOARCH=%s", target.GOARCH),
		}

		ldflags := fmt.Sprintf(
			"-s -w -X lfr-tunnel/pkg/config.Version=%s"+
				" -X lfr-tunnel/pkg/config.DefaultServerURL=%s"+
				" -X lfr-tunnel/pkg/config.DefaultStatusPageURL=%s"+
				" -X lfr-tunnel/pkg/config.DefaultPortalURL=%s",
			version, serverURL, statusPageURL, portalURL)

		err := RunCommandWithEnv(env, "go", "build", "-ldflags", ldflags, "-trimpath", "-o", target.Output, "./cmd/lfr-tunnel")
		CheckFatal(err, fmt.Sprintf("Failed to build for %s/%s", target.GOOS, target.GOARCH))
	}

	fmt.Println("Build complete!")
}

// formatDefault renders one build-time deployment default, naming the consequence when it
// is empty. Each of the three means something different when unset, so the explanation
// belongs with the value rather than being shared -- an unset portal URL does not make
// clients ask for a gateway, and saying so would be worse than saying nothing.
func formatDefault(name, value, whenEmpty string) string {
	if value == "" {
		return fmt.Sprintf("  %s: (unset -- %s)", name, whenEmpty)
	}
	return fmt.Sprintf("  %s: %s", name, value)
}

func reportDefault(name, value, whenEmpty string) {
	fmt.Println(formatDefault(name, value, whenEmpty))
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
