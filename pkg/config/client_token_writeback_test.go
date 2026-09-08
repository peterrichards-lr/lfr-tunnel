package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Tests for the property SaveClientConfig enforces (#1772):
//
//	A token resolved from anywhere other than auth_token: in this very file must never be
//	persisted into auth_token:.
//
// The defect these pin is that SaveClientConfig writes the whole in-memory ClientConfig back,
// and that struct carries AuthToken whichever source resolved it. So a user who followed the
// documented advice -- token in ~/.lfr-tunnel/token, auth_token: left empty -- had their PAT
// copied into config.yaml the first time they touched the Inspector's Settings tab or the tray
// GUI. Nothing told them, and config.yaml is routinely pasted into support threads.
//
// #1758 guarded exactly one member of this class: a config that set token_file:. The far more
// common implicit ~/.lfr-tunnel/token case, and every environment-variable case, survived.

// tokenWriteBackHome points HOME (and USERPROFILE, which is what os.UserHomeDir reads on the
// Windows leg of the matrix) at a fresh temp dir, so the implicit ~/.lfr-tunnel/token and
// ~/.config/lfr/secrets lookups resolve inside the test rather than against the machine
// running it.
func tokenWriteBackHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeUnder(t *testing.T, home string, rel string, body string) string {
	t.Helper()
	path := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

// TestSaveClientConfigNeverPersistsATokenItDidNotGetFromTheConfigFile drives the property
// through LoadClientConfig's own resolution ladder, one case per rung. Every rung other than
// the inline auth_token: must round-trip to a config file that does NOT contain the token.
//
// Driven by the loader rather than by hand-built ClientConfig values on purpose: this is the
// path the Inspector and the tray GUI actually take (load, mutate one unrelated field, save),
// so a rung that stops setting TokenSource fails here rather than passing a unit test about a
// struct nobody constructs that way.
func TestSaveClientConfigNeverPersistsATokenItDidNotGetFromTheConfigFile(t *testing.T) {
	const secretToken = "lft_pat_must_not_reach_the_config_file"

	tests := []struct {
		name string
		// arrange returns the path of the config file to load.
		arrange       func(t *testing.T, home string) string
		wantPersisted bool
	}{
		{
			// The reported case. docs/client_configuration.md tells people to do this.
			name: "implicit ~/.lfr-tunnel/token",
			arrange: func(t *testing.T, home string) string {
				writeUnder(t, home, ".lfr-tunnel/token", secretToken+"\n")
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			name: "implicit token file in secrets format",
			arrange: func(t *testing.T, home string) string {
				writeUnder(t, home, ".lfr-tunnel/token",
					"LFT_CLIENT_TOKEN="+secretToken+"\n")
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			name: "LDM credentials file",
			arrange: func(t *testing.T, home string) string {
				writeUnder(t, home, ".config/lfr/secrets",
					"LFT_CLIENT_TOKEN="+secretToken+"\n")
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			// The one case #1758 already covered; kept so its guard cannot be lost while
			// generalising it.
			name: "explicit token_file:",
			arrange: func(t *testing.T, home string) string {
				tokenPath := writeUnder(t, home, "elsewhere/token", secretToken+"\n")
				return writeClientConfigFile(t, "token_file: "+yamlPath(tokenPath)+"\n")
			},
		},
		{
			name: "LFT_TOKEN_FILE",
			arrange: func(t *testing.T, home string) string {
				t.Setenv("LFT_TOKEN_FILE", writeUnder(t, home, "env/token", secretToken+"\n"))
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			name: "LFT_CLIENT_TOKEN environment variable",
			arrange: func(t *testing.T, home string) string {
				t.Setenv("LFT_CLIENT_TOKEN", secretToken)
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			name: "LFT_TOKEN environment variable",
			arrange: func(t *testing.T, home string) string {
				t.Setenv("LFT_TOKEN", secretToken)
				return writeClientConfigFile(t, "subdomain: \"demo\"\n")
			},
		},
		{
			// The compatibility half. A user who deliberately keeps the token inline must
			// not have it silently deleted the next time they save.
			name: "inline auth_token:",
			arrange: func(t *testing.T, home string) string {
				return writeClientConfigFile(t, "auth_token: \""+secretToken+"\"\n")
			},
			wantPersisted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTokenEnvironment(t)
			home := tokenWriteBackHome(t)

			cfgPath := tc.arrange(t, home)
			cfg, err := LoadClientConfig(cfgPath)
			if err != nil {
				t.Fatalf("loading: %v", err)
			}
			if cfg.AuthToken != secretToken {
				t.Fatalf("fixture did not resolve the token (got %q) -- the save assertion "+
					"below would pass for the wrong reason", cfg.AuthToken)
			}

			// What the Settings tab does: change one unrelated field, save the whole config.
			cfg.Subdomain = "changed-by-the-settings-tab"
			savePath := filepath.Join(t.TempDir(), "config.yaml")
			if err := SaveClientConfig(savePath, cfg); err != nil {
				t.Fatalf("saving: %v", err)
			}
			written, err := os.ReadFile(savePath)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if got := strings.Contains(string(written), secretToken); got != tc.wantPersisted {
				if tc.wantPersisted {
					t.Errorf("a token the user put in auth_token: was dropped on save -- "+
						"saving must not delete a token they deliberately keep inline.\n%s",
						written)
				} else {
					t.Errorf("saving copied a token resolved from %s into the config file "+
						"(TokenSource %q). config.yaml is the file users are told is safe to "+
						"share (#1772).\n%s", tc.name, cfg.TokenSource, written)
				}
			}

			// The running client still needs its token; the redaction is on the copy.
			if cfg.AuthToken != secretToken {
				t.Error("saving cleared the caller's in-memory token")
			}
			// A save must not be able to strand the user with no token at all.
			if !tc.wantPersisted {
				if _, err := LoadClientConfig(savePath); err != nil {
					t.Fatalf("the saved config no longer loads: %v", err)
				}
			}
		})
	}
}

// TestSaveClientConfigDeniesATokenOfUnknownProvenance is the class-level assertion: the guard
// is default-DENY, so a resolution path added later that forgets to set TokenSource is covered
// without anyone adding a row to the table above.
//
// The invented source names matter more than the real ones here. They stand in for a rung of
// the ladder that does not exist yet, and they must be denied for the same reason the real
// ones are -- the file is only allowed to keep a token it already owned.
func TestSaveClientConfigDeniesATokenOfUnknownProvenance(t *testing.T) {
	const secretToken = "lft_pat_unknown_provenance"

	tests := []struct {
		source        string
		wantPersisted bool
	}{
		{source: "", wantPersisted: false},                          // never declared
		{source: "1Password service account", wantPersisted: false}, // hypothetical
		{source: "macOS keychain", wantPersisted: false},            // hypothetical
		{source: "-token flag", wantPersisted: false},               // real, cmd/lfr-tunnel
		{source: "token file (/home/u/.lfr-tunnel/token)", wantPersisted: false},
		{source: "LFT_CLIENT_TOKEN environment variable", wantPersisted: false},
		{source: "Config File", wantPersisted: false}, // not the constant; deny, do not guess
		{source: TokenSourceConfigFile, wantPersisted: true},
	}

	for _, tc := range tests {
		name := tc.source
		if name == "" {
			name = "(no source declared)"
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultClientConfig()
			cfg.AuthToken = secretToken
			cfg.TokenSource = tc.source

			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := SaveClientConfig(path, cfg); err != nil {
				t.Fatalf("saving: %v", err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if got := strings.Contains(string(written), secretToken); got != tc.wantPersisted {
				t.Errorf("TokenSource %q: token persisted = %v, want %v.\n"+
					"Only %q may be written back into auth_token:; anything else -- including "+
					"a source nobody has written yet -- must be denied, because failing to "+
					"save a token is a visible bug and silently copying one out of the user's "+
					"token file is not (#1772).",
					tc.source, got, tc.wantPersisted, TokenSourceConfigFile)
			}
		})
	}
}

// TestSaveClientConfigStillRefusesAnAuthoredTokenBesideATokenFile. A token typed into the
// Settings form IS authored, so the TokenSource rule alone would write it -- into a key the
// next load ignores, because token_file: outranks an inline auth_token: (#1758). That would be
// a leak that buys the user nothing, so the token_file clause is not redundant.
func TestSaveClientConfigStillRefusesAnAuthoredTokenBesideATokenFile(t *testing.T) {
	cfg := DefaultClientConfig()
	cfg.TokenFile = "~/.lfr-tunnel/token"
	cfg.SetInlineAuthToken("lft_pat_typed_into_the_settings_form")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveClientConfig(path, cfg); err != nil {
		t.Fatalf("saving: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if strings.Contains(string(written), "lft_pat_typed_into_the_settings_form") {
		t.Error("a config that names token_file: was written with an inline token beside it; " +
			"token_file: outranks it on load, so it is a leak with no effect")
	}
	if !strings.Contains(string(written), "token_file") {
		t.Error("saving dropped token_file:, so the next load would find no token at all")
	}
}

// TestSetInlineAuthTokenIsTheWayToAuthorAToken -- the counterpart to default-deny. A caller
// that genuinely means "put this in the config file" has one call that says so, and it must
// round-trip through a save.
func TestSetInlineAuthTokenIsTheWayToAuthorAToken(t *testing.T) {
	cfg := DefaultClientConfig()
	cfg.SetInlineAuthToken("lft_pat_typed_by_the_user")

	if cfg.TokenSource != TokenSourceConfigFile {
		t.Errorf("SetInlineAuthToken must declare provenance, got TokenSource %q", cfg.TokenSource)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveClientConfig(path, cfg); err != nil {
		t.Fatalf("saving: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !strings.Contains(string(written), "lft_pat_typed_by_the_user") {
		t.Errorf("a token the user typed into the Settings form was not saved -- the form "+
			"would silently do nothing.\n%s", written)
	}
}

// TestEveryClientTokenAssignmentDeclaresItsProvenance is the gate that stops the fix being
// forgotten at a fifth call site.
//
// SaveClientConfig's guard is fail-safe, so a caller that sets AuthToken without provenance
// leaks nothing -- but its token silently fails to save, which is its own bug. This asserts
// the other half at the source: outside this package, a ClientConfig's AuthToken is only ever
// set by SetInlineAuthToken, or beside an explicit TokenSource assignment.
//
// Two shapes are checked, not one. Scanning only for `x.AuthToken =` would miss a
// `ClientConfig{AuthToken: tok}` composite literal, and that blind spot is asserted here
// rather than noted in a comment: a comment does not fail. Matching the literal on the
// enclosing type name is what keeps pkg/client's RegisterRequest -- a wire payload with an
// AuthToken field of its own -- from reading as a violation.
// isNestedWorktreeRoot reports whether dir is the root of a git worktree nested inside the tree
// being walked -- an agent worktree under .claude/worktrees, or anything else `git worktree add`
// put here.
//
// Its contents are a second checkout of THIS repository, so every file in it is a duplicate. A
// walk that descends into one reports each finding once per worktree, naming paths that are
// copies of the file rather than the file. On 2026-09-08 that made `make test` fail on a clean
// master with three agent worktrees present, flagging the same three legitimate lines nine times
// and blocking every push through the pre-push hook (#1815).
//
// The test is the presence of `.git` as a FILE. A real repository root carries `.git` as a
// directory; a worktree root carries it as a regular file holding a `gitdir:` pointer. That is
// what identifies the class, rather than a directory name -- so this keeps working if the tooling
// stops using `.claude/worktrees`, which a hardcoded path would not.
func isNestedWorktreeRoot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.Mode().IsRegular()
}

func TestEveryClientTokenAssignmentDeclaresItsProvenance(t *testing.T) {
	// pkg/config is the definition site: LoadClientConfig's ladder and the redaction inside
	// SaveClientConfig both assign AuthToken by design, and the behavioural tests above are
	// what pin those. This is a scope statement, not a list of known violations, so it cannot
	// go stale the way a suppression list can.
	const scopeSkip = "pkg/config"

	assignment := regexp.MustCompile(`\.AuthToken\s*=[^=]`)
	composite := regexp.MustCompile(`(?s)ClientConfig\{[^}]*\bAuthToken:`)
	provenance := regexp.MustCompile(`TokenSource\s*=|SetInlineAuthToken\(`)

	root := filepath.Join("..", "..")
	scanned := 0
	var offenders []string
	var sawKnownCallers int

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "ui-dist", "bin":
				return filepath.SkipDir
			}
			if path != root && isNestedWorktreeRoot(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../../"))
		if strings.HasPrefix(rel, scopeSkip+"/") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		src := string(data)
		if rel == "pkg/gui/gui.go" || rel == "pkg/client/inspector.go" || rel == "cmd/lfr-tunnel/main.go" {
			sawKnownCallers++
		}

		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !assignment.MatchString(line) {
				continue
			}
			window := strings.Join(lines[max(0, i-2):min(len(lines), i+3)], "\n")
			if !provenance.MatchString(window) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		for _, m := range composite.FindAllString(src, -1) {
			if !provenance.MatchString(m) {
				offenders = append(offenders, rel+"  ClientConfig{...AuthToken: ...} literal")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	// A derivation that silently matched nothing would report a clean pass over an empty set,
	// which is exactly how a gate becomes decorative.
	if scanned < 50 {
		t.Fatalf("only %d non-test .go files scanned -- the walk is not reaching the tree, so "+
			"a pass here would mean nothing", scanned)
	}
	if sawKnownCallers != 3 {
		t.Fatalf("expected to scan pkg/gui/gui.go, pkg/client/inspector.go and "+
			"cmd/lfr-tunnel/main.go (the files that set a client token); saw %d of 3", sawKnownCallers)
	}

	if len(offenders) > 0 {
		t.Errorf("a ClientConfig token is set without declaring where it came from:\n  %s\n\n"+
			"Use cfg.SetInlineAuthToken(tok) for a token the user typed into the config file, "+
			"or set cfg.TokenSource beside the assignment. SaveClientConfig will not persist a "+
			"token of unknown provenance, so this silently fails to save (#1772).",
			strings.Join(offenders, "\n  "))
	}
}
