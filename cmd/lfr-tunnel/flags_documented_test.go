package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every flag this binary defines has to be findable in the documentation.
//
// Four were not: -check-version, -inspector-port, -no-tui and -status-json (#2081). Nobody
// noticed because a flag costs nothing to add and nothing reminds you it is now a feature the
// documentation does not mention. -inspector-port was the one that mattered: the Inspector is
// how you read request bodies, and a user whose 4040 is already taken had no documented way to
// move it.
//
// This RENDERS the parser rather than reading the source, so a flag cannot be added without
// appearing here, and a renamed flag fails immediately instead of leaving prose describing a
// spelling that no longer parses.
//
// Deliberately undocumented flags go in the map below WITH A REASON, so "not documented" and
// "not documented yet" stay distinguishable -- the same bargain mkdocs.yml's nav-exclude list
// strikes.
var undocumentedOnPurpose = map[string]string{
	// None today. Add entries as `"flag-name": "why users are not told about it",`.
}

func TestEveryFlagIsDocumented(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	corpus := readDocCorpus(t, root)

	// PREMISE: we actually loaded the documentation. An empty corpus would fail every flag
	// below with a confusing message, or -- if the assertion were inverted -- pass everything.
	if len(corpus) < 50_000 {
		t.Fatalf("only %d bytes of documentation loaded from %s; the corpus is ~84k words, so "+
			"this test is not reading what it thinks it is", len(corpus), root)
	}

	var missing []string
	flag.VisitAll(func(f *flag.Flag) {
		// `go test` registers its own -test.* flags on the same FlagSet. They are the test
		// harness, not this program's interface.
		if strings.HasPrefix(f.Name, "test.") {
			return
		}
		if _, ok := undocumentedOnPurpose[f.Name]; ok {
			return
		}
		// A boundary, not a substring. `strings.Contains(corpus, "-region")` is satisfied by
		// the word "-prefer-region", so the deprecated spelling would have looked documented
		// while appearing nowhere -- and a control proved exactly that: renaming the
		// documented `-inspector-port` to `-inspector-portXX` left this test green.
		// Boundaries on BOTH sides. Without the leading one, the URL path `/api/tunnel-status`
		// makes `-status` look documented -- any hyphenated word ending in a flag's name would
		// do. Without the trailing one, `-prefer-region` vouches for `-region`.
		documented := regexp.MustCompile(
			`([^0-9A-Za-z-]|^)-` + regexp.QuoteMeta(f.Name) + `([^0-9A-Za-z-]|$)`)
		if !documented.MatchString(corpus) {
			missing = append(missing, f.Name)
		}
	})

	if len(missing) > 0 {
		t.Errorf("these flags exist but appear nowhere in docs/ or README.md: %v\n"+
			"A flag nobody documents is a feature nobody can find. Document it, or add it to "+
			"undocumentedOnPurpose with the reason.", missing)
	}
}

func readDocCorpus(t *testing.T, root string) string {
	t.Helper()

	// docs/ (recursively) and README.md -- the published documentation, and nothing else.
	//
	// This used to read every .md at the repository root as well, which silently swallowed
	// `.agent-state.md`: a gitignored 133KB agent scratch file. A flag mentioned only in
	// working notes therefore counted as documented, and the test passed locally while the
	// corpus it depends on does not even exist in CI. A control caught it -- undocumenting
	// -inspector-port left this green, twice.
	var out strings.Builder

	walkErr := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out.Write(body)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("reading docs/: %v", walkErr)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	out.Write(readme)

	return out.String()
}
