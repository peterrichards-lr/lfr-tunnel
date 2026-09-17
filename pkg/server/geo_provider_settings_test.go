package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/geo"
)

// Choosing the geo-IP vendor from System Settings (#1995).
//
// What has to hold, and why each of these is here rather than being obvious:
//
//  1. The dropdown offers exactly geo.SelectableProviders(). An option the endpoint would
//     refuse, or a vendor the endpoint accepts that the dropdown never offers, are both
//     silent -- and #1882 is the precedent: the portal's own copy of the alert vocabulary
//     left three alerts with no control at all.
//  2. Changing it changes the credit that gets rendered, with no restart. The CONTROL is
//     that the two credits DIFFER: a select wired to nothing renders the same thing twice
//     and must fail.
//  3. With no vendor in force, no credit is rendered and no rows are served. That is the
//     licence protection from #1990, reachable at runtime instead of only at boot, and it
//     must fail loudly if it is removed.
//  4. Precedence is visible: YAML-only, portal-only and both-set-and-disagreeing each report
//     the right value AND the right source.

// withOpenGeoDatabase puts srv in the state a deployment with a readable database is in,
// without shipping one -- no vendor permits redistributing their file.
//
// stubResolver (server_geo_test.go) reports ProviderUnknown, and that matters: the property
// under test is that the rendered vendor comes from the SETTING rather than from the open
// file, so a stub returning the expected vendor would pass whether or not the setting was
// consulted at all -- an assertion satisfied by the wrong cause (github-workflow SKILL §5c).
func withOpenGeoDatabase(t *testing.T, srv *Server) {
	t.Helper()
	srv.geo = geo.New(stubResolver{}, geoStore{database: srv.db}, geo.Options{})
	if srv.geo == nil {
		t.Fatal("PREMISE: the stub aggregator is nil, so every assertion below would be " +
			"measuring the switched-off path instead")
	}
	srv.geoDiagnosis = geoDiagnosis{Path: "/srv/geo/country.mmdb"}
}

func getAdminSettings(t *testing.T, srv *Server) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), "owner@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return out
}

func postAdminSettings(t *testing.T, srv *Server, payload map[string]string) int {
	t.Helper()
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", bytes.NewReader(body)), "owner@example.com")
	return rec.Code
}

func getLocationAnalytics(t *testing.T, srv *Server) locationAnalyticsResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleGetLocationAnalytics(rec, httptest.NewRequest(http.MethodGet, "/api/admin/analytics/locations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET locations: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out locationAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode locations: %v", err)
	}
	return out
}

func TestTheDropdownOffersExactlyTheProviderVocabulary(t *testing.T) {
	srv := setupTestServerForAPI(t)

	want := geo.SelectableProviders()
	if len(want) == 0 {
		t.Fatal("PREMISE: geo.SelectableProviders() is empty, so this would compare two empty sets")
	}

	out := getAdminSettings(t, srv)
	offered, ok := out["geo_providers"].([]interface{})
	if !ok {
		t.Fatalf("geo_providers: want a list, got %v -- the portal cannot render a dropdown "+
			"it was never sent, and would have to carry its own copy of the vendor list", out["geo_providers"])
	}
	if len(offered) != len(want) {
		t.Fatalf("geo_providers: want %d entries, got %d", len(want), len(offered))
	}
	for i, p := range want {
		entry, entryOK := offered[i].(map[string]interface{})
		if !entryOK {
			t.Fatalf("geo_providers[%d] is not an object: %v", i, offered[i])
		}
		if entry["value"] != string(p) {
			t.Errorf("geo_providers[%d].value = %v, want %q -- an option the endpoint would "+
				"refuse on save is as silent as one that is never offered", i, entry["value"], p)
		}
		// Every offered vendor must be one ParseProvider accepts, or the save 400s.
		if _, err := geo.ParseProvider(string(p)); err != nil {
			t.Errorf("%q is offered by the dropdown but ParseProvider refuses it: %v", p, err)
		}
	}

	// "unknown" is a RESULT, never a choice. Offering it would let an operator declare that
	// they do not know who published their data, which is the state rows are refused in.
	for _, entry := range offered {
		if m, mOK := entry.(map[string]interface{}); mOK && m["value"] == string(geo.ProviderUnknown) {
			t.Error("the dropdown offers \"unknown\" as a vendor; it is what the gateway says " +
				"when it cannot tell, not something an operator may declare")
		}
	}
}

func TestEveryOfferedVendorHasALabelAndACredit(t *testing.T) {
	// The label and attribution keys travel as KEYS and are resolved by the portals through
	// the bundle, so nothing in the Go build fails if one has no entry -- and t() falls back
	// to the raw key, which renders as "geo_provider_dbip" to the admin. check-i18n-keys
	// cannot see them either: they reach t() as a variable, and that gate only reads literals.
	bundle, err := os.ReadFile(filepath.Join("i18n", "Language.properties"))
	if err != nil {
		t.Fatalf("read the English bundle: %v", err)
	}
	src := string(bundle)

	options := GeoProviderOptions()
	if len(options) == 0 {
		t.Fatal("PREMISE: no vendors are offered, so this would check nothing")
	}
	for _, o := range options {
		for _, key := range []string{o.LabelKey, o.AttributionKey} {
			if !strings.Contains(src, "\n"+key+"=") {
				t.Errorf("%s is offered as a vendor but %q has no entry in Language.properties; "+
					"the portal would render the raw key to the admin", o.Value, key)
			}
		}
	}
}

// CONTROL for the one above. Without it, both would pass on a bundle-reader that matched
// everything -- and the licence text is the whole point of this screen.
func TestTheBundleReaderNoticesAnAbsentKey(t *testing.T) {
	bundle, err := os.ReadFile(filepath.Join("i18n", "Language.properties"))
	if err != nil {
		t.Fatalf("read the English bundle: %v", err)
	}
	if strings.Contains(string(bundle), "\ngeo_provider_nosuchvendor=") {
		t.Fatal("CONTROL: a key that should not exist was found, so the check above proves nothing")
	}
}

func TestTheYAMLValueAppliesWhenThePortalHasSetNothing(t *testing.T) {
	srv := setupTestServerForAPI(t)
	srv.cfg.CountryDBProvider = "dbip"

	out := getAdminSettings(t, srv)
	if out["country_db_provider"] != "dbip" {
		t.Errorf("provider in force = %v, want \"dbip\"", out["country_db_provider"])
	}
	// The SOURCE, not just the value. An operator who edits server-config.yaml and sees no
	// change has no way to learn why unless the screen names which source won.
	if out["country_db_provider_source"] != string(geoProviderSourceServerConfig) {
		t.Errorf("source = %v, want %q", out["country_db_provider_source"], geoProviderSourceServerConfig)
	}
}

func TestAProviderSetInThePortalWinsOverTheYAMLValue(t *testing.T) {
	srv := setupTestServerForAPI(t)
	// The two DISAGREE on purpose: if they agreed, "the portal won" and "the YAML won" would
	// produce identical output and the assertion could not tell them apart.
	srv.cfg.CountryDBProvider = "maxmind"

	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "ip2location"}); code != http.StatusOK {
		t.Fatalf("POST provider: want 200, got %d", code)
	}

	out := getAdminSettings(t, srv)
	if out["country_db_provider"] != "ip2location" {
		t.Errorf("provider in force = %v, want \"ip2location\" -- the portal row must win", out["country_db_provider"])
	}
	if out["country_db_provider_source"] != string(geoProviderSourcePortal) {
		t.Errorf("source = %v, want %q", out["country_db_provider_source"], geoProviderSourcePortal)
	}
	// The overridden value is still shown, so the screen can say what is being overridden
	// rather than leaving the YAML edit looking like it did nothing.
	if out["country_db_provider_config"] != "maxmind" {
		t.Errorf("the server-config value is reported as %v, want \"maxmind\"", out["country_db_provider_config"])
	}
}

func TestClearingThePortalRowFallsBackToTheYAMLValue(t *testing.T) {
	srv := setupTestServerForAPI(t)
	srv.cfg.CountryDBProvider = "maxmind"

	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "dbip"}); code != http.StatusOK {
		t.Fatalf("POST provider: want 200, got %d", code)
	}
	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: ""}); code != http.StatusOK {
		t.Fatalf("POST empty provider: want 200, got %d", code)
	}

	out := getAdminSettings(t, srv)
	if out["country_db_provider"] != "maxmind" {
		t.Errorf("after clearing the portal row the YAML value must apply again; got %v", out["country_db_provider"])
	}
	if out["country_db_provider_source"] != string(geoProviderSourceServerConfig) {
		t.Errorf("source = %v, want %q", out["country_db_provider_source"], geoProviderSourceServerConfig)
	}
}

func TestWithNeitherSourceSetNoVendorIsReported(t *testing.T) {
	srv := setupTestServerForAPI(t)

	out := getAdminSettings(t, srv)
	if out["country_db_provider"] != "" {
		t.Errorf("provider in force = %v, want \"\" -- an unset vendor must not round to one", out["country_db_provider"])
	}
	if out["country_db_provider_source"] != string(geoProviderSourceUnset) {
		t.Errorf("source = %v, want %q", out["country_db_provider_source"], geoProviderSourceUnset)
	}
}

func TestAVendorThisBuildDoesNotKnowIsRefusedRatherThanStored(t *testing.T) {
	srv := setupTestServerForAPI(t)

	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "maxmnid"}); code != http.StatusBadRequest {
		t.Fatalf("a misspelled vendor: want 400, got %d", code)
	}
	stored, err := srv.db.GetAdminSetting(geoProviderSettingKey)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored != "" {
		t.Errorf("a refused vendor was stored anyway (%q); the 400 would be advisory", stored)
	}
	// CONTROL: the endpoint is not simply refusing everything, which would disable the
	// dropdown for every correctly configured deployment.
	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "maxmind"}); code != http.StatusOK {
		t.Fatalf("CONTROL: a supported vendor was refused (%d); the case above proves nothing", code)
	}
}

// THE CONTROL (#1995). Changing the vendor must change the credit that gets rendered, with no
// restart -- and the two credits must DIFFER, so a select wired to nothing fails here.
//
// Driven through the real handler against a real settings write, not through
// effectiveGeoProvider directly: the defect this guards is the wiring between the two, and a
// unit test of the resolver would pass with the handler still reading the resolver's own
// vendor. The stub resolver reports ProviderUnknown for the same reason -- if the handler read
// the file's vendor rather than the setting, both cases below would return "unknown" and the
// inequality assertion would catch it.
func TestChangingTheVendorChangesTheRenderedCreditWithNoRestart(t *testing.T) {
	srv := setupTestServerForAPI(t)
	withOpenGeoDatabase(t, srv)

	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "maxmind"}); code != http.StatusOK {
		t.Fatalf("POST maxmind: want 200, got %d", code)
	}
	first := getLocationAnalytics(t, srv)
	if !first.Available {
		t.Fatalf("a readable database with a declared vendor reported unavailable (reason %q)", first.Reason)
	}
	if first.Provider != "maxmind" {
		t.Fatalf("credit rendered for %q, want \"maxmind\"", first.Provider)
	}

	// No restart, no reopening of the database, no new aggregator -- the same *Server.
	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "dbip"}); code != http.StatusOK {
		t.Fatalf("POST dbip: want 200, got %d", code)
	}
	second := getLocationAnalytics(t, srv)
	if !second.Available {
		t.Fatalf("switching vendor switched the panel off (reason %q)", second.Reason)
	}
	if second.Provider != "dbip" {
		t.Fatalf("credit rendered for %q, want \"dbip\" -- the change did not take effect "+
			"without a restart", second.Provider)
	}

	if first.Provider == second.Provider {
		t.Fatal("CONTROL: the same credit was rendered for two different vendors, so the " +
			"dropdown is wired to nothing and the assertions above prove nothing")
	}
	// And the two credits are genuinely different licence text, not two names for one line.
	if geoAttributionKeyFor(first.Provider) == geoAttributionKeyFor(second.Provider) {
		t.Fatalf("both vendors resolve to the same attribution key (%q); one vendor's licence "+
			"would be met by the other's credit, which is not how any of them read",
			geoAttributionKeyFor(first.Provider))
	}
}

// geoAttributionKeyFor is the key the portals render for one vendor, looked up through the
// vocabulary the gateway serves rather than rebuilt here.
func geoAttributionKeyFor(provider string) string {
	for _, o := range GeoProviderOptions() {
		if o.Value == provider {
			return o.AttributionKey
		}
	}
	return ""
}

// The licence protection, at runtime rather than only at boot (#1990, #1995).
//
// With a readable database open and no vendor in force, the panel must show the "choose a
// vendor" state: no credit, and no rows. Rendering rows under no credit publishes somebody's
// data with their licence unmet; rendering a credit picked by guesswork is a false statement
// about provenance. Both are worse than an off panel.
func TestWithNoVendorInForceNoCreditAndNoRowsAreServed(t *testing.T) {
	srv := setupTestServerForAPI(t)
	withOpenGeoDatabase(t, srv)
	// Nothing in YAML and nothing in the portal.
	srv.cfg.CountryDBProvider = ""

	resp := getLocationAnalytics(t, srv)
	if resp.Available {
		t.Fatal("the panel reported itself available with no vendor named, so it would render " +
			"rows under nobody's credit")
	}
	if resp.Provider != "" {
		t.Errorf("a vendor was credited (%q) with none in force", resp.Provider)
	}
	if len(resp.Buckets) != 0 {
		t.Errorf("%d row(s) were served with no vendor in force", len(resp.Buckets))
	}
	// The reason has to say WHICH problem it is, or the operator is sent to check the path.
	if resp.Reason != geoReasonProviderNotDeclared {
		t.Errorf("reason = %q, want %q", resp.Reason, geoReasonProviderNotDeclared)
	}
	// The path is still reported, because the operator needs it to see the database IS found.
	if resp.ConfiguredPath == "" {
		t.Error("the configured path was withheld, so the panel cannot show that the file was found")
	}

	// CONTROL: the panel is not simply always off. Naming a vendor turns it on, through the
	// same handler, with no restart.
	if code := postAdminSettings(t, srv, map[string]string{geoProviderSettingKey: "dbip"}); code != http.StatusOK {
		t.Fatalf("POST dbip: want 200, got %d", code)
	}
	if on := getLocationAnalytics(t, srv); !on.Available || on.Provider != "dbip" {
		t.Fatalf("CONTROL: naming a vendor did not turn the panel on (available=%v provider=%q); "+
			"the assertions above would pass on a panel that is off for any reason", on.Available, on.Provider)
	}
}

// A vendor stored in the portal that this build does not recognise reports the typo, not a
// missing setting -- the two need different remedies, which is why #1964 kept them apart.
//
// Unreachable through the endpoint, which refuses the value: reachable by a hand-written row,
// and by a gateway downgraded to a build that has dropped a vendor.
func TestAnUnrecognisedStoredVendorIsReportedAsATypo(t *testing.T) {
	srv := setupTestServerForAPI(t)
	withOpenGeoDatabase(t, srv)
	if err := srv.db.SetAdminSetting(geoProviderSettingKey, "maxmnid"); err != nil {
		t.Fatalf("seed the row: %v", err)
	}

	resp := getLocationAnalytics(t, srv)
	if resp.Available {
		t.Fatal("an unrecognised vendor left the panel on, so it would credit nobody while showing rows")
	}
	if resp.Reason != geoReasonProviderUnknown {
		t.Fatalf("reason = %q, want %q", resp.Reason, geoReasonProviderUnknown)
	}
	// The operator has to see what was actually set or they read straight past their own typo.
	if !strings.Contains(resp.Detail, "maxmnid") {
		t.Errorf("the detail does not quote what was set: %q", resp.Detail)
	}

	// And System Settings agrees about WHOSE value it is: saying a bad portal row came from
	// server-config.yaml would send someone to edit a file that is not the problem.
	out := getAdminSettings(t, srv)
	if out["country_db_provider_source"] != string(geoProviderSourcePortal) {
		t.Errorf("source = %v, want %q", out["country_db_provider_source"], geoProviderSourcePortal)
	}
	if out["country_db_provider"] != "" {
		t.Errorf("an unusable vendor was reported as in force (%v)", out["country_db_provider"])
	}
}

// country_db_path stays YAML-only, and is shown read-only so a wrong path is diagnosable.
func TestTheDatabasePathIsReportedButNotSettable(t *testing.T) {
	srv := setupTestServerForAPI(t)
	srv.cfg.CountryDBPath = "/srv/geo/country.mmdb"

	out := getAdminSettings(t, srv)
	if out["country_db_path"] != "/srv/geo/country.mmdb" {
		t.Errorf("country_db_path = %v, want the configured path", out["country_db_path"])
	}
	// Settable would be a file-disclosure vector: an admin session could point the gateway at
	// any path it can read. The endpoint must refuse it as an unknown setting.
	if code := postAdminSettings(t, srv, map[string]string{"country_db_path": "/etc/shadow"}); code != http.StatusBadRequest {
		t.Fatalf("setting country_db_path from the portal: want 400, got %d", code)
	}
}
