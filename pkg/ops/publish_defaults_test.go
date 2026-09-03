package ops

import (
	"os"
	"strings"
	"testing"
)

// The guard from #1692: a client binary with no default gateway must not be publishable.
//
// The user-visible failure is that `lfr-tunnel -subdomain x` refuses to start until the user
// passes -server, which pins the client and disables region selection and failover (#1691). The
// build has warned about this since #1256 and the warning was ignored twice, because it scrolls
// past in a build that succeeds. These tests cover the refusal that replaces the warning.

func TestCheckDefaultGateway(t *testing.T) {
	cases := []struct {
		name     string
		defaults BuildDefaults
		wantErr  bool
	}{
		{
			name:     "a default gateway passes",
			defaults: BuildDefaults{ServerURL: "https://tunnel.example.com"},
		},
		{
			// The #1692 build: ldflags never reached the compiler, so all three are empty.
			name:    "no defaults at all fails",
			wantErr: true,
		},
		{
			// The gateway is the only one gated on. A deployment with no status page or portal
			// is ordinary; a client with no gateway is unusable as downloaded.
			name:     "only the gateway is required",
			defaults: BuildDefaults{ServerURL: "https://tunnel.example.com"},
		},
		{
			// The trap this guard exists to survive: everything else looks populated, so the
			// build output reads as a normal one, and the one value that matters is empty.
			name:     "other defaults present but no gateway fails",
			defaults: BuildDefaults{StatusPageURL: "https://status.example.com", PortalURL: "https://portal.example.com"},
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDefaultGateway(BuildManifest{Defaults: tc.defaults})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			// The message has to name the variable to set, because the person reading it is
			// mid-release and the fix is a rebuild with one env var, not a code change.
			if !strings.Contains(err.Error(), "LFT_DEFAULT_SERVER_URL") {
				t.Errorf("error does not say which variable to set: %q", err)
			}
		})
	}
}

// TestBuildManifestCarriesDefaults is the half of the guard that lives on disk: the check reads
// what `build` recorded, so a manifest that drops the record on the way through JSON makes every
// legitimately-built dist/ look like it has no gateway.
func TestBuildManifestCarriesDefaults(t *testing.T) {
	dir := t.TempDir()
	want := BuildManifest{
		Version:       "v1.48.22",
		SourceVersion: "v1.48.22",
		Artifacts:     []string{"lfr-tunnel-linux-amd64"},
		Defaults: BuildDefaults{
			ServerURL:     "https://tunnel.example.com",
			StatusPageURL: "https://status.example.com",
			PortalURL:     "https://portal.example.com",
		},
	}
	if err := WriteBuildManifest(dir, want); err != nil {
		t.Fatalf("WriteBuildManifest: %v", err)
	}

	got, err := ReadBuildManifest(dir)
	if err != nil {
		t.Fatalf("ReadBuildManifest: %v", err)
	}
	if got.Defaults != want.Defaults {
		t.Errorf("defaults did not survive the round trip: got %+v want %+v", got.Defaults, want.Defaults)
	}
	if err := CheckDefaultGateway(got); err != nil {
		t.Errorf("a manifest recording a gateway was rejected: %v", err)
	}
}

// TestBuildManifestWithoutDefaultsIsNotPublishable covers dist/ built before this record existed,
// and dist/ built by a `build` that stopped recording. Both read as "no gateway" and both are
// refused: the record is the only evidence there is, so an absent record cannot be read as
// consent. -allow-no-default and a rebuild are the two ways out, and the message says so.
func TestBuildManifestWithoutDefaultsIsNotPublishable(t *testing.T) {
	dir := t.TempDir()
	if err := WriteBuildManifest(dir, BuildManifest{Version: "v1.48.12", SourceVersion: "v1.48.12"}); err != nil {
		t.Fatalf("WriteBuildManifest: %v", err)
	}
	m, err := ReadBuildManifest(dir)
	if err != nil {
		t.Fatalf("ReadBuildManifest: %v", err)
	}

	err = CheckDefaultGateway(m)
	if err == nil {
		t.Fatal("a manifest with no recorded defaults was accepted for publishing")
	}
	if !strings.Contains(err.Error(), BuildManifestName) {
		t.Errorf("error does not mention that an older %s reads the same way: %q", BuildManifestName, err)
	}
}

// TestParseDeployClientsFlagsAllowNoDefault covers the opt-in itself. A real FlagSet for the same
// reason `sign` uses one (#1279): `-allow-no-defualt` must be rejected rather than silently
// publishing the thing the flag was meant to authorise.
func TestParseDeployClientsFlagsAllowNoDefault(t *testing.T) {
	flags, err := parseDeployClientsFlags("deploy-clients", nil)
	if err != nil {
		t.Fatalf("parsing no arguments: %v", err)
	}
	if flags.allowNoDefault {
		t.Error("allowNoDefault defaults to true -- the guard would never fire")
	}

	flags, err = parseDeployClientsFlags("deploy-clients", []string{"-allow-no-default", "-target", "central"})
	if err != nil {
		t.Fatalf("parsing -allow-no-default: %v", err)
	}
	if !flags.allowNoDefault {
		t.Error("-allow-no-default was accepted but not recorded")
	}
	if flags.target != "central" {
		t.Errorf("target = %q, want central -- the new flag must not disturb the others", flags.target)
	}
	// The two overrides are independent: -allow-stale is about which version is in dist/,
	// -allow-no-default about what was compiled into it. Artefacts can be current and still
	// have no gateway, which is exactly the #1692 build.
	if flags.allowStale || flags.skipVerify {
		t.Errorf("-allow-no-default turned on another override: %+v", flags)
	}

	if _, err := parseDeployClientsFlags("deploy-clients", []string{"-allow-no-defualt"}); err == nil {
		t.Error("a misspelled override was accepted")
	}
}

// TestBuildRecordsTheDefaultsItBaked is the other half of the wiring, and static for the same
// reason: BuildCommand cross-compiles five targets, so there is nothing to call in a test.
//
// If `build` stops recording what it baked in, the guard fails closed -- every dist/ reads as
// having no gateway -- which is loud, but it makes -allow-no-default the routine answer, and a
// guard that is routinely overridden is the warning this replaced.
func TestBuildRecordsTheDefaultsItBaked(t *testing.T) {
	src, err := os.ReadFile("build.go")
	if err != nil {
		t.Fatalf("could not read build.go: %v", err)
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")

	if !strings.Contains(text, "Defaults: BuildDefaults{") {
		t.Fatal("BuildCommand's manifest no longer records BuildDefaults -- deploy-clients reads " +
			"that record to decide whether these clients have a gateway (#1692)")
	}
	if !strings.Contains(text, "ServerURL:     serverURL") {
		t.Error("the recorded gateway is no longer the serverURL that went into the ldflags -- the " +
			"record has to describe the bytes, or it is worse than no record at all")
	}
}

// TestDeployClientsCommandRequiresADefaultGateway checks the wiring, statically, in the shape
// TestDeployCommandHasNoCheckFatalAfterPowerRestore established for this file.
//
// Read from source rather than exercised, because DeployClientsCommand exits the process and
// uploads over SSH; there is no seam to call it through. The failure it guards against is a
// guard that exists and is never called -- which looks exactly like a guard that works, on
// every deploy where the defaults happened to be set.
func TestDeployClientsCommandRequiresADefaultGateway(t *testing.T) {
	src, err := os.ReadFile("deploy.go")
	if err != nil {
		t.Fatalf("could not read deploy.go: %v", err)
	}
	// Normalised for the same reason as in deploy_power_test.go: git checks this file out with
	// CRLF on Windows, and a scan comparing a line to "}" exactly never matched there (#1481).
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")

	startAt := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func DeployClientsCommand(") {
			startAt = i
			break
		}
	}
	if startAt < 0 {
		t.Fatal("could not find DeployClientsCommand in deploy.go -- if it moved, move this guard " +
			"with it rather than deleting it")
	}
	endAt := len(lines)
	for i := startAt + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			endAt = i
			break
		}
	}

	guardAt, uploadAt := -1, -1
	for i := startAt; i < endAt; i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if guardAt < 0 && strings.Contains(line, "RequireDefaultGateway(") {
			guardAt = i
		}
		if uploadAt < 0 && strings.Contains(line, `"scp"`) {
			uploadAt = i
		}
	}

	if guardAt < 0 {
		t.Fatal("DeployClientsCommand does not call RequireDefaultGateway -- clients with no " +
			"default gateway would be publishable again, and the build's warning about it has " +
			"already been ignored twice (#1188, #1256, #1692)")
	}
	if uploadAt < 0 {
		t.Fatal("could not find the scp upload in DeployClientsCommand -- if the upload changed, " +
			"update this guard's anchor rather than deleting it")
	}
	if guardAt > uploadAt {
		t.Errorf("RequireDefaultGateway is called at line %d, after the upload at line %d -- "+
			"refusing to publish has to happen before the bytes leave the machine", guardAt+1, uploadAt+1)
	}
}
