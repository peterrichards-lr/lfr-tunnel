package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestProbeFastestRegionReportsUnreachable is half the regression test for #1148. The
// probe used to discard non-responding regions silently, so the caller could not tell an
// election made from the full set apart from one made while an edge was powered down.
func TestProbeFastestRegionReportsUnreachable(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	// A listener that is bound and immediately closed gives an address nothing answers on.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	downURL := down.URL
	down.Close()

	best, unreachable := probeFastestRegion(map[string]string{"eu": up.URL, "us": downURL})

	if best != "eu" {
		t.Errorf("expected the reachable region to be elected, got %q", best)
	}
	if len(unreachable) != 1 || unreachable[0] != "us" {
		t.Errorf("expected 'us' reported unreachable, got %v -- without this the caller cannot know the election was partial", unreachable)
	}
}

// TestRegionCacheProvisionalExpiresSooner is the other half. A user whose nearest edge is
// inside its power-off window elects a distant region; pinning that for a day means they
// stay there all the next working day. Marking it provisional re-probes far sooner.
//
// Ages are expressed relative to the constants rather than in absolute hours (#1706). The
// "survives past the provisional window" case was written as a literal 1h30m, which asserted
// the old 24h TTL by accident and failed the moment that value was reconsidered -- the
// property being tested is that a complete election outlives a provisional one, not what
// either duration happens to be this month.
func TestRegionCacheProvisionalExpiresSooner(t *testing.T) {
	if provisionalRegionCacheTTL >= regionCacheTTL {
		t.Fatalf("a provisional election must expire sooner than a complete one (%s vs %s)", provisionalRegionCacheTTL, regionCacheTTL)
	}

	cases := []struct {
		name        string
		provisional bool
		age         time.Duration
		wantValid   bool
	}{
		{"complete election survives past the provisional window", false, provisionalRegionCacheTTL + (regionCacheTTL-provisionalRegionCacheTTL)/2, true},
		{"complete election expires after its own TTL", false, regionCacheTTL + time.Minute, false},
		{"provisional election is still valid while fresh", true, time.Minute, true},
		{"provisional election expires quickly", true, provisionalRegionCacheTTL + time.Minute, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := RegionCacheData{
				BestRegion:  "central",
				ServerURL:   "https://tunnel.example",
				Timestamp:   time.Now().Add(-tc.age),
				Provisional: tc.provisional,
			}

			ttl := regionCacheTTL
			if cache.Provisional {
				ttl = provisionalRegionCacheTTL
			}
			valid := time.Since(cache.Timestamp) <= ttl

			if valid != tc.wantValid {
				t.Errorf("cache valid = %v, want %v (age %s, provisional %v)", valid, tc.wantValid, tc.age, tc.provisional)
			}
		})
	}
}
