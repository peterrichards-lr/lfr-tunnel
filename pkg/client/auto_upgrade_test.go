package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jedisct1/go-minisign"
)

// resetAutoUpgradeState clears the process-wide mid-session flag.
//
// Needed at the START of each test as well as after: RegisterTunnel sets the flag on every
// successful registration, and several tests in this package register. Without this, whether
// an auto-upgrade test saw a "tunnel running" would depend on test ordering -- an assertion
// satisfied by the wrong cause.
func resetAutoUpgradeState(t *testing.T) {
	t.Helper()
	ResetTunnelEstablishedForTest()
	t.Cleanup(ResetTunnelEstablishedForTest)
}

// Default OFF is the decision, so it is the first thing asserted.
func TestShouldAutoUpgrade(t *testing.T) {
	resetAutoUpgradeState(t)

	newer := &ServerVersionInfo{LatestVersion: "v1.48.0"}

	cases := []struct {
		name       string
		enabled    bool
		current    string
		info       *ServerVersionInfo
		want       bool
		wantReason AutoUpgradeSkipReason
	}{
		{
			// The whole opt-in decision in one case: a newer version is advertised and
			// nothing happens, because nobody asked for it.
			name:    "off by default even when a newer version exists",
			enabled: false, current: "v1.40.0", info: newer,
			want: false, wantReason: SkipNotEnabled,
		},
		{
			name:    "opted in with a newer version",
			enabled: true, current: "v1.40.0", info: newer,
			want: true,
		},
		{
			name:    "opted in but already current",
			enabled: true, current: "v1.48.0", info: newer,
			want: false, wantReason: SkipAlreadyCurrent,
		},
		{
			name:    "opted in but ahead of the gateway",
			enabled: true, current: "v1.49.0", info: newer,
			want: false, wantReason: SkipAlreadyCurrent,
		},
		{
			// A source build orders below every release, so an unguarded comparison would
			// replace a developer's own build with a release binary.
			name:    "development build is never replaced",
			enabled: true, current: "dev", info: newer,
			want: false, wantReason: SkipDevBuild,
		},
		{
			name:    "gateway unreachable",
			enabled: true, current: "v1.40.0", info: nil,
			want: false, wantReason: SkipNoGatewayInfo,
		},
		{
			name:    "gateway advertised no version",
			enabled: true, current: "v1.40.0", info: &ServerVersionInfo{},
			want: false, wantReason: SkipNoGatewayInfo,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := ShouldAutoUpgrade(tc.enabled, tc.current, tc.info)
			if got != tc.want {
				t.Errorf("ShouldAutoUpgrade = %v (%s), want %v", got, reason, tc.want)
			}
			if !tc.want && reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// The mid-session rule, as a RUNTIME guard rather than a property of the call site.
//
// The flag is set through the real registration path -- RegisterTunnel against a gateway that
// accepts -- not by poking the variable, so this fails if the production code stops recording
// it where a tunnel actually begins.
func TestAutoUpgradeRefusesOnceATunnelIsRunning(t *testing.T) {
	resetAutoUpgradeState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(RegisterResponse{
			Status:          "success",
			SessionToken:    "tok",
			SubdomainPrefix: "sub",
			Remotes:         []string{"R:127.0.0.1:1:localhost:8080"},
		}); err != nil {
			t.Errorf("encoding the registration response: %v", err)
		}
	}))
	defer srv.Close()

	info := &ServerVersionInfo{LatestVersion: "v1.48.0"}

	// Before the tunnel: opted in, newer version available, so it would run.
	if ok, reason := ShouldAutoUpgrade(true, "v1.40.0", info); !ok {
		t.Fatalf("precondition: expected an upgrade to be due before any tunnel exists, got %q", reason)
	}

	if _, err := RegisterTunnel(srv.URL, "token", "sub", "", []PortMapping{{LocalPort: 8080}}, 0, "", nil, "linux", "", ""); err != nil {
		t.Fatalf("registering: %v", err)
	}

	ok, reason := ShouldAutoUpgrade(true, "v1.40.0", info)
	if ok {
		t.Fatal("an automatic upgrade was allowed while a tunnel is running -- this would replace the binary under a live session")
	}
	// Assert the CAUSE. Every other skip reason also returns false, so "false" alone is
	// satisfied by five unrelated conditions.
	if reason != SkipTunnelRunning {
		t.Errorf("reason = %q, want %q", reason, SkipTunnelRunning)
	}
}

// autoUpgradeGateway serves the gateway half of an upgrade. signMode selects whether a valid
// signature is published at all, which is the axis these tests turn on.
func autoUpgradeGateway(t *testing.T, signMode string, testSK string) *httptest.Server {
	t.Helper()
	const newContent = "fake-binary-new-content"
	h := sha256.Sum256([]byte(newContent))
	checksums := fmt.Sprintf("%s  lfr-tunnel-fake\n", hex.EncodeToString(h[:]))

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			osKey := runtime.GOOS
			if osKey == "darwin" {
				osKey = "macos"
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(ServerVersionInfo{
				LatestVersion: "v1.0.3",
				MinVersion:    "v1.0.0",
				ClientPlatforms: map[string]ServerPlatformInfo{
					fmt.Sprintf("%s_%s", osKey, runtime.GOARCH): {
						URL:         "/static/downloads/lfr-tunnel-fake",
						BinaryName:  "lfr-tunnel-fake",
						Recommended: "url",
					},
				},
			}); err != nil {
				t.Errorf("encoding /api/version: %v", err)
			}
		case "/static/downloads/checksums.txt":
			if _, err := fmt.Fprint(w, checksums); err != nil {
				t.Errorf("writing checksums: %v", err)
			}
		case "/static/downloads/checksums.txt.minisig":
			if signMode == "none" {
				// The gateway publishes no signature. Nothing about the binary itself is
				// wrong -- its checksum matches -- so an implementation that verified only
				// the checksum would happily install it.
				w.WriteHeader(http.StatusNotFound)
				return
			}
			sk, err := minisign.DecodePrivateKey(testSK)
			if err != nil {
				t.Errorf("decoding the test private key: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			sig, err := sk.Sign([]byte(checksums), minisign.SignOptions{Hashed: true})
			if err != nil {
				t.Errorf("signing: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if _, err := w.Write(sig.Encode()); err != nil {
				t.Errorf("writing the signature: %v", err)
			}
		case "/static/downloads/lfr-tunnel-fake":
			if _, err := fmt.Fprint(w, newContent); err != nil {
				t.Errorf("writing the binary: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// fakeInstalledBinary writes a stand-in for the running executable and points SelfUpgrade at it.
func fakeInstalledBinary(t *testing.T) string {
	t.Helper()
	name := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	// 0o755: this stands in for the client binary, which has to be executable.
	if err := os.WriteFile(path, []byte("fake-binary-old-content"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("writing the fake binary: %v", err)
	}
	targetExecPath = path
	t.Cleanup(func() { targetExecPath = "" })
	return path
}

// The security property this whole feature rests on: the AUTOMATIC path refuses an
// unverifiable download, because it runs the same verification a manual upgrade runs.
//
// The gateway here serves a binary whose CHECKSUM IS CORRECT and simply publishes no
// signature. That is deliberate: an implementation that downloaded and checksummed the binary
// itself -- the obvious shortcut for an unattended path -- would install it and pass any test
// that only looked at the checksum.
func TestAutoUpgradeRefusesAnUnverifiableDownload(t *testing.T) {
	resetAutoUpgradeState(t)
	path := fakeInstalledBinary(t)
	srv := autoUpgradeGateway(t, "none", "")
	defer srv.Close()

	upgraded, err := AutoUpgradeAtStart(true, "v1.0.2", srv.URL, &ServerVersionInfo{LatestVersion: "v1.0.3"})
	if err == nil {
		t.Fatal("the automatic path installed a download it could not verify")
	}
	if upgraded {
		t.Error("AutoUpgradeAtStart reported success on an unverifiable download")
	}
	// Assert the cause, not merely that something failed: a transport error, a missing asset
	// and a 404 on the binary would all produce a non-nil error here.
	if !strings.Contains(err.Error(), "cannot be verified") {
		t.Errorf("the failure does not name verification as the cause: %v", err)
	}

	// And the installed binary is untouched. An error return that still replaced the file
	// would be the worst of both worlds.
	content, readErr := os.ReadFile(path) //nolint:gosec
	if readErr != nil {
		t.Fatalf("reading the binary after the refused upgrade: %v", readErr)
	}
	if string(content) != "fake-binary-old-content" {
		t.Errorf("the binary was replaced despite verification failing: %q", string(content))
	}
}

// The other half: with a valid signature over matching checksums, the automatic path does
// install. Without this the test above is satisfied by an auto-upgrade that never works at all.
//
// Needs the disposable test signing key, so it runs in CI and skips locally.
func TestAutoUpgradeInstallsAVerifiedDownload(t *testing.T) {
	resetAutoUpgradeState(t)
	testSK := testMinisignSecretKey(t)
	path := fakeInstalledBinary(t)
	srv := autoUpgradeGateway(t, "valid", testSK)
	defer srv.Close()

	upgraded, err := AutoUpgradeAtStart(true, "v1.0.2", srv.URL, &ServerVersionInfo{LatestVersion: "v1.0.3"})
	if err != nil {
		t.Fatalf("a correctly signed upgrade was refused: %v", err)
	}
	if !upgraded {
		t.Error("AutoUpgradeAtStart reported that it did not run")
	}
	content, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("reading the binary after the upgrade: %v", err)
	}
	if string(content) != "fake-binary-new-content" {
		t.Errorf("the binary was not replaced: %q", string(content))
	}
}

// Opting out must reach the same gateway and do nothing at all -- not merely skip the install,
// but leave the binary exactly as it was.
func TestAutoUpgradeDoesNothingWhenNotOptedIn(t *testing.T) {
	resetAutoUpgradeState(t)
	path := fakeInstalledBinary(t)
	srv := autoUpgradeGateway(t, "none", "")
	defer srv.Close()

	upgraded, err := AutoUpgradeAtStart(false, "v1.0.2", srv.URL, &ServerVersionInfo{LatestVersion: "v1.0.3"})
	if err != nil {
		t.Errorf("an opted-out client reported an error: %v", err)
	}
	if upgraded {
		t.Error("an opted-out client upgraded itself")
	}
	content, readErr := os.ReadFile(path) //nolint:gosec
	if readErr != nil {
		t.Fatalf("reading the binary: %v", readErr)
	}
	if string(content) != "fake-binary-old-content" {
		t.Errorf("an opted-out client replaced its binary: %q", string(content))
	}
}
