package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
)

// #2594: `-status` named ~/.lfr-tunnel/client-<sub>.log while the tunnel wrote, and `-background`
// printed, ~/.lfr-tunnel/logs/client-<sub>.log. Someone following `-status` to a failure opened a
// stale file or none.
func TestStatusNamesTheLogBackgroundPrinted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	const sub = "peter-test"
	printed, err := client.ClientLogPath(sub) // what handleBackground prints and writes
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(printed), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(printed, []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A leftover pre-move log must not win over the one being written.
	legacy := filepath.Join(home, ".lfr-tunnel", "client-"+sub+".log")
	if err := os.WriteFile(legacy, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := statusLogPath(sub); got != printed {
		t.Fatalf("-status names %s; -background printed %s", got, printed)
	}
}

// The guard on the call site: the status handler must not rebuild the path by hand again.
func TestStatusDoesNotBuildTheLegacyLogPath(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `".lfr-tunnel", fmt.Sprintf("client-%s.log"`) {
		t.Error("main.go builds the legacy log path by hand; use statusLogPath / client.ResolveClientLogPath (#2594)")
	}
}

func TestLeaseLogLineIsSpelledCorrectly(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "pkg", "client", "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "Establised") {
		t.Error(`client.go still logs "Establised lease" (#2594)`)
	}
}
