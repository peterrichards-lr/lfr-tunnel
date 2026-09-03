package main

import (
	"reflect"
	"testing"

	"lfr-tunnel/pkg/config"
)

// An election made while a known edge is asleep must be cached provisionally (#1690).
//
// This is the whole bug. The 30-minute provisional TTL exists precisely so a client that
// elected during an edge's power-off window re-probes once the edge returns, but it keyed off
// regions that were advertised and silent -- and a sleeping edge was not advertised at all.
// The election therefore looked complete, took the 24h TTL, and stranded the client on a
// distant gateway for the rest of the day.
func TestResolveServerURL_SleepingEdgeMakesElectionProvisional(t *testing.T) {
	euSrv := newHealthyEdge(t)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(*config.ClientConfig) {}

	var gotProvisional bool
	var saved bool
	saveRegionCacheFn = func(_, _ string, provisional bool, _ []string) {
		saved, gotProvisional = true, provisional
	}

	cfg := &config.ClientConfig{
		ServerURL: "https://tunnel.invalid",
		Regions:   map[string]string{"eu": euSrv.URL},
		// edge-us is configured but currently down, as the gateway now reports.
		RegionsUnavailable: map[string]string{
			"us":      "https://us.lfr-demo.se",
			"edge-us": "https://us.lfr-demo.se",
		},
	}

	resolveServerURL(cfg, false)

	if !saved {
		t.Fatalf("no election was cached at all; cfg.Region = %q", cfg.Region)
	}
	if !gotProvisional {
		t.Error("election made while edge-us was asleep was cached as COMPLETE (24h) -- " +
			"the client will stay on the fallback all day after edge-us returns")
	}
}

// The counterpart: with nothing missing, the election is complete and earns the 24h TTL.
// Without this, marking everything provisional would 'fix' the bug by making every client
// re-probe every 30 minutes forever.
func TestResolveServerURL_CompleteElectionIsNotProvisional(t *testing.T) {
	euSrv := newHealthyEdge(t)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(*config.ClientConfig) {}

	var gotProvisional, saved bool
	saveRegionCacheFn = func(_, _ string, provisional bool, _ []string) {
		saved, gotProvisional = true, provisional
	}

	cfg := &config.ClientConfig{
		ServerURL: "https://tunnel.invalid",
		Regions:   map[string]string{"eu": euSrv.URL},
	}

	resolveServerURL(cfg, false)

	if !saved {
		t.Fatalf("no election was cached at all; cfg.Region = %q", cfg.Region)
	}
	if gotProvisional {
		t.Error("every region answered, so the election is complete and should get the 24h TTL")
	}
}

func TestMissingRegionNames(t *testing.T) {
	tests := []struct {
		name        string
		available   map[string]string
		unavailable map[string]string
		want        []string
	}{
		{
			name:        "nothing unavailable",
			available:   map[string]string{"eu": "https://tunnel.lfr-demo.se"},
			unavailable: nil,
			want:        nil,
		},
		{
			// One sleeping edge is advertised under two names. Counting names rather than
			// hosts would report a single node as two missing regions.
			name:      "aliases of one edge collapse to one name",
			available: map[string]string{"eu": "https://tunnel.lfr-demo.se"},
			unavailable: map[string]string{
				"us":      "https://us.lfr-demo.se",
				"edge-us": "https://us.lfr-demo.se",
			},
			want: []string{"us"},
		},
		{
			// A name can appear in both sets when a gateway serves several regions. It is
			// reachable, so it is not missing -- otherwise a healthy deployment would
			// re-probe every 30 minutes forever.
			name: "a host that is also up is not missing",
			available: map[string]string{
				"eu":      "https://tunnel.lfr-demo.se",
				"central": "https://tunnel.lfr-demo.se",
			},
			unavailable: map[string]string{"eu": "https://tunnel.lfr-demo.se"},
			want:        nil,
		},
		{
			name:      "two separate edges down",
			available: map[string]string{"eu": "https://tunnel.lfr-demo.se"},
			unavailable: map[string]string{
				"us":   "https://us.lfr-demo.se",
				"apac": "https://apac.lfr-demo.se",
			},
			want: []string{"apac", "us"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := missingRegionNames(tc.available, tc.unavailable)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("missingRegionNames() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
