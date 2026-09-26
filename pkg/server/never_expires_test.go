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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// never_expires is only worth anything if the SERVER refuses (#2264).
//
// The defect this replaces was not that permanence existed. It was that the rule lived in the
// two portals -- `currentUser.role !== 'admin'` in dashboard.js, `user?.role === 'admin'` in
// Dashboard.tsx -- and each arm was only ever checked against itself, so they disagreed for
// months without a build failing (#2259). A gate rendered by a client is a suggestion.
//
// So every test below goes at a SERVER entry point, and the important ones assert the CLASS:
// there are four separate ways to end up with something that never expires, and a policy that
// closes three of them closes nothing.

func serverWithPolicy(t *testing.T, tokens, subdomains, customDomains config.NeverExpiresPolicy) *Server {
	t.Helper()
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.com"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
		DefaultMaxCustomDomains:    5,
		DefaultMaxReservations:     5,
		NeverExpires: config.NeverExpiresConfig{
			Tokens:        tokens,
			Subdomains:    subdomains,
			CustomDomains: customDomains,
		},
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "never_expires_test.db")
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func userWithSession(t *testing.T, srv *Server, email, role string) (*db.User, string) {
	t.Helper()
	u := &db.User{ID: email, Email: email, Role: role, Status: "approved"}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	session := generateToken(16)
	srv.portalMap.Store("admin_session_"+session, PortalSessionData{Email: email, ExpiresAt: time.Now().Add(time.Hour)})
	return u, session
}

// postCreateToken asks for a token with the given lifetime, where days <= 0 means "never".
func postCreateToken(t *testing.T, srv *Server, session string, days int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"name": "t", "expires_in_days": days})
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/tokens", bytes.NewBuffer(body))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	rec := httptest.NewRecorder()
	srv.handleCreateToken(rec, req)
	return rec
}

// patByID re-reads a token through the only lookup the repo has. There is no GetPATByID, and
// adding one just for a test would put an unused method on the production interface.
func patByID(t *testing.T, srv *Server, userID string, id int64) *db.PersonalAccessToken {
	t.Helper()
	pats, err := srv.db.ListPATs(userID)
	if err != nil {
		t.Fatalf("listing tokens for %s: %v", userID, err)
	}
	for _, p := range pats {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("token %d is not among the %d listed for %s", id, len(pats), userID)
	return nil
}

// postExtendToken is the admin door: days == 0 means "never".
func postExtendToken(t *testing.T, srv *Server, patID int64, days int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]int{"days": days})
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://example.com/api/admin/tokens/%d/extend", patID), bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	srv.handleAdminExtendToken(rec, req, "admin@example.com")
	return rec
}

// FIRING, and the class assertion. Three implementations can mint a permanent token; under
// `disabled` all three must refuse.
//
// Listed rather than tested one at a time on purpose. handleCreateToken had a role gate and
// portalService.CreateToken -- its unrouted replacement -- had none, which is precisely the
// asymmetry that survives a per-function test and does not survive this one.
func TestEveryDoorToAPermanentTokenIsShutUnderDisabled(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresAllowed, config.NeverExpiresAllowed)
	admin, session := userWithSession(t, srv, "admin@example.com", "admin")

	t.Run("the routed create handler", func(t *testing.T) {
		if rec := postCreateToken(t, srv, session, 0); rec.Code != http.StatusForbidden {
			t.Errorf("POST /api/tokens with expires_in_days=0: got %d, want 403 -- an admin minted a non-expiring token on a gateway that forbids them", rec.Code)
		}
	})

	t.Run("the portal service", func(t *testing.T) {
		_, _, err := srv.portalService.CreateToken(admin, "t", "", "127.0.0.1")
		if !errors.Is(err, ErrPermanenceNotAllowed) {
			t.Errorf("portalService.CreateToken with no expiry: got %v, want ErrPermanenceNotAllowed", err)
		}
	})

	t.Run("the admin extend route", func(t *testing.T) {
		// A token that already has an expiry; the admin then tries to remove it.
		expiry := time.Now().AddDate(0, 0, 30)
		pat := &db.PersonalAccessToken{UserID: admin.ID, Name: "t", TokenHash: "h", TokenPrefix: "p", ExpiresAt: &expiry}
		if err := srv.db.CreatePAT(pat); err != nil {
			t.Fatalf("seeding a token: %v", err)
		}
		if rec := postExtendToken(t, srv, pat.ID, 0); rec.Code != http.StatusForbidden {
			t.Errorf("POST /api/admin/tokens/%d/extend with days=0: got %d, want 403", pat.ID, rec.Code)
		}
		if got := patByID(t, srv, admin.ID, pat.ID); got.ExpiresAt == nil {
			t.Error("the refusal was reported but the expiry was removed anyway")
		}
	})
}

// CONTROL / anti-vacuity. Under `allowed`, the same three doors must open -- otherwise the test
// above passes on a build where nothing can ever be permanent, which is not the feature.
func TestTheSameThreeDoorsOpenUnderAllowed(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresAllowed, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")

	t.Run("the routed create handler, for a non-admin", func(t *testing.T) {
		rec := postCreateToken(t, srv, session, 0)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/tokens: got %d, want 201 -- `allowed` means every role, not every admin. Body: %s", rec.Code, rec.Body.String())
		}
		pats, err := srv.db.ListPATs(dev.ID)
		if err != nil || len(pats) == 0 {
			t.Fatalf("listing tokens: %v (%d found)", err, len(pats))
		}
		if pats[0].ExpiresAt != nil {
			t.Errorf("the token was created with an expiry of %v; `allowed` was asked for never", pats[0].ExpiresAt)
		}
	})

	t.Run("the portal service", func(t *testing.T) {
		_, pat, err := srv.portalService.CreateToken(dev, "t2", "", "127.0.0.1")
		if err != nil {
			t.Fatalf("portalService.CreateToken: %v", err)
		}
		if pat.ExpiresAt != nil {
			t.Errorf("want a permanent token, got one expiring %v", pat.ExpiresAt)
		}
	})

	t.Run("the admin extend route", func(t *testing.T) {
		expiry := time.Now().AddDate(0, 0, 30)
		pat := &db.PersonalAccessToken{UserID: dev.ID, Name: "t3", TokenHash: "h3", TokenPrefix: "p3", ExpiresAt: &expiry}
		if err := srv.db.CreatePAT(pat); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		if rec := postExtendToken(t, srv, pat.ID, 0); rec.Code != http.StatusOK {
			t.Fatalf("extend to never: got %d, want 200", rec.Code)
		}
		if got := patByID(t, srv, dev.ID, pat.ID); got.ExpiresAt != nil {
			t.Errorf("the grant was accepted but the expiry is still %v", got.ExpiresAt)
		}
	})
}

// Under `approval` the token is real and usable immediately -- what waits on an admin is the
// permanence, not the credential. A user who asked for a long-lived token and got nothing has
// no way to work while they wait.
func TestUnderApprovalATokenIsIssuedWithAnExpiryRatherThanRefused(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")

	rec := postCreateToken(t, srv, session, 0)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/tokens under approval: got %d, want 201. Body: %s", rec.Code, rec.Body.String())
	}
	pats, err := srv.db.ListPATs(dev.ID)
	if err != nil || len(pats) != 1 {
		t.Fatalf("listing tokens: %v (%d found)", err, len(pats))
	}
	if pats[0].ExpiresAt == nil {
		t.Fatal("approval granted permanence on the spot; the admin was never consulted")
	}
}

// The owner's requirement, at the enforcement layer rather than the config layer: one policy
// being open must not open another. A single shared check would pass every per-resource test
// above and fail this one.
func TestAnOpenPolicyOnOneResourceDoesNotOpenAnother(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresAllowed, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")

	if rec := postCreateToken(t, srv, session, 0); rec.Code != http.StatusCreated {
		t.Fatalf("tokens are `allowed` and the token was refused: %d", rec.Code)
	}

	// Same gateway, same user: a permanent subdomain and a permanent custom domain must both
	// still be refused.
	res := &db.SubdomainReservation{UserID: dev.ID, Subdomain: "app", Domain: "example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.db.CreateSubdomainReservation(res); err != nil {
		t.Fatalf("seeding a reservation: %v", err)
	}
	if _, err := srv.portalService.AdminApproveExtension("admin@example.com", fmt.Sprintf("%d", res.ID), 0, true, "127.0.0.1"); !errors.Is(err, ErrPermanenceNotAllowed) {
		t.Errorf("subdomains are `disabled` and a permanent extension was approved: %v", err)
	}

	cd, err := srv.portalService.CreateCustomDomain(dev, "vanity.example.net", "127.0.0.1")
	if err != nil {
		t.Fatalf("creating a custom domain: %v", err)
	}
	if cd.ExpiresAt == nil {
		t.Error("custom domains are `disabled` and one was created permanent")
	}
}

// Custom domains were unconditionally permanent (#1009). That is now the operator's call, and
// both answers have to work.
func TestACustomDomainIsPermanentOnlyWhereTheOperatorSaidSo(t *testing.T) {
	for _, tc := range []struct {
		policy    config.NeverExpiresPolicy
		permanent bool
	}{
		{config.NeverExpiresDisabled, false},
		{config.NeverExpiresApproval, false},
		{config.NeverExpiresAllowed, true},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, tc.policy)
			dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

			res, err := srv.portalService.CreateCustomDomain(dev, "vanity.example.net", "127.0.0.1")
			if err != nil {
				t.Fatalf("CreateCustomDomain: %v", err)
			}
			if tc.permanent && res.ExpiresAt != nil {
				t.Errorf("policy %q: want permanent, got an expiry of %v", tc.policy, res.ExpiresAt)
			}
			if !tc.permanent && res.ExpiresAt == nil {
				t.Errorf("policy %q: want an expiry, got permanent", tc.policy)
			}
		})
	}
}

// The FIFTH door, and the one the owner specifically asked to be covered: the client.
//
// `lfr-tunnel -domain x.example.com` with auto-reservation does not go near the portal service.
// The registration handler writes the reservation row itself, and wrote ExpiresAt: nil
// unconditionally. A policy enforced at the two portal entry points would have left a client
// able to obtain precisely what both portals refuse -- the same defect as the role gate that
// lived only in the browser, one layer down.
func TestTheClientsRegistrationPathIsSubjectToTheSamePolicy(t *testing.T) {
	for _, tc := range []struct {
		policy    config.NeverExpiresPolicy
		permanent bool
	}{
		{config.NeverExpiresDisabled, false},
		{config.NeverExpiresAllowed, true},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, tc.policy)

			email := "client@example.com"
			if err := srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "developer", Status: "approved"}); err != nil {
				t.Fatalf("creating the user: %v", err)
			}
			secret := "pat_never_expires_test"
			hash := sha256.Sum256([]byte(secret))
			if err := srv.db.CreatePAT(&db.PersonalAccessToken{UserID: email, TokenHash: hex.EncodeToString(hash[:]), TokenPrefix: "pat_never_e"}); err != nil {
				t.Fatalf("creating the token: %v", err)
			}

			body, _ := json.Marshal(RegisterRequest{CustomDomain: "client.customer.com", Ports: []PortMapping{{LocalPort: 8080}}, AuthToken: secret})
			req := httptest.NewRequest(http.MethodPost, "http://example.com/api/register", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("registering with a custom domain: got %d, want 200. Body: %s", rec.Code, rec.Body.String())
			}

			stored, err := srv.db.GetSubdomainReservationByName("", "client.customer.com")
			if err != nil || stored == nil {
				t.Fatalf("the registration did not create a reservation: %v", err)
			}
			if tc.permanent && stored.ExpiresAt != nil {
				t.Errorf("policy %q: the client's reservation expires %v, want permanent", tc.policy, stored.ExpiresAt)
			}
			if !tc.permanent && stored.ExpiresAt == nil {
				t.Errorf("policy %q: the client obtained a permanent custom domain that both portals would refuse", tc.policy)
			}
		})
	}
}

// The fourth door, and the one with no request behind it to refuse: a role whose
// subdomain_expiry_days is 0 or less makes every reservation that role creates permanent, with
// nobody having asked. Both getUserSubdomainExpiry implementations have to close it.
func TestARoleConfiguredPermanentIsClampedUnderDisabled(t *testing.T) {
	zero := 0
	build := func(t *testing.T, policy config.NeverExpiresPolicy) *Server {
		t.Helper()
		srv := serverWithPolicy(t, config.NeverExpiresDisabled, policy, config.NeverExpiresDisabled)
		srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {SubdomainExpiryDays: &zero}}
		return srv
	}
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer"}

	t.Run("disabled clamps both implementations", func(t *testing.T) {
		srv := build(t, config.NeverExpiresDisabled)
		if got := srv.getUserSubdomainExpiry(dev); got == nil {
			t.Error("Server.getUserSubdomainExpiry returned permanent for a role setting the policy forbids")
		}
		ps, ok := srv.portalService.(*portalService)
		if !ok {
			t.Fatal("portalService is not the concrete type")
		}
		if got := ps.getUserSubdomainExpiry(dev); got == nil {
			t.Error("portalService.getUserSubdomainExpiry returned permanent for a role setting the policy forbids")
		}
	})

	// CONTROL. Writing subdomain_expiry_days: 0 into the config file IS an operator granting
	// permanence in advance, so `approval` and `allowed` must both honour it -- otherwise the
	// clamp above is indistinguishable from the setting being ignored outright.
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		t.Run(string(policy)+" honours the role setting", func(t *testing.T) {
			srv := build(t, policy)
			if got := srv.getUserSubdomainExpiry(dev); got != nil {
				t.Errorf("policy %q: the role is configured permanent and the reservation expires %v", policy, got)
			}
		})
	}
}

// A role with a POSITIVE subdomain_expiry_days must be untouched by any of this. Without it, a
// clamp that ignored the role setting entirely would pass every case above.
func TestAPositiveRoleExpiryIsUnaffectedByThePolicy(t *testing.T) {
	twenty := 20
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		srv := serverWithPolicy(t, config.NeverExpiresDisabled, policy, config.NeverExpiresDisabled)
		srv.cfg.RoleSettings = map[string]config.RoleSetting{"developer": {SubdomainExpiryDays: &twenty}}
		got := srv.getUserSubdomainExpiry(&db.User{ID: "d", Email: "d", Role: "developer"})
		if got == nil {
			t.Fatalf("policy %q: a 20-day role became permanent", policy)
		}
		if days := int(time.Until(*got).Hours() / 24); days < 19 || days > 20 {
			t.Errorf("policy %q: a 20-day role expiry came back as %d days", policy, days)
		}
	}
}

// Both portal arms and the client render the option from this. A policy the server enforces and
// does not advertise produces an arm that offers what the server will refuse.
func TestTheVersionEndpointAdvertisesAllThreePolicies(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresAllowed, config.NeverExpiresApproval, config.NeverExpiresAllowed)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version: got %d, want 200", rec.Code)
	}

	var resp struct {
		NeverExpires map[string]string `json:"never_expires"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for key, want := range map[string]string{"tokens": "allowed", "subdomains": "approval", "custom_domains": "allowed"} {
		if got := resp.NeverExpires[key]; got != want {
			t.Errorf("never_expires.%s advertised as %q, want %q", key, got, want)
		}
	}
}

// A gateway that has never heard of never_expires -- every ServerConfig literal in this suite,
// and any config file predating the setting -- must advertise the strict default rather than an
// empty string that a portal would have to guess at.
func TestAnUnsetPolicyIsAdvertisedAsDisabledRatherThanBlank(t *testing.T) {
	srv := setupTestServerForAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		NeverExpires map[string]string `json:"never_expires"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(resp.NeverExpires) != 3 {
		t.Fatalf("want three policies, got %v", resp.NeverExpires)
	}
	for key, got := range resp.NeverExpires {
		if got != "disabled" {
			t.Errorf("never_expires.%s advertised as %q on a gateway that never configured it", key, got)
		}
	}
}

// A requested lifetime is never touched by the policy. The policy is about "never", and a
// gateway that shortened a 90-day token because permanence is disabled would be a different bug.
func TestAPositiveLifetimeIsUnaffectedByAnyPolicy(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, policy := range []config.NeverExpiresPolicy{config.NeverExpiresDisabled, config.NeverExpiresApproval, config.NeverExpiresAllowed} {
		got, requested, err := resolvePATExpiry(policy, 90, now)
		if err != nil {
			t.Fatalf("policy %q refused a 90-day token: %v", policy, err)
		}
		if got == nil || !got.Equal(now.AddDate(0, 0, 90)) {
			t.Errorf("policy %q: 90 days became %v", policy, got)
		}
		// And it raises no permanence request: the holder did not ask for one (#2267).
		if requested {
			t.Errorf("policy %q: a 90-day token queued a permanence request", policy)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// The gate for the SIXTH door.
//
// Five separate places could grant permanence, and all five were found by reading the code. That
// is not a method that survives the next feature: the fifth was a struct literal in the middle of
// a 200-line registration branch, written years after the first, by somebody who had no reason to
// know a policy existed.
//
// So the rule is structural. Anywhere in this package that sets an expiry to nil -- the only way
// to say "never" -- must sit in a function that consults the policy. Not a naming convention, not
// a comment: the function has to name one of the resolvers or the config accessor, or the build
// fails and the author has to decide which.

// ANY write of an expiry, not only a literal nil.
//
// This matched `ExpiresAt: nil`, `ExpiresAt = nil` and UpdatePATExpiry(..., nil) -- the syntactic
// shape of the five doors that had already been found. Review of #2267 found a sixth it could
// not see: `res.ExpiresAt = s.getUserSubdomainExpiry(resOwner)`, a nil arriving through a
// function call. The gate was green on a package containing the defect it was written to
// prevent, which is a gate asserting the instance and not the class (github-workflow SKILL 5b).
//
// So the question the gate now asks is the useful one -- "does this function decide an expiry?"
// -- and the answer has to be "yes, and it consults the policy to do it".
var permanenceWriteRE = regexp.MustCompile(`ExpiresAt:\s*\S|ExpiresAt\s*=[^=]|UpdatePATExpiry\(`)

// expiryCopyRE matches an expiry being carried from somewhere else -- `ExpiresAt: res.ExpiresAt`,
// `pat.ExpiresAt = existing.ExpiresAt`. The value was decided by whoever set the source.
var expiryCopyRE = regexp.MustCompile(`ExpiresAt\s*[:=]\s*[A-Za-z_][A-Za-z0-9_.]*\.ExpiresAt\b`)

// policyAware reports whether a function body consults the never_expires policy at all.
var policyMarkers = []string{
	"resolvePATExpiry",
	"resolveReservationExpiry",
	"resolveCustomDomainExpiry",
	"permanenceGrantAllowed",
	"NeverExpires",
	// The two resolvers that apply a policy on the caller's behalf. A function that takes its
	// expiry from one of these HAS consulted the policy -- indirectly, but through code that
	// cannot forget to. reservationExpiryForKind is the one that picks by resource kind, which
	// is the rule the sixth door broke.
	"reservationExpiryFor",
	"getUserSubdomainExpiry",
	"roleExpiry",
}

func policyAware(funcBody string) bool {
	// Comments stripped FIRST. They were not, and unguardedPermanenceWrites did strip them, so
	// a function that wrote an unguarded nil and merely NAMED a resolver in a comment was
	// judged guarded -- the gate vouching for prose (found reviewing #2267).
	stripped := stripComments(funcBody)
	for _, marker := range policyMarkers {
		if strings.Contains(stripped, marker) {
			return true
		}
	}
	return false
}

// stripComments removes line comments, so neither half of this gate can be satisfied by prose.
func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// unguardedPermanenceWrites returns "func name: line" for every expiry-to-nil write in src that
// sits in a function with no reference to the policy.
func unguardedPermanenceWrites(src string) []string {
	var found []string
	fn := "(file scope)"
	for _, line := range strings.Split(stripComments(src), "\n") {
		if strings.HasPrefix(line, "func ") {
			fn = strings.TrimSpace(strings.TrimPrefix(line, "func "))
			if i := strings.Index(fn, "{"); i > 0 {
				fn = strings.TrimSpace(fn[:i])
			}
		}
		if !permanenceWriteRE.MatchString(line) {
			continue
		}
		// Carrying a value that was already decided is not a second decision.
		if expiryCopyRE.MatchString(line) {
			continue
		}
		found = append(found, fn)
	}
	return found
}

// persistCalls are the four ways a governed row reaches the database. A function that calls one
// of them is deciding what gets stored; one that does not is reading, formatting or serialising,
// and its ExpiresAt is not a decision this policy governs.
//
// Scoped this way rather than by mentioning the TYPE, which was the first attempt and was still
// too broad: CreateInvitation looks a reservation up to validate against it and then writes a
// GuestInvitation's own expiry, and handleAdminListSubdomains copies res.ExpiresAt into a
// response DTO. Neither decides a governed expiry, and a gate that shouts about them is one
// nobody reads. Session cookies, guest invitations and IP bans all have an ExpiresAt and none of
// them is never_expires's business.
var persistCalls = []string{
	"CreateSubdomainReservation",
	"UpdateSubdomainReservation",
	"CreatePAT",
	"UpdatePATExpiry",
}

func persistsAGovernedRow(funcBody string) bool {
	for _, call := range persistCalls {
		if strings.Contains(funcBody, call+"(") {
			return true
		}
	}
	return false
}

// bodiesOf splits a Go source file into its top-level function bodies, keyed by signature. Crude
// on purpose: a brace counter is enough to answer "does this function mention the policy", and a
// real parser here would be more machinery than the rule is worth.
func bodiesOf(src string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(stripComments(src), "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "func ") {
			continue
		}
		sig := lines[i]
		var b strings.Builder
		depth := 0
		started := false
		for j := i; j < len(lines); j++ {
			b.WriteString(lines[j])
			b.WriteString("\n")
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			if strings.Contains(lines[j], "{") {
				started = true
			}
			if started && depth <= 0 {
				break
			}
		}
		out[sig] = b.String()
	}
	return out
}

// FIRING. Every expiry-to-nil in this package's production source must be policy-aware.
func TestNoUnguardedRouteToPermanenceInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// never_expires.go IS the policy; resolvePATExpiry returning nil is the grant.
		if name == "never_expires.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		scanned++
		for sig, body := range bodiesOf(string(src)) {
			if !persistsAGovernedRow(body) {
				continue
			}
			if len(unguardedPermanenceWrites(body)) == 0 {
				continue
			}
			if policyAware(body) {
				continue
			}
			t.Errorf("%s: %s sets an expiry to nil without consulting never_expires.\n"+
				"That is a route to a permanent resource the operator did not allow -- the sixth of\n"+
				"five such routes #2264 had to find by reading. Call one of %v, or move the write\n"+
				"behind something that does.", name, strings.TrimSuffix(sig, " {"), policyMarkers)
		}
	}

	// Anti-vacuity: a scan that found no files to read reports no problems and looks identical
	// to a clean package.
	if scanned < 20 {
		t.Fatalf("only %d production files were scanned; the gate is not looking at the package", scanned)
	}
}

// The gate must FIRE on the defect it describes, or it is decoration. Both halves asserted here,
// on synthetic source, because the real package is (and must stay) clean.
func TestTheUnguardedPermanenceGateFiresAndIsNotVacuous(t *testing.T) {
	unguarded := `func createSomething(user *db.User) *db.SubdomainReservation {
	return &db.SubdomainReservation{
		UserID:    user.ID,
		ExpiresAt: nil,
	}
}
`
	guarded := `func createSomething(user *db.User) *db.SubdomainReservation {
	return &db.SubdomainReservation{
		UserID:    user.ID,
		ExpiresAt: resolveCustomDomainExpiry(s.cfg.NeverExpiresCustomDomains(), nil, time.Now()),
	}
}
`
	// A write guarded by being inside a function that checks the policy first -- the shape
	// AdminApproveExtension has.
	guardedByCheck := `func approve(permanent bool) error {
	if permanent && !permanenceGrantAllowed(s.cfg.NeverExpiresSubdomains()) {
		return ErrPermanenceNotAllowed
	}
	res.ExpiresAt = nil
	return nil
}
`

	// THE SIXTH DOOR, as it was actually written. A nil arriving through a function call, in a
	// function that persists the row. The gate matched only the literal `= nil` and was green on
	// this; that is the case it exists to fail now.
	sixthDoor := `func (s *portalService) AdminDemoteReservation(actor, idStr, ip string) (*db.SubdomainReservation, error) {
	res.ExpiresAt = s.somethingThatDoesNotConsultThePolicy(resOwner)
	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}
	return res, nil
}
`
	if got := unguardedPermanenceWrites(sixthDoor); len(got) == 0 {
		t.Error("the gate cannot see an expiry written through a function call; that is the defect review of #2267 found")
	}
	if policyAware(sixthDoor) {
		t.Error("a function that names no resolver was judged policy-aware")
	}
	if !persistsAGovernedRow(sixthDoor) {
		t.Error("a function calling UpdateSubdomainReservation is not recognised as persisting a governed row")
	}

	// A policy marker in a COMMENT must not launder an unguarded write. policyAware read the
	// raw body while the write scan stripped comments, so prose satisfied one half of the gate.
	commented := `func grantForever(res *db.SubdomainReservation) {
	// permanenceGrantAllowed is deliberately not called here
	res.ExpiresAt = nil
	_ = db.UpdateSubdomainReservation(res)
}
`
	if policyAware(commented) {
		t.Error("a policy marker inside a comment made an unguarded write look guarded")
	}

	// CONTROL for the scope: a guest invitation decides its own expiry and is none of this
	// policy's business. Flagging it is how a gate becomes noise and stops being read.
	invitation := `func (s *portalService) CreateInvitation(user *db.User) (*db.GuestInvitation, error) {
	res, _ := s.db.GetSubdomainReservationByName(subdomain, domain)
	inv := &db.GuestInvitation{ExpiresAt: expiresAt}
	return inv, nil
}
`
	if persistsAGovernedRow(invitation) {
		t.Error("a function that writes a GuestInvitation is being treated as writing a governed row")
	}

	// CONTROL: carrying an already-decided value into a response is not a decision.
	copying := `func view(res *db.SubdomainReservation) any {
	_ = s.db.UpdateSubdomainReservation(res)
	return map[string]any{"x": row{ExpiresAt: res.ExpiresAt}}
}
`
	if got := unguardedPermanenceWrites(copying); len(got) != 0 {
		t.Errorf("copying an expiry was read as deciding one: %v", got)
	}

	if got := unguardedPermanenceWrites(unguarded); len(got) == 0 {
		t.Error("the gate did not see an unguarded `ExpiresAt: nil`; it would pass on the defect it exists for")
	}
	if policyAware(unguarded) {
		t.Error("source with no policy reference was judged policy-aware")
	}
	if !policyAware(guarded) {
		t.Error("a write that goes through resolveCustomDomainExpiry was judged unguarded")
	}
	if !policyAware(guardedByCheck) {
		t.Error("a write behind permanenceGrantAllowed was judged unguarded")
	}
	// A comment mentioning the pattern is prose, not a write.
	if got := unguardedPermanenceWrites("\t// sets ExpiresAt = nil when permanent\n"); len(got) != 0 {
		t.Errorf("the gate fired on a comment: %v", got)
	}
}
