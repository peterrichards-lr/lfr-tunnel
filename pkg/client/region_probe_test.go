package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The client reports the probe results it already measured (#1151).
//
// The measurement is taken to choose a gateway and every result but the winner used to be
// thrown away. What matters here is that the report is attached to the registration, and that
// the opt-out actually stops it.

func TestRegistrationCarriesRegionProbes(t *testing.T) {
	t.Cleanup(func() { RecordRegionProbes(nil) })

	RecordRegionProbes([]RegionProbe{
		{Region: "eu", RTTMs: 12},
		{Region: "us", RTTMs: 98},
		{Region: "sa", Unreachable: true},
	})

	var got RegisterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decoding the registration payload: %v", err)
		}
		respondRegistered(t, w)
	}))
	defer server.Close()

	if _, err := RegisterTunnel(server.URL, "token", "sub", "", []PortMapping{{LocalPort: 8080}}, 0, "", nil, "linux", "", ""); err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	if len(got.RegionProbes) != 3 {
		t.Fatalf("expected all three probes to be reported, got %d: %+v", len(got.RegionProbes), got.RegionProbes)
	}

	byRegion := map[string]RegionProbe{}
	for _, p := range got.RegionProbes {
		byRegion[p.Region] = p
	}
	if byRegion["eu"].RTTMs != 12 {
		t.Errorf("the winning region's RTT must be reported, got %d", byRegion["eu"].RTTMs)
	}
	// The losers are the point: they are the evidence for whether a region needs its own edge.
	if byRegion["us"].RTTMs != 98 {
		t.Errorf("a region that was NOT chosen must still be reported, got %d", byRegion["us"].RTTMs)
	}
	// A region nobody could reach is a placement fact, not missing data, and must not read as
	// an RTT of zero.
	if !byRegion["sa"].Unreachable || byRegion["sa"].RTTMs != 0 {
		t.Errorf("an unreachable region must be marked, not reported as 0ms: %+v", byRegion["sa"])
	}
}

// TestRegistrationOmitsProbesWhenNoneRecorded — a client that never probed (one pinned with
// -server, or with reporting turned off) must send nothing at all, not an empty array.
func TestRegistrationOmitsProbesWhenNoneRecorded(t *testing.T) {
	t.Cleanup(func() { RecordRegionProbes(nil) })
	RecordRegionProbes(nil)

	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decoding the registration payload: %v", err)
		}
		respondRegistered(t, w)
	}))
	defer server.Close()

	if _, err := RegisterTunnel(server.URL, "token", "sub", "", []PortMapping{{LocalPort: 8080}}, 0, "", nil, "linux", "", ""); err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	if _, present := raw["region_probes"]; present {
		t.Error("a client with nothing to report must omit the field entirely")
	}
}

func respondRegistered(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"status":"success","session_token":"t","remotes":["R:10001:localhost:8080"]}`)); err != nil {
		t.Errorf("writing the mock response: %v", err)
	}
}
