package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// requireAdmin is the SOLE authorization for the ~47 routes under /api/admin/*, and its cookie
// branch promoted any authenticated portal user to admin (#1760).
//
// The bug was one condition: the role was read authoritatively from the database and then
// `actorRole == "user"` was folded in with `actorRole == ""` before defaulting to admin, so
// "the database says this person is an ordinary user" and "we could not find out" took the same
// branch. Reachable with nothing but an ordinary browser session — magic-link, SSO or MFA — and
// it opened database backup download, audit export, user deletion and lease kicks.
//
// These cases assert the role decision itself rather than any one route, because the route list
// changes and the decision is what every one of them trusts.

// adminAuthFixture builds a server whose session store holds one live portal session for email,
// and whose database returns a user with the given role.
func adminAuthFixture(t *testing.T, email, role, ownerID string) (*Server, *http.Request) {
	t.Helper()

	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.Owner.UserID = ownerID
	cfg.DisableBackupScheduler = true
	// A real database, because the whole point is that the role read back from it is believed.
	cfg.DBPath = filepath.Join(t.TempDir(), "require_admin_test.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	// Stop closes the SQLite handle; leaving it open makes the temp-dir cleanup fail on Windows.
	t.Cleanup(srv.Stop)

	if role != "" {
		// Update if the row already exists -- the configured owner may be seeded at startup,
		// and for TestRequireAdmin_ConfiguredOwnerOutranksAUserRow the row MUST say "user"
		// or the test proves nothing.
		if existing, err := srv.db.GetUserByEmail(email); err == nil && existing != nil {
			existing.Role = role
			if err := srv.db.UpdateUser(existing); err != nil {
				t.Fatalf("setting role on the existing row: %v", err)
			}
		} else if err := srv.db.CreateUser(&db.User{ID: email, Email: email, Role: role, Status: "approved"}); err != nil {
			t.Fatalf("seeding user: %v", err)
		}
	}

	// Same fixture shape the existing portal-session tests use.
	const sessionToken = "role-fixture-session"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	token := sessionToken

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audit", nil)
	req.Host = "tunnel.lfr-demo.se"
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: token})
	return srv, req
}

// The defect itself. A plain user with a valid session must not be admin.
func TestRequireAdmin_PlainUserWithASessionIsRefused(t *testing.T) {
	srv, req := adminAuthFixture(t, "user@example.com", "user", "owner@example.com")

	rec := httptest.NewRecorder()
	_, role, ok := srv.requireAdmin(rec, req)

	if ok {
		t.Errorf("a 'user' role account was authorised for /api/admin/* as %q -- "+
			"that is database backup download, audit export and user deletion (#1760)", role)
	}
	if role == roleAdmin || role == roleOwner {
		t.Errorf("role resolved to %q for a plain user", role)
	}
}

// The counterpart: fixing this must not lock out the people who are supposed to get in,
// otherwise the "fix" is an outage.
func TestRequireAdmin_AdminAndOwnerStillPass(t *testing.T) {
	for _, tc := range []struct{ name, role, want string }{
		{"an admin is admitted", roleAdmin, roleAdmin},
		{"an owner is admitted", roleOwner, roleOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, req := adminAuthFixture(t, "someone@example.com", tc.role, "owner@example.com")

			rec := httptest.NewRecorder()
			_, role, ok := srv.requireAdmin(rec, req)

			if !ok {
				t.Fatalf("a %q was refused; the fix has locked out legitimate access", tc.role)
			}
			if role != tc.want {
				t.Errorf("role = %q, want %q", role, tc.want)
			}
		})
	}
}

// The configured owner outranks the row, because a login path can create the account with
// Role: "user" -- SSO does exactly that -- and the deployment's own configuration is the more
// authoritative statement of who owns it.
func TestRequireAdmin_ConfiguredOwnerOutranksAUserRow(t *testing.T) {
	srv, req := adminAuthFixture(t, "owner@example.com", "user", "owner@example.com")

	rec := httptest.NewRecorder()
	_, role, ok := srv.requireAdmin(rec, req)

	if !ok {
		t.Fatal("the configured owner was refused, so an SSO-created owner account could not administer its own gateway")
	}
	if role != roleOwner {
		t.Errorf("role = %q, want %q", role, roleOwner)
	}
}

// Fails closed. An unreadable role must refuse rather than default to admin -- which is the
// shape of the original bug, and the reason "" and "user" must not share a branch.
func TestRequireAdmin_UnknownUserIsRefused(t *testing.T) {
	// A session for an email with no row behind it: the lookup finds nothing, so the role is
	// unknown rather than merely low.
	srv, req := adminAuthFixture(t, "ghost@example.com", "", "owner@example.com")

	rec := httptest.NewRecorder()
	_, role, ok := srv.requireAdmin(rec, req)

	if ok {
		t.Errorf("a session whose user could not be resolved was authorised as %q -- "+
			"admin routes must fail closed when the role cannot be established", role)
	}
}
