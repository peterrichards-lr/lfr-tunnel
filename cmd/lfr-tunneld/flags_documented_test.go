package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every flag this daemon accepts has to be findable in the operator documentation.
//
// Four were not -- -bind, -http-bind, -cert and -key (#2092) -- and nobody noticed because
// nothing could list them: they were locals inside main(), so the only way to see them was to
// read the source. That is the same habit that left six client flags undocumented until
// something rendered that parser.
//
// This renders the real parser without executing anything. Running the binary to ask it, even
// with -h, is what triggered the SentinelOne incident on 2026-09-20; the EDR skill says by name
// that there is no verified-safe way to execute lfr-tunneld here.
//
// Deliberately undocumented flags go in the map below WITH A REASON, so "not documented" and
// "not documented yet" stay distinguishable.
var serverFlagsUndocumentedOnPurpose = map[string]string{
	// None today. Add entries as `"flag-name": "why operators are not told about it",`.
}

func TestEveryServerFlagIsDocumented(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	lines := readServerDocs(t, root)
	corpus := strings.Join(lines, "\n")

	// PREMISE: the documentation was actually loaded. An empty corpus would report every flag
	// as missing, which reads like a real failure and is not one.
	if len(corpus) < 50_000 {
		t.Fatalf("only %d bytes of server documentation loaded from %s; setup_guide.md alone is "+
			"~100KB, so this test is not reading what it thinks it is", len(corpus), root)
	}

	fs := flag.NewFlagSet("lfr-tunneld", flag.ContinueOnError)
	registerFlags(fs)

	var missing []string
	fs.VisitAll(func(f *flag.Flag) {
		if _, ok := serverFlagsUndocumentedOnPurpose[f.Name]; ok {
			return
		}
		// Boundaries on both sides: without the leading one `ssl_key_file` or
		// `--full-generate-key` vouches for -key; without the trailing one -check-config
		// vouches for -check.
		documented := regexp.MustCompile(
			`([^0-9A-Za-z-]|^)-` + regexp.QuoteMeta(f.Name) + `([^0-9A-Za-z-]|$)`)

		// And the mention has to be about THIS daemon. A control caught the alternative:
		// un-documenting -key left this green, because the guide also documents
		// `openssl req -new -key self-signed-key.key` for the code-signing certificate.
		// openssl's flag is not ours, and -cert, -config and -key are common enough that
		// several tools in this guide have their own.
		//
		// So a line counts when it names lfr-tunneld, or when it is a row in a flags table.
		found := false
		for _, line := range lines {
			if !documented.MatchString(line) {
				continue
			}
			if strings.Contains(line, "lfr-tunneld") || strings.HasPrefix(strings.TrimSpace(line), "| `-") {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, f.Name)
		}
	})

	if len(missing) > 0 {
		t.Errorf("these lfr-tunneld flags exist but appear nowhere in docs/server/: %v\n"+
			"An operator cannot use a flag nobody wrote down, and nothing else lists them -- "+
			"the binary cannot be run here to ask it. Document it, or add it to "+
			"serverFlagsUndocumentedOnPurpose with the reason.", missing)
	}
}

// readServerDocs loads the operator documentation: docs/server/, plus the architecture and
// infosec guides, which also describe how the gateway is run.
func readServerDocs(t *testing.T, root string) []string {
	t.Helper()

	var out strings.Builder
	serverDir := filepath.Join(root, "docs", "server")
	entries, err := os.ReadDir(serverDir)
	if err != nil {
		t.Fatalf("reading %s: %v", serverDir, err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(serverDir, entry.Name()))
		if readErr != nil {
			t.Fatalf("reading %s: %v", entry.Name(), readErr)
		}
		out.Write(body)
	}

	for _, name := range []string{"architecture.md", "infosec.md"} {
		body, readErr := os.ReadFile(filepath.Join(root, "docs", name))
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}
		out.Write(body)
	}

	return strings.Split(out.String(), "\n")
}
