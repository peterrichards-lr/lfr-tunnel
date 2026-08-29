package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/geo"
)

// stubResolver stands in for a MaxMind database so these tests need no licensed
// artefact. geo.Resolver is an interface for exactly this reason.
type stubResolver struct {
	countries map[string]string
}

func (s stubResolver) Country(ip netip.Addr) (string, bool) {
	c, ok := s.countries[ip.String()]
	return c, ok
}

func (s stubResolver) Close() error { return nil }

// setupGeoTestServer builds a server from the shipped defaults rather than a bare
// ServerConfig, because these tests drive a real registration and the default
// reservation quotas are part of what makes one succeed.
func setupGeoTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(t.TempDir(), "geo_test.db")
	cfg.DisableBackupScheduler = true
	// So a registration can claim its own subdomain rather than needing one reserved in
	// the portal first -- these tests are about what registration records, not about the
	// reservation rules it enforces on the way.
	cfg.AllowClientAutoReservation = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite TempDir cleanup races
	})
	return srv
}

// seedGeoUser creates an approved user and a PAT that handleRegister will accept.
func seedGeoUser(t *testing.T, srv *Server, email, token string) {
	t.Helper()
	user := &db.User{ID: email, Email: email, Role: "user", Status: "approved"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("creating user %s: %v", email, err)
	}
	sum := sha256.Sum256([]byte(token))
	pat := &db.PersonalAccessToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(sum[:]),
		Name:      "geo-test-pat",
		CreatedAt: time.Now(),
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("creating PAT for %s: %v", email, err)
	}
}

// registerFrom drives a client registration as if it arrived from remoteIP.
func registerFrom(t *testing.T, srv *Server, token, subdomain, remoteIP string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		AuthToken:       token,
		Ports:           []PortMapping{{LocalPort: 8080}},
	})
	if err != nil {
		t.Fatalf("marshalling register payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(payload))
	req.RemoteAddr = remoteIP + ":54321"
	rec := httptest.NewRecorder()
	srv.handleRegister(rec, req)
	return rec
}

// TestGeoDisabledByDefault: no MaxMind database ships with the server, so the untouched
// configuration must leave the feature off rather than half-on.
func TestGeoDisabledByDefault(t *testing.T) {
	srv := setupGeoTestServer(t)

	if srv.geo != nil {
		t.Errorf("geo aggregator is active with no database configured")
	}
}

// TestServerStartsWithAMissingGeoDatabase is the graceful-absence requirement: a
// configured-but-absent database disables the feature, it does not fail startup.
func TestServerStartsWithAMissingGeoDatabase(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
		DBPath:                 filepath.Join(t.TempDir(), "geo_missing.db"),
		GeoLite2DBPath:         filepath.Join(t.TempDir(), "nope", "GeoLite2-Country.mmdb"),
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer refused to start without a geo database: %v", err)
	}
	defer srv.Stop()

	if srv.geo != nil {
		t.Errorf("geo aggregator is active despite the database being absent")
	}
}

// TestRegistrationSucceedsWithoutGeoDatabase is the hard requirement from the design: a
// geo lookup must never be able to block a user connecting.
func TestRegistrationSucceedsWithoutGeoDatabase(t *testing.T) {
	srv := setupGeoTestServer(t)

	if srv.geo != nil {
		t.Fatalf("test precondition: expected no geo aggregator")
	}
	seedGeoUser(t, srv, "nogeo@example.com", "nogeo-token")

	rec := registerFrom(t, srv, "nogeo-token", "nogeo-sub", "203.0.113.10")
	if rec.Code != http.StatusOK {
		t.Fatalf("registration failed with the geo feature disabled: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRegistrationIsCountedAnonymously walks the whole path: register, flush, read back.
// The threshold is lowered to 2 so the test needs two users rather than five; that it can
// be lowered at all is covered in pkg/geo, as is the fact that it cannot be disabled.
func TestRegistrationIsCountedAnonymously(t *testing.T) {
	srv := setupGeoTestServer(t)

	srv.geo = geo.New(
		stubResolver{countries: map[string]string{
			"203.0.113.10": "GB",
			"203.0.113.11": "GB",
		}},
		geoStore{database: srv.db},
		geo.Options{Threshold: 2},
	)
	if srv.geo == nil {
		t.Fatalf("test setup: aggregator was not created")
	}

	seedGeoUser(t, srv, "geo-one@example.com", "geo-token-1")
	seedGeoUser(t, srv, "geo-two@example.com", "geo-token-2")

	if rec := registerFrom(t, srv, "geo-token-1", "geo-sub-1", "203.0.113.10"); rec.Code != http.StatusOK {
		t.Fatalf("first registration: %d %s", rec.Code, rec.Body.String())
	}
	if rec := registerFrom(t, srv, "geo-token-2", "geo-sub-2", "203.0.113.11"); rec.Code != http.StatusOK {
		t.Fatalf("second registration: %d %s", rec.Code, rec.Body.String())
	}

	if err := srv.geo.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	period, stats, err := srv.db.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if period != geo.PeriodKey(time.Now().UTC()) {
		t.Errorf("period: got %q, want the current ISO week %q", period, geo.PeriodKey(time.Now().UTC()))
	}
	if len(stats) != 1 || stats[0].Bucket != "GB" || stats[0].Count != 2 {
		t.Fatalf("got %+v, want a single GB bucket holding 2", stats)
	}

	// The stored row must carry nothing but the bucket and the count. Asserted against
	// the database rather than the API so a renderer cannot be what makes it anonymous.
	_, rows, err := srv.db.GetLocationStats(period)
	if err != nil {
		t.Fatalf("re-reading stats: %v", err)
	}
	for _, r := range rows {
		if r.Bucket != "GB" && r.Bucket != geo.OtherBucket {
			t.Errorf("unexpected bucket %q -- only country codes and %q may be stored", r.Bucket, geo.OtherBucket)
		}
	}
}

// TestSubThresholdRegistrationNeverReachesStorage is the k-anonymity guarantee at the
// level an operator experiences it: one user in a country produces no row at all.
func TestSubThresholdRegistrationNeverReachesStorage(t *testing.T) {
	srv := setupGeoTestServer(t)

	srv.geo = geo.New(
		stubResolver{countries: map[string]string{"203.0.113.20": "PT"}},
		geoStore{database: srv.db},
		geo.Options{},
	)
	seedGeoUser(t, srv, "lonely@example.com", "lonely-token")

	if rec := registerFrom(t, srv, "lonely-token", "lonely-sub", "203.0.113.20"); rec.Code != http.StatusOK {
		t.Fatalf("registration: %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.geo.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	_, stats, err := srv.db.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("a single user reached storage as %+v", stats)
	}
}

// TestLocationAnalyticsHandlerWhenUnavailable checks the panel can tell "no database
// deployed" from "deployed, but nothing has cleared the threshold" -- they look identical
// in the data and mean very different things.
func TestLocationAnalyticsHandlerWhenUnavailable(t *testing.T) {
	srv := setupGeoTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/analytics/locations", nil)
	rec := httptest.NewRecorder()
	srv.handleGetLocationAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp locationAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Available {
		t.Errorf("available: got true, want false with no database configured")
	}
	if resp.Buckets == nil {
		t.Errorf("buckets: got null, want an empty array so the panel need not guard")
	}
	if len(resp.Buckets) != 0 {
		t.Errorf("buckets: got %+v, want empty", resp.Buckets)
	}
	if resp.Threshold != geo.DefaultThreshold {
		t.Errorf("threshold: got %d, want %d", resp.Threshold, geo.DefaultThreshold)
	}
}

// TestLocationAnalyticsHandlerReturnsStoredBuckets covers the populated case.
func TestLocationAnalyticsHandlerReturnsStoredBuckets(t *testing.T) {
	srv := setupGeoTestServer(t)

	srv.geo = geo.New(stubResolver{}, geoStore{database: srv.db}, geo.Options{})
	if err := srv.db.UpsertLocationStats("2026-W34", []db.LocationStat{{Bucket: "GB", Count: 6}}); err != nil {
		t.Fatalf("seeding W34: %v", err)
	}
	if err := srv.db.UpsertLocationStats("2026-W35", []db.LocationStat{
		{Bucket: "GB", Count: 11},
		{Bucket: geo.OtherBucket, Count: 5},
	}); err != nil {
		t.Fatalf("seeding W35: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/analytics/locations", nil)
	rec := httptest.NewRecorder()
	srv.handleGetLocationAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp locationAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !resp.Available {
		t.Errorf("available: got false, want true")
	}
	if resp.Period != "2026-W35" {
		t.Errorf("period: got %q, want the most recent week %q", resp.Period, "2026-W35")
	}
	if len(resp.Buckets) != 2 || resp.Buckets[0].Bucket != "GB" || resp.Buckets[0].Count != 11 {
		t.Errorf("buckets: got %+v, want GB=11 first then %s=5", resp.Buckets, geo.OtherBucket)
	}
}

func TestLocationAnalyticsHandlerRejectsNonGet(t *testing.T) {
	srv := setupGeoTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/analytics/locations", nil)
	rec := httptest.NewRecorder()
	srv.handleGetLocationAnalytics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rec.Code)
	}
}

// TestObserveGeoLocationAcceptsBothAddressForms covers the two shapes a client address
// arrives in: bare from clientIPFrom, host:port when an edge node forwards what it
// resolved.
func TestObserveGeoLocationAcceptsBothAddressForms(t *testing.T) {
	srv := setupGeoTestServer(t)

	srv.geo = geo.New(
		stubResolver{countries: map[string]string{
			"203.0.113.30": "GB",
			"2001:db8::1":  "GB",
		}},
		geoStore{database: srv.db},
		geo.Options{Threshold: 2},
	)

	srv.observeGeoLocation("user-a@example.com", "203.0.113.30")
	srv.observeGeoLocation("user-b@example.com", "203.0.113.30:9999")
	srv.observeGeoLocation("user-c@example.com", "[2001:db8::1]:9999")
	// Neither of these is an address, and neither may panic or count.
	srv.observeGeoLocation("user-d@example.com", "not-an-ip")
	srv.observeGeoLocation("user-e@example.com", "")

	if err := srv.geo.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_, stats, err := srv.db.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Bucket != "GB" || stats[0].Count != 3 {
		t.Errorf("got %+v, want a single GB bucket holding 3", stats)
	}
}

// TestObserveGeoLocationIsSafeWhenDisabled is the nil-aggregator contract the
// registration path depends on.
func TestObserveGeoLocationIsSafeWhenDisabled(t *testing.T) {
	srv := setupGeoTestServer(t)

	srv.geo = nil
	srv.observeGeoLocation("someone@example.com", "203.0.113.40")
}
