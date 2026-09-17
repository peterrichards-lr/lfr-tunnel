package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// The Analytics screen had no time dimension at all: every figure was an unlabelled aggregate,
// so metrics recorded before the v1.48.35 watermark fix (#1970) -- inflated about 54x -- could
// not be told apart from corrected ones (#1981).
//
// pkg/db/analytics_period_test.go proves the window is applied to the data. These prove the
// response says which window that was, because a screen that states bounds it did not honour is
// worse than one that states none: it looks authoritative.

func analyticsFor(t *testing.T, srv *Server, query string) map[string]interface{} {
	t.Helper()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: "approved"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/analytics"+query, nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleGetAnalytics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/analytics%s = %d, want 200", query, w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding analytics response: %v", err)
	}
	return body
}

func periodOf(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	period, ok := body["period"].(map[string]interface{})
	if !ok {
		t.Fatal("the response carries no period block, so the screen has nothing to label its figures with")
	}
	return period
}

func TestAnalyticsResponseStatesItsWindow(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	period := periodOf(t, analyticsFor(t, srv, "?days=1"))

	if got := period["days"]; got != float64(1) {
		t.Errorf("period.days = %v, want 1", got)
	}
	from, _ := period["from"].(string)
	to, _ := period["to"].(string)
	if from == "" || to == "" {
		t.Fatalf("a bounded window must state both bounds, got from=%q to=%q", from, to)
	}
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		t.Fatalf("period.from %q is not RFC3339: %v", from, err)
	}
	end, err := time.Parse(time.RFC3339, to)
	if err != nil {
		t.Fatalf("period.to %q is not RFC3339: %v", to, err)
	}
	if span := end.Sub(start); span != 24*time.Hour {
		t.Errorf("days=1 spans %v, want 24h -- the floor used to round down to a date, which made this between 24 and 48", span)
	}
}

// All Time has no lower bound, and both portals branch on that being expressible. A `from` of
// "now minus nothing" would make the screen claim a window of zero length.
func TestAnalyticsAllTimeReportsAnUnboundedWindow(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	all := periodOf(t, analyticsFor(t, srv, "?days=0"))
	if got := all["from"]; got != "" {
		t.Errorf("All Time reported a lower bound of %v, want an empty one", got)
	}
	if got := all["to"]; got == "" {
		t.Error("All Time must still state what it covers up to")
	}

	month := periodOf(t, analyticsFor(t, srv, "?days=30"))
	if month["from"] == all["from"] {
		t.Fatal("All Time and Last 30 Days report the same window -- #1565's symptom, in the label this time")
	}
}

// The default has to be bounded. All-time-by-default is what let one anomalous historical
// session distort every figure on the screen permanently.
func TestAnalyticsDefaultWindowIsBounded(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	period := periodOf(t, analyticsFor(t, srv, ""))
	if got := period["days"]; got != float64(30) {
		t.Errorf("default period.days = %v, want 30", got)
	}
	if from, _ := period["from"].(string); from == "" {
		t.Fatal("the default window is unbounded")
	}
}

func TestPadNodeTotalsGivesASilentGatewayAZeroRow(t *testing.T) {
	// "us" reported nothing in the window. Without padding it produces no row and disappears
	// from the table, which reads as "not part of this deployment" rather than as the finding.
	got := padNodeTotals(
		[]db.NodeBandwidth{{NodeID: "eu", BytesIn: 10, BytesOut: 20, Sessions: 2}},
		[]string{"control", "eu", "us"},
	)

	byNode := map[string]db.NodeBandwidth{}
	for _, n := range got {
		byNode[n.NodeID] = n
	}
	for _, want := range []string{"control", "eu", "us"} {
		if _, ok := byNode[want]; !ok {
			t.Errorf("gateway %q has no row -- a missing row and a zero row mean different things", want)
		}
	}
	if byNode["us"].BytesIn+byNode["us"].BytesOut != 0 {
		t.Errorf("us should have been padded at zero, got %+v", byNode["us"])
	}
	if byNode["eu"].Sessions != 2 {
		t.Errorf("padding changed a real row: %+v", byNode["eu"])
	}
	// Busiest first, so padding cannot reorder how the table reads.
	if got[0].NodeID != "eu" {
		t.Errorf("rows are not ordered by traffic, got %q first", got[0].NodeID)
	}
}

// Padding an empty result is deliberate, and differs from padNodeDaily: there is no x-axis to
// invent points along, and "every gateway moved nothing" is a legitimate answer that an empty
// table cannot give.
func TestPadNodeTotalsFillsAnEmptyResult(t *testing.T) {
	got := padNodeTotals(nil, []string{"control", "eu"})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want one per known gateway", len(got))
	}
	for _, n := range got {
		if n.BytesIn != 0 || n.BytesOut != 0 || n.Sessions != 0 {
			t.Errorf("padded row %+v is not zero", n)
		}
	}
}

func TestPadNodeTotalsWithNoKnownGatewaysReturnsInput(t *testing.T) {
	in := []db.NodeBandwidth{{NodeID: "eu", BytesIn: 1}}
	got := padNodeTotals(in, nil)
	if len(got) != 1 || got[0].NodeID != "eu" {
		t.Fatalf("got %+v, want the input unchanged", got)
	}
}
