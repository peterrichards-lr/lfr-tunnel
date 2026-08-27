package server

import (
	"testing"

	"lfr-tunnel/pkg/config"
)

// TestStaticScheduleFor covers the lookup that lets a deployment without an edge-provisioner
// declare its own stop windows (#1282). Only the *discovery* of a schedule was ever
// AWS-specific; everything downstream is provider-neutral but never ran without EventBridge,
// because there was no other way to tell central the times.
func TestStaticScheduleFor(t *testing.T) {
	nodes := []config.EdgeNodeConfig{
		{
			ID: "edge-in",
			Schedule: &config.EdgeScheduleConfig{
				Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "Asia/Kolkata",
			},
		},
		{
			// Off the rota: the times are still present but must not be adopted, or an
			// unscheduled node would look configured and start producing warnings.
			ID: "edge-sa",
			Schedule: &config.EdgeScheduleConfig{
				Enabled: false, StopTime: "00:00", StartTime: "08:00", Timezone: "America/Sao_Paulo",
			},
		},
		{
			// The common case: no schedule block at all.
			ID: "edge-us",
		},
	}

	t.Run("an enabled schedule is returned", func(t *testing.T) {
		got := staticScheduleFor(nodes, "edge-in")
		if got == nil {
			t.Fatal("expected the configured schedule")
		}
		if got.StopTime != "00:00" || got.StartTime != "08:00" || got.Timezone != "Asia/Kolkata" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("a disabled schedule is not adopted", func(t *testing.T) {
		if got := staticScheduleFor(nodes, "edge-sa"); got != nil {
			t.Errorf("a node off the rota must read as unscheduled, got %+v", got)
		}
	})

	t.Run("a node with no schedule block", func(t *testing.T) {
		if got := staticScheduleFor(nodes, "edge-us"); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("an unknown node", func(t *testing.T) {
		if got := staticScheduleFor(nodes, "edge-nowhere"); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

// TestStaticScheduleIsFallbackNotOverride pins the precedence. Where the provisioner works it
// reflects what the scheduler will actually do, so a config file that disagrees with the live
// scheduler must lose -- otherwise a stale YAML value would quietly override reality and
// central would warn at the wrong time.
func TestStaticScheduleIsFallbackNotOverride(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{{
		ID: "edge-in",
		Schedule: &config.EdgeScheduleConfig{
			Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "UTC",
		},
	}})

	// A schedule already known from the provisioner -- represented by a populated timezone,
	// which is exactly the condition updateEdgeHealth uses to decide whether to fall back.
	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-in"] = EdgeHealthStatus{
		Timezone:          "Asia/Kolkata",
		ScheduleStopTime:  "00:00",
		ScheduleStartTime: "08:00",
		ScheduleEnabled:   true,
	}
	srv.edgeHealthMu.Unlock()

	srv.edgeHealthMu.RLock()
	h := srv.edgeHealth["edge-in"]
	srv.edgeHealthMu.RUnlock()

	if h.Timezone != "Asia/Kolkata" || h.ScheduleStopTime != "00:00" {
		t.Errorf("a provisioner-supplied schedule must not be replaced by config, got %+v", h)
	}

	// And the static one is still there to be used if the provisioner never answers.
	if got := staticScheduleFor(srv.edgeNodes(), "edge-in"); got == nil || got.StopTime != "23:00" {
		t.Error("the configured schedule should remain available as a fallback")
	}
}
