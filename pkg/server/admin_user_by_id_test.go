package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2469: the admin user routes looked their target up by email, and since #2466 an email lookup
// returns the OLDEST of any case duplicates. An admin deleting the newer duplicate erased the
// original account. The portals now send the user's id, which names exactly one row.

// caseDuplicates seeds the original account and a newer one whose address differs only in case.
func caseDuplicates(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv := setupTestServerForAPI(t)
	t.Cleanup(srv.Stop)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, u := range []*db.User{
		{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: db.UserStatusApproved, CreatedAt: created},
		{ID: "Dup@Example.com", Email: "Dup@Example.com", Role: "user", Status: db.UserStatusApproved, CreatedAt: created},
		{ID: "dup@example.com", Email: "dup@example.com", Role: "user", Status: db.UserStatusApproved, CreatedAt: created.Add(time.Hour)},
	} {
		if err := srv.db.CreateUser(u); err != nil {
			t.Fatalf("seeding %s: %v", u.ID, err)
		}
	}
	return srv, "Dup@Example.com", "dup@example.com"
}

func adminUserRequest(method, key, suffix, body string) *http.Request {
	return httptest.NewRequest(method, "http://example.com/api/admin/users/"+key+suffix, strings.NewReader(body))
}

func TestAdminDeleteByIdErasesTheNamedDuplicateOnly(t *testing.T) {
	srv, original, newer := caseDuplicates(t)

	rec := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rec, adminUserRequest(http.MethodDelete, newer, "", ""), "admin@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE %s = %d %s", newer, rec.Code, rec.Body.String())
	}
	if _, err := srv.db.GetUser(original); err != nil {
		t.Errorf("deleting %s erased the original account %s: %v", newer, original, err)
	}
	if _, err := srv.db.GetUser(newer); err == nil {
		t.Errorf("%s, the account named, still exists", newer)
	}
}

func TestAdminEditsByIdLandOnTheNamedDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name, method, suffix, body string
		call                       func(*Server, http.ResponseWriter, *http.Request)
		changed                    func(*db.User) bool
	}{
		{"status", http.MethodPatch, "", `{"status":"revoked"}`,
			func(s *Server, w http.ResponseWriter, r *http.Request) {
				s.handleAdminPatchUser(w, r, "admin@example.com", "admin")
			},
			func(u *db.User) bool { return u.Status == db.UserStatusRevoked }},
		{"reservations limit", http.MethodPost, "/limit", `{"max_reservations":7}`,
			func(s *Server, w http.ResponseWriter, r *http.Request) {
				s.handleAdminOverrideLimit(w, r, "admin@example.com")
			},
			func(u *db.User) bool { return u.MaxReservations != nil && *u.MaxReservations == 7 }},
		{"tunnels limit", http.MethodPost, "/tunnels-limit", `{"max_tunnels":7}`,
			func(s *Server, w http.ResponseWriter, r *http.Request) {
				s.handleAdminOverrideTunnelsLimit(w, r, "admin@example.com")
			},
			func(u *db.User) bool { return u.MaxTunnels != nil && *u.MaxTunnels == 7 }},
		{"preferred domain", http.MethodPut, "/preferred-domain", `{"preferred_domain":"example.org"}`,
			func(s *Server, w http.ResponseWriter, r *http.Request) {
				s.handleAdminOverridePreferredDomain(w, r, "admin@example.com")
			},
			func(u *db.User) bool { return u.PreferredDomain == "example.org" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, original, newer := caseDuplicates(t)
			rec := httptest.NewRecorder()
			tc.call(srv, rec, adminUserRequest(tc.method, newer, tc.suffix, tc.body))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s%s = %d %s", tc.method, newer, tc.suffix, rec.Code, rec.Body.String())
			}
			for id, want := range map[string]bool{newer: true, original: false} {
				u, err := srv.db.GetUser(id)
				if err != nil {
					t.Fatalf("reading %s: %v", id, err)
				}
				if tc.changed(u) != want {
					t.Errorf("after editing %s, %s changed = %v, want %v", newer, id, !want, want)
				}
			}
		})
	}
}

func TestAdminGetByIdReturnsTheNamedDuplicate(t *testing.T) {
	srv, _, newer := caseDuplicates(t)
	rec := httptest.NewRecorder()
	srv.handleAdminGetUser(rec, adminUserRequest(http.MethodGet, newer, "", ""), "admin@example.com")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+newer+`"`) {
		t.Errorf("GET %s = %d %s, want that row", newer, rec.Code, rec.Body.String())
	}
}

// An API caller that sends an address in another case still reaches the account, as before.
func TestAdminRoutesStillAcceptAnEmail(t *testing.T) {
	srv, original, _ := caseDuplicates(t)
	rec := httptest.NewRecorder()
	srv.handleAdminGetUser(rec, adminUserRequest(http.MethodGet, "DUP@EXAMPLE.COM", "", ""), "admin@example.com")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+original+`"`) {
		t.Errorf("GET by email = %d %s, want the original account", rec.Code, rec.Body.String())
	}
}
