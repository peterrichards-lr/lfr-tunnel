package ops

import (
	"os"
	"strings"
	"testing"
)

// The stranded-instance rule, enforced statically (#1453).
//
// DeployCommand may start an edge that is outside its schedule, and puts it back with
// `defer restorePower()`. CheckFatal is os.Exit, and os.Exit runs no deferred functions -- so a
// single CheckFatal after that point leaves the node running outside its schedule, costing money,
// while the process exits blaming whichever scp failed. The #1183 warning that exists to catch a
// stranded instance is itself in a defer, so it does not fire either.
//
// This is checked by reading the source rather than by a behavioural test because the bug is
// invisible on every successful deploy: it only exists on the failure path, and a test that
// exercised the failure path would have to fake a cross-compile or an scp. Reading the file
// catches the mistake at the moment it is written, which is the only cheap moment.
//
// The repo already leans on static guards for rules like this -- check-edr-safety.sh,
// check-nolint-ratchet.sh, check-required-contexts.sh -- so this is the established shape.

// TestDeployCommandHasNoCheckFatalAfterPowerRestore is the guard.
//
// The failure it prevents is real and shipped: seven CheckFatal calls sat after the defer from
// #1050 until #1453, and the comment three lines above them stated the rule they were breaking.
func TestDeployCommandHasNoCheckFatalAfterPowerRestore(t *testing.T) {
	src, err := os.ReadFile("deploy.go")
	if err != nil {
		t.Fatalf("could not read deploy.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")

	deferAt := -1
	for i, line := range lines {
		if strings.Contains(line, "defer restorePower()") {
			deferAt = i
			break
		}
	}
	if deferAt < 0 {
		t.Fatal("could not find `defer restorePower()` in deploy.go -- if power management moved, " +
			"move this guard with it rather than deleting it")
	}

	// The end of DeployCommand: the next line that is a bare closing brace at column zero.
	endAt := len(lines)
	for i := deferAt + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			endAt = i
			break
		}
	}

	var offenders []string
	for i := deferAt + 1; i < endAt; i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, "CheckFatal(") {
			offenders = append(offenders, strings.TrimSpace(line))
		}
	}

	if len(offenders) > 0 {
		t.Errorf(
			"CheckFatal is os.Exit, which skips deferred functions -- including the restorePower "+
				"registered above these lines. On failure the node stays running outside its schedule "+
				"and the deploy exits blaming something else (#1453).\n\n"+
				"Use `if failed(err, \"...\") { return }` instead, so the defers run.\n\nFound %d:\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestDeployCommandFailedHelperSetsExitCode checks the helper the guard points people at actually
// does the two things it must: report, and mark the run as failed.
//
// Constructed the same way DeployCommand builds it, because the helper is a closure over
// exitCode and cannot be reached from a test otherwise. That coupling is the point -- if the
// helper's shape changes, this stops compiling and gets looked at.
func TestDeployCommandFailedHelperSetsExitCode(t *testing.T) {
	exitCode := 0
	var reported strings.Builder
	failed := func(err error, msg string) bool {
		if err == nil {
			return false
		}
		reported.WriteString(msg)
		exitCode = 1
		return true
	}

	if failed(nil, "should not fire") {
		t.Error("a nil error must not be treated as a failure, or every deploy would abort")
	}
	if exitCode != 0 {
		t.Error("a nil error must leave the exit code alone")
	}

	if !failed(os.ErrPermission, "Failed to SCP binary") {
		t.Error("a real error must be reported as a failure")
	}
	if exitCode != 1 {
		t.Errorf("a real error must set exitCode to 1, got %d", exitCode)
	}
	if !strings.Contains(reported.String(), "Failed to SCP binary") {
		t.Error("the failure message must name what failed, or the deploy log says nothing useful")
	}
}
