package configtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The needles that identify a filesystem-walking gate. They are the same two spellings the
// issue's own derivation greps for:
//
//	grep -rln 'filepath.WalkDir\|filepath.Walk(' --include='*_test.go' pkg cmd
//
// This file matches them itself, and consults IsNestedWorktreeRoot itself, so it satisfies its
// own rule rather than being exempted from it -- the self-match trap in §5c rule 6 turned into
// a property instead of an exception.
var walkNeedles = []string{"filepath.Walk(", "filepath.WalkDir("}

// minimumWalkingGates is a floor, not a count.
//
// Five walkers exist as this is written. The floor is deliberately below that and above zero:
// its job is to fail when the DERIVATION breaks -- a wrong root, a skip rule that swallowed the
// tree -- rather than to encode how many walkers there should be. A test that asserted exactly
// five would have to be edited by whoever adds the sixth, which is the hand-maintained list this
// whole test exists to replace.
const minimumWalkingGates = 5

// TestEveryFilesystemWalkingGateSkipsNestedWorktrees is the durable half of #2211.
//
// #1815 found this defect, fixed two walkers, and COPIED the rule into pkg/server rather than
// sharing it. Three more walkers were written afterwards and none of them picked it up, because
// there was nowhere to pick it up from. Listing the five by hand here would reproduce that
// failure one walker later, so the set is derived from the source (§5b rule 4): any _test.go
// under pkg/ or cmd/ that walks the filesystem must consult IsNestedWorktreeRoot.
//
// What it does NOT check is that the call is reached -- a gate could call it inside a branch that
// never runs. Asserting the call site is what a test can see from the source; the fixture case
// below is what pins the rule the call implements.
func TestEveryFilesystemWalkingGateSkipsNestedWorktrees(t *testing.T) {
	root := repoRoot(t)

	var walkers, offenders []string
	for _, dir := range []string{"pkg", "cmd"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if path != base && (skippedDirs[name] || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				if path != base && IsNestedWorktreeRoot(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path) //nolint:gosec
			if readErr != nil {
				return readErr
			}
			src := string(body)
			walks := false
			for _, needle := range walkNeedles {
				if strings.Contains(src, needle) {
					walks = true
					break
				}
			}
			if !walks {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			walkers = append(walkers, rel)
			if !strings.Contains(src, "IsNestedWorktreeRoot") {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("deriving the walking gates under %s: %v", dir, err)
		}
	}

	if len(walkers) < minimumWalkingGates {
		t.Fatalf("the derivation found only %d filesystem-walking test file(s) under pkg/ and "+
			"cmd/ (%v); there are at least %d, so the walk is not reaching the tree and a green "+
			"result here would mean nothing was checked",
			len(walkers), walkers, minimumWalkingGates)
	}

	if len(offenders) > 0 {
		t.Errorf("%d of %d filesystem-walking test gate(s) never consult "+
			"configtest.IsNestedWorktreeRoot:\n  %s\n\n"+
			"A gate that walks the filesystem cannot see .git/info/exclude, so it reads every "+
			"nested git worktree as ordinary source -- and this repo's own guidance is to run "+
			"concurrent agents in worktrees. An absence-style gate then names copies of real "+
			"source as offenders; a presence-style one goes false green on a duplicate. Add "+
			"`if path != root && configtest.IsNestedWorktreeRoot(path) { return filepath.SkipDir }` "+
			"to the directory branch of the walk (#2211, #1815).",
			len(offenders), len(walkers), strings.Join(offenders, "\n  "))
	}
}

// TestANestedWorktreeIsSkippedAndARepositoryRootIsNot stages the fixture #2211 asks for and
// drives a walk over it, with a CONTROL arm that shows the same fixture defeating a walk that
// does not consult the rule.
//
// Without the control this proves nothing: a tree containing one planted duplicate satisfies
// "the walk found only the original" just as well when the fixture is incapable of producing a
// duplicate in the first place (§5c rule 3). So both arms run over the same directory.
func TestANestedWorktreeIsSkippedAndARepositoryRootIsNot(t *testing.T) {
	const planted = "PLANTED_OFFENDER"

	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("staging %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("staging %s: %v", rel, err)
		}
	}

	// The tree under test: the real source, a nested worktree holding a copy of it, and a
	// directory that is a repository root in its own right.
	write("pkg/real.go", "// "+planted+"\n")
	write("nested/.git", "gitdir: /somewhere/.git/worktrees/nested\n")
	write("nested/pkg/real.go", "// "+planted+"\n")
	write("standalone/.git/HEAD", "ref: refs/heads/master\n")
	write("standalone/pkg/real.go", "// "+planted+"\n")

	// scan walks root the way every gate in this repo does, optionally applying the shared rule.
	scan := func(skipWorktrees bool) []string {
		t.Helper()
		var hits []string
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == ".git" {
					return filepath.SkipDir
				}
				if skipWorktrees && path != root && IsNestedWorktreeRoot(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, readErr := os.ReadFile(path) //nolint:gosec
			if readErr != nil {
				return readErr
			}
			if !strings.Contains(string(body), planted) {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			hits = append(hits, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walking the staged tree: %v", err)
		}
		return hits
	}

	// CONTROL: the shape the three unfixed walkers had. If this does not see the duplicate, the
	// fixture cannot stage the defect and everything below is vacuous.
	without := scan(false)
	if !contains(without, "nested/pkg/real.go") {
		t.Fatalf("a walk with no worktree rule did not read the copy inside the nested "+
			"worktree (saw %v) -- the fixture does not stage the defect, so the assertion "+
			"below would pass for the wrong reason", without)
	}

	// FIRING: the copy inside the nested worktree is not read.
	with := scan(true)
	if contains(with, "nested/pkg/real.go") {
		t.Errorf("the walk descended into a nested worktree (saw %v). Its .git is a regular "+
			"file holding a gitdir: pointer, which is what identifies a worktree root whatever "+
			"directory the tooling put it in (#1815, #2211).", with)
	}
	if !contains(with, "pkg/real.go") {
		t.Errorf("the walk stopped reading the real source too (saw %v) -- the rule is "+
			"skipping the tree it is supposed to scan", with)
	}

	// BOUNDING: a directory holding its own .git DIRECTORY is a repository root, not a worktree
	// of this one, and is still walked. The rule keys on `.git` being a FILE precisely so the two
	// stay distinguishable; if a reason ever appears to skip nested repositories as well, this
	// case goes red and the widening is a decision rather than a side effect (§5b rule 6).
	if !contains(with, "standalone/pkg/real.go") {
		t.Errorf("a directory whose .git is a DIRECTORY was skipped (saw %v). That is a "+
			"repository root, not a worktree of this one, and the rule deliberately does not "+
			"cover it.", with)
	}
}

func contains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}
