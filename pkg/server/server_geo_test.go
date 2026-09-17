package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
	// provider is what the panel would attribute the data to. Zero value is the empty
	// string rather than a vendor, so a test that does not care cannot accidentally assert
	// somebody's credit line (#1921).
	provider geo.Provider
}

func (s stubResolver) Country(ip netip.Addr) (string, bool) {
	c, ok := s.countries[ip.String()]
	return c, ok
}

func (s stubResolver) Provider() geo.Provider {
	if s.provider == "" {
		return geo.ProviderUnknown
	}
	return s.provider
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
	// A vendor has to be in force for rows to be served at all (#1995): rendering a table
	// under nobody's credit is the licence breach the panel is gated on.
	srv.cfg.CountryDBProvider = string(geo.ProviderMaxMind)
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
	// With the feature ON there is nothing to diagnose, and the configured path -- a
	// filesystem path on the gateway host -- must not be sent at all (#1938).
	if resp.Reason != "" || resp.ConfiguredPath != "" || resp.Detail != "" {
		t.Errorf("a working panel carried diagnosis fields: reason=%q path=%q detail=%q",
			resp.Reason, resp.ConfiguredPath, resp.Detail)
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

// geoServerWithPath builds a server whose geolite2_db_path is exactly path, driving the
// real constructor (NewServer -> newGeoAggregator -> geo.OpenResolver) rather than setting
// the diagnosis by hand. What the panel reports has to be what that path actually produces
// in production; a hand-set field would assert against a state the gateway cannot reach.
func geoServerWithPath(t *testing.T, path string) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(t.TempDir(), "geo_diag.db")
	cfg.DisableBackupScheduler = true
	cfg.GeoLite2DBPath = path

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer refused to start with geolite2_db_path=%q: %v", path, err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite TempDir cleanup races
	})
	return srv
}

// locationsFor drives the real handler and decodes what an admin's panel receives.
func locationsFor(t *testing.T, srv *Server) locationAnalyticsResponse {
	t.Helper()
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
	return resp
}

// TestGeoPanelTellsAMistypedPathFromAnUnsetOne is the assertion #1938 was filed for.
//
// Both states leave the feature off and both serialise to available:false, so every
// pre-existing test in this file passes either way -- the defect was that the panel had no
// way to say WHICH, and told an operator who had mistyped geolite2_db_path that they had
// configured nothing. The reason is asserted by value, and the path by being named, because
// "the response differs" is satisfied by any incidental field.
func TestGeoPanelTellsAMistypedPathFromAnUnsetOne(t *testing.T) {
	typo := filepath.Join(t.TempDir(), "geoip", "GeoLite2-Cuntry.mmdb")

	unset := locationsFor(t, geoServerWithPath(t, ""))
	mistyped := locationsFor(t, geoServerWithPath(t, typo))

	// Unchanged for every existing client: the feature is off in both cases and `available`
	// still says so on its own.
	if unset.Available || mistyped.Available {
		t.Fatalf("available: got unset=%v mistyped=%v, want false for both", unset.Available, mistyped.Available)
	}

	if unset.Reason != geoReasonNotConfigured {
		t.Errorf("unset path: reason %q, want %q", unset.Reason, geoReasonNotConfigured)
	}
	if mistyped.Reason != geoReasonPathNotFound {
		t.Errorf("mistyped path: reason %q, want %q", mistyped.Reason, geoReasonPathNotFound)
	}
	if mistyped.Reason == unset.Reason {
		t.Errorf("a mistyped path and an unset one still report the same reason %q", unset.Reason)
	}
	// The typo has to be visible in the message, which is the whole point: an operator
	// comparing what they configured against what the gateway tried is how a wrong path
	// gets spotted.
	if mistyped.ConfiguredPath != typo {
		t.Errorf("configured_path: got %q, want %q", mistyped.ConfiguredPath, typo)
	}
	// Nothing was configured, so there is no path to name -- and inventing one would tell
	// the operator the opposite of the truth.
	if unset.ConfiguredPath != "" {
		t.Errorf("an unset path reported configured_path %q", unset.ConfiguredPath)
	}
}

// TestGeoPanelReportsAnUnreadableDatabase is the third state: the file is where the
// operator said and cannot be used. Distinct from the missing-file case because the remedy
// is different -- a different file, not a different path.
func TestGeoPanelReportsAnUnreadableDatabase(t *testing.T) {
	// Named .BIN because that is the real-world instance: IP2Location's default download is
	// their proprietary format, and geo.OpenResolver names that rather than reporting an
	// opaque parse error (#1921). Contents are not an mmdb either way.
	path := filepath.Join(t.TempDir(), "IP2LOCATION-LITE-DB1.BIN")
	if err := os.WriteFile(path, []byte("not a MaxMind database"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	srv := geoServerWithPath(t, path)

	resp := locationsFor(t, srv)
	if resp.Available {
		t.Fatalf("available: got true with an unreadable database")
	}
	if resp.Reason != geoReasonUnreadable {
		t.Errorf("reason: got %q, want %q", resp.Reason, geoReasonUnreadable)
	}
	if resp.ConfiguredPath != path {
		t.Errorf("configured_path: got %q, want %q", resp.ConfiguredPath, path)
	}
	// The vendor diagnosis is the one sentence that resolves this state, so it has to
	// survive the trip to the panel rather than staying in the journal.
	if !strings.Contains(resp.Detail, "MMDB edition") {
		t.Errorf("detail %q does not carry the .BIN diagnosis an operator needs", resp.Detail)
	}

	// An unusable file must not take the rest of the analytics page with it, and must not
	// have stopped the server starting -- geoServerWithPath would have failed already, so
	// this asserts the surviving half: the handler answers normally.
	if resp.Threshold != geo.DefaultThreshold || resp.Buckets == nil {
		t.Errorf("the panel degraded: threshold=%d buckets=%v", resp.Threshold, resp.Buckets)
	}
}

// TestGeoDiagnosisIsNotServedToANonAdmin. The reason names a filesystem path on the gateway
// host, which is admin-only detail. Driven through handleAdminEndpoints -- the real
// dispatch, which calls requireAdmin before any route below it -- rather than through the
// handler directly, because the handler has no gate of its own and testing it would prove
// nothing about what an anonymous caller can reach.
func TestGeoDiagnosisIsNotServedToANonAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geoip", "secret-looking-path.mmdb")
	srv := geoServerWithPath(t, path)

	// Control: an admin does get it, so a "no path in the body" pass below cannot be the
	// route being broken for everyone.
	if admin := locationsFor(t, srv); admin.ConfiguredPath != path {
		t.Fatalf("test precondition: an admin must see the path, got %q", admin.ConfiguredPath)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/analytics/locations", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminEndpoints(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("an unauthenticated caller reached the locations endpoint: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), path) {
		t.Errorf("the configured geo-IP path leaked to an unauthenticated caller: %s", rec.Body.String())
	}
}

// TestLocationAnalyticsCarriesTheProviderForAttribution (#1921, #1964, #1995).
//
// The panel has to render the credit the supplying vendor's licence requires, and each
// vendor requires a different one -- so "which vendor" has to reach the browser.
//
// It is DECLARED, not derived. #1921 read it from the file's own metadata; #1964 measured a
// real IP2Location MMDB reporting MaxMind's database_type, description, languages AND record
// schema, so derivation credited MaxMind for IP2Location's data. The stub therefore reports
// ProviderUnknown while the setting says dbip: if the handler read the resolver rather than
// the setting, this would return "unknown" and fail.
func TestLocationAnalyticsCarriesTheProviderForAttribution(t *testing.T) {
	srv := setupGeoTestServer(t)
	srv.geo = geo.New(
		stubResolver{},
		geoStore{database: srv.db},
		geo.Options{},
	)
	srv.cfg.CountryDBProvider = string(geo.ProviderDBIP)

	if resp := locationsFor(t, srv); resp.Provider != string(geo.ProviderDBIP) {
		t.Errorf("provider: got %q, want %q -- the panel cannot render DB-IP's required "+
			"link back to db-ip.com without it", resp.Provider, geo.ProviderDBIP)
	}
}

// TestLocationAnalyticsNamesNoProviderWhenThereIsNoDatabase.
//
// With nothing open there is no data on screen, so there is nothing to attribute -- and
// naming a vendor here would put a credit line under a panel that is switched off. This is
// the state most deployments are in permanently, so it is the one most likely to be seen.
func TestLocationAnalyticsNamesNoProviderWhenThereIsNoDatabase(t *testing.T) {
	srv := setupGeoTestServer(t)
	if srv.geo != nil {
		t.Fatalf("premise broken: the test server should have no geo database")
	}

	if resp := locationsFor(t, srv); resp.Provider != "" {
		t.Errorf("provider: got %q with no database configured, want empty", resp.Provider)
	}
}

// TestAnUndeclaredDatabaseIsNotAttributedToAVendor.
//
// The failure this guards is silent by construction: a plausible, complete credit line
// naming a company that did not supply the data, with the actual supplier's licence still
// unmet and nothing on the page to contradict it.
//
// Before #1964 the vendor was derived and "unknown" travelled to the client so the panel
// could say it could not tell. Since #1990 it is declared, so the honest answer to "who
// published this?" with nothing declared is to render no rows and credit nobody -- which is
// what this now asserts. #1995 moved that decision from startup to request time; the property
// is unchanged.
func TestAnUndeclaredDatabaseIsNotAttributedToAVendor(t *testing.T) {
	srv := setupGeoTestServer(t)
	srv.geo = geo.New(stubResolver{}, geoStore{database: srv.db}, geo.Options{})

	resp := locationsFor(t, srv)
	if resp.Provider != "" {
		t.Errorf("provider: got %q with no vendor declared, want empty -- \"we do not know\" "+
			"must never round to somebody's trademark", resp.Provider)
	}
	if resp.Available {
		t.Error("the panel reported itself available with no vendor declared, so it would " +
			"render rows under nobody's credit")
	}
	if resp.Reason != geoReasonProviderNotDeclared {
		t.Errorf("reason: got %q, want %q -- telling an operator who deployed a database that "+
			"they deployed none sends them to check the path", resp.Reason, geoReasonProviderNotDeclared)
	}
}

// TestTheServerOpensThePathTheResolvedKeyNames (#1921).
//
// pkg/config proves the two spellings of the geo database path resolve to one value; this
// proves server.go reads that resolved value rather than the field it used to read. Leaving
// `cfg.GeoLite2DBPath` in server.go still compiles and still passes every test in
// pkg/config, and ships a gateway that ignores `country_db_path` entirely.
//
// Asserted through `configured_path` in the admin payload -- the field #1938 added for
// exactly this question -- rather than through the log, because that is the value an
// operator is shown and the one that has to name the file the gateway actually tried.
func TestTheServerOpensThePathTheResolvedKeyNames(t *testing.T) {
	cases := []struct {
		name string
		set  func(*config.ServerConfig, string)
	}{
		{"legacy alias", func(c *config.ServerConfig, p string) { c.GeoLite2DBPath = p }},
		{"neutral key", func(c *config.ServerConfig, p string) { c.CountryDBPath = p }},
		// Both set, disagreeing: the neutral key wins. See
		// config.CountryDatabasePath for why that direction and not the other.
		{"both, neutral wins", func(c *config.ServerConfig, p string) {
			c.GeoLite2DBPath = filepath.Join(t.TempDir(), "absent", "ignored-alias.mmdb")
			c.CountryDBPath = p
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Unique per case so a reported path cannot have come from another one.
			wantPath := filepath.Join(t.TempDir(), "absent", tc.name+"-country.mmdb")

			cfg := config.DefaultServerConfig()
			cfg.Domains = []string{"example.com"}
			cfg.DBPath = filepath.Join(t.TempDir(), "geo_keys.db")
			cfg.DisableBackupScheduler = true
			tc.set(cfg, wantPath)

			srv, err := NewServer(cfg)
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			t.Cleanup(func() {
				srv.Stop()
				time.Sleep(50 * time.Millisecond) // prevent SQLite TempDir cleanup races
			})

			// A missing file disables the feature gracefully whichever spelling named it.
			if srv.geo != nil {
				t.Errorf("geo is active despite the database being absent")
			}
			resp := locationsFor(t, srv)
			if resp.ConfiguredPath != wantPath {
				t.Errorf("%s: the panel reports configured_path=%q, want %q -- the server is "+
					"not opening the path the resolved key names", tc.name, resp.ConfiguredPath, wantPath)
			}
		})
	}
}
