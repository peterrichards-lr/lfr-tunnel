package server

import (
	"net/http"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// #2474: a successful SSO sign-in's audit row names the provider it came through. It used to have
// empty details, so the log said "via SSO" and nothing about which one.
func TestSSOSignInAuditNamesTheProvider(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st) // provider id "stub", display name "Stub"
	cookies, state := startSignIn(t, srv, st)
	if rec := finishSignIn(srv, cookies, state); rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body.String())
	}

	rows, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "user.login.sso", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one user.login.sso row, got %d (%v)", len(rows), err)
	}
	if d := rows[0].Details; !strings.Contains(d, "Stub") || !strings.Contains(d, "stub") {
		t.Errorf("audit details %q do not name the provider (Stub / stub)", d)
	}
}
