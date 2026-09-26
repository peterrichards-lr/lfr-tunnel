package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// The `approval` state (#2267): ask, wait, be told.
//
// The property under test is not "an admin can make a token permanent" -- an admin could always
// do that. It is that a HOLDER can ask, that the asking is recorded where somebody will see it,
// and that a decision either way is distinguishable from no decision at all. A request flow whose
// only outcomes are "granted" and "still sitting there" tells the holder nothing and tells the
// next admin nothing about what the last one already refused.

func tokenFor(t *testing.T, srv *Server, user *db.User, name string, expires *time.Time) *db.PersonalAccessToken {
	t.Helper()
	pat := &db.PersonalAccessToken{
		UserID:      user.ID,
		Name:        name,
		TokenHash:   "hash-" + name,
		TokenPrefix: "pfx-" + name,
		ExpiresAt:   expires,
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("seeding token %s: %v", name, err)
	}
	return pat
}

// mustToken re-reads a token, failing the test on the read rather than discarding the error and
// letting the assertion below dereference nil. Six call sites, which is why it is a helper: a
// discarded read error there reports as a nil-pointer panic in an unrelated line.
func mustToken(t *testing.T, srv *Server, id int64) *db.PersonalAccessToken {
	t.Helper()
	pat, err := srv.db.GetPATByID(id)
	if err != nil {
		t.Fatalf("re-reading token %d: %v", id, err)
	}
	return pat
}

// mustQueue reads the pending-request queue, failing on the error for the same reason.
func mustQueue(t *testing.T, srv *Server) []*db.PersonalAccessToken {
	t.Helper()
	queue, err := srv.portalService.AdminListTokenPermanenceRequests()
	if err != nil {
		t.Fatalf("reading the permanence queue: %v", err)
	}
	return queue
}

func in30Days() *time.Time {
	t := time.Now().AddDate(0, 0, 30)
	return &t
}

// Creating a token with "never" under `approval` is itself the request. Making the holder ask a
// second time is a flow that loses requests.
func TestChoosingNeverUnderApprovalRaisesTheRequestAtCreation(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")

	if rec := postCreateToken(t, srv, session, 0); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/tokens: got %d", rec.Code)
	}

	pats, err := srv.db.ListPATs(dev.ID)
	if err != nil || len(pats) != 1 {
		t.Fatalf("listing: %v (%d)", err, len(pats))
	}
	if pats[0].PermanenceState != db.PATPermanencePending {
		t.Errorf("state is %q, want pending -- the holder asked and nothing recorded it", pats[0].PermanenceState)
	}

	queue := mustQueue(t, srv)
	if len(queue) != 1 || queue[0].ID != pats[0].ID {
		t.Errorf("the request is not in the admin queue (%d entries); a request nobody can see is not a request", len(queue))
	}
}

// CONTROL. Under the other two policies nothing is ever pending -- `allowed` grants outright and
// `disabled` refuses -- so a queue entry there would be one no admin action could ever clear.
func TestNothingIsPendingUnderTheOtherTwoPolicies(t *testing.T) {
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresAllowed, config.NeverExpiresDisabled} {
		t.Run(string(policy), func(t *testing.T) {
			srv := serverWithPolicy(t, policy, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
			_, session := userWithSession(t, srv, "dev@example.com", "developer")
			postCreateToken(t, srv, session, 0)

			if queue := mustQueue(t, srv); len(queue) != 0 {
				t.Errorf("policy %q left %d request(s) pending", policy, len(queue))
			}
		})
	}
}

// A token's needs change after it is created: a fortnight's token that becomes a standing
// integration should not have to be replaced, because replacing it means a new secret in
// somebody's CI.
func TestAHolderCanAskForAnExistingTokenToBecomePermanent(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())

	body := bytes.NewBufferString("{}")
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://example.com/api/tokens/%d/request-permanence", pat.ID), body)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("request-permanence: got %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}

	got := mustToken(t, srv, pat.ID)
	if got.PermanenceState != db.PATPermanencePending {
		t.Errorf("state is %q, want pending", got.PermanenceState)
	}
	if got.ExpiresAt == nil {
		t.Error("asking made the token permanent; the admin was never consulted")
	}
}

// Asking twice is one request, not two queue entries.
func TestAskingTwiceLeavesOneRequest(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())

	for i := 0; i < 2; i++ {
		if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if queue := mustQueue(t, srv); len(queue) != 1 {
		t.Errorf("two clicks produced %d queue entries", len(queue))
	}
}

// A request is only meaningful under `approval`. Under the other two it would sit pending with no
// admin action able to clear it.
func TestAskingIsRefusedWhereThereIsNothingToDecide(t *testing.T) {
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresAllowed, config.NeverExpiresDisabled} {
		srv := serverWithPolicy(t, policy, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
		dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
		pat := tokenFor(t, srv, dev, "ci", in30Days())

		_, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1")
		if !errors.Is(err, ErrPermanenceNotAllowed) {
			t.Errorf("policy %q: got %v, want ErrPermanenceNotAllowed", policy, err)
		}
	}
}

// Somebody else's token is NOT FOUND, not FORBIDDEN. A 403 confirms the id exists, which turns
// the id space into an enumeration oracle for every token on the gateway.
func TestAnotherUsersTokenIsNotFoundRatherThanForbidden(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	owner, _ := userWithSession(t, srv, "owner@example.com", "developer")
	other, _ := userWithSession(t, srv, "other@example.com", "developer")
	pat := tokenFor(t, srv, owner, "theirs", in30Days())

	_, err := srv.portalService.RequestTokenPermanence(other, fmt.Sprintf("%d", pat.ID), "127.0.0.1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound -- a distinguishable refusal tells a stranger the id is real", err)
	}
	if errors.Is(err, ErrForbidden) {
		t.Error("answered Forbidden, which confirms the token exists")
	}

	if got := mustToken(t, srv, pat.ID); got.PermanenceState == db.PATPermanencePending {
		t.Error("a stranger raised a request against somebody else's token")
	}
}

// Granting and denying, and the difference between them surviving into the row.
func TestAnAdminDecisionIsRecordedEitherWay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		grant     bool
		wantState string
		permanent bool
	}{
		{"granted", true, db.PATPermanenceGranted, true},
		{"denied", false, db.PATPermanenceDenied, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
			dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
			pat := tokenFor(t, srv, dev, "ci", in30Days())
			if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
				t.Fatalf("request: %v", err)
			}

			if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", pat.ID), tc.grant, "127.0.0.1"); err != nil {
				t.Fatalf("decide: %v", err)
			}

			got := mustToken(t, srv, pat.ID)
			if got.PermanenceState != tc.wantState {
				t.Errorf("state is %q, want %q", got.PermanenceState, tc.wantState)
			}
			if tc.permanent && got.ExpiresAt != nil {
				t.Errorf("granted, but the token still expires %v", got.ExpiresAt)
			}
			if !tc.permanent && got.ExpiresAt == nil {
				t.Error("denied, but the token was made permanent anyway")
			}

			// Decided means gone from the queue -- including the denial. A denied request
			// left in the queue is one the next admin decides again.
			if queue := mustQueue(t, srv); len(queue) != 0 {
				t.Errorf("a decided request is still queued (%d)", len(queue))
			}
		})
	}
}

// A denial is a state, not the absence of one. Without this the holder cannot tell "no" from
// "nobody has looked", and asks again.
func TestADenialIsDistinguishableFromNeverHavingAsked(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	asked := tokenFor(t, srv, dev, "asked", in30Days())
	never := tokenFor(t, srv, dev, "never-asked", in30Days())

	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", asked.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", asked.ID), false, "127.0.0.1"); err != nil {
		t.Fatalf("deny: %v", err)
	}

	deniedRow := mustToken(t, srv, asked.ID)
	neverRow := mustToken(t, srv, never.ID)
	if deniedRow.PermanenceState == neverRow.PermanenceState {
		t.Errorf("a denied token and one nobody asked about both read %q", deniedRow.PermanenceState)
	}
	if deniedRow.PermanenceState != db.PATPermanenceDenied {
		t.Errorf("denied token reads %q", deniedRow.PermanenceState)
	}
}

// Deciding a request that is not pending would let a second admin silently reverse the first.
func TestDecidingATokenThatIsNotPendingIsAConflict(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())

	_, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", pat.ID), true, "127.0.0.1")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("deciding an unrequested token: got %v, want ErrConflict", err)
	}
}

// An operator can tighten the policy while requests are already queued. The queue must not then
// be a way to grant what the gateway no longer allows.
func TestTighteningThePolicyDisarmsTheQueue(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}

	srv.cfg.NeverExpires.Tokens = config.NeverExpiresDisabled

	if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", pat.ID), true, "127.0.0.1"); !errors.Is(err, ErrPermanenceNotAllowed) {
		t.Errorf("granting under a tightened policy: got %v, want ErrPermanenceNotAllowed", err)
	}
	// Denying must still work, or the queue cannot be cleared at all.
	if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", pat.ID), false, "127.0.0.1"); err != nil {
		t.Errorf("denying under a tightened policy: %v -- the queue would be unclearable", err)
	}
}

// FIRING. A malformed or truncated body must not read as a denial: that records a decision
// against an admin who never made one, on a request they may have meant to grant.
func TestABodyThatDoesNotSayIsNotADenial(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}

	for _, body := range []string{`{}`, `{"grant":`, ``} {
		req, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://example.com/api/admin/tokens/%d/permanence", pat.ID), bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		srv.handleAdminDecideTokenPermanence(rec, req, "admin@example.com")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}

	if got := mustToken(t, srv, pat.ID); got.PermanenceState != db.PATPermanencePending {
		t.Errorf("a body that said nothing moved the request to %q", got.PermanenceState)
	}
}

// CONTROL. The same route with a body that DOES say must work, both ways.
func TestABodyThatSaysIsHonouredBothWays(t *testing.T) {
	for _, grant := range []bool{true, false} {
		srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
		dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
		pat := tokenFor(t, srv, dev, "ci", in30Days())
		if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
			t.Fatalf("request: %v", err)
		}

		body, _ := json.Marshal(map[string]bool{"grant": grant})
		req, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://example.com/api/admin/tokens/%d/permanence", pat.ID), bytes.NewBuffer(body))
		rec := httptest.NewRecorder()
		srv.handleAdminDecideTokenPermanence(rec, req, "admin@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("grant=%v: got %d. Body: %s", grant, rec.Code, rec.Body.String())
		}

		got := mustToken(t, srv, pat.ID)
		if grant && got.ExpiresAt != nil {
			t.Error("granted over HTTP but the token still expires")
		}
		if !grant && got.ExpiresAt == nil {
			t.Error("denied over HTTP but the token became permanent")
		}
	}
}

// A revoked token's request is not a decision anybody needs to make, and leaving it queued means
// a queue that grows with abandoned rows and stops being read.
func TestARevokedTokensRequestLeavesTheQueue(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if queue := mustQueue(t, srv); len(queue) != 1 {
		t.Fatalf("the request was not queued to begin with (%d)", len(queue))
	}

	if err := srv.db.RevokePAT(pat.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if queue := mustQueue(t, srv); len(queue) != 0 {
		t.Errorf("a revoked token is still in the queue (%d)", len(queue))
	}
}

// ---------------------------------------------------------------------------------------------
// The extension queue now names what it is deciding about.

// A custom domain IS a reservation with an empty subdomain (#1004), and the queue called every
// entry a subdomain extension. An admin was being shown the wrong noun for the thing in front of
// them.
func TestTheExtensionQueueNamesTheResourceItIsAbout(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresAllowed)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	sub := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "app", Domain: "example.com", ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	cd := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "", Domain: "vanity.customer.com", ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, r := range []*db.SubdomainReservation{sub, cd} {
		if err := srv.db.CreateSubdomainReservation(r); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	list, err := srv.portalService.AdminListExtensions()
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d", len(list))
	}

	byKind := map[string]*ExtensionRequestView{}
	for _, e := range list {
		byKind[e.ResourceKind] = e
	}
	if byKind[resourceKindSubdomain] == nil || byKind[resourceKindSubdomain].Subdomain != "app" {
		t.Errorf("the subdomain entry is not labelled %q: %+v", resourceKindSubdomain, byKind)
	}
	if byKind[resourceKindCustomDomain] == nil || byKind[resourceKindCustomDomain].Domain != "vanity.customer.com" {
		t.Errorf("the custom domain entry is not labelled %q: %+v", resourceKindCustomDomain, byKind)
	}
}

// The owner asked for three independent settings. Here is the one place they could most easily
// have been collapsed: both rows live in the same table, so charging a custom domain against
// never_expires.subdomains would look right and be wrong.
func TestTheQueueChargesEachRowToItsOwnPolicy(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresAllowed)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	sub := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "app", Domain: "example.com", ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	cd := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "", Domain: "vanity.customer.com", ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, r := range []*db.SubdomainReservation{sub, cd} {
		if err := srv.db.CreateSubdomainReservation(r); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	list, err := srv.portalService.AdminListExtensions()
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	for _, e := range list {
		want := e.ResourceKind == resourceKindCustomDomain
		if e.PermanenceAllowed != want {
			t.Errorf("%s %q: permanence_allowed=%v, want %v -- subdomains are disabled and custom domains allowed",
				e.ResourceKind, e.Domain, e.PermanenceAllowed, want)
		}
	}

	// And the approval itself has to agree with what the queue advertised, or the button is
	// offered and then answers 403.
	if _, err := srv.portalService.AdminApproveExtension("admin@example.com", fmt.Sprintf("%d", cd.ID), 0, true, "127.0.0.1"); err != nil {
		t.Errorf("approving a permanent CUSTOM DOMAIN where custom domains are allowed: %v", err)
	}
	if _, err := srv.portalService.AdminApproveExtension("admin@example.com", fmt.Sprintf("%d", sub.ID), 0, true, "127.0.0.1"); !errors.Is(err, ErrPermanenceNotAllowed) {
		t.Errorf("approving a permanent SUBDOMAIN where subdomains are disabled: got %v, want ErrPermanenceNotAllowed", err)
	}
}

// CONTROL / anti-vacuity. An empty queue must serialise as [] and not null, or a portal cannot
// tell "no requests" from "the call failed" without reading a JSON literal.
func TestAnEmptyExtensionQueueIsAListAndNotNull(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresDisabled)

	list, err := srv.portalService.AdminListExtensions()
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if list == nil {
		t.Fatal("an empty queue came back nil")
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if string(encoded) != "[]" {
		t.Errorf("an empty queue encodes as %s", encoded)
	}
}
