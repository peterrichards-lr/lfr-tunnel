package client

import (
	"os"
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

	got := clientsByConfig(planRelaunch([][]string{tray, tunnel}))

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

		plan := planRelaunch([][]string{tray, tunnel})
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

	if got := clientsByConfig(planRelaunch([][]string{tray, tunnel})); got[want] != 1 {
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

	got := clientsByConfig(planRelaunch([][]string{tray, other}))

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

	got := clientsByConfig(planRelaunch(in))

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
	got := clientsByConfig(planRelaunch([][]string{{}, tunnel}))
	if got["-subdomain demo"] != 1 {
		t.Errorf("an empty argv in the relaunch list cost the tunnel its restart: %v", got)
	}

	// With a tray, the planner reaches its comparison, which is where an empty argv would be
	// indexed.
	tray, trayTunnel := trayAndItsTunnel()
	got = clientsByConfig(planRelaunch([][]string{tray, {}, trayTunnel, tunnel}))
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

	got := clientsByConfig(planRelaunch([][]string{tray, tunnel, tunnel}))

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

	if !strings.Contains(text, "range planRelaunch(relaunch)") {
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
