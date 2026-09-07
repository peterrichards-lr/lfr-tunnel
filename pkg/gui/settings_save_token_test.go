package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// The reported defect (#1772), end to end through the handler that causes it.
//
// The Settings form renders the token as "********" and posts that back unchanged when the
// user did not retype it, so the handler leaves cfg.AuthToken as LOADED -- which for a user
// following the documented advice is the token out of ~/.lfr-tunnel/token. Saving then wrote
// the whole config, PAT and all, into ~/.lfr-tunnel/config.yaml. Changing the destination port
// was enough to do it, and nothing said so.
//
// Driven through handleConfigPost rather than asserted on the source, because the whole point
// is the interaction between the mask sentinel, the loader and the save. HOME and USERPROFILE
// are redirected so the save lands in a temp dir rather than the developer's own config.
func TestSettingsSaveDoesNotCopyTheTokenFileIntoTheConfig(t *testing.T) {
	const secretToken = "lft_pat_from_the_users_token_file"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN", "LFT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}

	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	// Exactly the documented layout: the token in its own file, auth_token: absent.
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(secretToken+"\n"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("server_url: \"https://gw.example.com\"\nsubdomain: \"demo\"\n"), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}

	loaded, err := config.LoadClientConfig("")
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	if loaded.AuthToken != secretToken {
		t.Fatalf("the fixture did not resolve the token from ~/.lfr-tunnel/token (got %q); "+
			"the assertion below would pass for the wrong reason", loaded.AuthToken)
	}

	// What the Settings tab posts when the user changes the port and leaves the masked token
	// field alone.
	body := `{"server_url":"https://gw.example.com","auth_token":"********",` +
		`"target_host":"localhost","dest_port":9090,"subdomain":"demo"}`
	rec := httptest.NewRecorder()
	NewTempSettingsServer(0).handleConfigPost(rec,
		httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("saving settings failed: %d %s", rec.Code, rec.Body.String())
	}

	written, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading the saved config: %v", err)
	}
	if strings.Contains(string(written), secretToken) {
		t.Errorf("saving from the Settings tab copied the PAT out of ~/.lfr-tunnel/token into "+
			"config.yaml -- the file users are told is safe to paste into a support thread "+
			"(#1772).\n%s", written)
	}
	// The save must still have done its job, and must not have stranded the user: the token
	// file is still found on the next load.
	if !strings.Contains(string(written), "9090") {
		t.Errorf("the setting the user actually changed was not saved.\n%s", written)
	}
	reloaded, err := config.LoadClientConfig("")
	if err != nil {
		t.Fatalf("the saved config no longer loads: %v", err)
	}
	if reloaded.AuthToken != secretToken {
		t.Errorf("after saving, the client can no longer find its token (got %q) -- the "+
			"redaction must not break the token file lookup", reloaded.AuthToken)
	}
}

// The other half at the same call site: a token the user actually types into the Settings form
// has nowhere else to go, so it must reach config.yaml. Default-deny would otherwise turn this
// into a form that silently does nothing.
func TestSettingsSaveStillWritesATokenTheUserTyped(t *testing.T) {
	const typedToken = "lft_pat_typed_into_the_form"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN", "LFT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}

	body := `{"server_url":"https://gw.example.com","auth_token":"` + typedToken + `",` +
		`"target_host":"localhost","dest_port":8080,"subdomain":"demo"}`
	rec := httptest.NewRecorder()
	NewTempSettingsServer(0).handleConfigPost(rec,
		httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("saving settings failed: %d %s", rec.Code, rec.Body.String())
	}

	written, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "config.yaml"))
	if err != nil {
		t.Fatalf("reading the saved config: %v", err)
	}
	if !strings.Contains(string(written), typedToken) {
		t.Errorf("a token typed into the Settings form was not saved anywhere -- the form "+
			"would appear to work and do nothing.\n%s", written)
	}
}
