package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	var resp struct {
		Error string `json:"error"`
	}
	// A decode failure is not a finding here: the body is asserted through status+message, and
	// an unparseable body shows up as an empty message against a non-200, which no case wants.
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return outcome{status: rec.Code, message: resp.Error}
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

	var resp struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return outcome{status: rec.Code, message: resp.Error}
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
