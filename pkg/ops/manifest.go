package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildManifestName is the record `build` leaves in dist/ so that `sign` and `deploy-clients`
// can tell whether what is sitting there came from the current source (#1279).
//
// Before this existed, both commands operated on whatever happened to be in dist/. Twice in one
// day the downloads page was populated with binaries from an earlier version while the release,
// the tag, the gateway and latest_version all agreed on v1.48.3. A user running --upgrade was
// told a new version was available, downloaded it, had the signature and integrity verified,
// installed it, and was still on the old version. Nothing in that chain was wrong except the
// bytes -- and every step reported success.
const BuildManifestName = "build-manifest.json"

// BuildManifest records what a build produced. Deliberately small: this exists to answer "are
// these artefacts from this source tree" and nothing more.
type BuildManifest struct {
	// Version is what was embedded in the binaries via ldflags. Usually the same as
	// SourceVersion, but a VERSION env override changes it, which is exactly why both are
	// recorded rather than one being inferred from the other.
	Version string `json:"version"`

	// SourceVersion is what pkg/config/version.go said when the build ran. This is the field
	// the staleness check compares, because it tracks the source tree rather than whatever the
	// operator asked to embed. Comparing Version instead would make every legitimate VERSION
	// override look like stale artefacts.
	SourceVersion string `json:"source_version"`

	// Commit is the short git commit, or "unknown" outside a repository. Informational: it is
	// reported but never gates anything, since a build from a dirty tree is normal here.
	Commit string `json:"commit"`

	BuiltAt time.Time `json:"built_at"`

	// Artifacts are the files build wrote, so a partially-cleaned dist/ is detectable.
	Artifacts []string `json:"artifacts"`
}

// BuildManifestPath is where the manifest lives for a given dist directory.
func BuildManifestPath(dir string) string {
	return filepath.Join(dir, BuildManifestName)
}

// WriteBuildManifest records a completed build.
func WriteBuildManifest(dir string, m BuildManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(BuildManifestPath(dir), append(data, '\n'), 0o644)
}

// ReadBuildManifest loads the manifest, or reports why it could not.
func ReadBuildManifest(dir string) (BuildManifest, error) {
	var m BuildManifest
	data, err := os.ReadFile(BuildManifestPath(dir))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s is not readable as a build manifest: %w", BuildManifestPath(dir), err)
	}
	return m, nil
}

// CheckDistCurrent reports whether dir holds artefacts built from sourceVersion.
//
// Separate from RequireCurrentDist so the decision is testable without a process exit. The
// returned manifest is usable even when err is non-nil, so a caller overriding the check can
// still report what it found.
func CheckDistCurrent(dir, sourceVersion string) (BuildManifest, error) {
	m, err := ReadBuildManifest(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("%s has no %s, so there is no record of what these artefacts are "+
				"or when they were built -- run `lfr-tunnel-ops build` first", dir, BuildManifestName)
		}
		return m, err
	}

	if m.SourceVersion == "" {
		return m, fmt.Errorf("%s records no source version, so it cannot be checked against "+
			"pkg/config/version.go -- rebuild with `lfr-tunnel-ops build`", BuildManifestPath(dir))
	}

	if m.SourceVersion != sourceVersion {
		return m, fmt.Errorf("%s was built from %s but pkg/config/version.go now says %s -- "+
			"these are last release's artefacts, rebuild with `lfr-tunnel-ops build`",
			dir, m.SourceVersion, sourceVersion)
	}

	var missing []string
	for _, name := range m.Artifacts {
		if !fileExists(filepath.Join(dir, name)) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return m, fmt.Errorf("%s is missing %d artefact(s) the manifest says were built (%s) -- "+
			"rebuild with `lfr-tunnel-ops build`", dir, len(missing), strings.Join(missing, ", "))
	}

	return m, nil
}

// RequireCurrentDist is the guard `sign` and `deploy-clients` call before touching dist/.
//
// It exits rather than returning an error: both callers reach users, and continuing past a
// staleness failure is the thing this is here to stop. allowStale downgrades it to a warning,
// because shipping an earlier version's binaries is occasionally the right call -- it just has
// to be a deliberate act rather than the default.
func RequireCurrentDist(dir, command string, allowStale bool) BuildManifest {
	sourceVersion := extractVersion()

	m, err := CheckDistCurrent(dir, sourceVersion)
	if err == nil {
		fmt.Printf("%s/ holds %s (built %s from commit %s).\n",
			dir, m.Version, m.BuiltAt.Format(time.RFC3339), m.Commit)
		if m.Version != m.SourceVersion {
			fmt.Printf("Note: embedded version %s differs from source %s -- a VERSION override was used at build time.\n",
				m.Version, m.SourceVersion)
		}
		return m
	}

	if allowStale {
		fmt.Printf("WARNING: %s\n", err)
		fmt.Printf("Continuing anyway because -allow-stale was passed. %s will publish whatever is in %s/.\n",
			command, dir)
		return m
	}

	fmt.Fprintf(os.Stderr, "FATAL: refusing to %s stale artefacts: %v\n", command, err)
	fmt.Fprintf(os.Stderr, "Pass -allow-stale to override if you really mean to %s these.\n", command)
	os.Exit(1)
	return m // unreachable; keeps the compiler happy
}

// currentGitCommit returns the short commit, or "unknown". Never fatal: building outside a
// checkout is unusual but not a reason to refuse to build.
func currentGitCommit() string {
	out, err := RunCommandCaptureOutput("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return "unknown"
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "unknown"
	}
	return commit
}
