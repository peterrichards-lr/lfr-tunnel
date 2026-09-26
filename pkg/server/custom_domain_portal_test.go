package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Registering a custom domain from the portal (#2222).
//
// The property that matters is not "the endpoint returns 200". It is that a user WITHOUT
// auto-reservation -- which is everyone who is not admin or owner, and stays that way
// deliberately -- can obtain a custom domain at all. Before this endpoint they could not:
// registration refused them with "Custom domains must be reserved in the portal prior to
// connecting" and no portal route created one, so the refusal named a control that did not exist.

func aPortalUser(t *testing.T, srv *Server, email string) (*db.User, string) {
	t.Helper()
	return seedDiagnosticsUser(t, srv, email, "user")
}

// aPortalServerAllowing builds a server whose custom-domain quota is stated rather than inherited.
//
// setupTestServerForAPI constructs a config LITERAL, so every field DefaultServerConfig would
// have filled is its zero value -- DefaultMaxCustomDomains included. That is 0, not the 1 a real
// deployment gets, and 0 means "none allowed". Setting it here makes each test say which limit it
// is asserting against, which the quota test below depends on to mean anything at all.
func aPortalServerAllowing(t *testing.T, customDomains int) *Server {
	t.Helper()
	srv := setupTestServerForAPI(t)
	srv.cfg.DefaultMaxCustomDomains = customDomains
	// Custom domains are permanent only where the operator has said so (#2264). This was
	// unconditional when #2222 was written, and the config literal setupTestServerForAPI builds
	// leaves every never_expires policy at its zero value, which reads as "disabled". Stated
	// here so these tests keep asserting #1009's behaviour rather than silently switching to
	// asserting the new default -- which pkg/server/never_expires_test.go covers separately.
	srv.cfg.NeverExpires.CustomDomains = config.NeverExpiresAllowed
	return srv
}

// The whole point: a plain user, no auto-reservation, ends up holding the domain.
func TestAPlainUserCanRegisterACustomDomainFromThePortal(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	user, session := aPortalUser(t, srv, "colleague@example.com")

	rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", session,
		map[string]string{"domain": "demo.customer.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("registering a custom domain returned %d: %s", rec.Code, rec.Body.String())
	}

	var res db.SubdomainReservation
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decoding the reservation: %v", err)
	}
	if res.Domain != "demo.customer.com" {
		t.Errorf("the reservation names %q, want demo.customer.com", res.Domain)
	}
	if res.Subdomain != "" {
		t.Errorf("the reservation carries subdomain %q; a custom domain is a row with an EMPTY "+
			"subdomain, and one with a subdomain is charged to the wrong quota and expires",
			res.Subdomain)
	}
	// Permanent, matching what the registration path creates (#1009) -- on a gateway whose
	// never_expires.custom_domains says so, which aPortalServerAllowing sets (#2264).
	if res.ExpiresAt != nil {
		t.Errorf("the reservation expires at %v; custom domains are permanent where the operator "+
			"allows it, because nobody else can claim a name its holder controls through DNS", res.ExpiresAt)
	}
	if res.UserID != user.ID {
		t.Errorf("the reservation belongs to %q, want %q", res.UserID, user.ID)
	}

	// And the registration path can now find it -- which is the thing the endpoint exists for.
	stored, err := srv.db.GetSubdomainReservationByName("", "demo.customer.com")
	if err != nil || stored == nil {
		t.Fatalf("the reservation is not readable the way registration looks it up "+
			"(GetSubdomainReservationByName with an empty subdomain): %v", err)
	}
	if srv.standingOf(stored) != reservationLive {
		t.Error("the reservation is not live, so registration would not accept it")
	}
}

// A name under a domain this gateway already serves is a SUBDOMAIN, and has its own quota,
// expiry and portal control. Accepting it here would create a permanent row in the wrong quota
// for a name the subdomain flow believes it owns.
func TestADomainThisGatewayAlreadyServesIsRefused(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, session := aPortalUser(t, srv, "colleague2@example.com")

	for _, domain := range []string{"example.com", "demo.example.com"} {
		rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", session,
			map[string]string{"domain": domain})
		if rec.Code == http.StatusOK {
			t.Errorf("%q was accepted as a custom domain, but this gateway serves example.com -- "+
				"that is a subdomain reservation, with a different quota and an expiry", domain)
		}
	}
}

// Shape, through the validator the registration path already uses rather than a second one.
func TestAMalformedDomainIsRefused(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, session := aPortalUser(t, srv, "colleague3@example.com")

	for _, domain := range []string{"", "nodot", "-leading.customer.com", "trailing-.customer.com", "spaced out.com"} {
		rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", session,
			map[string]string{"domain": domain})
		if rec.Code == http.StatusOK {
			t.Errorf("%q was accepted as a custom domain", domain)
		}
	}
}

// The quota is the custom-domain one, not the subdomain one (#1004). Default is 1.
func TestTheCustomDomainQuotaIsEnforcedAndIsItsOwn(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, session := aPortalUser(t, srv, "colleague4@example.com")

	first := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", session,
		map[string]string{"domain": "one.customer.com"})
	if first.Code != http.StatusOK {
		t.Fatalf("the first custom domain returned %d: %s", first.Code, first.Body.String())
	}

	second := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", session,
		map[string]string{"domain": "two.customer.com"})
	if second.Code == http.StatusOK {
		t.Errorf("a second custom domain was accepted while DefaultMaxCustomDomains is %d -- the "+
			"quota is not being counted, or is being counted against the subdomain limit",
			srv.cfg.DefaultMaxCustomDomains)
	}
}

// Another user's live domain is a conflict; the holder's own is idempotent.
//
// Both halves in one test on purpose: "it returned non-200" is satisfied by refusing everybody,
// and "it returned 200" by accepting everybody. Only the pair distinguishes the rule.
func TestADomainHeldByAnotherUserConflictsButTheHoldersOwnIsIdempotent(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, holder := aPortalUser(t, srv, "holder@example.com")
	_, other := aPortalUser(t, srv, "other@example.com")

	if rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", holder,
		map[string]string{"domain": "shared.customer.com"}); rec.Code != http.StatusOK {
		t.Fatalf("the holder's first registration returned %d: %s", rec.Code, rec.Body.String())
	}

	if rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", other,
		map[string]string{"domain": "shared.customer.com"}); rec.Code == http.StatusOK {
		t.Error("a second user registered a domain another user already holds; the reservation " +
			"is what stops two tunnels claiming one name")
	}

	if rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", holder,
		map[string]string{"domain": "shared.customer.com"}); rec.Code != http.StatusOK {
		t.Errorf("the holder re-registering their own domain returned %d, want 200: landing on "+
			"the form twice has changed nothing and should not read as a conflict", rec.Code)
	}
}

// Anonymous callers get nothing. Reserving a name is not a read.
func TestRegisteringACustomDomainRequiresASession(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	rec := postAs(t, srv, srv.handleCreateCustomDomain, "/api/portal/custom-domains", "not-a-session",
		map[string]string{"domain": "anon.customer.com"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated registration returned %d, want 401", rec.Code)
	}
}

// The handler has to be REACHABLE, not merely correct.
//
// Every test above calls handleCreateCustomDomain directly, so all of them would pass with the
// route misspelled, mounted on the wrong method, or never added -- a handler nothing dispatches
// to changes no behaviour at all. This one goes through Server.ServeHTTP, which is what a browser
// reaches.
func TestThePortalRouteReachesTheCustomDomainHandler(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, session := aPortalUser(t, srv, "routed@example.com")

	body := bytes.NewReader([]byte(`{"domain":"routed.customer.com"}`))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/portal/custom-domains", body)
	req.RemoteAddr = "203.0.113.7:5555"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/portal/custom-domains through the real mux returned %d: %s\n\n"+
			"The handler is only as good as the route: every other test here calls it directly "+
			"and would pass with no route at all.", rec.Code, rec.Body.String())
	}
	if stored, err := srv.db.GetSubdomainReservationByName("", "routed.customer.com"); err != nil || stored == nil {
		t.Errorf("the route answered 200 but no reservation exists, so something else served "+
			"that path: %v", err)
	}
}

// GET is not a way to reserve a name. Asserted because the route is matched on method AND path,
// and a route that ignored the method would let a link, a prefetch or a crawler reserve domains.
func TestTheCustomDomainRouteDoesNotAnswerGET(t *testing.T) {
	srv := aPortalServerAllowing(t, 1)
	defer srv.Stop()

	_, session := aPortalUser(t, srv, "getter@example.com")

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/portal/custom-domains", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("GET /api/portal/custom-domains returned 200; reserving a name is not a read, " +
			"and a route that ignores the method can be triggered by a prefetch")
	}
}
