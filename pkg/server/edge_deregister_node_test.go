package server

import "testing"

// A deregistration must not remove a lease on a DIFFERENT node (#2161).
//
// This is the production failure of 2026-09-22, reduced. `edge-us` was stopped and restarted;
// three clients failed over and failed back. Two re-registered on `edge-us` and only then saw
// their old `edge-sa` session torn down -- and that deregistration, matching on subdomain
// alone, removed the lease they had just created. Their tunnels served 404 while the clients
// sat happily connected to `edge-us`, which still held its own lease. Central had simply
// forgotten. The third client deregistered before re-registering and was unaffected, which is
// why the failure presents as intermittent.
func TestDeregisterDoesNotRemoveALeaseOnAnotherNode(t *testing.T) {
	// The state during a failback: the new lease exists alongside the old one, briefly.
	leases := []EdgeLease{
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-us"},
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-sa"},
	}

	kept, dropped := partitionEdgeLeasesForDeregister(leases, "ngriffin", "edge-sa")

	if len(dropped) != 1 || dropped[0].NodeID != "edge-sa" {
		t.Fatalf("expected only the edge-sa lease to be dropped, got %+v", dropped)
	}
	if len(kept) != 1 || kept[0].NodeID != "edge-us" {
		t.Fatalf("the lease the client had just failed back onto was removed by the OLD "+
			"gateway's deregistration. Its tunnel now serves 404 while the client stays "+
			"connected. kept=%+v", kept)
	}
}

// Every port of a multi-port client goes together, but still only on the matching node.
//
// One of the two clients that lost its tunnel mapped three ports -- ngriffin, ngriffin-58081
// and ngriffin-8222 -- so a single deregistration took three leases with it.
func TestDeregisterRemovesEveryPortOfTheMatchingNodeOnly(t *testing.T) {
	leases := []EdgeLease{
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-sa"},
		{Subdomain: "ngriffin", FullHost: "ngriffin-58081.lfr-demo.se", NodeID: "edge-sa"},
		{Subdomain: "ngriffin", FullHost: "ngriffin-8222.lfr-demo.se", NodeID: "edge-sa"},
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-us"},
		{Subdomain: "ngriffin", FullHost: "ngriffin-58081.lfr-demo.se", NodeID: "edge-us"},
		{Subdomain: "ngriffin", FullHost: "ngriffin-8222.lfr-demo.se", NodeID: "edge-us"},
	}

	kept, dropped := partitionEdgeLeasesForDeregister(leases, "ngriffin", "edge-sa")

	if len(dropped) != 3 {
		t.Errorf("expected all three edge-sa ports dropped, got %d: %+v", len(dropped), dropped)
	}
	for _, d := range dropped {
		if d.NodeID != "edge-sa" {
			t.Errorf("dropped a lease on %s", d.NodeID)
		}
	}
	if len(kept) != 3 {
		t.Errorf("expected all three edge-us ports kept, got %d: %+v", len(kept), kept)
	}
	for _, k := range kept {
		if k.NodeID != "edge-us" {
			t.Errorf("kept a lease on %s", k.NodeID)
		}
	}
}

// A different user's or a different subdomain's leases are untouched, and the ordinary case --
// one client on one node going away -- still removes what it should.
func TestDeregisterStillRemovesTheOrdinaryCase(t *testing.T) {
	leases := []EdgeLease{
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-us"},
		{Subdomain: "gatxdemo", FullHost: "gatxdemo.lfr-demo.se", NodeID: "edge-us"},
	}

	kept, dropped := partitionEdgeLeasesForDeregister(leases, "ngriffin", "edge-us")

	if len(dropped) != 1 || dropped[0].Subdomain != "ngriffin" {
		t.Errorf("the ordinary deregistration stopped working: %+v", dropped)
	}
	if len(kept) != 1 || kept[0].Subdomain != "gatxdemo" {
		t.Errorf("another subdomain was affected: %+v", kept)
	}
}

// A node with no id matches only leases that also have none.
//
// EdgeLease.NodeID is assigned from the same node.ID at registration, so the two always agree.
// This pins that a misconfigured node cannot become a wildcard -- which is what an
// "empty means match anything" fallback would make it, reinstating the defect for every caller
// that failed to identify itself.
func TestAnEmptyNodeIsNotAWildcard(t *testing.T) {
	leases := []EdgeLease{
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: "edge-us"},
		{Subdomain: "ngriffin", FullHost: "ngriffin.lfr-demo.se", NodeID: ""},
	}

	kept, dropped := partitionEdgeLeasesForDeregister(leases, "ngriffin", "")

	if len(dropped) != 1 || dropped[0].NodeID != "" {
		t.Errorf("an unidentified caller dropped a lease belonging to a named node: %+v", dropped)
	}
	if len(kept) != 1 || kept[0].NodeID != "edge-us" {
		t.Errorf("edge-us lost its lease to a caller that did not identify itself: %+v", kept)
	}
}
