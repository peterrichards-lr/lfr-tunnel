package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// TestServer_HandleListVanityDomainStatus verifies the portal-facing, user-scoped read
// endpoint added for #967: a user only ever sees their own domains' provisioning status,
// never anyone else's.
func TestServer_HandleListVanityDomainStatus(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev) //nolint:errcheck
	other := &db.User{ID: "other@example.com", Email: "other@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(other) //nolint:errcheck

	// dev's domain reaches the nginx_config stage.
	_ = srv.db.StartVanityDomainAttempt("dev-site.com", dev.ID)      //nolint:errcheck
	_ = srv.db.MarkVanityDomainStage("dev-site.com", "nginx_config") //nolint:errcheck
	// other's domain is a fully separate attempt -- must never show up in dev's response.
	_ = srv.db.StartVanityDomainAttempt("other-site.com", other.ID) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/portal/vanity-domain-status", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleListVanityDomainStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp []db.VanityDomainStatus
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected exactly 1 status (only dev's own domain), got %d", len(resp))
	}
	if resp[0].FullHost != "dev-site.com" {
		t.Errorf("expected dev-site.com, got %q", resp[0].FullHost)
	}
	if resp[0].NginxConfigAt == nil {
		t.Error("expected NginxConfigAt to be set after MarkVanityDomainStage")
	}
	if resp[0].CertIssuedAt != nil {
		t.Error("expected CertIssuedAt to still be nil -- that stage was never reached")
	}
}

// TestServer_HandleAdminListVanityDomainStatus verifies the admin-facing variant returns
// every user's domains, not just the requesting admin's own.
func TestServer_HandleAdminListVanityDomainStatus(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	_ = srv.db.StartVanityDomainAttempt("site-a.com", "user-a@example.com")                //nolint:errcheck
	_ = srv.db.StartVanityDomainAttempt("site-b.com", "user-b@example.com")                //nolint:errcheck
	_ = srv.db.MarkVanityDomainFailed("site-b.com", "cert_issued", "Certbot rate limited") //nolint:errcheck

	sessionToken := newAdminSession(t, srv, "admin@example.com")
	req := adminRequest(http.MethodGet, "http://example.com/api/admin/vanity-domain-status", nil, sessionToken)

	w := httptest.NewRecorder()
	srv.handleAdminListVanityDomainStatus(w, req, "admin@example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp []db.VanityDomainStatus
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected both domains across both users, got %d", len(resp))
	}

	var failed *db.VanityDomainStatus
	for i := range resp {
		if resp[i].FullHost == "site-b.com" {
			failed = &resp[i]
		}
	}
	if failed == nil {
		t.Fatal("expected site-b.com in the response")
	}
	if failed.FailedStage != "cert_issued" {
		t.Errorf("expected FailedStage cert_issued, got %q", failed.FailedStage)
	}
	if failed.ErrorMessage != "Certbot rate limited" {
		t.Errorf("expected error message to be preserved, got %q", failed.ErrorMessage)
	}
}

// TestServer_HandleListVanityDomainStatus_Unauthorized verifies the portal endpoint rejects
// an unauthenticated request rather than silently returning an empty list.
func TestServer_HandleListVanityDomainStatus_Unauthorized(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/portal/vanity-domain-status", nil)
	w := httptest.NewRecorder()
	srv.handleListVanityDomainStatus(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w.Code)
	}
}

// TestServer_HandleAdminRetryVanityDomain verifies the admin retry action queues cleanly
// for a tracked domain -- the actual hook re-run happens in a background goroutine (covered
// by runVanityDomainHook's own tests) and writeAudit's own DB write is *also* asynchronous
// ("run in a goroutine so it doesn't block the HTTP response"), so this only asserts on the
// synchronous part of the HTTP contract, matching every other admin-action test in this
// package -- none of them assert on the audit trail for the same reason.
func TestServer_HandleAdminRetryVanityDomain(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	_ = srv.db.StartVanityDomainAttempt("retry-me.com", "user@example.com")                  //nolint:errcheck
	_ = srv.db.MarkVanityDomainFailed("retry-me.com", "cert_issued", "Certbot rate limited") //nolint:errcheck

	sessionToken := newAdminSession(t, srv, "admin@example.com")
	req := adminRequest(http.MethodPost, "http://example.com/api/admin/vanity-domain-status/retry-me.com/retry", nil, sessionToken)

	w := httptest.NewRecorder()
	srv.handleAdminRetryVanityDomain(w, req, "admin@example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

// TestServer_HandleAdminRetryVanityDomain_NotFound verifies retrying an untracked domain
// 404s instead of silently queuing a no-op.
func TestServer_HandleAdminRetryVanityDomain_NotFound(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	sessionToken := newAdminSession(t, srv, "admin@example.com")
	req := adminRequest(http.MethodPost, "http://example.com/api/admin/vanity-domain-status/never-existed.com/retry", nil, sessionToken)

	w := httptest.NewRecorder()
	srv.handleAdminRetryVanityDomain(w, req, "admin@example.com")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w.Code)
	}
}

// TestServer_HandleAdminRemoveVanityDomain verifies the admin remove action queues cleanly,
// mirroring the retry test above (see its comment for why this doesn't assert on the audit
// trail, which writeAudit writes asynchronously).
func TestServer_HandleAdminRemoveVanityDomain(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	_ = srv.db.StartVanityDomainAttempt("remove-me.com", "user@example.com") //nolint:errcheck

	sessionToken := newAdminSession(t, srv, "admin@example.com")
	req := adminRequest(http.MethodPost, "http://example.com/api/admin/vanity-domain-status/remove-me.com/remove", nil, sessionToken)

	w := httptest.NewRecorder()
	srv.handleAdminRemoveVanityDomain(w, req, "admin@example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}
