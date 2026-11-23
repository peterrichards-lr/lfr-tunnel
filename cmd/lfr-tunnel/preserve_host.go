package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// preserve_host for a detected Liferay port (#2560).
//
// Liferay builds every absolute link from the Host it is asked for. With the default rewrite it
// is asked for the local target, so a tunnelled portal links to http://<target>:8080 and an SE's
// first demo looks broken. The client already identifies Liferay in two places, so when it has
// and nobody said otherwise, preserve_host goes on -- for the Liferay port only. A workspace
// also publishes client-extension dev servers, which can refuse a Host they do not know, and a
// non-Liferay target keeps the rewrite it has always had (-target-host behind an nginx virtual
// host depends on it).
//
// Precedence, highest first: -preserve-host / -preserve-host=false, then preserve_host: in the
// config file, then detection, then off.

// preserveHostFlagGiven reports whether -preserve-host appeared on the command line in either
// form. The bool alone cannot say: false is both its default and an explicit off.
//
// A variable for the same reason as portsFlagGiven: the flag package cannot forget that a flag
// was set, so setting it for real in one test would leak into every later one.
// flagPreserveHost is the flag's name, shared with doctor's own flag set.
const flagPreserveHost = "preserve-host"

var preserveHostFlagGiven = func() bool {
	given := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == flagPreserveHost {
			given = true
		}
	})
	return given
}

// applyPreserveHostFlag records an explicit -preserve-host on cfg, and which form it took.
func applyPreserveHostFlag(cfg *config.ClientConfig, on bool) {
	cfg.PreserveHost = &on
	facts.preserveHostSource = "-preserve-host=false"
	if on {
		facts.preserveHostSource = "-preserve-host"
		// Kept from before #2560: NewInterceptorEngine still reads it.
		if err := os.Setenv("LFT_PRESERVE_HOST", "true"); err != nil {
			slog.Debug(fmt.Sprintf("[Client] Could not export LFT_PRESERVE_HOST: %v", err))
		}
	}
}

// preserveHostPlan is what preserve_host resolves to for one run.
type preserveHostPlan struct {
	// all is the explicit every-port setting.
	all bool
	// ports are the ports it is on for automatically: the Liferay port, or none.
	ports []int
	// summary is the startup-summary text, saying where the value came from.
	summary string
}

// planPreserveHost resolves preserve_host from the explicit setting (nil when nobody set one),
// where that setting came from, and the port identified as Liferay (0 when none was).
func planPreserveHost(explicit *bool, source string, liferayPort int) preserveHostPlan {
	switch {
	case explicit != nil && *explicit:
		return preserveHostPlan{all: true, summary: fmt.Sprintf("on for every port (%s)", source)}
	case explicit != nil && liferayPort > 0:
		// Said out loud: a config file saved by an older tray or Inspector carries
		// preserve_host: false without anyone having chosen it, and this is where it shows.
		return preserveHostPlan{summary: fmt.Sprintf(
			"off (%s), although Liferay was detected on %d -- it will generate links for the local address",
			source, liferayPort)}
	case explicit != nil:
		return preserveHostPlan{summary: fmt.Sprintf("off (%s)", source)}
	case liferayPort > 0:
		return preserveHostPlan{ports: []int{liferayPort},
			summary: fmt.Sprintf("on for %d (Liferay detected)", liferayPort)}
	default:
		return preserveHostPlan{summary: "off (default; no Liferay port detected)"}
	}
}

// preserveHostSource names where cfg's explicit preserve_host came from.
func preserveHostSource(cfg *config.ClientConfig) string {
	if facts.preserveHostSource != "" {
		return facts.preserveHostSource
	}
	if cfg.PreserveHost != nil {
		return fmt.Sprintf("preserve_host: %t in the config file", *cfg.PreserveHost)
	}
	return ""
}

func currentPreserveHostPlan(cfg *config.ClientConfig) preserveHostPlan {
	return planPreserveHost(cfg.PreserveHost, preserveHostSource(cfg), facts.liferayPort)
}

// preserveHostSummary is the startup-summary line's value.
func preserveHostSummary(cfg *config.ClientConfig) string {
	return currentPreserveHostPlan(cfg).summary
}

// applyPreserveHost hands the resolved setting to the engine.
func applyPreserveHost(engine *client.InterceptorEngine, cfg *config.ClientConfig) {
	plan := currentPreserveHostPlan(cfg)
	engine.PreserveHost = plan.all
	engine.SetPreserveHostPorts(plan.ports)
	if facts.liferayPort > 0 {
		engine.SetLiferayPorts([]int{facts.liferayPort})
	}
}
