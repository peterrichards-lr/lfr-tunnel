package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seedClient writes a stand-in client binary into dir and returns its path.
func seedClient(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seeding %s: %v", dir, err)
	}
	exe := filepath.Join(dir, canonicalExeName())
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", exe, err)
	}
	t.Cleanup(func() { _ = os.Remove(exe) })
	return exe
}

// The gateway's install_dir decides where autostart points (#2552). Before this, autostart
// resolved only the override variables and the hardcoded default, so a path changed in the
// gateway's YAML moved new installs and upgrades but left Launch on Login on the old path.
//
// HOME is isolated for this package (testmain_home_test.go).
func TestResolveInstallExeFollowsTheServer(t *testing.T) {
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving the isolated home: %v", err)
	}

	// A canonical install EXISTS throughout, so a resolver that ignored the server would still
	// succeed -- and would be caught only by WHICH path it returns.
	canonical := seedClient(t, canonicalInstallDir(home))
	serverDir := filepath.Join(home, "corp", "lfr-tunnel")

	t.Run("the server's directory wins over the built-in default", func(t *testing.T) {
		want := seedClient(t, serverDir)
		got, err := resolveInstallExe(serverDir)
		if err != nil {
			t.Fatalf("a client installed where the server says did not resolve: %v", err)
		}
		if got != want {
			t.Errorf("resolved %q, want the server's %q", got, want)
		}
	})

	// CONTROL: the same tree with no server opinion resolves the default. Without this, the case
	// above would also pass against a resolver that always returned the server path.
	t.Run("with no server opinion the default still resolves", func(t *testing.T) {
		got, err := resolveInstallExe("")
		if err != nil {
			t.Fatalf("resolving with no server opinion: %v", err)
		}
		if got != canonical {
			t.Errorf("resolved %q, want the default %q", got, canonical)
		}
	})

	// The gateway sends its value unexpanded ("~/liferay/lfr-tunnel"), so the tilde form has to
	// land in the home folder -- not in a directory literally named "~".
	t.Run("a tilde path from the server expands into the home folder", func(t *testing.T) {
		want := seedClient(t, filepath.Join(home, "tilde", "lfr-tunnel"))
		got, err := resolveInstallExe("~/tilde/lfr-tunnel")
		if err != nil {
			t.Fatalf("resolving a tilde path: %v", err)
		}
		if got != want {
			t.Errorf("resolved %q, want %q", got, want)
		}
	})

	t.Run("an override variable still outranks the server", func(t *testing.T) {
		seedClient(t, serverDir)
		want := seedClient(t, filepath.Join(home, "override", "lfr-tunnel"))
		t.Setenv("LFR_TUNNEL_INSTALL_DIR", filepath.Dir(want))
		got, err := resolveInstallExe(serverDir)
		if err != nil {
			t.Fatalf("resolving with an override set: %v", err)
		}
		if got != want {
			t.Errorf("resolved %q, want the override's %q", got, want)
		}
	})
}

// A server directory with no client in it is named, not silently replaced by the default.
//
// The default EXISTS here, so a fallback would succeed -- and record a binary the gateway says is
// in the wrong place, possibly outside the EDR exclusion. Same reasoning as the override case in
// TestResolveInstallExeRefusesAnOverrideThatDoesNotResolve.
func TestResolveInstallExeRefusesAServerDirWithNoClient(t *testing.T) {
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	home, _ := os.UserHomeDir()
	canonical := seedClient(t, canonicalInstallDir(home))

	serverDir := filepath.Join(home, "moved", "lfr-tunnel")
	got, err := resolveInstallExe(serverDir)
	if err == nil {
		t.Fatalf("resolved %q when the server's directory holds no client", got)
	}
	if got == canonical {
		t.Errorf("fell back to the default %q", canonical)
	}
	if !strings.Contains(err.Error(), serverDir) {
		t.Errorf("the refusal does not name the server's directory: %v", err)
	}
	if !strings.Contains(err.Error(), "--upgrade") {
		t.Errorf("the refusal does not say how to move the client there: %v", err)
	}
}

// A relative install_dir from the server would resolve against the working directory and be
// recorded in an autostart entry as that (#2552 review). One YAML edit reaches every client, so it
// is refused -- even when a client happens to exist at that relative path from here.
func TestResolveInstallExeRefusesARelativeServerDir(t *testing.T) {
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	t.Chdir(t.TempDir())
	seedClient(t, filepath.Join("liferay", "lfr-tunnel"))

	got, err := resolveInstallExe(filepath.Join("liferay", "lfr-tunnel"))
	if err == nil {
		t.Fatalf("a relative server path resolved to %q", got)
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the refusal does not say what is wrong with the path: %v", err)
	}
}

// A bare "~" means the home folder, as install.sh treats it.
func TestExpandInstallDirBareTilde(t *testing.T) {
	if got := expandInstallDir("~", "/home/u"); got != "/home/u" {
		t.Errorf("expandInstallDir(\"~\") = %q, want the home folder", got)
	}
}

// versionGateway serves /api/version with the given client_platforms map.
func versionGateway(t *testing.T, platforms map[string]ServerPlatformInfo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(ServerVersionInfo{LatestVersion: "v1.0.0", ClientPlatforms: platforms}); err != nil {
			t.Errorf("encoding /api/version: %v", err)
		}
	}))
}

// unreachableURL returns the address of a server that has been shut down.
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

func TestServerInstallDir(t *testing.T) {
	t.Run("returns this platform's install_dir, unexpanded", func(t *testing.T) {
		srv := versionGateway(t, map[string]ServerPlatformInfo{
			gatewayPlatformKey(): {InstallDir: "~/corp/lfr-tunnel"},
			// Another platform's value must not leak in.
			"plan9_mips": {InstallDir: "~/wrong"},
		})
		defer srv.Close()
		got, err := ServerInstallDir(srv.URL)
		if err != nil {
			t.Fatalf("asking a reachable gateway: %v", err)
		}
		if got != "~/corp/lfr-tunnel" {
			t.Errorf("got %q, want %q", got, "~/corp/lfr-tunnel")
		}
	})

	t.Run("no entry for this platform is no opinion, not an error", func(t *testing.T) {
		srv := versionGateway(t, map[string]ServerPlatformInfo{"plan9_mips": {InstallDir: "~/wrong"}})
		defer srv.Close()
		got, err := ServerInstallDir(srv.URL)
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	// Unknown is not absent: an unreachable gateway is an error, never "no opinion".
	t.Run("an unreachable gateway is an error", func(t *testing.T) {
		if got, err := ServerInstallDir(unreachableURL(t)); err == nil {
			t.Errorf("an unreachable gateway answered %q with no error", got)
		}
	})

	// An older gateway sends the platform entry without install_dir. That is no opinion.
	t.Run("an entry with no install_dir is no opinion", func(t *testing.T) {
		srv := versionGateway(t, map[string]ServerPlatformInfo{gatewayPlatformKey(): {URL: "/x"}})
		defer srv.Close()
		if got, err := ServerInstallDir(srv.URL); err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	// A URL that is not a gateway, or a gateway in trouble, is unknown, not "no opinion". The
	// body is VALID and names a directory, so only the status check can refuse it -- an HTML 404
	// would be refused by the decoder instead, and this case would pass with the status check gone.
	t.Run("a non-200 answer is an error, even with a usable body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(ServerVersionInfo{ClientPlatforms: map[string]ServerPlatformInfo{
				gatewayPlatformKey(): {InstallDir: "~/from-a-failing-gateway"},
			}}); err != nil {
				t.Errorf("encoding: %v", err)
			}
		}))
		defer srv.Close()
		if got, err := ServerInstallDir(srv.URL); err == nil {
			t.Errorf("a 503 answered %q with no error", got)
		}
	})
	t.Run("an answer that will not decode is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, err := w.Write([]byte("<html>not json</html>")); err != nil {
				t.Errorf("writing: %v", err)
			}
		}))
		defer srv.Close()
		if got, err := ServerInstallDir(srv.URL); err == nil {
			t.Errorf("an undecodable answer produced %q with no error", got)
		}
	})

	t.Run("no configured gateway asks nobody", func(t *testing.T) {
		if got, err := ServerInstallDir(""); err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})
}

// When the gateway cannot be asked, autostart proceeds on the default -- refusing would break
// Launch on Login offline -- but it SAYS the server's setting is unknown, rather than letting the
// unknown pass for "the server has no opinion".
func TestAutostartServerDirLabelsAnUnknownAnswer(t *testing.T) {
	t.Run("unreachable: empty, with a warning", func(t *testing.T) {
		var warn bytes.Buffer
		if got := autostartServerDir(unreachableURL(t), &warn); got != "" {
			t.Errorf("an unreachable gateway produced %q", got)
		}
		if !strings.Contains(warn.String(), "unknown") {
			t.Errorf("no warning that the server's setting is unknown; got %q", warn.String())
		}
	})

	// CONTROL: a reachable gateway with an answer produces it and warns about nothing.
	t.Run("reachable: the answer, silently", func(t *testing.T) {
		srv := versionGateway(t, map[string]ServerPlatformInfo{gatewayPlatformKey(): {InstallDir: "~/corp/lfr-tunnel"}})
		defer srv.Close()
		var warn bytes.Buffer
		if got := autostartServerDir(srv.URL, &warn); got != "~/corp/lfr-tunnel" {
			t.Errorf("got %q, want %q", got, "~/corp/lfr-tunnel")
		}
		if warn.Len() != 0 {
			t.Errorf("warned on a good answer: %q", warn.String())
		}
	})
}

// After an upgrade moves the client, the child that re-registers autostart is told where the
// client now lives (#2552). It used to inherit only the environment, so a move driven by the
// gateway's install_dir re-registered against the hardcoded default, failed, and the discarded
// error left autostart pointing at the binary the upgrade had just removed.
func TestReregisterAutostartTellsTheChildWhereTheClientMoved(t *testing.T) {
	var ran *exec.Cmd
	orig := runAutostartCmd
	t.Cleanup(func() { runAutostartCmd = orig })
	runAutostartCmd = func(cmd *exec.Cmd) ([]byte, error) {
		ran = cmd
		return nil, nil
	}

	exe := filepath.Join("/new", "home", "lfr-tunnel")
	if err := reregisterAutostart(exe, "/new/home", "install-service"); err != nil {
		t.Fatalf("reregisterAutostart: %v", err)
	}
	if ran == nil {
		t.Fatal("no child was run")
	}
	if ran.Path != exe || !slices.Equal(ran.Args[1:], []string{"install-service"}) {
		t.Errorf("ran %q %q, want %q install-service", ran.Path, ran.Args[1:], exe)
	}
	if !slices.Contains(ran.Env, "LFR_TUNNEL_INSTALL_DIR=/new/home") {
		t.Errorf("the child was not told the new install directory; env had %d entries", len(ran.Env))
	}
}

// And a failure comes back to the caller with the child's output, instead of being discarded.
func TestReregisterAutostartReportsAFailure(t *testing.T) {
	orig := runAutostartCmd
	t.Cleanup(func() { runAutostartCmd = orig })
	runAutostartCmd = func(*exec.Cmd) ([]byte, error) {
		return []byte("no installed client found"), errors.New("exit status 1")
	}

	err := reregisterAutostart("/x/lfr-tunnel", "/x", "install-service")
	if err == nil {
		t.Fatal("a failing child was reported as success")
	}
	if !strings.Contains(err.Error(), "no installed client found") {
		t.Errorf("the child's output was dropped: %v", err)
	}
}
