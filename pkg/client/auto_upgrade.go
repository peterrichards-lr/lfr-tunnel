package client

import (
	"fmt"
	"sync/atomic"

	"lfr-tunnel/pkg/config"
)

// tunnelEstablished records that this process has had a tunnel accepted by a gateway.
//
// It exists to make the mid-session rule a RUNTIME guard rather than a property of where the
// call happens to sit. Replacing the binary under a live tunnel would drop a customer demo --
// worse than the problem auto-upgrade solves -- and "nothing calls it late today" is a
// statement about the current code, not a guarantee about the next caller (#2000).
var tunnelEstablished atomic.Bool

// markTunnelEstablished is called from RegisterTunnel the moment a gateway accepts a
// registration, which is the earliest instant at which "a tunnel exists" is true.
func markTunnelEstablished() { tunnelEstablished.Store(true) }

// ResetTunnelEstablishedForTest clears the flag. Test-only: the guard is process-wide state
// and a test that sets it would otherwise leak into every test that runs after it.
func ResetTunnelEstablishedForTest() { tunnelEstablished.Store(false) }

// AutoUpgradeSkipReason explains why an automatic upgrade did not run. Empty means it did.
type AutoUpgradeSkipReason string

const (
	// SkipNotEnabled is the default state: the user has not opted in.
	SkipNotEnabled AutoUpgradeSkipReason = "auto_upgrade is off"
	// SkipNoGatewayInfo means the gateway could not be asked what the latest version is.
	SkipNoGatewayInfo AutoUpgradeSkipReason = "the gateway did not say what the latest version is"
	// SkipDevBuild means this is a build from source, which orders below every release and
	// must not be silently replaced by one.
	SkipDevBuild AutoUpgradeSkipReason = "this is a development build"
	// SkipAlreadyCurrent means there is nothing newer to move to.
	SkipAlreadyCurrent AutoUpgradeSkipReason = "already running the latest version"
	// SkipTunnelRunning is the mid-session guard. See tunnelEstablished above.
	SkipTunnelRunning AutoUpgradeSkipReason = "a tunnel is already running"
)

// ShouldAutoUpgrade decides whether an opt-in automatic upgrade should run, and says why not
// when it should not.
//
// A pure function of its inputs plus the one piece of process state the mid-session rule needs,
// so every branch is reachable from a test without a network, a gateway or a second process.
func ShouldAutoUpgrade(enabled bool, currentVersion string, info *ServerVersionInfo) (bool, AutoUpgradeSkipReason) {
	if !enabled {
		return false, SkipNotEnabled
	}
	// Checked before anything else that could succeed: a tunnel already running is the one
	// condition where proceeding does active harm rather than merely being unnecessary.
	if tunnelEstablished.Load() {
		return false, SkipTunnelRunning
	}
	if currentVersion == "dev" || currentVersion == "" {
		return false, SkipDevBuild
	}
	if info == nil || info.LatestVersion == "" {
		return false, SkipNoGatewayInfo
	}
	if config.CompareVersions(currentVersion, info.LatestVersion) >= 0 {
		return false, SkipAlreadyCurrent
	}
	return true, ""
}

// AutoUpgradeAtStart runs the opt-in automatic upgrade, and reports whether it ran.
//
// It calls SelfUpgrade -- the SAME function `lfr-tunnel -upgrade` calls, not a copy tuned for
// unattended use. That is the whole security argument for this feature: the minisign signature
// over the checksums file, and the SHA-256 of the binary against it, are verified by exactly
// the code a manual upgrade verifies with, so an automatic upgrade cannot be laxer than a
// manual one. A second implementation here would be free to drift into being laxer, which is
// precisely what must not be possible when the binary is replaced without anybody watching.
//
// A failure is returned, not fatal. An upgrade that could not be verified must not happen, but
// it also must not stop somebody opening a tunnel: the client they have still works, and
// min_version (#1988) is what eventually refuses one that is too old.
func AutoUpgradeAtStart(enabled bool, currentVersion, serverURL string, info *ServerVersionInfo) (bool, error) {
	if ok, _ := ShouldAutoUpgrade(enabled, currentVersion, info); !ok {
		return false, nil
	}

	fmt.Printf("[Update] auto_upgrade is on and the gateway advertises %s (this client is %s). Upgrading before starting the tunnel...\n", info.LatestVersion, currentVersion)
	if err := SelfUpgrade(currentVersion, serverURL); err != nil {
		return false, err
	}
	// Said plainly rather than left to be inferred. SelfUpgrade replaces the binary on disk;
	// this process keeps executing the image it started with, and re-executing it mid-start
	// is deliberately not done -- the honest statement is cheaper than the surprise.
	fmt.Println("[Update] The upgrade is installed. This session continues on the version it started with; the new one runs from the next start.")
	return true, nil
}
