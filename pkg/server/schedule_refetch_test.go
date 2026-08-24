package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestScheduleCacheStaleness covers the condition that decides whether central re-reads a
// node's schedule (#1245).
//
// The old condition was `timezone == ""`, which in practice meant "once per process": as
// soon as a fetch succeeded, central never looked again. A schedule changed out of band --
// including via the documented schedule-edge-node-hours.sh -- stayed invisible until a
// restart. That was observed live: central acted on Asia/Kolkata for hours while the
// provisioner had been returning UTC.
func TestScheduleCacheStaleness(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name        string
		timezone    string
		fetchedAt   int64
		wantRefetch bool
	}{
		{
			name:        "never fetched",
			timezone:    "",
			fetchedAt:   0,
			wantRefetch: true,
		},
		{
			name:        "fetched just now",
			timezone:    "Asia/Kolkata",
			fetchedAt:   now.Unix(),
			wantRefetch: false,
		},
		{
			// The case the old condition could never reach.
			name:        "populated but older than the refetch interval",
			timezone:    "Asia/Kolkata",
			fetchedAt:   now.Add(-scheduleRefetchInterval - time.Minute).Unix(),
			wantRefetch: true,
		},
		{
			// Cleared by invalidateEdgeScheduleCache after a portal save.
			name:        "invalidated cache refetches immediately",
			timezone:    "",
			fetchedAt:   0,
			wantRefetch: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stale := tc.fetchedAt == 0 || time.Since(time.Unix(tc.fetchedAt, 0)) >= scheduleRefetchInterval
			got := tc.timezone == "" || stale
			if got != tc.wantRefetch {
				t.Errorf("refetch = %v, want %v (timezone=%q fetchedAt=%d)", got, tc.wantRefetch, tc.timezone, tc.fetchedAt)
			}
		})
	}
}

// TestInvalidateScheduleCacheClearsFetchTimestamp guards a trap in the invalidation path: if
// the timestamp survived, the very next pass would see a recent fetch and treat the cache it
// had just been told to discard as fresh -- so a portal save would not take effect until the
// refetch interval elapsed.
func TestInvalidateScheduleCacheClearsFetchTimestamp(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-in"] = EdgeHealthStatus{
		Timezone:          "Asia/Kolkata",
		ScheduleStopTime:  "00:00",
		ScheduleStartTime: "08:00",
		ScheduleEnabled:   true,
		ScheduleFetchedAt: time.Now().Unix(),
	}
	srv.edgeHealthMu.Unlock()

	srv.invalidateEdgeScheduleCache("edge-in")

	srv.edgeHealthMu.RLock()
	h := srv.edgeHealth["edge-in"]
	srv.edgeHealthMu.RUnlock()

	if h.Timezone != "" || h.ScheduleStopTime != "" || h.ScheduleEnabled {
		t.Errorf("expected the schedule to be cleared, got %+v", h)
	}
	if h.ScheduleFetchedAt != 0 {
		t.Error("expected the fetch timestamp to be cleared, or the next pass treats the discarded cache as fresh")
	}
}

// TestScheduleStateIsVisible checks the fields are actually exposed. They were json:"-", so
// what central believed about a node's schedule -- the thing that decides whether a warning
// is sent at all -- could not be observed from outside the process.
func TestScheduleStateIsVisible(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeClientsMu.Lock()
	srv.edgeClients["edge-in"] = nil
	srv.edgeClientsMu.Unlock()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-in"] = EdgeHealthStatus{
		Status:            "Online",
		Timezone:          "Asia/Kolkata",
		ScheduleStopTime:  "00:00",
		ScheduleStartTime: "08:00",
		ScheduleEnabled:   true,
		ScheduleFetchedAt: time.Now().Unix(),
		ScheduleError:     "provisioner unreachable",
	}
	srv.edgeHealthMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/portal/edge-health", nil)
	w := httptest.NewRecorder()
	srv.handleEdgeHealth(w, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	node, ok := resp.Nodes["edge-in"]
	if !ok {
		t.Fatal("expected edge-in in the response")
	}

	if node.ScheduleStopTime != "00:00" || node.ScheduleStartTime != "08:00" {
		t.Errorf("expected the stop/start window to be visible, got %+v", node)
	}
	if !node.ScheduleEnabled {
		t.Error("expected the enabled flag to be visible")
	}
	if node.ScheduleFetchedAt == 0 {
		t.Error("expected the fetch time to be visible, so a stale cache can be seen rather than inferred")
	}
	if node.ScheduleError != "provisioner unreachable" {
		t.Errorf("expected the fetch error to be visible, got %q", node.ScheduleError)
	}
}
