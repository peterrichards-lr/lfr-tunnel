package ops

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// BuildCommand handles the cross-compilation of client binaries.
func BuildCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops build [-allow-no-default]")
		fmt.Println("\nCross-compiles client binaries for linux/amd64, linux/arm64, darwin/amd64,")
		fmt.Println("darwin/arm64, and windows/amd64 into dist/. Set VERSION to override the")
		fmt.Println("version embedded via ldflags (defaults to pkg/config/version.go's Version).")
		fmt.Println("\nThe gateway, status page and portal URLs baked into the clients come from")
		fmt.Println("LFT_DEFAULT_SERVER_URL / LFT_DEFAULT_STATUS_PAGE_URL / LFT_DEFAULT_PORTAL_URL,")
		fmt.Println("falling back to the client_defaults block in lfr-tunnel-ops.yaml.")
		fmt.Println("\nRefuses to build clients with no default gateway, because they force every")
		fmt.Println("user to pass -server, which pins them and disables failover (#1692).")
		fmt.Println("-allow-no-default overrides that, for a deployment that wants none.")
		return
	}

	// A real FlagSet rather than scanning args, for the reason sign records: `-allow-no-defalt`
	// must be rejected, not silently ignored into building exactly what the flag was meant to
	// authorise.
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	allowNoDefaultFlag := fs.Bool("allow-no-default", false, "build clients with no default gateway compiled in")
	CheckFatal(fs.Parse(args), "Failed to parse arguments")
	allowNoDefault := *allowNoDefaultFlag

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
	// Environment first, then lfr-tunnel-ops.yaml (#1723). The release workflow sets these
	// from repository variables and must keep winning; the config file exists so a LOCAL build
	// has any source at all. Before this, it had none: `ops build` on a laptop silently
	// produced clients with no gateway -- the #1692 condition all over again -- because three
	// environment variables nothing mentions were the only way to supply them.
	fileDefaults, err := LoadClientDefaults()
	CheckFatal(err, "Failed to read client defaults from the ops config")

	defaults := ResolveClientDefaults(fileDefaults)
	serverURL := defaults.ServerURL
	statusPageURL := defaults.StatusPageURL
	portalURL := defaults.PortalURL

	// Say which defaults are going in before compiling. An empty value is invisible in the
	// finished binary, which is how a release shipped with none and nobody noticed.
	reportDefault("DefaultServerURL", serverURL, "clients will ask to be pointed at a gateway")
	reportDefault("DefaultStatusPageURL", statusPageURL, "no status-page hint when a gateway looks unreachable")
	reportDefault("DefaultPortalURL", portalURL, "browser login falls back to the gateway, which serves the portal too")

	// Refuse here, before compiling, rather than leaving it to deploy-clients (#1723).
	// The guard downstream is correct and was never bypassed, but signing sits BETWEEN the two
	// in the documented order, so the natural sequence was to build, codesign, Authenticode-sign
	// and GPG-sign a set of binaries that the very next command then refused. Failing at the
	// start costs seconds; failing at publish costs the whole cycle.
	RequireBuildableDefaults(serverURL, allowNoDefault)

	sourceVersion := extractVersion()
	var built []string

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
		built = append(built, filepath.Base(target.Output))
	}

	// Written last, after every target has succeeded, and that ordering is the point (#1279).
	// This command's output is routinely piped, and a pipe that closes early -- `| head -4` is
	// the case that actually happened -- kills the process with SIGPIPE before it compiles
	// anything. Nothing in-process can catch that. But a build that dies part-way then leaves
	// the PREVIOUS manifest in place, so `sign` and `deploy-clients` see a source version that
	// no longer matches version.go and refuse. The absent manifest is the signal, rather than
	// this command having to survive its own death.
	manifest := BuildManifest{
		Version:       version,
		SourceVersion: sourceVersion,
		Commit:        currentGitCommit(),
		BuiltAt:       time.Now().UTC(),
		Artifacts:     built,
		// What went in via ldflags, recorded rather than only reported (#1692). An empty
		// default is invisible in the finished binary, so this is the only place the fact
		// survives the build -- and deploy-clients refuses to publish clients with no
		// gateway on the strength of it.
		Defaults: BuildDefaults{
			ServerURL:     serverURL,
			StatusPageURL: statusPageURL,
			PortalURL:     portalURL,
		},
	}
	CheckFatal(WriteBuildManifest("dist", manifest), "Failed to write build manifest")

	fmt.Printf("Build complete! dist/ now holds %s (source %s, commit %s).\n",
		manifest.Version, manifest.SourceVersion, manifest.Commit)
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
		return unknownValue
	}
	re := regexp.MustCompile(`Version\s*=\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(string(content))
	if len(matches) > 1 {
		return matches[1]
	}
	return unknownValue
}
