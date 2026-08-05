package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestGetAdminSettingOptional(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	if srv.db == nil {
		t.Skip("Database not initialized")
	}

	// 1. Check nonexistent key
	val, exists, err := srv.db.GetAdminSettingOptional("nonexistent_setting_key")
	if err != nil {
		t.Fatalf("expected no error for nonexistent key, got: %v", err)
	}
	if !exists {
		// This is correct! Let's check it:
	} else {
		t.Errorf("expected exists=false for nonexistent key")
	}
	if val != "" {
		t.Errorf("expected empty string for nonexistent key, got: %q", val)
	}

	// 2. Set and check value
	err = srv.db.SetAdminSetting("test_setting_key", "hello-world")
	if err != nil {
		t.Fatalf("failed to set setting: %v", err)
	}

	val, exists, err = srv.db.GetAdminSettingOptional("test_setting_key")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !exists {
		t.Errorf("expected exists=true")
	}
	if val != "hello-world" {
		t.Errorf("expected 'hello-world', got: %q", val)
	}

	// 3. Clear (empty string override) and check
	err = srv.db.SetAdminSetting("test_setting_key", "")
	if err != nil {
		t.Fatalf("failed to set setting to empty: %v", err)
	}

	val, exists, err = srv.db.GetAdminSettingOptional("test_setting_key")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !exists {
		t.Errorf("expected exists=true for empty string override")
	}
	if val != "" {
		t.Errorf("expected empty string, got: %q", val)
	}
}

func TestVanityDomainHookConfigFallbacks(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Default fallback config path
	srv.cfg.VanityDomainHook = "/usr/local/bin/lfr-vanity-hook.sh"

	// 1. No database config yet: should return fallback config
	path, enabled := srv.getVanityDomainHookConfig()
	if path != "/usr/local/bin/lfr-vanity-hook.sh" {
		t.Errorf("expected fallback path '/usr/local/bin/lfr-vanity-hook.sh', got: %q", path)
	}
	if !enabled {
		t.Errorf("expected enabled=true as fallback has non-empty path")
	}

	// 2. Override path to empty string in DB
	err := srv.db.SetAdminSetting("vanity_domain_hook_path", "")
	if err != nil {
		t.Fatalf("failed to set path in DB: %v", err)
	}

	path, _ = srv.getVanityDomainHookConfig()
	if path != "" {
		t.Errorf("expected path override to empty string, got: %q", path)
	}

	// 3. Disable in DB
	err = srv.db.SetAdminSetting("enable_vanity_domain_hook", "false")
	if err != nil {
		t.Fatalf("failed to set enable_vanity_domain_hook in DB: %v", err)
	}

	_, enabled = srv.getVanityDomainHookConfig()
	if enabled {
		t.Errorf("expected enabled to be false from DB override")
	}
}

func TestAPIUpdateVanityDomainHookSecurity(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Create an admin user and an owner user
	adminUser := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: "approved"}
	ownerUser := &db.User{ID: "owner@example.com", Email: "owner@example.com", Role: "owner", Status: "approved"}
	if err := srv.db.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}
	if err := srv.db.CreateUser(ownerUser); err != nil {
		t.Fatalf("failed to create owner user: %v", err)
	}

	adminToken := generateToken(16)
	ownerToken := generateToken(16)

	srv.portalMap.Store("admin_session_"+adminToken, PortalSessionData{
		Email:     adminUser.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	srv.portalMap.Store("admin_session_"+ownerToken, PortalSessionData{
		Email:     ownerUser.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// 1. Admin attempts to change hook configurations -> expected 403
	reqBody := map[string]interface{}{
		"domain_allocation_rule":    "round_robin",
		"default_domain":            "example.com",
		"maintenance_page_path":     "",
		"vanity_domain_hook_path":   "/usr/local/bin/some-hook.sh",
		"enable_vanity_domain_hook": true,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: adminToken})

	w := httptest.NewRecorder()
	srv.handleAdminEndpoints(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for admin trying to change hook path, got: %d", w.Code)
	}

	// 2. Owner attempts to change hook using a relative path -> expected 400
	reqBody["vanity_domain_hook_path"] = "relative/path.sh"
	bodyBytes, _ = json.Marshal(reqBody)
	req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

	w = httptest.NewRecorder()
	srv.handleAdminEndpoints(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for relative path, got: %d", w.Code)
	}

	// 3. Owner attempts to change hook using a nonexistent path -> expected 400
	reqBody["vanity_domain_hook_path"] = "/usr/local/bin/nonexistent-hook.sh"
	bodyBytes, _ = json.Marshal(reqBody)
	req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

	w = httptest.NewRecorder()
	srv.handleAdminEndpoints(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for nonexistent path, got: %d. Body: %s", w.Code, w.Body.String())
	}

	// 4. Owner attempts to change hook pointing to a directory -> expected 400
	dirPath := "/usr/local/bin"
	if runtime.GOOS == "windows" {
		dirPath = os.TempDir()
	}
	reqBody["vanity_domain_hook_path"] = dirPath
	bodyBytes, _ = json.Marshal(reqBody)
	req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

	w = httptest.NewRecorder()
	srv.handleAdminEndpoints(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for directory path, got: %d", w.Code)
	}

	// 5. Non-trusted folder path check (unix only)
	if runtime.GOOS != "windows" {
		// Create a valid executable temp file outside of /usr/local/bin
		tmpFile, err := os.CreateTemp("", "bad-hook-*.sh")
		if err == nil {
			defer os.Remove(tmpFile.Name())
			if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
				t.Fatalf("failed to chmod temp file: %v", err)
			}

			reqBody["vanity_domain_hook_path"] = tmpFile.Name()
			bodyBytes, _ = json.Marshal(reqBody)
			req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

			w = httptest.NewRecorder()
			srv.handleAdminEndpoints(w, req)

			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "must reside under") {
				t.Errorf("expected 400 Bad Request indicating untrusted folder path, got: %d. Body: %s", w.Code, w.Body.String())
			}
		}
	}

	// 6. Non-executable file check (unix only)
	if runtime.GOOS != "windows" {
		// Create a temporary file in /usr/local/bin if writable, otherwise skip
		tmpFile, err := os.CreateTemp("/usr/local/bin", "test-nonexec-*.sh")
		if err == nil {
			defer os.Remove(tmpFile.Name())
			if err := os.Chmod(tmpFile.Name(), 0644); err != nil {
				t.Fatalf("failed to chmod temp file: %v", err)
			} // No execution permissions

			reqBody["vanity_domain_hook_path"] = tmpFile.Name()
			bodyBytes, _ = json.Marshal(reqBody)
			req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

			w = httptest.NewRecorder()
			srv.handleAdminEndpoints(w, req)

			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "must have execute permissions") {
				t.Errorf("expected 400 Bad Request indicating non-executable error, got: %d. Body: %s", w.Code, w.Body.String())
			}
		}
	}

	// 7. Owner setting a valid script file -> expected 200
	dir := "/usr/local/bin"
	if runtime.GOOS == "windows" {
		dir = os.TempDir()
	}
	tmpFile, err := os.CreateTemp(dir, "test-valid-hook-*.sh")
	if err != nil {
		t.Logf("Skipping valid script file test because cannot write to trusted directory: %v", err)
	} else {
		defer os.Remove(tmpFile.Name())
		if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
			t.Fatalf("failed to chmod temp file: %v", err)
		}

		reqBody["vanity_domain_hook_path"] = tmpFile.Name()
		bodyBytes, _ = json.Marshal(reqBody)
		req, _ = http.NewRequest(http.MethodPut, "http://example.com/api/admin/system-settings", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "lfr_session", Value: ownerToken})

		w = httptest.NewRecorder()
		srv.handleAdminEndpoints(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200 OK for valid executable in trusted folder, got: %d. Body: %s", w.Code, w.Body.String())
		}
	}
}
