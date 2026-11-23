package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jedisct1/go-minisign"
)

// ephemeralMinisignKey makes a throwaway signing key and points MinisignPublicKey at its public
// half for the test. Unlike testMinisignSecretKey it needs no secret from CI, so the move tests
// below run everywhere, including the Docker runs used while the host cannot run Go.
func ephemeralMinisignKey(t *testing.T) minisign.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var sk minisign.PrivateKey
	sk.SignatureAlgorithm = [2]byte{'E', 'd'}
	copy(sk.KeyId[:], "lfrtmove")
	copy(sk.SecretKey[:], priv)
	pk := sk.PublicKey()
	pub := base64.StdEncoding.EncodeToString(slices.Concat(pk.SignatureAlgorithm[:], pk.KeyId[:], pk.PublicKey[:]))

	orig := MinisignPublicKey
	MinisignPublicKey = pub
	t.Cleanup(func() { MinisignPublicKey = orig })
	return sk
}

// moveRig is one upgrade through a gateway, with every side effect outside the test's own
// directories replaced by a recorder.
type moveRig struct {
	oldExe, target string
	err            error

	autostart []string    // install-service / install-gui-service, in order
	linkedTo  []string    // targets the LDM link was re-pointed at
	envMoves  [][2]string // (old, new) pairs handed to repointInstallEnv
	restart   struct {
		plists   []string
		systemd  bool
		relaunch [][]string
	}
}

type moveFaults struct {
	autostart, link, env error
	// moveTo is the install_dir the gateway publishes; empty publishes none (an in-place upgrade).
	moveTo bool
	// installDirIsOldDirViaSymlink publishes an install_dir that is a symlink to the directory the
	// client already runs from: the same place, spelled differently.
	installDirIsOldDirViaSymlink bool
	// noAutostart: the machine has neither autostart entry installed.
	noAutostart bool
	// wasRunning is what the stop found running before the upgrade. relaunchArgs, if set, is a
	// process it killed, started as the old binary with these arguments.
	wasRunning struct {
		plists       []string
		systemd      bool
		relaunchArgs []string
	}
}

func runMoveUpgrade(t *testing.T, f moveFaults) *moveRig {
	t.Helper()
	sk := ephemeralMinisignKey(t)
	tmp := t.TempDir()

	oldDir := filepath.Join(tmp, "old")
	newDir := filepath.Join(tmp, "new")
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatal(err)
	}
	rig := &moveRig{oldExe: filepath.Join(oldDir, canonicalExeName())}
	if err := os.WriteFile(rig.oldExe, []byte("old-binary"), 0o755); err != nil { //nolint:gosec // a fake client binary
		t.Fatal(err)
	}
	resolvedOld, err := filepath.EvalSymlinks(rig.oldExe)
	if err != nil {
		t.Fatal(err)
	}
	rig.oldExe = resolvedOld
	rig.target = rig.oldExe
	installDir := ""
	if f.moveTo {
		installDir = newDir
		rig.target = filepath.Join(newDir, canonicalExeName())
	}
	if f.installDirIsOldDirViaSymlink {
		installDir = filepath.Join(tmp, "via-link")
		if err := os.Symlink(oldDir, installDir); err != nil {
			t.Fatal(err)
		}
	}

	const asset = "lfr-tunnel-move-test"
	newBinary := []byte("new-binary")
	sum := sha256.Sum256(newBinary)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)
	sig, err := sk.Sign([]byte(checksums), minisign.SignOptions{Hashed: true})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			platform := map[string]string{"url": "/download/asset", "binary_name": asset}
			if installDir != "" {
				platform["install_dir"] = installDir
			}
			if err := json.NewEncoder(w).Encode(map[string]any{
				"latest_version":   "v1.0.3",
				"client_platforms": map[string]any{gatewayPlatformKey(): platform},
			}); err != nil {
				t.Errorf("encoding /api/version: %v", err)
			}
		case "/static/downloads/checksums.txt":
			fmt.Fprintf(w, "%s", checksums)
		case "/static/downloads/checksums.txt.minisig":
			fmt.Fprintf(w, "%s", sig.Encode())
		case "/download/asset":
			fmt.Fprintf(w, "%s", newBinary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	origExec, origStop, origRestart := targetExecPath, stopForUpgrade, restartAfterUpgrade
	origAutostart, origLink, origEnv := runAutostartCmd, repointLDMLinkForPlatform, repointInstallEnv
	origSvc, origGUI := isServiceInstalled, isGUIServiceInstalled
	t.Cleanup(func() {
		targetExecPath, stopForUpgrade, restartAfterUpgrade = origExec, origStop, origRestart
		runAutostartCmd, repointLDMLinkForPlatform, repointInstallEnv = origAutostart, origLink, origEnv
		isServiceInstalled, isGUIServiceInstalled = origSvc, origGUI
	})
	isServiceInstalled = func() bool { return !f.noAutostart }
	isGUIServiceInstalled = func() bool { return !f.noAutostart }
	targetExecPath = rig.oldExe
	stopForUpgrade = func() ([]string, bool, [][]string) {
		var relaunch [][]string
		if f.wasRunning.relaunchArgs != nil {
			relaunch = [][]string{append([]string{rig.oldExe}, f.wasRunning.relaunchArgs...)}
		}
		return slices.Clone(f.wasRunning.plists), f.wasRunning.systemd, relaunch
	}
	restartAfterUpgrade = func(plists []string, systemd bool, relaunch [][]string) {
		rig.restart.plists, rig.restart.systemd, rig.restart.relaunch = plists, systemd, relaunch
	}
	runAutostartCmd = func(cmd *exec.Cmd) ([]byte, error) {
		rig.autostart = append(rig.autostart, cmd.Args[len(cmd.Args)-1])
		return nil, f.autostart
	}
	repointLDMLinkForPlatform = func(_, _, target string) error {
		rig.linkedTo = append(rig.linkedTo, target)
		return f.link
	}
	repointInstallEnv = func(_, oldExe, newExe string) error {
		rig.envMoves = append(rig.envMoves, [2]string{oldExe, newExe})
		return f.env
	}

	rig.err = SelfUpgrade("v1.0.2", srv.URL)
	return rig
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// A move with every step succeeding ends with the client at the new path, the LDM link and the
// installers' environment naming it, and the old binary gone -- removed last.
func TestAMoveRepointsEverythingThenRemovesTheOldBinary(t *testing.T) {
	rig := runMoveUpgrade(t, moveFaults{moveTo: true})
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	if !exists(t, rig.target) {
		t.Fatalf("no client at %s", rig.target)
	}
	if exists(t, rig.oldExe) {
		t.Errorf("the old binary %s is still there after a clean move", rig.oldExe)
	}
	if !slices.Equal(rig.linkedTo, []string{rig.target}) {
		t.Errorf("LDM link re-pointed at %v, want [%s]", rig.linkedTo, rig.target)
	}
	if len(rig.envMoves) != 1 || rig.envMoves[0] != [2]string{rig.oldExe, rig.target} {
		t.Errorf("installers' environment moved %v, want [%s -> %s]", rig.envMoves, rig.oldExe, rig.target)
	}
}

// Any step that fails keeps the old binary: whatever still names it -- the old autostart entry,
// the LDM link, LDM_LFR_TUNNEL_BIN -- then still runs a working client, from a path that is still
// excluded. Removing it first, as the upgrade used to, left them all pointing at nothing.
func TestAFailedStepKeepsTheOldBinary(t *testing.T) {
	boom := errors.New("boom")
	for name, f := range map[string]moveFaults{
		"autostart": {moveTo: true, autostart: boom},
		"LDM link":  {moveTo: true, link: boom},
		"env":       {moveTo: true, env: boom},
	} {
		t.Run(name, func(t *testing.T) {
			rig := runMoveUpgrade(t, f)
			if rig.err != nil {
				t.Fatalf("SelfUpgrade: %v", rig.err)
			}
			if !exists(t, rig.oldExe) {
				t.Errorf("a failed %s step removed the old binary, which something still names", name)
			}
			if !exists(t, rig.target) {
				t.Errorf("the new binary is missing at %s", rig.target)
			}
		})
	}
}

// A failed re-registration leaves the old unit or plist naming the old binary, which is kept; so
// what was running before comes back as it was, and the planner is told so. Telling it nothing
// (as when the binary was removed) would leave a service that was running stopped for good.
func TestAFailedReregistrationRestartsWhatWasRunning(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	daemonPlist := filepath.Join(home, "Library", "LaunchAgents", daemonPlistName)

	var f moveFaults
	f.moveTo = true
	f.autostart = errors.New("boom")
	f.wasRunning.plists = []string{daemonPlist}
	f.wasRunning.systemd = true
	rig := runMoveUpgrade(t, f)
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	switch runtime.GOOS {
	case "linux":
		if !rig.restart.systemd {
			t.Error("the systemd unit that was running is not restarted after a failed re-registration")
		}
	case "darwin":
		if !slices.Equal(rig.restart.plists, []string{daemonPlist}) {
			t.Errorf("plists reloaded = %v, want the one that was loaded, %s", rig.restart.plists, daemonPlist)
		}
	}
}

// A successful re-registration started the service itself, so the planner is told it is coming
// up even if it was not running before (#2194, #2552).
func TestASuccessfulReregistrationIsToldToThePlanner(t *testing.T) {
	rig := runMoveUpgrade(t, moveFaults{moveTo: true})
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	switch runtime.GOOS {
	case "linux":
		if !rig.restart.systemd {
			t.Error("the planner was not told install-service started the systemd unit")
		}
	case "darwin":
		if len(rig.restart.plists) != 2 {
			t.Errorf("plists reloaded = %v, want the daemon and GUI plists install-service wrote", rig.restart.plists)
		}
	}
}

// The LDM link is re-pointed after an in-place upgrade too: on Windows it is a hardlink, which
// kept naming the replaced binary's content, so LDM ran the previous version (#2553).
func TestAnInPlaceUpgradeRepointsTheLDMLink(t *testing.T) {
	rig := runMoveUpgrade(t, moveFaults{})
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	if !slices.Equal(rig.linkedTo, []string{rig.oldExe}) {
		t.Errorf("LDM link re-pointed at %v, want [%s]", rig.linkedTo, rig.oldExe)
	}
	if len(rig.envMoves) != 0 || len(rig.autostart) != 0 {
		t.Errorf("an in-place upgrade moved the environment (%v) or re-registered autostart (%v)", rig.envMoves, rig.autostart)
	}
	if got, err := os.ReadFile(rig.oldExe); err != nil || string(got) != "new-binary" { //nolint:gosec // test path
		t.Errorf("the in-place upgrade left %q (%v)", got, err)
	}
}

// A process the upgrade killed is relaunched from the NEW binary after a move, with its own
// arguments. Relaunched as captured it would start from the old path, which is removed (#2553
// review): every -background tunnel down after a clean move.
func TestAMoveRelaunchesKilledProcessesFromTheNewBinary(t *testing.T) {
	var f moveFaults
	f.moveTo = true
	f.wasRunning.relaunchArgs = []string{"-background", "-port", "8080"}
	rig := runMoveUpgrade(t, f)
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	want := []string{rig.target, "-background", "-port", "8080"}
	if len(rig.restart.relaunch) != 1 || !slices.Equal(rig.restart.relaunch[0], want) {
		t.Errorf("relaunched %v, want [%v]", rig.restart.relaunch, want)
	}
}

// Only the autostart entries the user has are re-registered (#2553 review). install-service used
// to run on every move, installing and starting a service nobody had set up.
func TestAMoveReregistersOnlyWhatIsInstalled(t *testing.T) {
	rig := runMoveUpgrade(t, moveFaults{moveTo: true, noAutostart: true})
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	if len(rig.autostart) != 0 {
		t.Errorf("re-registered %v on a machine with no autostart installed", rig.autostart)
	}
	if exists(t, rig.oldExe) {
		t.Error("with nothing to re-register and nothing failing, the old binary should be gone")
	}

	// And with both installed, both are re-registered on every OS -- the GUI entry included,
	// which used to be macOS only.
	rig = runMoveUpgrade(t, moveFaults{moveTo: true})
	if !slices.Equal(rig.autostart, []string{"install-service", "install-gui-service"}) {
		t.Errorf("re-registered %v, want install-service then install-gui-service", rig.autostart)
	}
}

// An install_dir that names the directory the client already runs from, through a symlink, is
// not a move (#2553 review). Treated as one, the new binary was written over the current one and
// then removed as "the old binary", leaving no client at all.
func TestTheSameDirectoryThroughASymlinkIsNotAMove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege Windows runners do not grant")
	}
	rig := runMoveUpgrade(t, moveFaults{installDirIsOldDirViaSymlink: true})
	if rig.err != nil {
		t.Fatalf("SelfUpgrade: %v", rig.err)
	}
	got, err := os.ReadFile(rig.oldExe) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("the client is gone: %v", err)
	}
	if string(got) != "new-binary" {
		t.Errorf("the client holds %q, want the upgraded binary", got)
	}
	if len(rig.autostart) != 0 || len(rig.envMoves) != 0 {
		t.Errorf("treated as a move: autostart %v, environment %v", rig.autostart, rig.envMoves)
	}
}
