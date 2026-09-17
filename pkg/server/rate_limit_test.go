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

// The two registration paths must grant the SAME rate limit (#2005).
//
// The arithmetic lived in two identical blocks 4,700 lines apart -- handleRegister for a client
// registering directly on this gateway, handleEdgeRegister for central validating on an edge's
// behalf -- and nothing would have gone red had one been changed and the other forgotten. Both
// paths still register successfully; only the granted number differs, and the user it differs
// for is whoever happened to be routed to the other half of the fleet.
//
// So this asserts the PROPERTY across both paths rather than the arithmetic in either:
// same server, same user record, same ceiling, same requested value -- same answer. A future
// divergence fails here instead of shipping. That is §5b's rule, and the reason the extraction
// alone is not the fix: one method with one test of that method would go green again the moment
// somebody inlined a "small tweak" back into one handler.

// rateLimitFleet is one control plane that accepts both a direct registration and an edge's
// validate call, so the two paths can be compared without a second process.
type rateLimitFleet struct {
	srv       *Server
	authToken string
}

func newRateLimitFleet(t *testing.T, userLimit, fleetCeiling int) *rateLimitFleet {
	t.Helper()

	hash := sha256.Sum256([]byte("rate-limit-edge-secret"))
	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfg.Domains = []string{"control.lfr-demo.se"}
	cfg.DisableBackupScheduler = true
	cfg.AllowClientAutoReservation = true
	cfg.MaxTunnelRateLimit = fleetCeiling
	cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "us-edge", TokenHash: hex.EncodeToString(hash[:])}}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("starting the control plane: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races
	})

	// RateLimit on the USER record, which is the first clamp. Written through the database
	// rather than set on a struct, because both handlers re-read the user with GetUser and a
	// struct-level fixture would skip the read they actually make.
	if err := srv.db.CreateUser(&db.User{
		ID:        "rate-user",
		Email:     "rate@example.com",
		Role:      "user",
		Status:    "approved",
		RateLimit: userLimit,
	}); err != nil {
		t.Fatalf("creating the user: %v", err)
	}
	patHash := sha256.Sum256([]byte("pat-rate-123"))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    "rate-user",
		TokenHash: hex.EncodeToString(patHash[:]),
		Name:      "rate-limit-test-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("creating the PAT: %v", err)
	}

	return &rateLimitFleet{srv: srv, authToken: "pat-rate-123"}
}

// grantedDirect registers straight on this gateway and reports the limit the lease carries.
//
// Read off the LEASE rather than the response, because the direct path does not put the number
// in its reply -- it hands it to registry.Register, and the lease is where the proxy then reads
// it from. Asserting anything else would be asserting a value nothing enforces.
func (f *rateLimitFleet) grantedDirect(t *testing.T, subdomain string, requested int) int {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		AuthToken:       f.authToken,
		Ports:           []PortMapping{{LocalPort: 8080}},
		RateLimit:       requested,
	})
	if err != nil {
		t.Fatalf("marshalling the direct registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://control.lfr-demo.se/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the direct registration was refused with %d: %s", rec.Code, rec.Body.String())
	}

	for _, lease := range f.srv.registry.ListLeases() {
		if lease.SubdomainPrefix == subdomain {
			// RateLimit, not BaseRateLimit. BaseRateLimit is the value a quota throttle
			// restores TO, and on this path it comes back 0 -- which is #2006, fixed in
			// #2007 by not publishing it rather than by populating it. RateLimit is what
			// the proxy actually enforces, so it is the only one worth asserting.
			return lease.RateLimit
		}
	}
	t.Fatalf("no lease for %q after a registration that returned 200 -- nothing to read a limit from", subdomain)
	return 0
}

// grantedViaEdge asks central to validate on an edge's behalf and reports the limit it returns.
//
// The edge has no database and no opinion: whatever comes back on this response is what its
// lease is created with, so this response field IS the granted limit for every edge-served
// client.
func (f *rateLimitFleet) grantedViaEdge(t *testing.T, subdomain string, requested int) int {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{
		"subdomain_prefix": %q,
		"auth_token": %q,
		"ports": [{"local_port": 8080}],
		"domains": ["control.lfr-demo.se"],
		"client_ip": "8.8.8.8",
		"rate_limit": %d
	}`, subdomain, f.authToken, requested))
	req := httptest.NewRequest(http.MethodPost, "http://control.lfr-demo.se/api/internal/edge-register", bytes.NewReader(payload))
	req.Header.Set("X-Edge-Token", "rate-limit-edge-secret")
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the edge validation was refused with %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		RateLimit int `json:"rate_limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the edge validation response: %v", err)
	}
	return resp.RateLimit
}

func TestBothRegistrationPathsGrantTheSameRateLimit(t *testing.T) {
	// Each case pins one clamp, and the cases together pin the ORDER of the two -- which is
	// the part a re-implementation gets wrong. "user limit below the ceiling" and "ceiling
	// below the user limit" have different answers only if the clamps are applied in the
	// right sequence.
	cases := []struct {
		name         string
		userLimit    int
		fleetCeiling int
		requested    int
		want         int
	}{
		{"no limits anywhere is unlimited", 0, 0, 0, 0},
		{"a request with no limits set is honoured", 0, 0, 500, 500},
		{"the user's limit caps a larger request", 100, 0, 500, 100},
		{"the user's limit fills in an absent request", 100, 0, 0, 100},
		{"a smaller request beats the user's limit", 100, 0, 50, 50},
		{"the ceiling caps a larger request", 0, 200, 500, 200},
		{"the ceiling fills in an absent request", 0, 200, 0, 200},
		{"the ceiling caps the user's own larger limit", 400, 200, 0, 200},
		{"the user's smaller limit survives a higher ceiling", 100, 400, 0, 100},
		{"the ceiling caps everything", 400, 200, 900, 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRateLimitFleet(t, tc.userLimit, tc.fleetCeiling)

			direct := f.grantedDirect(t, "direct-sub", tc.requested)
			viaEdge := f.grantedViaEdge(t, "edge-sub", tc.requested)

			if direct != viaEdge {
				t.Errorf("the two registration paths disagree: direct granted %d, edge granted %d. "+
					"Whichever half of the fleet a client is routed to decides their rate limit, "+
					"and both still register successfully, so nothing else would report this (#2005)",
					direct, viaEdge)
			}
			// Asserted as well as compared: two paths that are identically wrong agree with
			// each other, and "they agree" is satisfied by that (§5c).
			if direct != tc.want {
				t.Errorf("direct registration granted %d, want %d (requested %d, user limit %d, ceiling %d)",
					direct, tc.want, tc.requested, tc.userLimit, tc.fleetCeiling)
			}
		})
	}
}
