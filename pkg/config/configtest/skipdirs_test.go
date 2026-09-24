package configtest

import "testing"

// The union of what the five walkers each used to spell for themselves (#2217).
//
// Written out rather than derived from IsNonSourceDir, deliberately: a test that asked the
// function what it returns and then asserted that is a mirror, and would agree with the function
// however wrong it became (§5c rule 4). These are the names the copies actually carried, read off
// the five files before they were consolidated, so this is the evidence that consolidating lost
// nothing.
func TestTheSharedSkipListCoversWhatEveryWalkerUsedToSpellItself(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "vendor", "ui-dist", "dist", "bin"} {
		if !IsNonSourceDir(name) {
			t.Errorf("IsNonSourceDir(%q) is false, but at least one gate skipped it before the "+
				"five copies were consolidated -- consolidating has narrowed a walk rather than "+
				"widening it, which is how a generated or vendored file becomes 'source'", name)
		}
	}
}

// The other direction, which matters more: a predicate that returned true for everything would
// satisfy the test above and silently stop every gate walking anything at all -- a green run over
// an empty corpus, which is the failure this repository's gates keep re-learning.
func TestTheSharedSkipListDoesNotSwallowRealSource(t *testing.T) {
	for _, name := range []string{"pkg", "cmd", "server", "client", "config", "configtest", "docs", "scripts", "binary", "distribution", "vendored"} {
		if IsNonSourceDir(name) {
			t.Errorf("IsNonSourceDir(%q) is true, so every walking gate would skip it. Note the "+
				"last three: a prefix or substring match would wrongly catch \"binary\", "+
				"\"distribution\" and \"vendored\" -- the comparison has to be on the whole name",
				name)
		}
	}
}
