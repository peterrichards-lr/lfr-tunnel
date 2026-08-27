package server

import (
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/db"
)

// Scoping Personal Usage to another user (#1294).
//
// The endpoint is reachable by every authenticated user, so `user_id` is an untrusted parameter
// on a path that reads someone's figures. Two properties matter: only an admin may re-scope,
// and a non-admin who tries learns nothing by trying.

func makeUser(t *testing.T, srv *Server, id, role string) *db.User {
	t.Helper()
	u := &db.User{ID: id, Email: id, Role: role, Status: "approved"}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("creating %s: %v", id, err)
	}
	return u
}

// TestAnalyticsSubjectIgnoresUserIDFromNonAdmins is the security property.
//
// A developer asking for somebody else's figures gets their own, and no error. Refusing would
// answer "does this user exist", which turns the parameter into an enumeration oracle on an
// endpoint any authenticated user can reach -- so the request is quietly honoured as a request
// for their own data instead.
func TestAnalyticsSubjectIgnoresUserIDFromNonAdmins(t *testing.T) {
	srv := setupTestServerForAPI(t)
	dev := makeUser(t, srv, "dev@example.com", "developer")
	makeUser(t, srv, "victim@example.com", "developer")

	req := httptest.NewRequest("GET", "http://example.com/api/analytics?user_id=victim@example.com", nil)
	subject, err := srv.analyticsSubject(req, dev, false)
	if err != nil {
		t.Fatalf("a non-admin passing a user_id must not be an error: %v", err)
	}
	if subject.ID != dev.ID {
		t.Errorf("a developer read %q's analytics; expected their own", subject.ID)
	}

	// The same request for a user who does NOT exist must behave identically, or the difference
	// between the two answers is the oracle.
	req = httptest.NewRequest("GET", "http://example.com/api/analytics?user_id=nobody@example.com", nil)
	subject, err = srv.analyticsSubject(req, dev, false)
	if err != nil || subject.ID != dev.ID {
		t.Errorf("an existing and a non-existent target must be indistinguishable to a non-admin; got id=%v err=%v",
			subject, err)
	}
}

// TestAnalyticsSubjectHonoursUserIDForAdmins — the feature itself.
func TestAnalyticsSubjectHonoursUserIDForAdmins(t *testing.T) {
	srv := setupTestServerForAPI(t)
	admin := makeUser(t, srv, "admin@example.com", "admin")
	target := makeUser(t, srv, "target@example.com", "developer")

	req := httptest.NewRequest("GET", "http://example.com/api/analytics?user_id="+target.ID, nil)
	subject, err := srv.analyticsSubject(req, admin, true)
	if err != nil {
		t.Fatalf("an admin must be able to scope to another user: %v", err)
	}
	if subject.ID != target.ID {
		t.Errorf("expected %q, got %q", target.ID, subject.ID)
	}
}

// TestAnalyticsSubjectRejectsUnknownUserForAdmins — an admin may be told a user does not exist,
// so this is a plain 404 rather than a silent fallback that would show them their own figures
// under somebody else's name.
func TestAnalyticsSubjectRejectsUnknownUserForAdmins(t *testing.T) {
	srv := setupTestServerForAPI(t)
	admin := makeUser(t, srv, "admin@example.com", "admin")

	req := httptest.NewRequest("GET", "http://example.com/api/analytics?user_id=ghost@example.com", nil)
	if _, err := srv.analyticsSubject(req, admin, true); err == nil {
		t.Error("an admin asking for a user that does not exist must be told, not shown their own data")
	}
}

// TestAnalyticsSubjectDefaultsToCaller — no parameter, and asking for yourself, both mean you.
func TestAnalyticsSubjectDefaultsToCaller(t *testing.T) {
	srv := setupTestServerForAPI(t)
	admin := makeUser(t, srv, "admin@example.com", "admin")

	for _, url := range []string{
		"http://example.com/api/analytics",
		"http://example.com/api/analytics?user_id=",
		"http://example.com/api/analytics?user_id=admin@example.com",
	} {
		req := httptest.NewRequest("GET", url, nil)
		subject, err := srv.analyticsSubject(req, admin, true)
		if err != nil || subject.ID != admin.ID {
			t.Errorf("%s should report the caller's own figures; got %v (err %v)", url, subject, err)
		}
	}
}

// TestAnalyticsViewIsAudited — reading another person's usage should leave a trail. Recorded on
// the read rather than on a UI action, so a request made with curl is logged like any other.
func TestAnalyticsViewIsAudited(t *testing.T) {
	srv := setupTestServerForAPI(t)
	admin := makeUser(t, srv, "admin@example.com", "admin")
	target := makeUser(t, srv, "target@example.com", "developer")

	req := httptest.NewRequest("GET", "http://example.com/api/analytics?user_id="+target.ID, nil)
	srv.auditAnalyticsView(admin, target, req)

	// The DEFAULT filter on purpose: routine portal-visit entries are hidden from it, and this
	// must not be one of them. An access record nobody sees without knowing to ask is not a
	// trail.
	entries, err := srv.db.ListAuditEntries(db.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("reading the audit log: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == "analytics.viewed_user" && e.TargetID == target.ID && e.ActorID == admin.Email {
			found = true
		}
	}
	if !found {
		t.Errorf("viewing another user's analytics must be audited; got %d entries", len(entries))
	}
}

// TestDisplayNameFallsBackToEmail — the UI labels the panel with whatever this returns, so it
// must never be blank.
func TestDisplayNameFallsBackToEmail(t *testing.T) {
	cases := []struct {
		user db.User
		want string
	}{
		{db.User{Email: "a@example.com"}, "a@example.com"},
		{db.User{Email: "a@example.com", FirstName: "Ada"}, "Ada"},
		{db.User{Email: "a@example.com", FirstName: "Ada", LastName: "Lovelace"}, "Ada Lovelace"},
		{db.User{Email: "a@example.com", FirstName: "Ada", PreferredName: "Ada L."}, "Ada L."},
		{db.User{Email: "a@example.com", FirstName: "   "}, "a@example.com"},
	}
	for _, c := range cases {
		if got := displayName(&c.user); got != c.want {
			t.Errorf("displayName(%+v) = %q, want %q", c.user, got, c.want)
		}
	}
}
