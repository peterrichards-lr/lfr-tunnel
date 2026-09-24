package configtest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tree-wide guard against a value concatenated into a double-quoted YAML scalar (#2029).
//
// It replaces pkg/config's TestNoDoubleQuotedPathsInThisFile, which asserted this property and
// -- by its own name, and nowhere else -- only for the one file it lived in. #2022 wrote the
// same defect in pkg/server's geo reload tests, nothing looked, and master's Windows leg went
// red for the third time.
//
// So the scope is the whole repository, and it is stated here rather than in a name:
//
//   - EVERY .go file under the repo root, not only _test.go. Production code building a config
//     file by hand would be a worse instance of the same defect, and including it costs nothing
//     -- measured: the wider scan finds exactly the same sites as the _test.go-only one.
//   - No key-name allowlist, and no "is this value path-shaped?" judgement. A regex cannot know
//     what a variable holds, and eyeballing it is precisely what let #1775 ship twice: the
//     first fix converted the eight sites spelled `tokenPath` and missed the two spelled
//     `configured` and `missing`. The rule enforced here is the flat one -- concatenate into a
//     SINGLE-quoted scalar, or use SingleQuoted, never into a double-quoted one.
//   - vendor/, node_modules/ and the dot-directories are skipped as not-our-source.
//
// The guard runs on every platform and needs no Windows leg, which matters: pkg/server is
// deliberately excluded from ci.yml's platform_sensitive filter for speed (#1363), so a PR
// touching it gets no Windows run at all. A defect that only Windows can see is invisible until
// master. This one is visible on the Linux leg of every PR.

// yamlScalarOffenders are the two boundaries of a double-quoted YAML scalar with a Go
// expression concatenated inside it. Either one alone is enough to condemn a line, so a site
// that spells one boundary unusually -- or splits the concatenation across lines -- is still
// caught by the other.
//
// The offending form is deliberately not written out in any comment or message in this file.
// An earlier version of the narrow check gave an example of it and then matched its own
// documentation, which is the self-match trap the github-workflow skill names in §5c rule 6.
// TestTheGuardScansItsOwnSource holds that in place.
var yamlScalarOffenders = []struct {
	name string
	re   *regexp.Regexp
}{
	{
		// The opening boundary: a Go string literal ending in a YAML key, its colon, and an
		// opening double quote, immediately concatenated with something.
		name: "opening",
		re:   regexp.MustCompile(`"[A-Za-z_][A-Za-z0-9_]*:[ \t]*\\""[ \t]*\+`),
	},
	{
		// The closing boundary: a concatenation onto a Go string literal that begins by
		// closing a double-quoted scalar.
		name: "closing",
		re:   regexp.MustCompile(`\+[ \t]*"\\"`),
	},
}

type yamlFinding struct {
	file     string
	line     int
	boundary string
	text     string
}

func (f yamlFinding) String() string {
	return fmt.Sprintf("%s:%d (%s boundary): %s", f.file, f.line, f.boundary, strings.TrimSpace(f.text))
}

// scanGoSource reports every line of src that embeds a concatenated value in a double-quoted
// YAML scalar. Exported through a function rather than inlined so the CONTROL below can drive
// it over a known offender.
func scanGoSource(name string, src []byte) []yamlFinding {
	var found []yamlFinding
	for i, line := range strings.Split(string(src), "\n") {
		for _, o := range yamlScalarOffenders {
			if m := o.re.FindString(line); m != "" {
				found = append(found, yamlFinding{file: name, line: i + 1, boundary: o.name, text: line})
				break
			}
		}
	}
	return found
}

// repoRoot walks up from the working directory to the directory holding go.mod.
//
// `make test` runs the compiled binary after cd-ing into the package directory, and `go test`
// does the same, so the working directory is this package -- not the root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolving the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %q, so the repository root could not be found", dir)
		}
		dir = parent
	}
}

var skippedDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	"ui-dist":      true,
	"dist":         true,
	"bin":          true,
}

// goSourceFiles returns every .go file in the repository, relative to its root.
func goSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (skippedDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			// A nested worktree holds a second copy of every .go file in the repository, so a
			// finding there is a finding about this tree reported under someone else's path
			// (#2211). The dot-directory rule above covers .claude/worktrees today; this covers
			// a worktree wherever `git worktree add` actually put it.
			if path != root && IsNestedWorktreeRoot(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %q: %v", root, err)
	}
	return files
}

// minimumGoFilesScanned is a floor, not a count. A walk that silently found nothing -- a wrong
// root, a skip rule that swallowed the tree -- would otherwise report a clean pass over an
// empty set, which is the failure mode the github-workflow skill's §5c rule 5 describes.
const minimumGoFilesScanned = 200

func TestNoValueIsConcatenatedIntoADoubleQuotedYAMLScalar(t *testing.T) {
	root := repoRoot(t)
	files := goSourceFiles(t, root)

	if len(files) < minimumGoFilesScanned {
		t.Fatalf("scanned only %d .go files under %q; expected at least %d. The walk found "+
			"almost nothing, so a clean result here would prove nothing",
			len(files), root, minimumGoFilesScanned)
	}

	var findings []yamlFinding
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		findings = append(findings, scanGoSource(rel, src)...)
	}

	if len(findings) == 0 {
		return
	}
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("\n  " + f.String())
	}
	t.Errorf("%d value(s) concatenated into a double-quoted YAML scalar, across %d .go files:%s\n\n"+
		"On Windows every temp path starts C:\\Users\\, and a backslash followed by U inside a "+
		"double-quoted YAML scalar is an escape expecting 8 hex digits -- so the file fails to "+
		"parse before the key under test is reached, and the test fails on Windows only for a "+
		"reason unrelated to its subject (#1775, #1773, #2029).\n"+
		"Fix: build the scalar with configtest.SingleQuoted(v). Single-quoted YAML does no "+
		"escape processing at all, so the value goes in verbatim.",
		len(findings), len(files), b.String())
}

// CONTROL. A scanner that matches nothing passes the test above in exactly the same way a clean
// tree does, and the two are indistinguishable from the output. This drives it over a known
// offender of each boundary and requires it to fire, naming which boundary it was.
//
// Each case is the real shape, built at run time from pieces so that this file does not contain
// the literal form and cannot be matched by its own scan.
func TestTheGuardFiresOnAKnownOffender(t *testing.T) {
	dq := `\"`
	plus := ` + `

	cases := []struct {
		name         string
		src          string
		wantBoundary string
	}{
		{
			name:         "the geo reload site that turned master red (#2029)",
			src:          `x := "country_db_path: ` + dq + `"` + plus + `src.Path` + plus + `"` + dq + `\n"`,
			wantBoundary: "opening",
		},
		{
			name:         "the token_file site that turned master red twice (#1775, #1773)",
			src:          `cfg := "token_file: ` + dq + `"+tokenPath+"` + dq + `\n"`,
			wantBoundary: "opening",
		},
		{
			name:         "a closing boundary whose opening is spelled some other way",
			src:          `cfg := key` + plus + `"` + dq + `\n"`,
			wantBoundary: "closing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := scanGoSource("synthetic.go", []byte(tc.src))
			if len(found) != 1 {
				t.Fatalf("the scanner found %d offenders in %s; it must find exactly 1, or the "+
					"tree-wide pass above proves nothing", len(found), tc.src)
			}
			if found[0].boundary != tc.wantBoundary {
				t.Errorf("matched the %s boundary, want %s", found[0].boundary, tc.wantBoundary)
			}
		})
	}
}

// The other half of the CONTROL: the scanner must not fire on the cure, or the fix and the
// defect would be indistinguishable to it.
func TestTheGuardIsSilentOnTheCure(t *testing.T) {
	clean := []string{
		`cfg := "token_file: " + SingleQuoted(tokenPath) + "\n"`,
		`cfg := "country_db_path: " + SingleQuoted(src.Path) + "\n"`,
		`cfg := "subdomain: \"demo\"\n"`,
		`s := fmt.Sprintf("a: %s\n", SingleQuoted(p))`,
	}
	for _, src := range clean {
		if found := scanGoSource("synthetic.go", []byte(src)); len(found) != 0 {
			t.Errorf("the scanner flagged a correct line, which would make it unusable: %s -> %v", src, found)
		}
	}
}

// The guard's own source is scanned like every other file, and is clean.
//
// Two blind spots at once. A checker that greps for a pattern finds that pattern in its own
// source and its own comments, and reports a finding nobody can fix; the usual repair is to
// exclude the checker's own file, which then hides a real offender written there. Neither is
// acceptable, so the file is included and asserted clean instead.
func TestTheGuardScansItsOwnSource(t *testing.T) {
	root := repoRoot(t)
	const self = "pkg/config/configtest/yaml_guard_test.go"

	files := goSourceFiles(t, root)
	var present bool
	for _, f := range files {
		if f == self {
			present = true
			break
		}
	}
	if !present {
		t.Fatalf("%s was not among the %d files walked; the guard is excluding itself", self, len(files))
	}

	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(self)))
	if err != nil {
		t.Fatalf("reading %s: %v", self, err)
	}
	if found := scanGoSource(self, src); len(found) != 0 {
		t.Errorf("the guard matched its own source or comments (self-match): %v", found)
	}
}

func TestSingleQuoted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The literal shape of a Windows temp path, so the hazard is exercised on Unix
			// too -- where the real temp path contains no backslash and would not.
			name: "a Windows path goes in verbatim, with no backslash escaping",
			in:   `C:\Users\RUNNER~1\AppData\Local\Temp\token`,
			want: `'C:\Users\RUNNER~1\AppData\Local\Temp\token'`,
		},
		{
			name: "a quote in the value is doubled, the one character single-quoted YAML escapes",
			in:   `/tmp/it's/token`,
			want: `'/tmp/it''s/token'`,
		},
		{
			name: "an empty value is still a scalar, not a missing one",
			in:   "",
			want: "''",
		},
		{
			name: "a value that would otherwise parse as a number stays a string",
			in:   "8080",
			want: "'8080'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SingleQuoted(tc.in); got != tc.want {
				t.Errorf("SingleQuoted(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
