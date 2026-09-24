package client

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// `lfr-tunnel -upgrade` on a `-gui -background` client left TWO tunnel clients running where
// there had been one (#2189).
//
// The upgrade stops what it finds and restarts it: the tray, and the background tunnel. But the
// tray starts a client of its own the moment it comes up (#2076), so the tunnel was started
// twice -- measured in production as two terminations, two "Restarting" lines and three
// processes, all within one second. The surplus client could not bind the Inspector's 4040 and
// took 4041, and the state file then advertised whichever of the two wrote it last.
//
// The property these tests assert is the one that matters: after the upgrade, exactly ONE client
// is serving a given configuration -- one Inspector bound, one lease held. Counting the entries
// in the plan is the weaker check, and it is not what is asserted here: the tray is an entry
// that starts a client without being one, so an entry count of two is correct in one of these
// scenarios and a defect in another.
//
// Modelled rather than executed. Running a client binary on this host is forbidden by the EDR
// rules and has cost three environment reinstalls, so the model below turns a relaunch plan into
// the clients it will produce using production's OWN predicates (IsTrayArgv, TrayConnectsOnStart,
// TrayTunnelArgs) rather than a re-implementation of them. Its one assumption is the upgrade's
// own situation: everything was just killed, so nothing is running when the tray starts and its
// isRunning check (gui.go autoConnectOnStart) cannot suppress the spawn.

// configKey is what makes two clients rivals: the flags that configure the tunnel, with the
// three that only describe how a process RUNS removed. Two clients with the same key want the
// same subdomain, the same lease and the same Inspector port -- which is what #2189 is actually
// about, and why the key is not simply the command line.
//
// Spelled out here rather than reusing production's filter, on purpose. The identity the
// property is stated in terms of must not be redefinable by the code under test: if the filter
// stopped stripping a flag, a derived key would quietly stop noticing that two clients collide.
func configKey(args []string) string {
	var out []string
	for _, a := range args {
		switch {
		case a == "-gui", a == "--gui",
			a == "-background", a == "--background",
			strings.HasPrefix(a, "-no-autoconnect"), strings.HasPrefix(a, "--no-autoconnect"):
			continue
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

// clientsByConfig returns how many clients will be serving each configuration once the plan has
// run.
func clientsByConfig(plan [][]string) map[string]int {
	out := map[string]int{}
	for _, argv := range plan {
		if len(argv) == 0 {
			continue
		}
		if IsTrayArgv(argv) {
			// A tray is not a client, but it starts one -- unless it was told not to.
			if TrayConnectsOnStart(argv[1:]) {
				out[configKey(TrayTunnelArgs(argv[1:]))]++
			}
			continue
		}
		out[configKey(argv[1:])]++
	}
	return out
}

const testExePath = "/usr/local/bin/lfr-tunnel"

// The two command lines are written out, not derived from TrayTunnelArgs, and they are the ones
// #2189 reported from `ps`:
//
//	25293  lfr-tunnel -prefer-region apac -gui     <- tray
//	25297  lfr-tunnel -prefer-region apac          <- the client it spawned
//
// Deriving the second from the function the planner also uses would make the fixture agree with
// production however wrong production was: TrayTunnelArgs could stop stripping -background and
// every test here would still match itself and pass.
const trayTunnelCmdline = "-prefer-region apac"

// The reported scenario, exactly: a tray and the background tunnel it had spawned, both killed
// by the upgrade, both captured for relaunch.
func trayAndItsTunnel() (tray []string, tunnel []string) {
	tray = []string{testExePath, "-prefer-region", "apac", "-gui"}
	tunnel = []string{testExePath, "-prefer-region", "apac"}
	return tray, tunnel
}

func TestExactlyOneClientServesTheConfigurationAfterUpgradingATray(t *testing.T) {
	tray, tunnel := trayAndItsTunnel()
	want := trayTunnelCmdline

	got := clientsByConfig(planRelaunch([][]string{tray, tunnel}, nil))

	if got[want] != 1 {
		t.Errorf("%d clients end up serving %q, want exactly 1.\nThis is #2189: the upgrade "+
			"restarts the tunnel AND the tray that starts one, so two clients race for the "+
			"subdomain and the loser's Inspector silently moves to 4041.\nplan produced: %v",
			got[want], want, got)
	}
}

// The #2164 guard, and the reason "a GUI came back, so skip the tunnel" is not the fix.
//
// -no-autoconnect makes the tray a control surface that starts nothing. Skipping the tunnel's
// relaunch there leaves the user with NO tunnel after an upgrade that reported success -- which
// is precisely the defect #2164 fixed, verified working for the first time the day #2189 was
// filed.
func TestTheTunnelComesBackWhenTheTrayWillNotConnect(t *testing.T) {
	for _, optOut := range []string{"-no-autoconnect", "--no-autoconnect", "-no-autoconnect=true"} {
		tray := []string{testExePath, "-prefer-region", "apac", "-gui", optOut}
		tunnel := []string{testExePath, "-prefer-region", "apac"}
		want := trayTunnelCmdline

		plan := planRelaunch([][]string{tray, tunnel}, nil)
		got := clientsByConfig(plan)

		if got[want] == 0 {
			t.Errorf("with %s the upgrade leaves NO client serving %q -- the tray does not "+
				"connect and the tunnel was not restarted either. That is #2164 reintroduced.\n"+
				"plan: %v", optOut, want, plan)
		}
		if got[want] > 1 {
			t.Errorf("with %s there are %d clients serving %q, want 1", optOut, got[want], want)
		}
	}
}

// ...and the opt-out spelled as its own negation is not an opt-out. -no-autoconnect=false asks
// for the default, so the tray connects and the separate relaunch is again a second client.
func TestTheOptOutSpelledFalseStillConnects(t *testing.T) {
	tray := []string{testExePath, "-prefer-region", "apac", "-gui", "-no-autoconnect=false"}
	tunnel := []string{testExePath, "-prefer-region", "apac"}
	want := trayTunnelCmdline

	if got := clientsByConfig(planRelaunch([][]string{tray, tunnel}, nil)); got[want] != 1 {
		t.Errorf("-no-autoconnect=false produced %d clients for %q, want 1: the flag is present "+
			"but asks for the default, so the tray connects", got[want], want)
	}
}

// A tunnel the tray will NOT bring up is still the upgrade's to restart. Matching on "a GUI
// exists" rather than on the command line would silently drop this one -- an unrelated tunnel,
// left down by an upgrade that said it succeeded.
func TestATunnelTheTrayWillNotStartIsStillRelaunched(t *testing.T) {
	tray, _ := trayAndItsTunnel()
	other := []string{testExePath, "-subdomain", "billing"}

	got := clientsByConfig(planRelaunch([][]string{tray, other}, nil))

	if n := got["-subdomain billing"]; n != 1 {
		t.Errorf("the billing tunnel has %d clients after the upgrade, want 1. It is configured "+
			"differently from the one the tray starts, so nothing else brings it back.\nall: %v",
			n, got)
	}
	if n := got[trayTunnelCmdline]; n != 1 {
		t.Errorf("the tray's own tunnel has %d clients, want 1.\nall: %v", n, got)
	}
}

// With no tray in the plan there is nothing to defer to, so every tunnel is restarted exactly as
// #2164 requires.
func TestWithoutATrayEveryTunnelComesBack(t *testing.T) {
	in := [][]string{
		{testExePath, "-subdomain", "demo"},
		{testExePath, "-subdomain", "billing"},
	}

	got := clientsByConfig(planRelaunch(in, nil))

	for _, want := range []string{"-subdomain demo", "-subdomain billing"} {
		if got[want] != 1 {
			t.Errorf("%q has %d clients after an upgrade with no GUI involved, want 1 -- "+
				"nothing else was restarted that could start it.\nall: %v", want, got[want], got)
		}
	}
}

// A process whose command line could not be read is not in the plan at all (appendRelaunchTarget
// drops it and says so), and the restart loop guards against an empty entry anyway. The planner
// runs BEFORE that guard, so it has to survive one too: a plan that panics restarts nothing at
// all, which is every tunnel lost rather than one duplicated.
func TestAnEmptyArgvNeitherCrashesThePlanNorPassesForATray(t *testing.T) {
	tunnel := []string{testExePath, "-subdomain", "demo"}

	// No tray: the empty entry must not be read as one.
	got := clientsByConfig(planRelaunch([][]string{{}, tunnel}, nil))
	if got["-subdomain demo"] != 1 {
		t.Errorf("an empty argv in the relaunch list cost the tunnel its restart: %v", got)
	}

	// With a tray, the planner reaches its comparison, which is where an empty argv would be
	// indexed.
	tray, trayTunnel := trayAndItsTunnel()
	got = clientsByConfig(planRelaunch([][]string{tray, {}, trayTunnel, tunnel}, nil))
	if got["-subdomain demo"] != 1 {
		t.Errorf("the demo tunnel lost its restart alongside an empty argv: %v", got)
	}
	if got[trayTunnelCmdline] != 1 {
		t.Errorf("%d clients serve %q, want 1: %v", got[trayTunnelCmdline], trayTunnelCmdline, got)
	}
}

// A machine already in the doubled state does not get half of it back: every relaunch that the
// tray will duplicate is dropped, not just the first.
func TestAnAlreadyDoubledMachineComesBackSingle(t *testing.T) {
	tray, tunnel := trayAndItsTunnel()
	want := trayTunnelCmdline

	got := clientsByConfig(planRelaunch([][]string{tray, tunnel, tunnel}, nil))

	if got[want] != 1 {
		t.Errorf("%d clients serve %q after upgrading a machine that already had two, want 1: "+
			"an upgrade that preserves the surplus client preserves the defect", got[want], want)
	}
}

// TrayTunnelArgs is what the comparison above rests on, so it is asserted directly rather than
// only through the planner.
//
// The tray spawns `-background`; handleBackground re-execs without it. So the process that owns
// the pid file -- the one the upgrade reads, kills and compares -- carries neither -background
// nor the two flags that describe the tray.
func TestTheTrayTunnelArgsAreWhatTheTrayActuallyLeavesRunning(t *testing.T) {
	got := strings.Join(TrayTunnelArgs([]string{"-gui", "-no-autoconnect", "-prefer-region", "apac"}), " ")

	if got != "-prefer-region apac" {
		t.Errorf("TrayTunnelArgs = %q, want %q: everything the user asked for survives, and the "+
			"three flags that describe how the TRAY runs do not", got, "-prefer-region apac")
	}
}

// A pure planner nothing calls changes no behaviour whatsoever -- the lesson #2076 paid for.
//
// Read from the source because the alternative is an upgrade that downloads a release, swaps a
// binary and spawns clients, none of which may happen on this machine.
func TestTheRestartActuallyAppliesThePlan(t *testing.T) {
	src, err := os.ReadFile("upgrade.go")
	if err != nil {
		t.Fatalf("read upgrade.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(text, "range planRelaunch(relaunch, serviceStarts)") {
		t.Error("restartActiveProcessesAndServices does not restart planRelaunch's output, so " +
			"the plan is computed and thrown away and the upgrade still starts two clients")
	}
	// One spawn site, so the plan cannot be bypassed by a second loop that never saw it.
	if n := strings.Count(text, "osutil.BackgroundCommand("); n != 1 {
		t.Errorf("the upgrade spawns relaunch targets from %d places, want 1. A second spawn "+
			"site is a second way to start the surplus client, and only one of them is "+
			"downstream of planRelaunch", n)
	}
}

// --- #2194: the two starters the planner did not know about, and the client it loses track of ---

// The service definitions the upgrade reloads, modelled exactly as serviceStartArgv does. The
// scenarios below feed these in as the second argument to planRelaunch, which is what
// restartActiveProcessesAndServices passes after reloading them.
func daemonPlistStart() []string { return []string{testExePath, "-background"} }
func systemdStart() []string     { return []string{testExePath, "-background"} }
func guiPlistStart() []string    { return []string{testExePath, "-gui"} }

// serviceManagedTunnel is the client a service-managed machine is running before the upgrade:
// the service ran `lfr-tunnel -background`, handleBackground re-execed without the flag, and the
// process that owns the pid file -- the one the upgrade reads, kills and captures -- is bare.
func serviceManagedTunnel() []string { return []string{testExePath} }

// clientsAfterUpgrade counts every client running once the upgrade has finished: the ones its own
// plan starts, and the ones the service managers it reloaded start by themselves.
//
// The second half is the whole of #2194. Counting only the plan makes a double-start invisible,
// because the surplus client is the one the upgrade never spawned.
func clientsAfterUpgrade(plan, serviceStarts [][]string) map[string]int {
	out := clientsByConfig(plan)
	for cfg, n := range clientsByConfig(serviceStarts) {
		out[cfg] += n
	}
	return out
}

// The issue's own title. A machine with the CLI LaunchAgent installed: the upgrade unloads the
// plist, kills the client it finds, reloads the plist -- which starts a client -- and then
// relaunches the captured argv as well.
func TestExactlyOneClientAfterUpgradingAServiceManagedMachine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		services [][]string
	}{
		{"macOS LaunchAgent", [][]string{daemonPlistStart()}},
		{"Linux systemd user unit", [][]string{systemdStart()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relaunch := [][]string{serviceManagedTunnel()}

			plan := planRelaunch(relaunch, tc.services)
			got := clientsAfterUpgrade(plan, tc.services)

			if got[""] != 1 {
				t.Errorf("%d clients serve the service's configuration after the upgrade, want "+
					"exactly 1.\nThis is #2194: the reloaded service starts one and the captured "+
					"argv is relaunched as a second, so two clients hold the same lease and the "+
					"loser's Inspector silently moves to 4041.\nplan: %v\nall: %v",
					got[""], plan, got)
			}
		})
	}
}

// The GUI LaunchAgent has the same shape one level up: the upgrade kills the tray by gui.pid and
// captures its argv, then both reloads the plist that starts a tray and relaunches the captured
// one. Two trays, and a client from each.
func TestExactlyOneTrayAndOneClientAfterUpgradingTheGUILaunchAgent(t *testing.T) {
	services := [][]string{guiPlistStart()}
	// What the GUI LaunchAgent's tray leaves running, captured by the upgrade: the tray itself
	// and the client it spawned.
	relaunch := [][]string{{testExePath, "-gui"}, serviceManagedTunnel()}

	plan := planRelaunch(relaunch, services)

	trays := 0
	for _, argv := range plan {
		if IsTrayArgv(argv) {
			trays++
		}
	}
	if trays != 0 {
		t.Errorf("the plan restarts %d trays as well as the GUI LaunchAgent it reloaded, want 0: "+
			"the plist starts the tray, so restarting it separately is a second one.\nplan: %v",
			trays, plan)
	}
	if got := clientsAfterUpgrade(plan, services); got[""] != 1 {
		t.Errorf("%d clients serve the tray's configuration after the upgrade, want 1.\nplan: "+
			"%v\nall: %v", got[""], plan, got)
	}
}

// The #2164 guard for the new rule, and the reason it is stated as "the same configuration"
// rather than "a service came back".
//
// A tunnel the user started by hand with its own flags is not the one the service starts, and
// nothing else will bring it back. Dropping it because a service happened to be installed would
// leave the user with no tunnel after an upgrade that reported success.
func TestATunnelTheServiceWillNotStartIsStillRelaunched(t *testing.T) {
	services := [][]string{daemonPlistStart()}
	relaunch := [][]string{serviceManagedTunnel(), {testExePath, "-subdomain", "billing"}}

	got := clientsAfterUpgrade(planRelaunch(relaunch, services), services)

	if n := got["-subdomain billing"]; n != 1 {
		t.Errorf("the billing tunnel has %d clients after the upgrade, want 1. It is configured "+
			"differently from the one the service starts, so nothing else brings it back -- "+
			"that is #2164.\nall: %v", n, got)
	}
	if n := got[""]; n != 1 {
		t.Errorf("the service's own tunnel has %d clients, want 1.\nall: %v", n, got)
	}
}

// A machine already carrying the surplus untracked client does not get half of it back, for the
// service case as well as the tray one.
func TestAnAlreadyDoubledServiceMachineComesBackSingle(t *testing.T) {
	services := [][]string{daemonPlistStart()}
	relaunch := [][]string{serviceManagedTunnel(), serviceManagedTunnel()}

	got := clientsAfterUpgrade(planRelaunch(relaunch, services), services)

	if got[""] != 1 {
		t.Errorf("%d clients after upgrading a machine that already had two, want 1: an upgrade "+
			"that preserves the surplus client preserves the defect.\nall: %v", got[""], got)
	}
}

// The other half of #2194, and the one that makes it cumulative.
//
// Only handleBackground writes a pid file, and it only runs when -background is on the command
// line. A tunnel the upgrade starts without it is invisible to -stop, to -status and to every
// later -upgrade -- so it is never terminated, never upgraded, and a fresh one is started
// alongside it each time. The property is therefore about the plan, not about any one scenario:
// every tunnel the upgrade starts itself must be one the pid-file writer will see.
func TestEveryTunnelTheUpgradeStartsIsTracked(t *testing.T) {
	tray, trayTunnel := trayAndItsTunnel()
	plans := map[string][][]string{
		"a bare service-managed tunnel": planRelaunch([][]string{serviceManagedTunnel()}, nil),
		"a tunnel with flags":           planRelaunch([][]string{{testExePath, "-subdomain", "demo"}}, nil),
		"a tray that will not connect": planRelaunch([][]string{
			{testExePath, "-gui", "-no-autoconnect"}, trayTunnel}, nil),
		"a tray and an unrelated tunnel": planRelaunch([][]string{
			tray, {testExePath, "-subdomain", "billing"}}, nil),
	}

	for name, plan := range plans {
		for _, argv := range plan {
			if len(argv) == 0 || IsTrayArgv(argv) {
				continue // a tray writes gui.pid itself; it is not started through -background
			}
			if !slices.ContainsFunc(argv[1:], func(a string) bool {
				return isFlagToken(a, "background")
			}) {
				t.Errorf("%s: the upgrade starts %v, which does not pass through "+
					"handleBackground and so gets no pid file. That client is invisible to "+
					"-stop and to the next -upgrade, which will start another one beside it "+
					"(#2194).", name, argv)
			}
		}
	}
}

// ...and the claim the test above rests on: handleBackground is the only thing that records a
// client, so "was it started with -background" really is the same question as "can any lifecycle
// command see it".
//
// Read from the source of the other package because pkg/client cannot import package main, and
// because running a client on this host is forbidden by the EDR rules.
func TestTheOnlyPIDFileWriterIsHandleBackground(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "lfr-tunnel", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/lfr-tunnel/main.go: %v", err)
	}
	text := string(src)

	// The declaration plus exactly one call. A second call site would mean a client could be
	// recorded some other way, and the property above would no longer follow from the flag.
	if n := strings.Count(text, "writePID("); n != 2 {
		t.Errorf("writePID appears %d times in main.go, want 2 (its declaration and one call). "+
			"If a second writer has been added, TestEveryTunnelTheUpgradeStartsIsTracked no "+
			"longer says what it claims and both need revisiting", n)
	}

	start := strings.Index(text, "func handleBackground(")
	if start < 0 {
		t.Fatal("handleBackground is gone from main.go; the pid file is written somewhere else now")
	}
	end := strings.Index(text[start+1:], "\nfunc ")
	if end < 0 {
		end = len(text) - start - 1
	}
	if !strings.Contains(text[start:start+1+end], "writePID(") {
		t.Error("handleBackground no longer writes the pid file, so restoring -background on a " +
			"relaunched tunnel no longer makes it visible to -stop or to the next -upgrade")
	}
}

// serviceStartArgv models what the installed services run. If an installer template changes the
// flags it writes, the model stops matching the machine and the de-duplication silently stops
// firing -- which is #2194 again, with a passing test suite.
//
// Asserted over the templates themselves rather than against a copy of them.
func TestServiceStartArgvMatchesWhatTheInstallerWrites(t *testing.T) {
	src, err := os.ReadFile("service_installer.go")
	if err != nil {
		t.Fatalf("read service_installer.go: %v", err)
	}
	text := string(src)

	// Every flag any plist template passes to the binary.
	found := map[string]bool{}
	for _, m := range regexp.MustCompile(`<string>(-[^<]*)</string>`).FindAllStringSubmatch(text, -1) {
		found[m[1]] = true
	}
	// ...and the systemd unit's.
	for _, m := range regexp.MustCompile(`ExecStart=\S+\s+(-\S+)`).FindAllStringSubmatch(text, -1) {
		found[m[1]] = true
	}

	// What serviceStartArgv can produce, for every input it accepts.
	modelled := map[string]bool{}
	for _, argv := range serviceStartArgv(testExePath,
		[]string{"/x/" + daemonPlistName, "/x/" + guiPlistName}, true) {
		for _, a := range argv[1:] {
			modelled[a] = true
		}
	}

	for flag := range found {
		if !modelled[flag] {
			t.Errorf("an installer template runs the client with %q, which serviceStartArgv does "+
				"not model. The planner will not recognise the client that service starts, and "+
				"the upgrade will start a second one beside it (#2194)", flag)
		}
	}
	for flag := range modelled {
		if !found[flag] {
			t.Errorf("serviceStartArgv models the service as running %q, which no installer "+
				"template writes. The planner is de-duplicating against a service that does not "+
				"exist, which can drop a relaunch and leave the user with no tunnel (#2164)", flag)
		}
	}
}

// The third starter, and the one that is easiest to miss: the upgrade's own migration path.
//
// When the binary moves, SelfUpgrade re-registers the services at the new location — and
// `install-service` does not just write the unit file, it enables and STARTS it (installLinux) or
// loads the plist (installDarwin). A client is therefore already coming up before
// restartActiveProcessesAndServices is reached, and the planner only knows if that branch says
// so. On macOS it always did, via plistToReload; on Linux it did not, because restartSystemd was
// decided from whether the unit was active BEFORE the upgrade.
//
// Read from the source because reaching this branch means downloading a release and swapping a
// binary, neither of which may happen on this machine (the EDR rules).
func TestTheMigrationPathTellsThePlannerWhatItRestarted(t *testing.T) {
	src, err := os.ReadFile("upgrade.go")
	if err != nil {
		t.Fatalf("read upgrade.go: %v", err)
	}
	text := string(src)

	const call = `exec.Command(execPath, "install-service")`
	at := strings.Index(text, call)
	if at < 0 {
		t.Fatalf("the migration path no longer calls install-service; this test is describing "+
			"code that has moved, and %s needs revisiting", "TestTheMigrationPathTellsThePlannerWhatItRestarted")
	}
	if strings.Count(text, call) != 1 {
		t.Errorf("install-service is invoked from %d places, want 1. Each one starts a client, "+
			"so each one has to tell the planner", strings.Count(text, call))
	}

	// The branch that follows the call is where the starters it created are recorded.
	branch := text[at:]
	if end := strings.Index(branch, "\n\t\t}\n"); end > 0 {
		branch = branch[:end]
	}
	for _, want := range []string{"restartSystemd = true", "plistToReload = "} {
		if !strings.Contains(branch, want) {
			t.Errorf("the migration path calls install-service — which starts a client — but "+
				"does not record it with %q. planRelaunch will not know that service is coming "+
				"up and will relaunch the captured argv beside it (#2194).\nbranch read:\n%s",
				want, branch)
		}
	}
}
