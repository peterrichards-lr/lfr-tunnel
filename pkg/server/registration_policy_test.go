package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// The two registration paths must reach the SAME per-registration decisions (#2018).
//
// Same shape as TestBothRegistrationPathsGrantTheSameRateLimit (#2005/#2017) and for the same
// reason: handleRegister and handleEdgeRegister held byte-identical copies of two more policies,
// thousands of lines apart, and nothing would have gone red had one been changed and the other
// forgotten. Edge-served registration is the normal case since #1947, so the half of the fleet a
// client happens to be routed to would decide the answer.
//
// The max-active-tunnels one decides a REFUSAL, which is why every case here asserts the refusal
// AND its message rather than "not 200": a 403 is shared by consent, min-version, the bandwidth
// quota and the reservation quota, so "it refused" is satisfied by four things that are not this
// (§5c). The message names the limit, so only this cause produces it.
//
// The extraction alone would not be the fix. A test of maxActiveTunnelsFor by itself goes green
// again the moment somebody inlines a "small tweak" back into one handler; this one does not,
// because it drives both real HTTP paths.

// policyFleet is one control plane that serves both a direct /api/register and an edge's
// /api/internal/edge-register, so the two paths can be compared without a second process.
type policyFleet struct {
	srv       *Server
	authToken string
	user      *db.User
	domain    string
}

const (
	policyFleetDomain      = "control.lfr-demo.se"
	policyFleetEdgeToken   = "policy-edge-secret"
	policyFleetUserID      = "policy-user"
	policyFleetPAT         = "pat-policy-123"
	policyFleetOtherUserID = "policy-other-user"
)

// newPolicyFleet builds the control plane, with tune applied to the config before the server is
// created and to the user record before it is written. Both knobs are needed: the role default
// lives in the config and the per-user override lives on the row.
func newPolicyFleet(t *testing.T, tuneCfg func(*config.ServerConfig), tuneUser func(*db.User)) *policyFleet {
	t.Helper()

	hash := sha256.Sum256([]byte(policyFleetEdgeToken))
	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfg.Domains = []string{policyFleetDomain}
	cfg.DisableBackupScheduler = true
	cfg.AllowClientAutoReservation = true
	// Unlimited, so a reservation quota refusal cannot stand in for the refusal a
	// max-active-tunnels case is actually testing -- the reservation block runs first on both
	// paths, and a 403 from it carries a 403's status either way (§5c). The reservation cases
	// below set this deliberately when the quota IS the subject.
	//
	// All three, not just the default: getUserMaxReservations gives an admin a hardcoded 3 and
	// an owner an unlimited -1 when the role key is unset, so leaving these nil made the admin
	// cases refuse for the wrong reason at the third registration -- measured, not assumed.
	cfg.DefaultMaxReservations = -1
	cfg.AdminMaxReservations = intPtr(-1)
	cfg.OwnerMaxReservations = intPtr(-1)
	cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "us-edge", TokenHash: hex.EncodeToString(hash[:])}}
	if tuneCfg != nil {
		tuneCfg(cfg)
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("starting the control plane: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races
	})

	// Written through the database rather than set on a struct: both handlers re-read the user
	// with GetUser, and a struct-level fixture would skip the read they actually make.
	user := &db.User{
		ID:     policyFleetUserID,
		Email:  "policy@example.com",
		Role:   "user",
		Status: "approved",
	}
	if tuneUser != nil {
		tuneUser(user)
	}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	// A second, unrelated user, so a reservation case can seed a row held by somebody else.
	// The reservations table has a foreign key onto users, so this row has to exist before a
	// seeded reservation can name it.
	if err := srv.db.CreateUser(&db.User{
		ID:     policyFleetOtherUserID,
		Email:  "other@example.com",
		Role:   "user",
		Status: "approved",
	}); err != nil {
		t.Fatalf("creating the other user: %v", err)
	}

	patHash := sha256.Sum256([]byte(policyFleetPAT))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    policyFleetUserID,
		TokenHash: hex.EncodeToString(patHash[:]),
		Name:      "registration-policy-test-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("creating the PAT: %v", err)
	}

	return &policyFleet{srv: srv, authToken: policyFleetPAT, user: user, domain: policyFleetDomain}
}

// outcome is what one registration attempt produced, on either path, reduced to the two things
// both paths genuinely share: the HTTP status and the error text a user is shown.
type outcome struct {
	status  int
	message string
}

func (o outcome) String() string {
	if o.message == "" {
		return fmt.Sprintf("%d (accepted)", o.status)
	}
	return fmt.Sprintf("%d %q", o.status, o.message)
}

// decodeOutcome reduces a response to the status and the error text a user is shown.
//
// A decode failure is fatal rather than ignored. Both paths answer JSON on every outcome this
// test drives, so an unparseable body means the request went somewhere unexpected -- and
// swallowing it would turn that into an empty message, which is exactly how a case expecting a
// plain 200 would pass for the wrong reason (§5c).
func decodeOutcome(t *testing.T, path string, rec *httptest.ResponseRecorder) outcome {
	t.Helper()
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the %s path answered %d with a body that is not JSON (%v): %s",
			path, rec.Code, err, rec.Body.String())
	}
	return outcome{status: rec.Code, message: resp.Error}
}

// registerDirect registers straight on this gateway.
func (f *policyFleet) registerDirect(t *testing.T, subdomain string) outcome {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		AuthToken:       f.authToken,
		Ports:           []PortMapping{{LocalPort: 8080}},
	})
	if err != nil {
		t.Fatalf("marshalling the direct registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)

	return decodeOutcome(t, "direct", rec)
}

// registerViaEdge asks central to validate on an edge's behalf.
//
// The edge has no database and no opinion: what central answers here IS the decision for every
// edge-served client, and the edge relays the error text verbatim.
func (f *policyFleet) registerViaEdge(t *testing.T, subdomain string) outcome {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{
		"subdomain_prefix": %q,
		"auth_token": %q,
		"ports": [{"local_port": 8080}],
		"domains": [%q],
		"client_ip": "8.8.8.8"
	}`, subdomain, f.authToken, f.domain))
	req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/internal/edge-register", bytes.NewReader(payload))
	req.Header.Set("X-Edge-Token", policyFleetEdgeToken)
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)

	return decodeOutcome(t, "edge", rec)
}

// requireAgreement is the property itself: the two paths must answer identically, and the answer
// must be the intended one. Both halves matter -- two paths that are identically wrong agree with
// each other, and "they agree" alone is satisfied by that (§5c).
func requireAgreement(t *testing.T, direct, viaEdge, want outcome) {
	t.Helper()
	if direct != viaEdge {
		t.Errorf("the two registration paths disagree: direct answered %s, edge answered %s. "+
			"Which half of the fleet a client is routed to would decide this, and nothing else "+
			"would report it (#2018)", direct, viaEdge)
	}
	if direct != want {
		t.Errorf("direct registration answered %s, want %s", direct, want)
	}
	if viaEdge != want {
		t.Errorf("edge-served registration answered %s, want %s", viaEdge, want)
	}
}

func intPtr(v int) *int { return &v }

func TestBothRegistrationPathsEnforceTheSameMaxActiveTunnels(t *testing.T) {
	// Each case pins one step of the resolution, and the cases together pin its ORDER: the
	// role default, then the per-user override replacing it -- in BOTH directions, which is
	// the part a re-implementation gets wrong by writing a clamp instead of a replacement.
	cases := []struct {
		name      string
		tuneCfg   func(*config.ServerConfig)
		tuneUser  func(*db.User)
		wantLimit int
	}{
		{
			name:      "the default applies to an ordinary user",
			tuneCfg:   func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 1 },
			wantLimit: 1,
		},
		{
			name: "an admin gets the admin ceiling, not the default",
			tuneCfg: func(c *config.ServerConfig) {
				c.DefaultMaxActiveTunnels = 1
				c.AdminMaxActiveTunnels = intPtr(2)
			},
			tuneUser:  func(u *db.User) { u.Role = "admin" },
			wantLimit: 2,
		},
		{
			name: "an owner gets the owner ceiling, not the default",
			tuneCfg: func(c *config.ServerConfig) {
				c.DefaultMaxActiveTunnels = 1
				c.OwnerMaxActiveTunnels = intPtr(3)
			},
			tuneUser:  func(u *db.User) { u.Role = "owner" },
			wantLimit: 3,
		},
		{
			name: "an admin without an admin ceiling falls back to the default",
			tuneCfg: func(c *config.ServerConfig) {
				c.DefaultMaxActiveTunnels = 2
				c.AdminMaxActiveTunnels = nil
			},
			tuneUser:  func(u *db.User) { u.Role = "admin" },
			wantLimit: 2,
		},
		{
			name:      "the user's own override raises them above the default",
			tuneCfg:   func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 1 },
			tuneUser:  func(u *db.User) { u.MaxTunnels = intPtr(2) },
			wantLimit: 2,
		},
		{
			name: "the user's own override caps them below their role ceiling",
			tuneCfg: func(c *config.ServerConfig) {
				c.DefaultMaxActiveTunnels = 4
				c.AdminMaxActiveTunnels = intPtr(5)
			},
			tuneUser: func(u *db.User) {
				u.Role = "admin"
				u.MaxTunnels = intPtr(1)
			},
			wantLimit: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFleet(t, tc.tuneCfg, tc.tuneUser)

			// Fill the allowance exactly. These have to succeed, or the refusal below would
			// be a refusal of something else -- and a fixture that cannot register at all
			// refuses every case for free (§5c, rule 5).
			for i := 0; i < tc.wantLimit; i++ {
				sub := fmt.Sprintf("fill-%d", i)
				if got := f.registerDirect(t, sub); got.status != http.StatusOK {
					t.Fatalf("filling tunnel %d of %d was refused with %s -- the fixture cannot reach the limit under test",
						i+1, tc.wantLimit, got)
				}
			}

			// One more, on each path, on its own subdomain so neither counts as a
			// reconnection of the other.
			want := outcome{status: http.StatusForbidden, message: activeTunnelLimitRefusal(tc.wantLimit)}
			requireAgreement(t,
				f.registerDirect(t, "overflow-direct"),
				f.registerViaEdge(t, "overflow-edge"),
				want)
		})
	}
}

// seedReservation writes a reservation row directly, so a case can put the database into the
// exact state whose handling is under test.
func (f *policyFleet) seedReservation(t *testing.T, ownerID, subdomain string, expiresAt *time.Time) {
	t.Helper()
	if err := f.srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    ownerID,
		Subdomain: subdomain,
		Domain:    f.domain,
		ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("seeding a reservation for %q: %v", subdomain, err)
	}
}

func TestBothRegistrationPathsApplyTheSameSubdomainReservationPolicy(t *testing.T) {
	ago := func(d time.Duration) *time.Time { ts := time.Now().Add(-d); return &ts }
	ahead := func(d time.Duration) *time.Time { ts := time.Now().Add(d); return &ts }

	// Each path is driven against its OWN seeded subdomain rather than a shared one. The
	// policy is stateful -- a lapsed reservation is deleted and re-created by whichever path
	// reaches it first -- so a shared subdomain would have the second path answering a
	// question the first had already changed, and "both returned 200" would be satisfied for
	// two different reasons (§5c, rule 3).
	cases := []struct {
		name    string
		tuneCfg func(*config.ServerConfig)
		// seed runs once per path, against that path's own subdomain.
		seed func(t *testing.T, f *policyFleet, subdomain string)
		want outcome
	}{
		{
			name: "a live reservation held by another user is refused",
			seed: func(t *testing.T, f *policyFleet, sub string) {
				f.seedReservation(t, policyFleetOtherUserID, sub, ahead(72*time.Hour))
			},
			want: outcome{status: http.StatusConflict, message: subdomainReservedByOther},
		},
		{
			name: "a permanent reservation held by another user is refused",
			seed: func(t *testing.T, f *policyFleet, sub string) {
				f.seedReservation(t, policyFleetOtherUserID, sub, nil)
			},
			want: outcome{status: http.StatusConflict, message: subdomainReservedByOther},
		},
		{
			name: "another user's expired reservation still inside quarantine is refused",
			seed: func(t *testing.T, f *policyFleet, sub string) {
				// Expired a day ago, quarantine is three days: still theirs.
				f.seedReservation(t, policyFleetOtherUserID, sub, ago(24*time.Hour))
			},
			want: outcome{status: http.StatusConflict, message: subdomainQuarantinedMessage},
		},
		{
			name: "another user's reservation past quarantine is reclaimable",
			seed: func(t *testing.T, f *policyFleet, sub string) {
				// Expired ten days ago, well past the three-day quarantine.
				f.seedReservation(t, policyFleetOtherUserID, sub, ago(240*time.Hour))
			},
			want: outcome{status: http.StatusOK},
		},
		{
			name: "this user's own quarantined reservation is theirs to take back",
			seed: func(t *testing.T, f *policyFleet, sub string) {
				f.seedReservation(t, policyFleetUserID, sub, ago(24*time.Hour))
			},
			want: outcome{status: http.StatusOK},
		},
		{
			name:    "an unreserved subdomain is refused when auto-reservation is off",
			tuneCfg: func(c *config.ServerConfig) { c.AllowClientAutoReservation = false },
			want:    outcome{status: http.StatusForbidden, message: subdomainMustBeReserved},
		},
		{
			name:    "an unreserved subdomain is auto-reserved when it is on",
			tuneCfg: func(c *config.ServerConfig) { c.AllowClientAutoReservation = true },
			want:    outcome{status: http.StatusOK},
		},
		{
			name: "the reservation quota refuses an auto-reservation past the limit",
			tuneCfg: func(c *config.ServerConfig) {
				c.AllowClientAutoReservation = true
				c.DefaultMaxReservations = 0
			},
			want: outcome{status: http.StatusForbidden, message: subdomainQuotaReachedMessage},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFleet(t, tc.tuneCfg, nil)

			const directSub, edgeSub = "policy-direct", "policy-edge"
			if tc.seed != nil {
				tc.seed(t, f, directSub)
				tc.seed(t, f, edgeSub)
			}

			requireAgreement(t,
				f.registerDirect(t, directSub),
				f.registerViaEdge(t, edgeSub),
				tc.want)
		})
	}
}

// ---------------------------------------------------------------------------------------------
// #2020: the random-subdomain grant, the one member of the class that had already diverged.
// ---------------------------------------------------------------------------------------------

// grant is one random-subdomain registration reduced to what both paths share: the outcome, and
// the name that was handed out.
type grant struct {
	outcome
	subdomain string
}

// decodeGrant reads both halves. Same fatal-on-unparseable rule as decodeOutcome, and for the
// same reason: a body that is not JSON would otherwise reduce to an empty subdomain, and "the
// name granted is not the taken one" is satisfied for free by an empty string (§5c).
func decodeGrant(t *testing.T, path string, rec *httptest.ResponseRecorder) grant {
	t.Helper()
	var resp struct {
		Error           string `json:"error"`
		SubdomainPrefix string `json:"subdomain_prefix"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the %s path answered %d with a body that is not JSON (%v): %s",
			path, rec.Code, err, rec.Body.String())
	}
	return grant{outcome: outcome{status: rec.Code, message: resp.Error}, subdomain: resp.SubdomainPrefix}
}

// firstCandidate makes the generator hand out name as the FIRST candidate of the next
// registration, then a fresh unique name for every attempt after it.
//
// The seam (server_domain.go) is the whole reason this test can fail. Against the real generator,
// "the name granted is not the one already held" is satisfied by the candidate space being in the
// millions, whether or not anything checks (§5c, rule 3) -- which is how the edge path went from
// #183 to #2020 without the check and without a red test. Installed per registration, so each path
// meets the collision at its own first attempt rather than inheriting the other's spent stream.
func (f *policyFleet) firstCandidate(name string) {
	var n int
	f.srv.randomSubdomainSource = func() string {
		n++
		if n == 1 {
			return name
		}
		return fmt.Sprintf("free-%d", n)
	}
}

// everyCandidate makes the generator hand out the same taken name every time, so all ten attempts
// collide and both paths must reach their refusal.
func (f *policyFleet) everyCandidate(name string) {
	f.srv.randomSubdomainSource = func() string { return name }
}

// grantRandomDirect asks this gateway for a random name.
func (f *policyFleet) grantRandomDirect(t *testing.T) grant {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: "random",
		AuthToken:       f.authToken,
		Ports:           []PortMapping{{LocalPort: 8080}},
	})
	if err != nil {
		t.Fatalf("marshalling the direct registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)

	return decodeGrant(t, "direct", rec)
}

// grantRandomViaEdge asks central for a random name on an edge's behalf.
func (f *policyFleet) grantRandomViaEdge(t *testing.T) grant {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{
		"subdomain_prefix": "random",
		"auth_token": %q,
		"ports": [{"local_port": 8080}],
		"domains": [%q],
		"client_ip": "8.8.8.8"
	}`, f.authToken, f.domain))
	req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/internal/edge-register", bytes.NewReader(payload))
	req.Header.Set("X-Edge-Token", policyFleetEdgeToken)
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)

	return decodeGrant(t, "edge", rec)
}

// dropReservation removes the reservation row a successful registration auto-created, and fails
// if there was none.
//
// This is what makes a lease case attributable. Registering "held" on either path also reserves
// it, so leaving the row in place would let the reservations check -- which BOTH paths already
// had -- account for the whole result, and the test would pass identically against the divergent
// code it is meant to catch (§5c, rule 3). With the row gone, the in-memory lease is the only
// thing left that can refuse the name. The fatal is deliberate: if auto-reservation stops
// happening, this isolation has silently stopped being isolation.
func (f *policyFleet) dropReservation(t *testing.T, subdomain string) {
	t.Helper()
	existing, err := f.srv.db.GetSubdomainReservationByName(subdomain, f.domain)
	if err != nil || existing == nil {
		t.Fatalf("expected registering %q to have auto-reserved it, so this case can isolate the "+
			"in-memory check; got row %v, err %v", subdomain, existing, err)
	}
	if err := f.srv.db.DeleteSubdomainReservation(existing.ID); err != nil {
		t.Fatalf("deleting the auto-created reservation for %q: %v", subdomain, err)
	}
}

// requireFreshGrant is the property for one path: it registered, and the name it handed out is
// not the one something already holds.
func requireFreshGrant(t *testing.T, path string, g grant, taken string) {
	t.Helper()
	if g.status != http.StatusOK {
		t.Fatalf("%s registration for a random subdomain answered %s, want 200 -- a path that "+
			"cannot register at all satisfies the collision assertion for free", path, g.outcome)
	}
	if g.subdomain == "" {
		t.Fatalf("%s registration answered 200 but named no subdomain", path)
	}
	if g.subdomain == taken {
		t.Errorf("%s registration handed out %q, which is already held. A random name granted on "+
			"one path must be one the other path would also refuse: since #1288 both gateways "+
			"issue on the same apex and #1295 publishes a per-tunnel DNS record, so one name with "+
			"two owners is decided by whichever record a visitor resolves (#2020)", path, g.subdomain)
	}
}

func TestBothRegistrationPathsRefuseAnAlreadyHeldRandomSubdomain(t *testing.T) {
	// Unlimited tunnels: every case registers the holder and then two more, and a
	// max-active-tunnels refusal would answer the collision question with a 403 that has
	// nothing to do with it (§5c).
	unlimited := func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 0 }

	cases := []struct {
		name string
		// hold puts the fleet into the state that must make taken unavailable, and is
		// responsible for leaving exactly ONE mechanism able to refuse it.
		hold func(t *testing.T, f *policyFleet, taken string)
	}{
		{
			// The case the direct path already handled and the edge path did not.
			name: "a live lease this gateway serves itself",
			hold: func(t *testing.T, f *policyFleet, taken string) {
				if got := f.registerDirect(t, taken); got.status != http.StatusOK {
					t.Fatalf("seeding a local lease on %q was refused with %s", taken, got)
				}
				f.dropReservation(t, taken)
			},
		},
		{
			// The mirror, which NEITHER path handled: an edge's lease lives in central's
			// edgeLeases, not in central's registry, so the direct path's registry check
			// could not see it either. Same class, opposite direction -- the one #1750 ->
			// #1757 -> #1767 was missed three times in a row.
			name: "a live lease an edge node serves",
			hold: func(t *testing.T, f *policyFleet, taken string) {
				if got := f.registerViaEdge(t, taken); got.status != http.StatusOK {
					t.Fatalf("seeding an edge lease on %q was refused with %s", taken, got)
				}
				f.dropReservation(t, taken)
			},
		},
		{
			// The check both paths did have. Here so the test still covers it after the
			// extraction, and so a regression that drops the reservation check from the
			// shared function is caught rather than only the lease ones.
			name: "a reservation held by another user",
			hold: func(t *testing.T, f *policyFleet, taken string) {
				f.seedReservation(t, policyFleetOtherUserID, taken, nil)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFleet(t, unlimited, nil)
			const taken = "already-held"
			tc.hold(t, f, taken)

			f.firstCandidate(taken)
			requireFreshGrant(t, "direct", f.grantRandomDirect(t), taken)

			f.firstCandidate(taken)
			requireFreshGrant(t, "edge", f.grantRandomViaEdge(t), taken)
		})
	}
}

func TestBothRegistrationPathsRefuseTheSameWayWhenNoRandomSubdomainIsFree(t *testing.T) {
	f := newPolicyFleet(t, func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 0 }, nil)

	const taken = "always-held"
	f.seedReservation(t, policyFleetOtherUserID, taken, nil)

	// Every one of the ten attempts collides, on both paths, so each must give up -- with the
	// same status and the same words, since the edge relays this text to its client verbatim.
	want := outcome{status: http.StatusInternalServerError, message: randomSubdomainRefusal}

	f.everyCandidate(taken)
	direct := f.grantRandomDirect(t)
	f.everyCandidate(taken)
	viaEdge := f.grantRandomViaEdge(t)

	requireAgreement(t, direct.outcome, viaEdge.outcome, want)
}

func TestBothRegistrationPathsRecordTheSameClientVersionAndOS(t *testing.T) {
	// The low-stakes half of #2020: five byte-identical lines in both handlers. A divergence
	// here is a stale value on an admin screen, so the property is that the same registration
	// leaves the same user row whichever path served it.
	const (
		version = "v9.9.9"
		osName  = "plan9/arm64"
	)

	for _, path := range []string{"direct", "edge"} {
		t.Run(path, func(t *testing.T) {
			f := newPolicyFleet(t, nil, nil)

			var rec *httptest.ResponseRecorder
			if path == "direct" {
				payload, err := json.Marshal(RegisterRequest{
					SubdomainPrefix: "versioned",
					AuthToken:       f.authToken,
					Ports:           []PortMapping{{LocalPort: 8080}},
					ClientVersion:   version,
					ClientOS:        osName,
				})
				if err != nil {
					t.Fatalf("marshalling the direct registration: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/register", bytes.NewReader(payload))
				req.RemoteAddr = "127.0.0.1:54321"
				rec = httptest.NewRecorder()
				f.srv.ServeHTTP(rec, req)
			} else {
				payload := []byte(fmt.Sprintf(`{
					"subdomain_prefix": "versioned",
					"auth_token": %q,
					"ports": [{"local_port": 8080}],
					"domains": [%q],
					"client_ip": "8.8.8.8",
					"client_version": %q,
					"client_os": %q
				}`, f.authToken, f.domain, version, osName))
				req := httptest.NewRequest(http.MethodPost, "http://"+f.domain+"/api/internal/edge-register", bytes.NewReader(payload))
				req.Header.Set("X-Edge-Token", policyFleetEdgeToken)
				rec = httptest.NewRecorder()
				f.srv.ServeHTTP(rec, req)
			}

			if got := decodeOutcome(t, path, rec); got.status != http.StatusOK {
				t.Fatalf("the %s registration was refused with %s -- a refusal writes no user row, "+
					"so the assertion below would be about the fixture rather than the bookkeeping", path, got)
			}

			// Re-read from the database, not from the struct the handler happened to hold: the
			// write itself is the thing under test.
			stored, err := f.srv.db.GetUser(policyFleetUserID)
			if err != nil || stored == nil {
				t.Fatalf("re-reading the user row: %v", err)
			}
			if stored.LastClientVersion != version {
				t.Errorf("the %s path stored last_client_version %q, want %q", path, stored.LastClientVersion, version)
			}
			if stored.LastClientOS != osName {
				t.Errorf("the %s path stored last_client_os %q, want %q", path, stored.LastClientOS, osName)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------
// #2031: the stored subdomain-style preference, honoured at registration.
// ---------------------------------------------------------------------------------------------

// subdomainStyleShapes describes each generator well enough to tell it from the others. They are
// mutually exclusive on purpose: a granted name that matches the wrong one is a style that was not
// honoured, which is precisely the defect, and "it generated something" would be satisfied by every
// style alike (§5c rule 1).
var subdomainStyleShapes = map[string]*regexp.Regexp{
	"liferay": regexp.MustCompile(`^[a-z]+(-[a-z]+)*-\d{3}$`),
	"heroku":  regexp.MustCompile(`^[a-z]+(-[a-z]+)*-\d{4}$`),
	"words":   regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`),
	"ngrok":   regexp.MustCompile(`^[0-9a-f]{4}-tunnel$`),
	"random":  regexp.MustCompile(`^[a-z0-9]{8}$`),
}

// requireStyle asserts the granted name is one THIS style produces and one no other style could
// have produced. Without the second half, "liferay was silently used instead" passes whenever the
// expected style's own pattern is loose.
func requireStyle(t *testing.T, path, style, granted string) {
	t.Helper()
	want, ok := subdomainStyleShapes[style]
	if !ok {
		t.Fatalf("no shape described for style %q", style)
	}
	if !want.MatchString(granted) {
		t.Errorf("the %s path granted %q, which is not a %q name -- the user's stored "+
			"SubdomainStyle was not honoured (#2031)", path, granted, style)
		return
	}
	for other, re := range subdomainStyleShapes {
		if other != style && re.MatchString(granted) {
			t.Errorf("the %s path granted %q, which matches both %q and %q -- this test cannot "+
				"tell the two styles apart, so it would not notice one being used for the other",
				path, granted, style, other)
		}
	}
}

func TestBothRegistrationPathsHonourTheUsersSubdomainStyle(t *testing.T) {
	// Every style the account settings screens offer. The point of #2031 is that a user who
	// picks one of these gets it on the CLI, which is what that setting's own help text promises.
	for _, style := range []string{"liferay", "words", "heroku", "ngrok", "random"} {
		t.Run(style, func(t *testing.T) {
			f := newPolicyFleet(t,
				func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 0 },
				func(u *db.User) { u.SubdomainStyle = style })

			// The generator seam from #2020 is deliberately NOT installed here: it replaces
			// generateRandomSubdomainPrefix outright, so a stubbed candidate stream would
			// bypass the very style resolution under test and every case would pass (§5c).
			direct := f.grantRandomDirect(t)
			if direct.status != http.StatusOK {
				t.Fatalf("direct registration was refused with %s", direct.outcome)
			}
			requireStyle(t, "direct", style, direct.subdomain)

			viaEdge := f.grantRandomViaEdge(t)
			if viaEdge.status != http.StatusOK {
				t.Fatalf("edge registration was refused with %s", viaEdge.outcome)
			}
			requireStyle(t, "edge", style, viaEdge.subdomain)
		})
	}
}

func TestAnUnusableSubdomainStyleFallsBackToLiferay(t *testing.T) {
	cases := []struct {
		name  string
		style string
	}{
		{"a style nothing recognises", "hieroglyphics"},
		{"an empty style", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFleet(t,
				func(c *config.ServerConfig) { c.DefaultMaxActiveTunnels = 0 },
				func(u *db.User) { u.SubdomainStyle = tc.style })

			got := f.grantRandomDirect(t)
			if got.status != http.StatusOK {
				t.Fatalf("registration was refused with %s -- an unusable preference must fall "+
					"back, not refuse: the name is the service's to choose", got.outcome)
			}
			// Specifically liferay, not merely "something": generateRandomSubdomainPrefix's
			// default branch is the "random" style, so a typo reaching it would silently grant
			// a different REAL style rather than the intended fallback.
			requireStyle(t, "direct", defaultSubdomainStyle, got.subdomain)
		})
	}
}

// TestSubdomainStyleForIgnoresAnAbsentUserRecord pins the nil case directly: userRec is nil when
// there is no database or the row could not be read, and an unavailable record is not evidence of
// a preference.
func TestSubdomainStyleForIgnoresAnAbsentUserRecord(t *testing.T) {
	if got := subdomainStyleFor(nil); got != defaultSubdomainStyle {
		t.Errorf("subdomainStyleFor(nil) = %q, want %q", got, defaultSubdomainStyle)
	}
}

// TestEveryStyleThePortalOffersIsOneTheServerHonours is the class-level half of #2031.
//
// The defect was not that one style was missed, it was that the column had a writer and no reader:
// the portal offered choices the server never consulted. Fixing only the reading leaves the same
// shape available to anyone who adds a sixth option. This reads the actual account-settings
// markup -- both portals, since either can write the column -- and requires every value offered to
// be one subdomainStyles recognises.
func TestEveryStyleThePortalOffersIsOneTheServerHonours(t *testing.T) {
	sources := []struct {
		path string
		// selectID is the element whose options write db.User.SubdomainStyle.
		marker string
	}{
		{path: "dashboard.html", marker: `id="acc-subdomain-style"`},
		{path: filepath.Join("..", "..", "ui", "src", "pages", "AccountSettings.tsx"), marker: `id="default-subdomain-style"`},
	}

	optionValue := regexp.MustCompile(`value="([a-z]+)"`)
	total := 0

	for _, src := range sources {
		raw, err := os.ReadFile(src.path)
		if err != nil {
			// Loud rather than skipped: a moved file must fail this test, not silently
			// shrink its corpus to nothing (§5c rule 5).
			t.Fatalf("reading %s: %v -- if this screen moved, point this test at its new home "+
				"rather than dropping it, or the portal can drift away from the server again", src.path, err)
		}
		body := string(raw)
		idx := strings.Index(body, src.marker)
		if idx < 0 {
			t.Fatalf("could not find %s in %s -- this test is looking at the wrong element and "+
				"would pass over any mismatch at all", src.marker, src.path)
		}
		// The <select> ends at the first closing tag after it.
		rest := body[idx:]
		if end := strings.Index(rest, "</select>"); end >= 0 {
			rest = rest[:end]
		}

		found := 0
		for _, m := range optionValue.FindAllStringSubmatch(rest, -1) {
			style := m[1]
			found++
			total++
			if !subdomainStyles[style] {
				t.Errorf("%s offers the subdomain style %q, which subdomainStyles does not "+
					"recognise -- a user choosing it would silently get %q instead. Either teach "+
					"generateRandomSubdomainPrefix that style and add it to subdomainStyles, or "+
					"remove the option (#2031).", src.path, style, defaultSubdomainStyle)
			}
		}
		if found == 0 {
			t.Errorf("found no <option value=...> under %s in %s -- the markup changed shape, so "+
				"this test is now checking nothing", src.marker, src.path)
		}
	}

	if total < 4 {
		t.Errorf("only %d style option(s) were checked across both portals, which is fewer than "+
			"either screen offers -- the scan is not reading what it thinks it is", total)
	}
}
