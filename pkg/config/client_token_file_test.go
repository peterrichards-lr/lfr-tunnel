package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests for the client's token_file: key (#1758).
//
// The key parsed but was never read from the day it was added until #1709 catalogued it and
// #1753 removed it; this is the feature that removal deliberately left to be written properly.
// The point of the key is that a Personal Access Token need not sit in ~/.lfr-tunnel/config.yaml
// -- a file that gets pasted into support threads and copied between machines -- so the
// behaviours worth pinning are the ones that decide whether the token in the file is the one
// actually used.

// isolateTokenEnvironment clears every variable that can supply a token or move the token file,
// so a developer with LFT_CLIENT_TOKEN exported does not get a different result from CI.
func isolateTokenEnvironment(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN", "LFT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
}

// writeClientConfigFile writes a config file and returns its path. Tests pass that path to
// LoadClientConfig explicitly rather than relying on the default location, which would make
// them depend on whatever the developer running them happens to have in their home directory.
func writeClientConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

func writeTokenFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}
	return path
}

func TestClientTokenFile_IsRead(t *testing.T) {
	isolateTokenEnvironment(t)

	tokenPath := writeTokenFile(t, "lft_pat_from_file\n")
	cfgPath := writeClientConfigFile(t, "server_url: \"https://x.example.com\"\ntoken_file: \""+tokenPath+"\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading a config with a valid token_file must succeed, got: %v", err)
	}
	if cfg.AuthToken != "lft_pat_from_file" {
		t.Errorf("token_file was not honoured: AuthToken = %q", cfg.AuthToken)
	}
	if !strings.Contains(cfg.TokenSource, "token_file") || !strings.Contains(cfg.TokenSource, tokenPath) {
		t.Errorf("TokenSource must name the key and the path so the startup block can report "+
			"where the token came from, got %q", cfg.TokenSource)
	}
}

// TestClientTokenFile_StripsSurroundingWhitespace is the single most likely real-world failure:
// every ordinary way of producing this file -- `echo`, a text editor, a `cat` from a password
// manager -- terminates the last line. A token carrying a trailing newline is rejected by the
// gateway as an invalid token, which points the user at their credentials rather than their file.
//
// Mutation-checked: replacing strings.TrimSpace(content) with content in readClientTokenFile
// fails every case here but the last, with the raw value showing in the diff.
func TestClientTokenFile_StripsSurroundingWhitespace(t *testing.T) {
	cases := []struct {
		name     string
		contents string
	}{
		{"trailing newline, as echo writes it", "lft_pat_abc\n"},
		{"CRLF, as a Windows editor writes it", "lft_pat_abc\r\n"},
		{"a blank line after it", "lft_pat_abc\n\n"},
		{"leading and trailing spaces", "   lft_pat_abc   \n"},
		{"a tab", "\tlft_pat_abc\t"},
		{"no terminator at all", "lft_pat_abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateTokenEnvironment(t)
			tokenPath := writeTokenFile(t, tc.contents)
			cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

			cfg, err := LoadClientConfig(cfgPath)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.AuthToken != "lft_pat_abc" {
				t.Errorf("expected the token stripped to %q, got %q", "lft_pat_abc", cfg.AuthToken)
			}
		})
	}
}

// TestClientTokenFile_OutranksInlineAuthToken pins the one place token_file: departs from the
// ladder's "the config file is a single layer" reading. Setting it says the token is not in this
// file, so an auth_token: left beside it is stale by construction -- and preferring the stale
// value is exactly the "the key did nothing and said nothing" defect #1709 filed.
//
// Mutation-checked: gating the token_file branch on `cfg.AuthToken == ""` fails here.
func TestClientTokenFile_OutranksInlineAuthToken(t *testing.T) {
	isolateTokenEnvironment(t)

	tokenPath := writeTokenFile(t, "lft_pat_from_file\n")
	cfgPath := writeClientConfigFile(t,
		"auth_token: \"lft_pat_stale_inline\"\ntoken_file: \""+tokenPath+"\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "lft_pat_from_file" {
		t.Errorf("token_file must beat an inline auth_token in the same file, got %q", cfg.AuthToken)
	}
}

// TestClientTokenFile_LosesToTheEnvironmentAndTheFlag is the other direction. The flag is not
// applied by LoadClientConfig -- cmd/lfr-tunnel's overrideConfigWithFlags does that after the
// load -- so what is provable here is that the environment wins, and that the token_file value
// is not written anywhere the later flag override could not replace.
//
// Mutation-checked: moving the LFT_CLIENT_TOKEN override above the token_file branch fails here.
func TestClientTokenFile_LosesToTheEnvironment(t *testing.T) {
	for _, envVar := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN"} {
		t.Run(envVar+" wins", func(t *testing.T) {
			isolateTokenEnvironment(t)
			t.Setenv(envVar, "lft_pat_from_env")

			tokenPath := writeTokenFile(t, "lft_pat_from_file\n")
			cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

			cfg, err := LoadClientConfig(cfgPath)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.AuthToken != "lft_pat_from_env" {
				t.Errorf("%s must beat token_file, got %q", envVar, cfg.AuthToken)
			}
			if !strings.Contains(cfg.TokenSource, envVar) {
				t.Errorf("TokenSource must name %s, got %q", envVar, cfg.TokenSource)
			}
		})
	}
}

// TestClientTokenFile_LFTTokenFileMovesThePath. docs/client_configuration.md already describes
// LFT_TOKEN_FILE as the variable that *moves* the token file, and the ladder puts environment
// variables above the config file. Both say the same thing here: the variable replaces the
// configured path rather than being shadowed by it.
func TestClientTokenFile_LFTTokenFileMovesThePath(t *testing.T) {
	isolateTokenEnvironment(t)

	configured := writeTokenFile(t, "lft_pat_configured\n")
	moved := writeTokenFile(t, "lft_pat_moved\n")
	t.Setenv("LFT_TOKEN_FILE", moved)

	cfgPath := writeClientConfigFile(t, "token_file: \""+configured+"\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "lft_pat_moved" {
		t.Errorf("LFT_TOKEN_FILE must replace the configured path, got %q", cfg.AuthToken)
	}
	if !strings.Contains(cfg.TokenSource, "LFT_TOKEN_FILE") {
		t.Errorf("TokenSource must say the path came from the environment, got %q", cfg.TokenSource)
	}
}

// TestClientTokenFile_MissingFileIsAnActionableError. A silent fall-through to "no token"
// presents later as an authentication failure, which sends people to the portal to re-issue a
// PAT that was never the problem.
func TestClientTokenFile_MissingFileIsAnActionableError(t *testing.T) {
	isolateTokenEnvironment(t)

	missing := filepath.Join(t.TempDir(), "not-there", "token")
	cfgPath := writeClientConfigFile(t, "token_file: \""+missing+"\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err == nil {
		t.Fatal("a token_file that does not exist must be an error, not an empty token")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the error must name the path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Personal Access Token") {
		t.Errorf("the error must say what the file was expected to contain, got: %v", err)
	}
	// pkg/gui calls LoadClientConfig as `cfg, _ :=`. Returning a nil config with the error
	// would turn a misconfigured path into a panic in the tray app.
	if cfg == nil {
		t.Error("LoadClientConfig must still return a usable config alongside this error")
	}
}

func TestClientTokenFile_EmptyFileIsAnActionableError(t *testing.T) {
	isolateTokenEnvironment(t)

	// Whitespace only: the file exists and is readable, so nothing but an explicit check
	// distinguishes it from a token.
	tokenPath := writeTokenFile(t, "\n\n  \n")
	cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

	_, err := LoadClientConfig(cfgPath)
	if err == nil {
		t.Fatal("an empty token_file must be an error")
	}
	if !strings.Contains(err.Error(), tokenPath) || !strings.Contains(err.Error(), "empty") {
		t.Errorf("the error must name the path and say it is empty, got: %v", err)
	}
}

func TestClientTokenFile_UnreadableFileIsAnActionableError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not control readability on Windows")
	}
	isolateTokenEnvironment(t)

	const secretValue = "lft_pat_must_not_be_echoed"
	tokenPath := writeTokenFile(t, secretValue+"\n")
	if err := os.Chmod(tokenPath, 0000); err != nil {
		t.Fatalf("failed to chmod: %v", err)
	}
	if _, err := os.ReadFile(tokenPath); err == nil {
		t.Skip("running as a user that ignores permission bits (root), so 0000 is still readable")
	}

	cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

	_, err := LoadClientConfig(cfgPath)
	if err == nil {
		t.Fatal("a token_file that cannot be read must be an error")
	}
	if !strings.Contains(err.Error(), tokenPath) {
		t.Errorf("the error must name the path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "readable") {
		t.Errorf("the error must say what is wrong with the file, got: %v", err)
	}
	// The error is printed to a terminal and from there into a support thread.
	if strings.Contains(err.Error(), secretValue) {
		t.Error("the error leaked the token file's contents")
	}
}

// TestClientTokenFile_AcceptsTheEnvFileForm. `lfr-tunnel login` writes ~/.lfr-tunnel/token, and
// the LDM flow writes an env-file. Pointing token_file: at either is the obvious thing to do, so
// both shapes are accepted rather than the bare-token one only.
func TestClientTokenFile_AcceptsTheEnvFileForm(t *testing.T) {
	isolateTokenEnvironment(t)

	tokenPath := writeTokenFile(t, "# written by lfr-tunnel login\nexport LFT_CLIENT_TOKEN=\"lft_pat_env_form\"\n")
	cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "lft_pat_env_form" {
		t.Errorf("expected the env-file form to be parsed, got %q", cfg.AuthToken)
	}
}

// TestClientTokenFile_ExpandsLeadingTilde. This is a path typed by hand into YAML, where no
// shell expands it -- the same reason log_dir: expands one.
func TestClientTokenFile_ExpandsLeadingTilde(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir reads USERPROFILE on Windows; the ~ form is not idiomatic there")
	}
	isolateTokenEnvironment(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "tok"), []byte("lft_pat_tilde\n"), 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	cfgPath := writeClientConfigFile(t, "token_file: \"~/tok\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("a leading ~ must be expanded, got: %v", err)
	}
	if cfg.AuthToken != "lft_pat_tilde" {
		t.Errorf("expected the token from ~/tok, got %q", cfg.AuthToken)
	}
}

// TestClientTokenFile_WorldReadableWarnsButLoads records the permissions decision, so that
// changing it has to be deliberate. Warning matches every other credential file this client
// reads; refusing would make `echo $TOKEN > ~/token` under a default 022 umask a hard failure,
// whose easiest workaround is putting the token back inline in a config file whose own
// permissions nothing checks at all.
func TestClientTokenFile_WorldReadableWarnsButLoads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not checked on Windows")
	}
	isolateTokenEnvironment(t)

	tokenPath := writeTokenFile(t, "lft_pat_world_readable\n")
	if err := os.Chmod(tokenPath, 0644); err != nil {
		t.Fatalf("failed to chmod: %v", err)
	}
	cfgPath := writeClientConfigFile(t, "token_file: \""+tokenPath+"\"\n")

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	cfg, err := LoadClientConfig(cfgPath)

	_ = w.Close()
	os.Stderr = oldStderr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("a world-readable token file must warn, not refuse, got: %v", err)
	}
	if cfg.AuthToken != "lft_pat_world_readable" {
		t.Errorf("the token must still be used, got %q", cfg.AuthToken)
	}
	if !strings.Contains(buf.String(), "Warning: Token file") {
		t.Errorf("expected a permissions warning naming the file, got: %q", buf.String())
	}
	if strings.Contains(buf.String(), "lft_pat_world_readable") {
		t.Error("the permissions warning leaked the token")
	}
}

// TestClientTokenFile_InlineAuthTokenUnaffected is the compatibility half: a config file that
// does not mention token_file: must behave exactly as it did before #1758.
func TestClientTokenFile_InlineAuthTokenUnaffected(t *testing.T) {
	isolateTokenEnvironment(t)

	cfgPath := writeClientConfigFile(t, "auth_token: \"lft_pat_inline\"\nsubdomain: \"demo\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "lft_pat_inline" {
		t.Errorf("an inline auth_token must still be used, got %q", cfg.AuthToken)
	}
	if cfg.TokenSource != "config file" {
		t.Errorf("TokenSource should name the config file, got %q", cfg.TokenSource)
	}
}

// TestClientTokenFile_LFTTokenFileStillLosesToAnInlineToken. Without token_file: set, the
// pre-#1758 ordering is untouched: the implicit fallbacks only run when auth_token is empty.
// Changing that would silently flip which token an existing user's client presents.
func TestClientTokenFile_LFTTokenFileStillLosesToAnInlineToken(t *testing.T) {
	isolateTokenEnvironment(t)

	t.Setenv("LFT_TOKEN_FILE", writeTokenFile(t, "lft_pat_from_env_file\n"))
	cfgPath := writeClientConfigFile(t, "auth_token: \"lft_pat_inline\"\n")

	cfg, err := LoadClientConfig(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "lft_pat_inline" {
		t.Errorf("pre-existing behaviour changed: an inline auth_token must still beat "+
			"LFT_TOKEN_FILE when token_file: is not set, got %q", cfg.AuthToken)
	}
}

// TestSaveClientConfigKeepsTheTokenOutOfAFileThatNamesATokenFile. The Inspector's Settings tab
// and the tray GUI both save the whole in-memory config back to ~/.lfr-tunnel/config.yaml. That
// config carries the token resolved from token_file:, so without a guard the first save would
// copy the token into the very file the key exists to keep it out of.
func TestSaveClientConfigKeepsTheTokenOutOfAFileThatNamesATokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := DefaultClientConfig()
	cfg.TokenFile = "/home/someone/.lfr-tunnel/token"
	cfg.AuthToken = "lft_pat_resolved_from_the_file"

	if err := SaveClientConfig(path, cfg); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if strings.Contains(string(written), "lft_pat_resolved_from_the_file") {
		t.Error("saving wrote the token into a config file that names a token_file")
	}
	if !strings.Contains(string(written), "token_file") {
		t.Error("saving dropped token_file, so the next load would find no token at all")
	}
	// The in-memory config must be untouched -- the running client still needs its token.
	if cfg.AuthToken != "lft_pat_resolved_from_the_file" {
		t.Error("saving cleared the caller's in-memory token")
	}
}

// TestSaveClientConfigStillWritesAnInlineToken is the other side of the guard: a user who has
// deliberately kept their token in the config file must not have it silently deleted on save.
func TestSaveClientConfigStillWritesAnInlineToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := DefaultClientConfig()
	cfg.AuthToken = "lft_pat_inline"

	if err := SaveClientConfig(path, cfg); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if !strings.Contains(string(written), "lft_pat_inline") {
		t.Error("an inline auth_token must survive a save when no token_file is configured")
	}
}
