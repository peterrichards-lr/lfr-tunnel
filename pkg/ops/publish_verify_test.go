package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestParseChecksums(t *testing.T) {
	// Trailing newline, blank line and a short/garbage line are all shapes a real file or a
	// truncated transfer produces.
	data := []byte("aaa  lfr-tunnel-linux-amd64\nbbb  lfr-tunnel-darwin-arm64\n\ngarbage\n")
	got := parseChecksums(data)

	if len(got) != 2 {
		t.Fatalf("parsed %d entries, want 2: %v", len(got), got)
	}
	if got["lfr-tunnel-linux-amd64"] != "aaa" {
		t.Errorf("linux-amd64 = %q, want aaa", got["lfr-tunnel-linux-amd64"])
	}
}

// TestDiffChecksums covers the comparison itself, including the deliberate asymmetry: a missing
// or differing artefact is a failure, an extra one left over from an earlier deploy is not.
func TestDiffChecksums(t *testing.T) {
	local := map[string]string{"a": "1", "b": "2"}

	if problems := diffChecksums(local, map[string]string{"a": "1", "b": "2"}); problems != "" {
		t.Errorf("identical sets reported problems: %s", problems)
	}
	if problems := diffChecksums(local, map[string]string{"a": "1"}); !strings.Contains(problems, "b is not published") {
		t.Errorf("missing artefact not reported: %q", problems)
	}
	if problems := diffChecksums(local, map[string]string{"a": "1", "b": "99"}); !strings.Contains(problems, "b published as") {
		t.Errorf("differing artefact not reported: %q", problems)
	}
	// An extra published entry is untidy, not wrong.
	if problems := diffChecksums(local, map[string]string{"a": "1", "b": "2", "old": "3"}); problems != "" {
		t.Errorf("extra published artefact treated as a failure: %s", problems)
	}
}

// TestVerifyPublishedClientsAt is the end-to-end shape of the #1279 incident: checksums.txt and
// the binaries are served, and the question is whether they are the ones just uploaded.
func TestVerifyPublishedClientsAt(t *testing.T) {
	binary := []byte("the freshly built binary")
	stale := []byte("last release's binary")

	cases := []struct {
		name string
		// served maps a downloads path to its body; absent means 404.
		served      func(local []byte) map[string][]byte
		wantErr     bool
		errContains string
	}{
		{
			name: "everything matches",
			served: func(local []byte) map[string][]byte {
				return map[string][]byte{"checksums.txt": local, "lfr-tunnel-linux-amd64": binary}
			},
		},
		{
			// The exact incident: checksums.txt was updated but the binary served is the old
			// one. A checksums-only comparison would call this a success.
			name: "fresh checksums but a stale binary is caught",
			served: func(local []byte) map[string][]byte {
				return map[string][]byte{"checksums.txt": local, "lfr-tunnel-linux-amd64": stale}
			},
			wantErr:     true,
			errContains: "serving different bytes",
		},
		{
			name: "published checksums differ",
			served: func(local []byte) map[string][]byte {
				return map[string][]byte{
					"checksums.txt":          []byte(fmt.Sprintf("%s  lfr-tunnel-linux-amd64\n", hashOf(stale))),
					"lfr-tunnel-linux-amd64": stale,
				}
			},
			wantErr:     true,
			errContains: "published checksums do not match",
		},
		{
			name: "checksums.txt not served at all",
			served: func(local []byte) map[string][]byte {
				return map[string][]byte{"lfr-tunnel-linux-amd64": binary}
			},
			wantErr:     true,
			errContains: "could not fetch the published checksums.txt",
		},
		{
			// A trailing-newline difference must not read as a deploy failure -- the comparison
			// is on parsed entries for exactly this reason.
			name: "trailing whitespace difference is tolerated",
			served: func(local []byte) map[string][]byte {
				return map[string][]byte{
					"checksums.txt":          append(local, '\n', '\n'),
					"lfr-tunnel-linux-amd64": binary,
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			local := []byte(fmt.Sprintf("%s  lfr-tunnel-linux-amd64\n", hashOf(binary)))
			if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), local, 0o600); err != nil {
				t.Fatal(err)
			}

			bodies := tc.served(local)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(r.URL.Path, "/static/downloads/")
				body, ok := bodies[name]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write(body) //nolint:errcheck
			}))
			defer srv.Close()

			err := verifyPublishedClientsAt(srv.URL, dist, srv.Client())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not mention %q", err, tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestVerifyPublishedClientsAtNoLocalChecksums guards the precondition: with nothing to compare
// against, reporting success would be the silent pass this whole change exists to remove.
func TestVerifyPublishedClientsAtNoLocalChecksums(t *testing.T) {
	if err := verifyPublishedClientsAt("https://example.invalid", t.TempDir(), http.DefaultClient); err == nil {
		t.Fatal("expected an error when dist/checksums.txt is absent")
	}

	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPublishedClientsAt("https://example.invalid", dist, http.DefaultClient); err == nil {
		t.Fatal("expected an error when checksums.txt lists no artefacts")
	}
}
