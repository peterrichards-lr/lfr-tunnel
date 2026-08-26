package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeArtifacts creates the files a manifest claims exist, so the artefact check has something
// real to look at rather than being stubbed out.
func writeArtifacts(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("binary"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", n, err)
		}
	}
}

func TestBuildManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := BuildManifest{
		Version:       "v1.48.5",
		SourceVersion: "v1.48.5",
		Commit:        "abc1234",
		BuiltAt:       time.Now().UTC().Truncate(time.Second),
		Artifacts:     []string{"lfr-tunnel-linux-amd64", "lfr-tunnel-windows-amd64.exe"},
	}

	if err := WriteBuildManifest(dir, want); err != nil {
		t.Fatalf("WriteBuildManifest: %v", err)
	}

	got, err := ReadBuildManifest(dir)
	if err != nil {
		t.Fatalf("ReadBuildManifest: %v", err)
	}
	if got.Version != want.Version || got.SourceVersion != want.SourceVersion || got.Commit != want.Commit {
		t.Errorf("round trip changed fields: got %+v want %+v", got, want)
	}
	if !got.BuiltAt.Equal(want.BuiltAt) {
		t.Errorf("BuiltAt = %v, want %v", got.BuiltAt, want.BuiltAt)
	}
	if len(got.Artifacts) != 2 {
		t.Errorf("Artifacts = %v, want 2 entries", got.Artifacts)
	}
}

// TestCheckDistCurrent covers each way dist/ can fail to be what the current source produced.
// The incident in #1279 is the "stale source version" case: a build died from SIGPIPE, the
// previous manifest stayed in place, and everything downstream reported success.
func TestCheckDistCurrent(t *testing.T) {
	cases := []struct {
		name string
		// setup prepares a dist dir and returns the source version to check against.
		setup   func(t *testing.T, dir string) string
		wantErr bool
		// errContains is checked only when wantErr; keeps the assertion about the cause rather
		// than the exact wording.
		errContains string
	}{
		{
			name: "matching version passes",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				m := BuildManifest{Version: "v1.48.5", SourceVersion: "v1.48.5",
					Artifacts: []string{"lfr-tunnel-linux-amd64"}}
				if err := WriteBuildManifest(dir, m); err != nil {
					t.Fatal(err)
				}
				return "v1.48.5"
			},
		},
		{
			// The real incident. dist/ holds last release's artefacts.
			name: "stale source version fails",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				m := BuildManifest{Version: "v1.48.0", SourceVersion: "v1.48.0",
					Artifacts: []string{"lfr-tunnel-linux-amd64"}}
				if err := WriteBuildManifest(dir, m); err != nil {
					t.Fatal(err)
				}
				return "v1.48.3"
			},
			wantErr:     true,
			errContains: "v1.48.0",
		},
		{
			// A VERSION override is legitimate and must NOT read as stale: the embedded version
			// differs from source on purpose, so the check compares source to source.
			name: "VERSION override with current source passes",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				m := BuildManifest{Version: "v9.9.9-rc1", SourceVersion: "v1.48.5",
					Artifacts: []string{"lfr-tunnel-linux-amd64"}}
				if err := WriteBuildManifest(dir, m); err != nil {
					t.Fatal(err)
				}
				return "v1.48.5"
			},
		},
		{
			name: "no manifest at all fails",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				return "v1.48.5"
			},
			wantErr:     true,
			errContains: BuildManifestName,
		},
		{
			// An older dist/ from before the manifest carried a source version.
			name: "manifest without a source version fails",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				m := BuildManifest{Version: "v1.48.5", Artifacts: []string{"lfr-tunnel-linux-amd64"}}
				if err := WriteBuildManifest(dir, m); err != nil {
					t.Fatal(err)
				}
				return "v1.48.5"
			},
			wantErr:     true,
			errContains: "no source version",
		},
		{
			name: "artefact named in the manifest but missing fails",
			setup: func(t *testing.T, dir string) string {
				writeArtifacts(t, dir, "lfr-tunnel-linux-amd64")
				m := BuildManifest{Version: "v1.48.5", SourceVersion: "v1.48.5",
					Artifacts: []string{"lfr-tunnel-linux-amd64", "lfr-tunnel-darwin-arm64"}}
				if err := WriteBuildManifest(dir, m); err != nil {
					t.Fatal(err)
				}
				return "v1.48.5"
			},
			wantErr:     true,
			errContains: "lfr-tunnel-darwin-arm64",
		},
		{
			name: "corrupt manifest fails",
			setup: func(t *testing.T, dir string) string {
				if err := os.WriteFile(BuildManifestPath(dir), []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
				return "v1.48.5"
			},
			wantErr:     true,
			errContains: "build manifest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sourceVersion := tc.setup(t, dir)

			_, err := CheckDistCurrent(dir, sourceVersion)
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
