package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A partial save must leave the fields it did not mention alone (#2056).
//
// Every save rewrites the whole document, so with plain value types an omitted field decoded to
// its zero value and was written over whatever was there. Found by POSTing `{}` at a live
// client: server_url, subdomain, target_host, ports and preserve_host were all zeroed, and the
// response was 200. The running process holds its config in memory, so nothing looked wrong
// until the next start.
//
// Driven through the handler rather than asserted on the source. The static guard in
// settings_preserve_access_control_test.go covers the SHAPE across both handlers; this covers
// the behaviour, because a shape check cannot tell whether the saved file actually kept its
// contents.

func writeConfigFixture(t *testing.T) string {
	t.Helper()
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
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "server_url: \"https://gw.example.com\"\n" +
		"subdomain: \"demo\"\n" +
		"target_host: \"localhost\"\n" +
		"ports:\n    - 8080\n" +
		"preserve_host: true\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	return cfgPath
}

// TestAPartialSaveKeepsTheFieldsItDidNotSend is the control the issue named: send only
// `subdomain`, and everything else must survive.
func TestAPartialSaveKeepsTheFieldsItDidNotSend(t *testing.T) {
	cfgPath := writeConfigFixture(t)

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config",
		strings.NewReader(`{"subdomain":"renamed"}`))
	s.handleConfigPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST returned %d, want 200: %s", rec.Code, rec.Body.String())
	}

	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading the saved config: %v", err)
	}
	got := string(saved)

	// The field that WAS sent must change -- otherwise this test would pass against a handler
	// that ignored the request entirely, which is not the behaviour being asked for.
	if !strings.Contains(got, "renamed") {
		t.Errorf("the subdomain that WAS sent did not reach the file:\n%s", got)
	}

	for _, keep := range []string{"https://gw.example.com", "localhost", "8080", "preserve_host: true"} {
		if !strings.Contains(got, keep) {
			t.Errorf("a field the request never mentioned was lost: %q is gone.\n"+
				"An omitted field must mean \"leave it alone\", not \"clear it\" (#2056).\nSaved:\n%s",
				keep, got)
		}
	}
}

// TestASaveNamingNoFieldsIsRejected keeps the `{}` case loud. Writing the config unchanged
// would be defensible, but answering 200 to a request that asked for nothing is how the
// original defect stayed invisible.
func TestASaveNamingNoFieldsIsRejected(t *testing.T) {
	writeConfigFixture(t)

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{}`))
	s.handleConfigPost(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST {} returned %d, want 400 -- a body naming no field is a malformed "+
			"request, not a save", rec.Code)
	}
}

// TestSavingKeepsABackupOfWhatItReplaced is the net underneath the merge semantics above.
func TestSavingKeepsABackupOfWhatItReplaced(t *testing.T) {
	cfgPath := writeConfigFixture(t)

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config",
		strings.NewReader(`{"subdomain":"renamed"}`))
	s.handleConfigPost(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST returned %d, want 200", rec.Code)
	}

	backup, err := os.ReadFile(cfgPath + ".bak")
	if err != nil {
		t.Fatalf("no backup was written beside the config: %v", err)
	}
	if !strings.Contains(string(backup), "demo") {
		t.Errorf("the backup does not hold the PREVIOUS contents (expected subdomain \"demo\"):\n%s", backup)
	}
}
