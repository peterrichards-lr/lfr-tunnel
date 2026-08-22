package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// viewAsSession creates a user, gives them a portal session, and returns the cookie value.
func viewAsSession(t *testing.T, srv *Server, email, role string) string {
	t.Helper()
	user := &db.User{ID: email, Email: email, Role: role, Status: "approved"}
	_ = srv.db.CreateUser(user) //nolint:errcheck

	token := generateToken(16)
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{
		Email:     email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	return token
}

func postViewAs(srv *Server, session, role string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"role": role}) //nolint:errcheck
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/me/view-as", bytes.NewBuffer(body))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	w := httptest.NewRecorder()
	srv.handleViewAs(w, req)
	return w
}

// Only the owner may preview another role. Anyone else asking is a privilege-escalation
// attempt, however casual (#1225).
func TestViewAs_RejectsNonOwners(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	for _, role := range []string{"user", "developer", "admin"} {
		t.Run(role, func(t *testing.T) {
			session := viewAsSession(t, srv, role+"@example.com", role)
			if w := postViewAs(srv, session, "user"); w.Code != http.StatusForbidden {
				t.Errorf("expected 403 for role %q, got %d: %s", role, w.Code, w.Body.String())
			}
			if got := srv.sessionViewAsRole(requestWithSession(session)); got != "" {
				t.Errorf("expected the session to be left untouched, got %q", got)
			}
		})
	}
}

// The override must never raise privilege, so "owner" is not a previewable role even for
// the owner, and neither is anything unrecognised.
func TestViewAs_RefusesToEscalateOrInvent(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	session := viewAsSession(t, srv, "owner@example.com", "owner")

	for _, role := range []string{"owner", "superuser", "root", "OWNER"} {
		if w := postViewAs(srv, session, role); w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for role %q, got %d: %s", role, w.Code, w.Body.String())
		}
	}
	if got := srv.sessionViewAsRole(requestWithSession(session)); got != "" {
		t.Errorf("expected no preview to have been set, got %q", got)
	}
}

func TestViewAs_OwnerCanEnterAndExit(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	session := viewAsSession(t, srv, "owner@example.com", "owner")

	if w := postViewAs(srv, session, "admin"); w.Code != http.StatusOK {
		t.Fatalf("expected 200 entering View As, got %d: %s", w.Code, w.Body.String())
	}
	if got := srv.sessionViewAsRole(requestWithSession(session)); got != "admin" {
		t.Errorf("expected the session to record 'admin', got %q", got)
	}

	if w := postViewAs(srv, session, ""); w.Code != http.StatusOK {
		t.Fatalf("expected 200 exiting View As, got %d: %s", w.Code, w.Body.String())
	}
	if got := srv.sessionViewAsRole(requestWithSession(session)); got != "" {
		t.Errorf("expected an empty role to exit the preview, got %q", got)
	}
}

// The point of the feature: handlers see the previewed role, so every existing role check
// behaves as it would for that role. Authority still reads the real one.
func TestViewAs_HandlersSeeThePreviewedRoleButAuthorityDoesNot(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	session := viewAsSession(t, srv, "owner@example.com", "owner")

	if w := postViewAs(srv, session, "user"); w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", w.Code, w.Body.String())
	}

	effective, err := srv.getCurrentUser(requestWithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective.Role != "user" {
		t.Errorf("expected handlers to see 'user', got %q", effective.Role)
	}

	real, err := srv.getCurrentUserRaw(requestWithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if real.Role != "owner" {
		t.Errorf("expected the real role to remain 'owner', got %q", real.Role)
	}
	// The preview must be a copy: downgrading what a handler sees must not rewrite the
	// underlying record.
	if effective == real {
		t.Error("expected the preview to be a distinct value, not the same user")
	}
}

// Defence in depth. If a session somehow carries a preview role without belonging to an
// owner, it must be ignored rather than honoured -- the stored value is not authority.
func TestViewAs_IgnoresAPreviewOnANonOwnerSession(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	email := "plain@example.com"
	user := &db.User{ID: email, Email: email, Role: "user", Status: "approved"}
	_ = srv.db.CreateUser(user) //nolint:errcheck

	token := generateToken(16)
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{
		Email:      email,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
		ViewAsRole: "admin", // never legitimately reachable; assert it grants nothing
	})

	effective, err := srv.getCurrentUser(requestWithSession(token))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective.Role != "user" {
		t.Errorf("a preview role on a non-owner session must be ignored, got %q", effective.Role)
	}
}

// The read-only guarantee, which is the whole safety argument: it is refused at the request
// boundary, so it does not depend on any control being disabled in any portal.
func TestViewAs_ReadOnlyGuard(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	session := viewAsSession(t, srv, "owner@example.com", "owner")
	if w := postViewAs(srv, session, "user"); w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", w.Code, w.Body.String())
	}

	tests := []struct {
		name    string
		method  string
		path    string
		blocked bool
	}{
		{"reading is allowed", http.MethodGet, "/api/portal/reservations", false},
		{"HEAD is allowed", http.MethodHead, "/api/me", false},
		{"creating is refused", http.MethodPost, "/api/portal/reservations", true},
		{"updating is refused", http.MethodPut, "/api/me", true},
		{"deleting is refused", http.MethodDelete, "/api/tokens/1", true},
		{"patching is refused", http.MethodPatch, "/api/admin/users/1", true},
		// Without these two the owner would be trapped: no way out and no way to log out.
		{"leaving View As is allowed", http.MethodPost, "/api/me/view-as", false},
		{"logging out is allowed", http.MethodPost, "/api/auth/logout", false},
		// The guard is for the API surface only; page loads are not its business.
		{"non-API paths are untouched", http.MethodPost, "/portalv2", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, "http://example.com"+tc.path, nil)
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
			w := httptest.NewRecorder()

			if got := srv.enforceViewAsReadOnly(w, req); got != tc.blocked {
				t.Fatalf("expected blocked=%v for %s %s, got %v", tc.blocked, tc.method, tc.path, got)
			}
			if tc.blocked && w.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", w.Code)
			}
		})
	}
}

// A normal session must be entirely unaffected -- the guard only exists for previews.
func TestViewAs_NormalSessionsAreNotReadOnly(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	session := viewAsSession(t, srv, "someone@example.com", "user")

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	w := httptest.NewRecorder()

	if srv.enforceViewAsReadOnly(w, req) {
		t.Error("a session that is not previewing must not be made read-only")
	}
}

func requestWithSession(token string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/me", nil) //nolint:errcheck
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: token})
	return req
}
