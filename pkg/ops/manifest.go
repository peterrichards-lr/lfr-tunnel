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

// unknownValue is what build records when it cannot determine a version or commit. Shared with
// extractVersion so the two cannot drift, and so goconst is not reporting the same literal from
// four separate places in this package.
const unknownValue = "unknown"

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

	// Defaults is what the build baked into those artefacts via ldflags (#1692). Recorded so
	// publishing can refuse binaries with no default gateway; see CheckDefaultGateway for why
	// the record is the evidence rather than the binaries themselves.
	Defaults BuildDefaults `json:"defaults"`
}

// BuildDefaults is the set of deployment defaults compiled into the client binaries.
//
// All three are recorded, not just the gateway that is gated on, because this manifest is
// published into the downloads directory as provenance for what was shipped (#1279) -- and
// "which defaults were in the binaries a user downloaded" is exactly the question #1692 had to
// be answered by counting strings in a downloaded binary.
type BuildDefaults struct {
	ServerURL     string `json:"default_server_url"`
	StatusPageURL string `json:"default_status_page_url"`
	PortalURL     string `json:"default_portal_url"`
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
	// 0644 deliberately (#1408): deploy-clients copies this into the downloads directory as
	// provenance for what was published. Version, commit and build time -- not a secret.
	return os.WriteFile(BuildManifestPath(dir), append(data, '\n'), 0o644) //nolint:gosec
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

// CheckDefaultGateway reports whether the artefacts described by m have a default gateway
// compiled in, which is the condition for publishing them (#1692).
//
// **Why the manifest rather than the binaries.** Reading the artefacts is the more obvious
// check and it cannot work here, in either direction:
//
//   - Absence is unreadable. An empty `-X ...DefaultServerURL=` writes nothing, so a client
//     built with no default is byte-indistinguishable from one whose ldflags never arrived.
//     That invisibility is the defect itself -- it is why the same shape shipped three times
//     (#1188, #1256, #1692) and why reportDefault, which only says so during the build, was
//     ignored twice.
//   - Presence is unreliable. #1692's own evidence table records `https://tunnel.lfr-demo.se`
//     occurring 7 times in a build with the ldflags unset, because the URL reaches the binary
//     from other sources too; and `build` links with `-s -w`, which strips the symbol table, so
//     config.DefaultServerURL's value cannot be located and read back out. A grep would have
//     called that broken binary fine.
//
// So `build` records what it baked in and publishing checks the record. The record is exactly
// as trustworthy as the manifest that already gates publishing on version (#1279): dist/ with
// no current manifest cannot be published at all, so there is no path where the guard is
// evaluated against a manifest that does not describe the bytes being uploaded.
func CheckDefaultGateway(m BuildManifest) error {
	if m.Defaults.ServerURL != "" {
		return nil
	}
	return fmt.Errorf("these binaries have no default gateway compiled in, so every user has to "+
		"pass -server -- which pins the client and disables region selection and failover (#1691). "+
		"Set LFT_DEFAULT_SERVER_URL and rebuild with `lfr-tunnel-ops build`. (A dist/ built before "+
		"%s recorded its defaults reads the same way; rebuilding also fixes that.)", BuildManifestName)
}

// RequireDefaultGateway is the guard deploy-clients calls before anything leaves the machine.
//
// It exits rather than returning an error, for the same reason RequireCurrentDist does: what is
// downstream of here reaches every user of `--upgrade` and the downloads page. allowNoDefault
// downgrades it to a warning, because a deployment that genuinely wants no baked-in gateway is
// a real case -- it just has to be stated rather than being what happens when a shell is missing
// a variable (#1692).
func RequireDefaultGateway(m BuildManifest, command string, allowNoDefault bool) {
	err := CheckDefaultGateway(m)
	if err == nil {
		fmt.Printf("Clients in dist/ default to %s.\n", m.Defaults.ServerURL)
		return
	}

	if allowNoDefault {
		fmt.Printf("WARNING: %s\n", err)
		fmt.Printf("Continuing anyway because -allow-no-default was passed. %s will proceed with "+
			"clients that ask to be pointed at a gateway.\n", command)
		return
	}

	fmt.Fprintf(os.Stderr, "FATAL: refusing to %s clients with no default gateway: %v\n", command, err)
	fmt.Fprintf(os.Stderr, "Pass -allow-no-default to override if this deployment really wants none.\n")
	os.Exit(1)
}

// currentGitCommit returns the short commit, or "unknown". Never fatal: building outside a
// checkout is unusual but not a reason to refuse to build.
func currentGitCommit() string {
	out, err := RunCommandCaptureOutput("git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return unknownValue
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return unknownValue
	}
	return commit
}

// RequireBuildableDefaults refuses to compile clients with no default gateway (#1723).
//
// The same condition RequireDefaultGateway enforces at publish time, moved to the start of the
// pipeline. That guard is correct and was never bypassed -- but signing happens between build
// and publish, so without this the natural sequence was to build, then codesign, Authenticode-sign
// and GPG-sign a set of binaries that deploy-clients then refused. The cost of learning this
// late is a whole build-and-sign cycle, and on a machine where signing raises a biometric
// prompt, a person's attention as well.
//
// Takes the URL rather than a manifest because at this point nothing has been built, so there
// is no manifest to read.
func RequireBuildableDefaults(serverURL string, allowNoDefault bool) {
	if serverURL != "" {
		return
	}

	msg := "these clients would have no default gateway compiled in, so every user has to pass " +
		"-server -- which pins the client and disables region selection and failover (#1691)"

	if allowNoDefault {
		fmt.Printf("WARNING: %s\n", msg)
		fmt.Println("Continuing anyway because -allow-no-default was passed.")
		return
	}

	fmt.Fprintf(os.Stderr, "FATAL: refusing to build clients with no default gateway: %s\n", msg)
	fmt.Fprintln(os.Stderr, "Set LFT_DEFAULT_SERVER_URL, or add a client_defaults block to lfr-tunnel-ops.yaml:")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "    client_defaults:")
	fmt.Fprintln(os.Stderr, "      server_url: https://tunnel.example.com")
	fmt.Fprintln(os.Stderr, "      status_page_url: https://status.example.com")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Pass -allow-no-default to override if this deployment really wants none.")
	os.Exit(1)
}
