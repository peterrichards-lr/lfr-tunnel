package ops

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Deploying a gateway with no portal (#1494).
//
// `make build` builds the UI and copies it into the embed directory. `deploy` cross-compiles
// directly and embeds whatever is there. On a fresh clone that is just .gitkeep; after `make test`
// or a CI run it is a ZERO-BYTE index.html, because both create a dummy to satisfy the embed.
// Either way the deploy succeeded, reported success, and shipped a gateway answering 500 for its
// portal -- which is what reached production.

// withWorkingDir runs fn with the process in dir, restoring afterwards. RequireBuiltUI reads a
// repo-relative path, so the test has to control the working directory.
func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restoring working dir: %v", err)
		}
	}()
	fn()
}

func TestRequireBuiltUI(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "server", "ui-dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, uiDistIndex)

	withWorkingDir(t, dir, func() {
		// 1. Nothing there at all -- a fresh clone, where ui-dist holds only .gitkeep.
		if err := RequireBuiltUI(); err == nil {
			t.Error("a missing index.html must refuse the deploy")
		} else if !strings.Contains(err.Error(), "make build") {
			t.Errorf("the error must name the remedy, got: %v", err)
		}

		// 2. The zero-byte placeholder `make test` and CI create. This is the important case:
		//    existence alone is satisfied by it, so anyone who has run the tests would sail past
		//    a naive check and ship a portal-less gateway.
		if err := os.WriteFile(index, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := RequireBuiltUI(); err == nil {
			t.Error("the zero-byte CI placeholder must not count as a built UI")
		} else if !strings.Contains(err.Error(), "placeholder") {
			t.Errorf("the error should explain what the empty file is, got: %v", err)
		}

		// 3. A real build.
		if err := os.WriteFile(index, []byte("<!doctype html><title>portal</title>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := RequireBuiltUI(); err != nil {
			t.Errorf("a built UI must be accepted, got: %v", err)
		}
	})
}

// TestVerifyPortalV2 covers the check that would have caught the bad deploy from the deploying
// side, rather than when somebody opened the page.
func TestVerifyPortalV2(t *testing.T) {
	// The exact failure being caught.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "UI not built. Run 'make build' first.", http.StatusInternalServerError)
	}))
	defer broken.Close()

	err := verifyPortalV2At(broken.URL)
	if err == nil {
		t.Fatal("a gateway serving 'UI not built' must fail the deploy")
	}
	if !strings.Contains(err.Error(), "make build") {
		t.Errorf("the error must name the remedy, got: %v", err)
	}

	// A working portal.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := verifyPortalV2At(ok.URL); err != nil {
		t.Errorf("a working portal must pass, got: %v", err)
	}

	// An edge has no portal, and a gateway in maintenance answers 503. Neither is this bug, and
	// failing a deploy for them would make the check something people disable.
	for _, code := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		if err := verifyPortalV2At(srv.URL); err != nil {
			t.Errorf("status %d must not fail the deploy, got: %v", code, err)
		}
		srv.Close()
	}

	// Unreachable is already reported by the version check; not worth failing twice for one
	// problem.
	if err := verifyPortalV2At("http://127.0.0.1:1"); err != nil {
		t.Errorf("an unreachable host must not double-report, got: %v", err)
	}
}

// A deploy must BUILD the UI, not merely check that one is present (#1632).
//
// RequireBuiltUI passes on any non-empty index.html, including one built weeks ago against
// entirely different source. That is not a hypothetical: v1.48.16 shipped a 12-day-old portal v2
// to all five gateways behind a correct version string, and every check reported success.
//
// So the guarantee under test is not "something is there" but "deploy runs the build rule".
func TestDeployBuildsTheUIRatherThanTrustingDisk(t *testing.T) {
	// A stale-but-valid ui-dist: exactly what an operator who ran `make build` a fortnight ago
	// has, and exactly what RequireBuiltUI is happy with.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "server", "ui-dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, uiDistIndex), []byte("<!doctype html><title>stale</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	withWorkingDir(t, dir, func() {
		// The precondition that used to be the only gate: it is satisfied, and it should be --
		// the point is that being satisfied is not sufficient.
		if err := RequireBuiltUI(); err != nil {
			t.Fatalf("a stale but non-empty index.html satisfies RequireBuiltUI by design, got: %v", err)
		}

		// BuildUI must actually try to run the build. In this temp dir there is no Makefile, so
		// it has to fail -- if it returned nil here it would mean BuildUI does not run anything,
		// which is the regression this test exists to catch.
		err := BuildUI()
		if err == nil {
			t.Fatal("BuildUI must invoke the build rule; it returned nil in a directory with no Makefile")
		}
		// The error has to point at the remedy, since the operator's next move depends on it.
		if !strings.Contains(err.Error(), "ui-dist") {
			t.Errorf("the error must name what failed to build, got: %v", err)
		}
	})
}
