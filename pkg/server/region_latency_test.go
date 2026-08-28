package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// The registration endpoint accepts region probe results from clients (#1151), which means the
// payload is attacker-controlled: it authenticates, but nothing about being a valid user makes
// the numbers honest. These cover what the server refuses to believe, and the rule that this is
// telemetry and must never cost anyone their tunnel.

func testUser(t *testing.T, srv *Server) *db.User {
	t.Helper()
	user := &db.User{ID: "probe.user@example.com", Email: "probe.user@example.com", Role: "developer", Status: "approved"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("creating the test user: %v", err)
	}
	return user
}

// TestRecordRegionProbesDropsImpossibleValues — a made-up figure that survives into the median
// is worse than a missing one, because the report is read as "somebody experienced this".
func TestRecordRegionProbesDropsImpossibleValues(t *testing.T) {
	srv := setupTestServerForAPI(t)
	user := testUser(t, srv)

	srv.recordRegionProbes(user, []RegionProbe{
		{Region: "us", RTTMs: -5},      // negative: a broken clock or a lie
		{Region: "eu", RTTMs: 999_999}, // eleven days: not a round trip
		{Region: "in", RTTMs: 42},      // plausible
		{Region: "", RTTMs: 10},        // no region to attribute it to
	})

	report, err := srv.db.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Regions) != 1 || report.Regions[0].Region != "in" {
		t.Fatalf("only the plausible sample should have been stored, got %+v", report.Regions)
	}
	if report.Regions[0].MedianMs != 42 {
		t.Errorf("expected the plausible value, got %d", report.Regions[0].MedianMs)
	}
}

// TestRecordRegionProbesCapsTheBatch — each entry is a database write on a request an
// authenticated caller can drive at will.
func TestRecordRegionProbesCapsTheBatch(t *testing.T) {
	srv := setupTestServerForAPI(t)
	user := testUser(t, srv)

	probes := make([]RegionProbe, 0, 500)
	for i := range 500 {
		probes = append(probes, RegionProbe{Region: "region-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), RTTMs: 10})
	}
	srv.recordRegionProbes(user, probes)

	report, err := srv.db.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Regions) > maxRegionProbesPerRegistration {
		t.Errorf("one registration wrote %d regions; the cap is %d", len(report.Regions), maxRegionProbesPerRegistration)
	}
}

// TestRecordRegionProbesNeverPanicsOnAbsentInput — this runs inside the handler that sets up a
// developer's tunnel. An older client sends no probes, and a client with reporting turned off
// sends none; neither may disturb the registration.
func TestRecordRegionProbesNeverPanicsOnAbsentInput(t *testing.T) {
	srv := setupTestServerForAPI(t)
	user := testUser(t, srv)

	srv.recordRegionProbes(nil, []RegionProbe{{Region: "us", RTTMs: 10}})
	srv.recordRegionProbes(user, nil)
	srv.recordRegionProbes(user, []RegionProbe{})

	report, err := srv.db.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Regions) != 0 {
		t.Errorf("nothing should have been recorded, got %d region(s)", len(report.Regions))
	}
}

// TestHandleRegionLatencyValidatesDays — `days` comes straight off the query string.
func TestHandleRegionLatencyValidatesDays(t *testing.T) {
	srv := setupTestServerForAPI(t)

	for _, bad := range []string{"0", "-1", "abc", "100000"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/analytics/region-latency?days="+bad, nil)
		srv.handleRegionLatency(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("days=%q should be rejected, got %d", bad, w.Code)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/analytics/region-latency?days=7", nil)
	srv.handleRegionLatency(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("a valid request should succeed, got %d: %s", w.Code, w.Body.String())
	}

	var report db.RegionLatencyReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("the response must be the report: %v", err)
	}
	if report.Days != 7 {
		t.Errorf("the report must state the window it used, got %d", report.Days)
	}
}

// TestHandleRegionLatencyRejectsWrites — a report is a read.
func TestHandleRegionLatencyRejectsWrites(t *testing.T) {
	srv := setupTestServerForAPI(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/admin/analytics/region-latency", strings.NewReader("{}"))
	srv.handleRegionLatency(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for a POST, got %d", w.Code)
	}
}

// TestRegionLatencyIsAdminOnly — the route must sit behind the admin check, not merely be
// undocumented. It is a fleet-wide view built from every user's measurements.
func TestRegionLatencyIsAdminOnly(t *testing.T) {
	srv := setupTestServerForAPI(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/analytics/region-latency", nil)
	srv.handleAdminEndpoints(w, req)

	if w.Code == http.StatusOK {
		t.Error("an unauthenticated caller reached the region latency report")
	}
}
