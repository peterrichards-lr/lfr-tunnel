package client

import (
	"strconv"
	"strings"
)

// How the tray launches its client, named in ONE place.
//
// This used to live in pkg/gui as clientArgsForConnect, which was correct while the tray was
// the only thing that needed to know. The upgrade path now needs the same answer -- it has to
// know whether the tray it is restarting will bring a tunnel up by itself, and with which
// arguments, or it starts a second one (#2189). pkg/gui imports pkg/client, so the shared
// answer has to live here; pkg/gui's clientArgsForConnect is a one-line call through to it.
//
// Copying the flag-spelling filter into the upgrade instead is exactly the shape that cost us
// #2128: getPIDFilePath and the upgrade path each spelled a pid file name independently, the
// two disagreed, and the upgrade terminated nothing for as long as that code existed. One
// spelling, one home.

// The flags that describe how to RUN the tray, and are therefore wrong or duplicated in the
// client it spawns.
const (
	guiFlagName           = "gui"
	backgroundFlagName    = "background"
	noAutoConnectFlagName = "no-autoconnect"
)

// boolFlagValue reports whether args set the named boolean flag, and to what.
//
// Go's flag package spells a boolean as -name, --name, -name=value or --name=value and NEVER as
// "-name value", so there is no paired value to look for. A value that does not parse is one
// the real binary would refuse to start on; reported as "not set" so that every caller here
// falls back to its conservative branch rather than acting on a reading the binary would never
// have reached.
func boolFlagValue(args []string, name string) (value bool, present bool) {
	for _, a := range args {
		body, ok := strings.CutPrefix(a, "--")
		if !ok {
			body, ok = strings.CutPrefix(a, "-")
		}
		if !ok {
			continue
		}
		switch {
		case body == name:
			value, present = true, true
		case strings.HasPrefix(body, name+"="):
			v, err := strconv.ParseBool(strings.TrimPrefix(body, name+"="))
			if err != nil {
				continue
			}
			value, present = v, true
		}
	}
	return value, present
}

// IsTrayArgv reports whether a command line starts a tray rather than a tunnel.
//
// The same condition main.go dispatches on (`if *guiFlag { gui.StartGUI(...) }`), read from the
// argument vector rather than re-derived: a process is a tray exactly when -gui is set on it.
// TrayClientArgs strips -gui, so a client the tray spawned can never satisfy this.
func IsTrayArgv(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	on, _ := boolFlagValue(argv[1:], guiFlagName)
	return on
}

// TrayConnectsOnStart reports whether a tray started with these arguments brings a tunnel up by
// itself (#2076).
//
// -no-autoconnect turns that off, which is the whole reason the upgrade cannot simply assume a
// restarted tray replaces the tunnel it killed: with the opt-out in force the tray comes up as a
// control surface and nothing else, and skipping the tunnel's relaunch would leave the user with
// no tunnel at all -- #2164 again.
//
// This answers "what will the tray do", not "was the flag present": -no-autoconnect=false is the
// flag, present, and asks for the default.
func TrayConnectsOnStart(guiArgs []string) bool {
	off, _ := boolFlagValue(guiArgs, noAutoConnectFlagName)
	return !off
}

// TrayClientArgs is what the tray's Connect spawns the client with.
//
// It used to be exactly []string{"-background"}, discarding every flag the GUI itself was
// started with (#2074). So `lfr-tunnel -gui -prefer-region apac` launched a tray that knew the
// user wanted apac, and then connected a client that did not -- and an empty cfg.Region is the
// exact condition that hands the choice to the region cache (main.go:2320), so a cached election
// silently won. The user asked for apac by name and got eu, with nothing saying so.
//
// Everything the GUI was given is forwarded except the flags that describe how to RUN, which
// would be wrong or duplicated in the child: -gui (the child is not a tray) and -background
// (added once, below).
//
// Values are separate argv elements for every flag this binary defines, so filtering whole
// tokens cannot orphan one. -gui and -background are booleans, which Go's flag package never
// spells as "-flag value", so there is no paired value to lose.
func TrayClientArgs(guiArgs []string) []string {
	out := make([]string, 0, len(guiArgs)+1)
	for _, a := range guiArgs {
		switch {
		case isFlagToken(a, guiFlagName):
			continue
		case isFlagToken(a, backgroundFlagName):
			continue // re-added below, so passing it twice cannot happen
		case isFlagToken(a, noAutoConnectFlagName):
			continue // describes the tray's startup (#2076); meaningless to the client
		}
		out = append(out, a)
	}
	return append(out, "-"+backgroundFlagName)
}

// TrayTunnelArgs is the argument vector the tray's client process ends up RUNNING with.
//
// Not the same thing as TrayClientArgs: the tray spawns `-background`, and handleBackground
// re-execs itself with that flag removed, so the process that survives -- the one with a pid
// file, the one the upgrade reads and kills -- carries everything except -background. This is
// the form the upgrade has to compare against.
func TrayTunnelArgs(guiArgs []string) []string {
	spawned := TrayClientArgs(guiArgs)
	out := make([]string, 0, len(spawned))
	for _, a := range spawned {
		// Exactly what handleBackground strips: the bare spellings only.
		if a == "-"+backgroundFlagName || a == "--"+backgroundFlagName {
			continue
		}
		out = append(out, a)
	}
	return out
}

// isFlagToken matches every spelling of a boolean flag, set or cleared.
func isFlagToken(arg, name string) bool {
	body, ok := strings.CutPrefix(arg, "--")
	if !ok {
		body, ok = strings.CutPrefix(arg, "-")
	}
	if !ok {
		return false
	}
	return body == name || strings.HasPrefix(body, name+"=")
}
