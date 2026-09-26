package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// ---------------------------------------------------------------------------------------------
// The sixth door, found reviewing this PR.
//
// The class rule: a function that writes a reservation's expiry must choose the policy from what
// the ROW is, not from where the function lives. Three functions in api_service_reservation.go
// write one, and two of them were fixed while the third -- the "Reject" button on the very queue
// this change taught to display custom domains -- kept reaching for the subdomain policy.

func TestDemotingACustomDomainUsesTheCustomDomainPolicy(t *testing.T) {
	// The configuration that makes it visible: the two policies DISAGREE, and the holder's role
	// is configured permanent. Under matching policies the bug is invisible, which is why it
	// survived a suite that never set them apart.
	zero := 0
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresAllowed, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {SubdomainExpiryDays: &zero}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "", Domain: "vanity.customer.com",
		ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.db.CreateSubdomainReservation(cd); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	// The admin presses REJECT.
	got, err := srv.portalService.AdminDemoteReservation("admin@example.com", fmt.Sprintf("%d", cd.ID), "127.0.0.1")
	if err != nil {
		t.Fatalf("demote: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Error("the admin pressed Reject and granted a PERMANENT custom domain on a gateway " +
			"whose never_expires.custom_domains is disabled -- the subdomain policy was applied to it")
	}

	stored, err := srv.db.GetSubdomainReservation(cd.ID)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if stored.ExpiresAt == nil {
		t.Error("the stored custom-domain row has no expiry")
	}
}

// CONTROL. A SUBDOMAIN demoted on the same gateway keeps the subdomain policy's answer, which
// here is permanent -- writing subdomain_expiry_days: 0 into the config IS the operator granting
// that in advance. Without this the fix above could be "always give everything an expiry", which
// is a different bug.
func TestDemotingASubdomainStillUsesTheSubdomainPolicy(t *testing.T) {
	zero := 0
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresAllowed, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {SubdomainExpiryDays: &zero}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	sub := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "app", Domain: "example.com",
		ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.db.CreateSubdomainReservation(sub); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	got, err := srv.portalService.AdminDemoteReservation("admin@example.com", fmt.Sprintf("%d", sub.ID), "127.0.0.1")
	if err != nil {
		t.Fatalf("demote: %v", err)
	}
	if got.ExpiresAt != nil {
		t.Errorf("a subdomain whose role is configured permanent, on a gateway that allows it, "+
			"came back expiring %v", got.ExpiresAt)
	}
}

// The other half of the misattribution, with no permanence involved at all: a custom domain's
// LIFETIME was being taken from role_settings.<role>.subdomain_expiry_days. Two settings the
// owner asked to be independent, one of them silently driving the other.
func TestADemotedCustomDomainDoesNotTakeTheSubdomainRoleLifetime(t *testing.T) {
	ninetyNine := 99
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {SubdomainExpiryDays: &ninetyNine}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "", Domain: "vanity.customer.com",
		ExtensionRequested: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.db.CreateSubdomainReservation(cd); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	got, err := srv.portalService.AdminDemoteReservation("admin@example.com", fmt.Sprintf("%d", cd.ID), "127.0.0.1")
	if err != nil {
		t.Fatalf("demote: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("permanent under a disabled custom-domain policy")
	}
	// The custom-domain default, exactly -- not 99, and not "something under 99", which a
	// borrowed subdomain setting of 89 would also have satisfied.
	days := int(time.Until(*got.ExpiresAt).Hours() / 24)
	if days < defaultCustomDomainExpiryDays-1 || days > defaultCustomDomainExpiryDays {
		t.Errorf("the custom domain got %d days; with no custom_domain_expiry_days set it must get "+
			"the custom-domain default (%d), never role_settings.developer.subdomain_expiry_days (99)",
			days, defaultCustomDomainExpiryDays)
	}
}

// ---------------------------------------------------------------------------------------------
// The rest of the review findings.

// An ADMIN saying "never" must get the same answer whichever button they press. Under `approval`
// the extend route ran the admin through the HOLDER's resolver and silently handed them 30 days
// and no request, while the permanence queue granted outright.
func TestTheTwoAdminRoutesToPermanenceAgree(t *testing.T) {
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		t.Run(string(policy), func(t *testing.T) {
			srv := serverWithPolicy(t, policy, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
			dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

			// Route one: extend to zero days.
			viaExtend := tokenFor(t, srv, dev, "extend", in30Days())
			if rec := postExtendToken(t, srv, viaExtend.ID, 0); rec.Code != http.StatusOK {
				t.Fatalf("extend to never: got %d. Body: %s", rec.Code, rec.Body.String())
			}
			if got := mustToken(t, srv, viaExtend.ID); got.ExpiresAt != nil {
				t.Errorf("policy %q: an admin extended to never and got an expiry of %v", policy, got.ExpiresAt)
			}

			// Route two: the permanence queue. Only reachable under `approval`, where a
			// request can exist.
			if !policy.RequiresApproval() {
				return
			}
			viaQueue := tokenFor(t, srv, dev, "queue", in30Days())
			if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", viaQueue.ID), "127.0.0.1"); err != nil {
				t.Fatalf("request: %v", err)
			}
			if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", viaQueue.ID), true, "127.0.0.1"); err != nil {
				t.Fatalf("grant: %v", err)
			}
			if got := mustToken(t, srv, viaQueue.ID); got.ExpiresAt != nil {
				t.Errorf("policy %q: the queue granted and the token still expires %v", policy, got.ExpiresAt)
			}
		})
	}
}

// CONTROL. `disabled` still refuses both admin routes -- being an admin is not an exception to
// a policy, or it is a preference rather than a policy.
func TestNeitherAdminRouteEscapesTheDisabledPolicy(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())

	if rec := postExtendToken(t, srv, pat.ID, 0); rec.Code != http.StatusForbidden {
		t.Errorf("extend to never under disabled: got %d, want 403", rec.Code)
	}
	if got := mustToken(t, srv, pat.ID); got.ExpiresAt == nil {
		t.Error("refused and removed the expiry anyway")
	}
}

// An admin may still extend by a real number of days under any policy -- the policy is about
// "never", and refusing a 60-day extension would be a different bug.
func TestAnAdminCanStillExtendByRealDaysUnderEveryPolicy(t *testing.T) {
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		srv := serverWithPolicy(t, policy, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
		dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
		pat := tokenFor(t, srv, dev, "ci", in30Days())

		if rec := postExtendToken(t, srv, pat.ID, 60); rec.Code != http.StatusOK {
			t.Fatalf("policy %q: extend by 60 days got %d", policy, rec.Code)
		}
		got := mustToken(t, srv, pat.ID)
		if got.ExpiresAt == nil {
			t.Errorf("policy %q: a 60-day extension made the token permanent", policy)
			continue
		}
		if days := int(time.Until(*got.ExpiresAt).Hours() / 24); days < 59 || days > 60 {
			t.Errorf("policy %q: 60 days became %d", policy, days)
		}
	}
}

// The queue excludes revoked tokens, but an admin can be looking at a page rendered a second
// before the revoke. Granting there would put `granted` and a NULL expiry on a dead credential.
func TestGrantingARevokedTokenIsRefused(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := srv.db.RevokePAT(pat.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", fmt.Sprintf("%d", pat.ID), true, "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Errorf("granting a revoked token: got %v, want ErrConflict", err)
	}
	if got := mustToken(t, srv, pat.ID); got.ExpiresAt == nil {
		t.Error("a revoked credential was made permanent")
	}
}

// The second admin to press a button on the same request loses, and the row is never left
// saying one thing while the expiry says another.
func TestASecondDecisionOnTheSameRequestLoses(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	if _, err := srv.portalService.RequestTokenPermanence(dev, fmt.Sprintf("%d", pat.ID), "127.0.0.1"); err != nil {
		t.Fatalf("request: %v", err)
	}

	if _, err := srv.portalService.AdminDecideTokenPermanence("admin-a@example.com", fmt.Sprintf("%d", pat.ID), true, "127.0.0.1"); err != nil {
		t.Fatalf("first decision: %v", err)
	}
	if _, err := srv.portalService.AdminDecideTokenPermanence("admin-b@example.com", fmt.Sprintf("%d", pat.ID), false, "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Errorf("second decision: got %v, want ErrConflict", err)
	}

	got := mustToken(t, srv, pat.ID)
	if got.PermanenceState != db.PATPermanenceGranted || got.ExpiresAt != nil {
		t.Errorf("after a granted-then-denied pair the row says %q with expiry %v; the two must never disagree",
			got.PermanenceState, got.ExpiresAt)
	}
}

// The transition is conditional at the STATEMENT, which is what makes the guard above more than
// a check-then-act race: SetMaxOpenConns(1) serialises statements, not sequences.
func TestTheStateTransitionIsConditionalAtTheStatement(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())

	// Not pending, so a transition FROM pending must not apply.
	if err := srv.db.TransitionPATPermanenceState(pat.ID, db.PATPermanencePending, db.PATPermanenceGranted); !errors.Is(err, db.ErrStateChanged) {
		t.Errorf("transition from the wrong state: got %v, want ErrStateChanged", err)
	}
	if got := mustToken(t, srv, pat.ID); got.PermanenceState != db.PATPermanenceNone {
		t.Errorf("a refused transition still wrote %q", got.PermanenceState)
	}

	// And it does apply from the right one -- otherwise the test above passes on a method
	// that never works.
	if err := srv.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); err != nil {
		t.Fatalf("seeding pending: %v", err)
	}
	if err := srv.db.TransitionPATPermanenceState(pat.ID, db.PATPermanencePending, db.PATPermanenceGranted); err != nil {
		t.Fatalf("transition from the right state: %v", err)
	}
	if got := mustToken(t, srv, pat.ID); got.PermanenceState != db.PATPermanenceGranted {
		t.Errorf("the transition did not apply: %q", got.PermanenceState)
	}
}

// A denied holder may ask again, and this records that as the deliberate choice it is.
//
// The consequence is that `denied` does not survive a re-request, so it tells the HOLDER "no"
// (which is the half that matters to them) but does not tell the next admin "your colleague
// already refused this". Keeping the refusal would need somewhere to keep it; a holder whose
// circumstances changed being unable to ask again is the worse of the two.
func TestADeniedHolderMayAskAgain(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")
	pat := tokenFor(t, srv, dev, "ci", in30Days())
	id := fmt.Sprintf("%d", pat.ID)

	if _, err := srv.portalService.RequestTokenPermanence(dev, id, "127.0.0.1"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := srv.portalService.AdminDecideTokenPermanence("admin@example.com", id, false, "127.0.0.1"); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if got := mustToken(t, srv, pat.ID); got.PermanenceState != db.PATPermanenceDenied {
		t.Fatalf("not denied: %q", got.PermanenceState)
	}

	if _, err := srv.portalService.RequestTokenPermanence(dev, id, "127.0.0.1"); err != nil {
		t.Fatalf("re-request after denial: %v", err)
	}
	if got := mustToken(t, srv, pat.ID); got.PermanenceState != db.PATPermanencePending {
		t.Errorf("a denied holder could not ask again: %q", got.PermanenceState)
	}
	if queue := mustQueue(t, srv); len(queue) != 1 {
		t.Errorf("the re-request is not in the queue (%d)", len(queue))
	}
}

// ---------------------------------------------------------------------------------------------
// custom_domain_expiry_days: a custom domain's lifetime is its own setting.

func daysUntil(t *testing.T, at *time.Time) int {
	t.Helper()
	if at == nil {
		t.Fatal("expected an expiry, got permanent")
	}
	return int(time.Until(*at).Hours() / 24)
}

// The two keys are independent in both directions, which is the whole reason the second one
// exists. Set them far apart and each resource must take its own.
func TestEachResourceTakesItsOwnExpiryDays(t *testing.T) {
	// Eleven, not seven. Seven IS defaultSubdomainExpiryDays, so a subdomain branch that
	// ignored the role setting entirely still produced 7 and the assertion passed -- satisfied
	// by the default rather than by the setting it names (#2276 review, github-workflow 5c).
	elevenDays, twoHundred := 11, 200
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {
		SubdomainExpiryDays:    &elevenDays,
		CustomDomainExpiryDays: &twoHundred,
	}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	sub := srv.getUserSubdomainExpiry(dev)
	if d := daysUntil(t, sub); d < 10 || d > 11 {
		t.Errorf("subdomain got %d days, want 11 -- it must take subdomain_expiry_days, not the "+
			"default and not custom_domain_expiry_days", d)
	}

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if d := daysUntil(t, cd.ExpiresAt); d < 199 || d > 200 {
		t.Errorf("custom domain got %d days, want 200 -- it must not take subdomain_expiry_days", d)
	}
}

// With no custom_domain_expiry_days the resource takes its OWN default, and that default is
// deliberately not the subdomain one: a custom domain is not in a contested namespace, and a
// week's hold on a name its holder proved through DNS serves nobody.
func TestAnUnsetCustomDomainExpiryTakesTheCustomDomainDefault(t *testing.T) {
	if defaultCustomDomainExpiryDays == defaultSubdomainExpiryDays {
		t.Fatal("the two defaults are the same, so this test cannot tell them apart")
	}
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if d := daysUntil(t, cd.ExpiresAt); d < defaultCustomDomainExpiryDays-1 || d > defaultCustomDomainExpiryDays {
		t.Errorf("custom domain got %d days, want the custom-domain default (%d)", d, defaultCustomDomainExpiryDays)
	}
}

// 0 means permanent, the same as its subdomain counterpart -- and is governed by
// never_expires.custom_domains, not by the subdomain policy.
func TestCustomDomainExpiryDaysZeroMeansPermanentAndIsPoliced(t *testing.T) {
	zero := 0
	for _, tc := range []struct {
		policy    config.NeverExpiresPolicy
		permanent bool
	}{
		// An operator writing 0 into the config file IS the approval, given in advance --
		// the same rule subdomains follow, so `approval` honours it.
		{config.NeverExpiresApproval, true},
		{config.NeverExpiresAllowed, true},
		// And `disabled` clamps it, because that is the state in which this gateway grants
		// permanence by no route at all.
		{config.NeverExpiresDisabled, false},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, tc.policy)
			srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {CustomDomainExpiryDays: &zero}}
			dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

			cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
			if err != nil {
				t.Fatalf("CreateCustomDomain: %v", err)
			}
			if tc.permanent && cd.ExpiresAt != nil {
				t.Errorf("policy %q: custom_domain_expiry_days is 0 and the domain expires %v", tc.policy, cd.ExpiresAt)
			}
			if !tc.permanent {
				if cd.ExpiresAt == nil {
					t.Errorf("policy %q: a disabled policy did not clamp a role configured permanent", tc.policy)
					return
				}
				// The NUMBER, not just "not nil". "Not nil" is shared by every way of
				// getting this wrong, including clamping to the subdomain default of 7
				// (#2276 review).
				if d := daysUntil(t, cd.ExpiresAt); d < defaultCustomDomainExpiryDays-1 || d > defaultCustomDomainExpiryDays {
					t.Errorf("policy %q: clamped to %d days, want the custom-domain default (%d)",
						tc.policy, d, defaultCustomDomainExpiryDays)
				}
			}
		})
	}
}

// The subdomain policy must not decide a custom domain configured permanent, or the two settings
// are not independent -- which is the defect this whole key exists to close.
func TestTheSubdomainPolicyDoesNotDecideAPermanentCustomDomain(t *testing.T) {
	// A POSITIVE key and `subdomains: allowed`. With a zero key this passed for the wrong
	// reason: PermanentByDefault plus a disabled custom-domain policy already produced an
	// expiry, so it would have passed even if custom_domain_expiry_days did not exist
	// (#2276 review). With 200, permanence here can only come from the subdomain policy.
	twoHundred, zero := 200, 0
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresAllowed, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {
		CustomDomainExpiryDays: &twoHundred,
		SubdomainExpiryDays:    &zero,
	}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if cd.ExpiresAt == nil {
		t.Fatal("subdomain_expiry_days is 0 and never_expires.subdomains is `allowed`, and the " +
			"CUSTOM DOMAIN came back permanent -- the subdomain settings decided it")
	}
	if d := daysUntil(t, cd.ExpiresAt); d < 199 || d > 200 {
		t.Errorf("the custom domain got %d days, want its own 200", d)
	}
}

// The CLIENT's door takes the same setting. The registration path writes the reservation row
// itself, and it is where the borrowed subdomain expiry survived longest.
func TestTheClientsRegistrationPathUsesTheCustomDomainExpiryDays(t *testing.T) {
	twoHundred, sevenDays := 200, 7
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {
		SubdomainExpiryDays:    &sevenDays,
		CustomDomainExpiryDays: &twoHundred,
	}}

	email := "client@example.com"
	if err := srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "developer", Status: "approved"}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	secret := "pat_custom_domain_days"
	hash := sha256.Sum256([]byte(secret))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{UserID: email, TokenHash: hex.EncodeToString(hash[:]), TokenPrefix: "pat_cd_days"}); err != nil {
		t.Fatalf("creating the token: %v", err)
	}

	body, _ := json.Marshal(RegisterRequest{CustomDomain: "client.customer.com", Ports: []PortMapping{{LocalPort: 8080}}, AuthToken: secret})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registering: got %d. Body: %s", rec.Code, rec.Body.String())
	}

	stored, err := srv.db.GetSubdomainReservationByName("", "client.customer.com")
	if err != nil || stored == nil {
		t.Fatalf("no reservation created: %v", err)
	}
	if d := daysUntil(t, stored.ExpiresAt); d < 199 || d > 200 {
		t.Errorf("the client's custom domain got %d days, want 200 -- the registration path still "+
			"takes its lifetime from subdomain_expiry_days (7)", d)
	}
}

// FIRING, and the cell the first round of tests missed entirely: every new custom-domain test
// that set a positive key ran under `disabled`, so nothing covered `allowed` -- which is the
// Liferay gateway's own setting, and where the key turned out to be inert (#2276 review).
func TestAPositiveCustomDomainExpiryBeatsTheAllowedDefault(t *testing.T) {
	twoHundred := 200
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresAllowed)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {CustomDomainExpiryDays: &twoHundred}}
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if cd.ExpiresAt == nil {
		t.Fatal("never_expires.custom_domains is `allowed` and the role asked for 200 days, and " +
			"the domain was stored PERMANENT -- the operator's lifetime was ignored")
	}
	if d := daysUntil(t, cd.ExpiresAt); d < 199 || d > 200 {
		t.Errorf("got %d days, want 200", d)
	}
}

// CONTROL for the above: with the key UNSET, `allowed` still means permanent. That is #1009's
// behaviour and the reason an operator chooses `allowed` at all -- the fix above must not have
// turned every custom domain into an expiring one.
func TestAnAllowedGatewayStillGivesPermanentCustomDomainsWhenNoKeyIsSet(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresAllowed)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.customer.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if cd.ExpiresAt != nil {
		t.Errorf("an `allowed` gateway with no custom_domain_expiry_days gave an expiry of %v; "+
			"#1009's behaviour is what `allowed` means", cd.ExpiresAt)
	}
}

// The same cell on the CLIENT's door, because that is the path that has been wrong twice.
func TestAPositiveCustomDomainExpiryReachesTheClientsPathUnderAllowed(t *testing.T) {
	twoHundred := 200
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresAllowed)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {CustomDomainExpiryDays: &twoHundred}}

	email := "client@example.com"
	if err := srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "developer", Status: "approved"}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	secret := "pat_allowed_positive"
	hash := sha256.Sum256([]byte(secret))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{UserID: email, TokenHash: hex.EncodeToString(hash[:]), TokenPrefix: "pat_allow_p"}); err != nil {
		t.Fatalf("creating the token: %v", err)
	}

	body, _ := json.Marshal(RegisterRequest{CustomDomain: "client.customer.com", Ports: []PortMapping{{LocalPort: 8080}}, AuthToken: secret})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registering: got %d. Body: %s", rec.Code, rec.Body.String())
	}

	stored, err := srv.db.GetSubdomainReservationByName("", "client.customer.com")
	if err != nil || stored == nil {
		t.Fatalf("no reservation: %v", err)
	}
	if stored.ExpiresAt == nil {
		t.Fatal("the client's custom domain was stored permanent despite a 200-day role setting")
	}
	if d := daysUntil(t, stored.ExpiresAt); d < 199 || d > 200 {
		t.Errorf("got %d days, want 200", d)
	}
}

// An owner's subdomain on a gateway with NO role_settings block is not permanent.
//
// Pinning the removal, not an accident: one of the two collapsed copies made it permanent and
// the other did not, so the collapse had to choose. Keeping it would have widened the portal and
// made Demote a no-op for the owner (#2276 review).
func TestAnOwnerSubdomainIsNotPermanentWithoutRoleSettings(t *testing.T) {
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		srv := serverWithPolicy(t, config.NeverExpiresDisabled, policy, config.NeverExpiresDisabled)
		srv.cfg.RoleSettings = nil
		owner := &db.User{ID: "owner@example.com", Email: "owner@example.com", Role: "owner"}

		if got := srv.getUserSubdomainExpiry(owner); got == nil {
			t.Errorf("policy %q: an owner's subdomain is permanent with no role_settings block; "+
				"an operator who wants that says role_settings.owner.subdomain_expiry_days: 0", policy)
		}
	}
}

// CONTROL. An owner who IS configured permanent still gets it, subject to the policy -- so the
// removal above did not simply make owner permanence unreachable.
func TestAnOwnerConfiguredPermanentStillIs(t *testing.T) {
	zero := 0
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresAllowed, config.NeverExpiresDisabled)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{"owner": {SubdomainExpiryDays: &zero}}
	owner := &db.User{ID: "owner@example.com", Email: "owner@example.com", Role: "owner"}

	if got := srv.getUserSubdomainExpiry(owner); got != nil {
		t.Errorf("role_settings.owner.subdomain_expiry_days is 0 and subdomains are allowed, "+
			"and the reservation still expires %v", got)
	}
}
