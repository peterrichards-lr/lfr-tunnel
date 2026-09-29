package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// The owner's address is personal data, and /api/version takes no credentials (#2297). It was
// published there to hide a Delete button that the server refuses independently
// (pkg/server/api.go's "Prevent deleting Owner"), so the disclosure bought nothing -- one
// unauthenticated GET per gateway, on hosts anyone can enumerate from Certificate Transparency.
//
// The assertion is on the VALUE, not the key name. Asserting that no key is called
// "owner_email" would pass the moment the same address came back as "contact",
// "administrator" or inside a nested block, which is precisely the class of regression worth
// guarding: the endpoint gains keys regularly (31 of them at the time of writing) and nothing
// else reads them for personal data.
func TestAPIVersion_PublishesNoValueMatchingTheOwnerAddress(t *testing.T) {
	const ownerEmail = "owner.person@example.com"

	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "owner_email_test.db")
	cfg.Owner = config.OwnerConfig{UserID: ownerEmail, Name: "Owner Person", Role: "admin"}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(srv.Stop)

	// CONTROL, before anything is asserted about absence.
	//
	// An absence assertion is satisfied by a fixture that never configured an owner at all, and
	// equally by an empty body. This proves the address really is live on this server and
	// really is reachable over HTTP -- so the absence below is a property of /api/version and
	// not of the fixture. It doubles as the guard that #2297's fix left the authenticated
	// copy alone: handleAdminSettings is where an admin page is meant to read this.
	settingsRec := httptest.NewRecorder()
	srv.handleAdminSettings(settingsRec, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), ownerEmail)
	if settingsRec.Code != http.StatusOK {
		t.Fatalf("control: GET /api/admin/settings returned %d (%s) -- the fixture cannot show the address anywhere, so the absence check below proves nothing", settingsRec.Code, settingsRec.Body.String())
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(settingsRec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("control: decoding /api/admin/settings: %v", err)
	}
	if got, _ := settings["owner_email"].(string); got != ownerEmail {
		t.Fatalf("control: /api/admin/settings owner_email = %q, want %q -- the admin-only source AdminUsers.tsx reads must keep carrying it", got, ownerEmail)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}

	// Second half of the reachability guard: an empty or truncated payload would satisfy every
	// absence assertion below. Pin a key that must always be there, and a floor on the size, so
	// "nothing leaked" cannot mean "nothing was served".
	if sv, _ := payload["server_version"].(string); sv == "" {
		t.Fatalf("/api/version carried no server_version -- the payload is not the one under test: %s", rec.Body.String())
	}
	if len(payload) < 10 {
		t.Fatalf("/api/version returned only %d keys -- too few to be the real payload, so an absence check over it is vacuous: %s", len(payload), rec.Body.String())
	}

	var found []string
	scanForOwnerAddress("", payload, strings.ToLower(ownerEmail), &found)
	if len(found) > 0 {
		t.Errorf("/api/version takes no credentials and must publish the owner's address under NO key (#2297); found it at:\n  %s", strings.Join(found, "\n  "))
	}
}

// scanForOwnerAddress walks a decoded JSON document and records the path of every string that
// contains needle, which must already be lower-cased.
//
// It matches on containment rather than equality so that an address embedded in a larger string
// -- "contact owner.person@example.com" -- is caught too; publishing it inside a sentence
// discloses exactly as much as publishing it alone.
func scanForOwnerAddress(path string, v interface{}, needle string, found *[]string) {
	switch typed := v.(type) {
	case map[string]interface{}:
		for k, nested := range typed {
			scanForOwnerAddress(path+"."+k, nested, needle, found)
		}
	case []interface{}:
		for i, nested := range typed {
			scanForOwnerAddress(fmt.Sprintf("%s[%d]", path, i), nested, needle, found)
		}
	case string:
		if strings.Contains(strings.ToLower(typed), needle) {
			*found = append(*found, fmt.Sprintf("%s = %q", strings.TrimPrefix(path, "."), typed))
		}
	}
}
