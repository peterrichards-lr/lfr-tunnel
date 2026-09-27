package main

import (
	"testing"

	"lfr-tunnel/pkg/client"
)

// The property, not the instance (github-workflow SKILL 5b).
//
// #2270 was not a bug in a loop. Both slices were populated correctly and the wrong one reached
// StartHealthChecks: portMappings, whose LocalPort main rewrites to the interceptor's dynamic
// port, instead of regPortMappings, the un-mutated copy. So the thing worth asserting is that
// the ports chosen are the ones a connection to the TARGET would use, and that they are not the
// intercept ports -- for any mapping, not for one example.

// rewriteLikeMain reproduces what main does to portMappings after registration: LocalPort
// becomes the interceptor's dynamic port. Written here so the test states the transformation it
// is guarding against instead of assuming it.
func rewriteLikeMain(mappings []client.PortMapping, interceptBase int) []client.PortMapping {
	out := make([]client.PortMapping, len(mappings))
	copy(out, mappings)
	for i := range out {
		out[i].LocalPort = interceptBase + i
	}
	return out
}

func TestHealthCheckPortsAreTheTargetsNotTheInterceptors(t *testing.T) {
	target := []client.PortMapping{
		{LocalPort: 80},
		{LocalPort: 8080, NameSuffix: "api"},
		{LocalPort: 3306, NameSuffix: "db"},
	}
	intercepted := rewriteLikeMain(target, 35000)

	got := healthCheckPortsFor(target)

	if len(got) != len(target) {
		t.Fatalf("got %d ports for %d mappings", len(got), len(target))
	}
	for i, pm := range target {
		if got[i] != pm.LocalPort {
			t.Errorf("mapping %d: health check would dial port %d, want the target's %d", i, got[i], pm.LocalPort)
		}
	}

	// And the negative half, which is the one that actually fails on the defect: not one of
	// the returned ports may be an intercept port. Without this, returning the rewritten
	// slice would satisfy every assertion above on a fixture where the two happened to match.
	interceptPorts := map[int]bool{}
	for _, pm := range intercepted {
		interceptPorts[pm.LocalPort] = true
	}
	for _, p := range got {
		if interceptPorts[p] {
			t.Errorf("health check would dial %d, which is an interceptor's local port -- paired "+
				"with TargetHost that addresses a socket open on neither host", p)
		}
	}
}

// CONTROL / anti-vacuity. If the rewrite produced the same numbers, the negative assertion above
// would be unfalsifiable and the test would pass on the defect.
func TestTheRewriteActuallyChangesThePorts(t *testing.T) {
	target := []client.PortMapping{{LocalPort: 80}}
	intercepted := rewriteLikeMain(target, 35000)

	if intercepted[0].LocalPort == target[0].LocalPort {
		t.Fatal("the fixture's rewrite is a no-op, so the negative assertion proves nothing")
	}
	// And the defect itself, stated as a test: passing the rewritten slice must produce ports
	// that are NOT the target's. This is what main used to do.
	wrong := healthCheckPortsFor(intercepted)
	if wrong[0] == target[0].LocalPort {
		t.Fatal("the rewritten slice yielded the target's port, so this test cannot tell the two apart")
	}
}

// An empty mapping list yields an empty slice, not nil-with-a-surprise. localTargetStatus loops
// over what it is given; "no ports" must mean "nothing to check", not a panic.
func TestNoMappingsYieldsNoPorts(t *testing.T) {
	if got := healthCheckPortsFor(nil); len(got) != 0 {
		t.Errorf("got %v for no mappings", got)
	}
	if got := healthCheckPortsFor([]client.PortMapping{}); got == nil {
		t.Error("an empty mapping list produced a nil slice rather than an empty one")
	}
}
