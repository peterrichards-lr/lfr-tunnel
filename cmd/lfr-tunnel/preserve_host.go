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
// Precedence, highest first:
//
//	-preserve-host            every port, this run
//	-preserve-host=false      no port, this run: no every-port setting AND no detection
//	preserve_host: true       every port
//	preserve_host_auto: false no detection
//	(otherwise)               the detected Liferay port, if any
//
// `preserve_host: false` in the file is deliberately NOT on that list. Every tray and Inspector
// save before #2560 wrote it whether or not anyone chose it, so it means "not set" -- switching
// detection off is preserve_host_auto's job. The flag is different: nobody types
// -preserve-host=false by accident, and "do not preserve the Host" read literally includes the
// Liferay port, so it covers both keys for the run.

// flagPreserveHost is the flag's name, shared with doctor's own flag set.
const flagPreserveHost = "preserve-host"

// preserveHostFlagGiven reports whether -preserve-host appeared on the command line in either
// form. The bool alone cannot say: false is both its default and an explicit off.
//
// A variable for the same reason as portsFlagGiven: the flag package cannot forget that a flag
// was set, so setting it for real in one test would leak into every later one.
var preserveHostFlagGiven = func() bool {
	given := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == flagPreserveHost {
			given = true
		}
	})
	return given
}

const (
	sourceFlagOn           = "-preserve-host"
	sourceFlagOff          = "-preserve-host=false"
	sourceFileOn           = "preserve_host: true in the config file"
	sourceFileAutoDisabled = "preserve_host_auto: false in the config file"
)

// applyPreserveHostFlag records an explicit -preserve-host for this run. It does not touch cfg,
// so the flag can never reach the config file through a save.
func applyPreserveHostFlag(_ *config.ClientConfig, on bool) {
	facts.preserveHostSource = sourceFlagOff
	if on {
		facts.preserveHostSource = sourceFlagOn
		// Kept from before #2560: NewInterceptorEngine still reads it.
		if err := os.Setenv("LFT_PRESERVE_HOST", "true"); err != nil {
			slog.Debug(fmt.Sprintf("[Client] Could not export LFT_PRESERVE_HOST: %v", err))
		}
	}
}

// preserveHostInputs are the settings that decide preserve_host, already reduced to what they
// mean.
type preserveHostInputs struct {
	flag        string // sourceFlagOn, sourceFlagOff, or "" when the flag was not given
	fileOn      bool   // preserve_host: true
	autoDisable bool   // preserve_host_auto: false
}

func preserveHostInputsFor(cfg *config.ClientConfig) preserveHostInputs {
	return preserveHostInputs{
		flag:        facts.preserveHostSource,
		fileOn:      cfg.PreserveHostOn(),
		autoDisable: cfg.PreserveHostAuto != nil && !*cfg.PreserveHostAuto,
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

// planPreserveHost resolves preserve_host for the port identified as Liferay (0 when none was).
func planPreserveHost(in preserveHostInputs, liferayPort int) preserveHostPlan {
	off := func(reason string) preserveHostPlan {
		if liferayPort > 0 {
			return preserveHostPlan{summary: fmt.Sprintf(
				"off (%s), although Liferay was detected on %d -- it will generate links for the local address",
				reason, liferayPort)}
		}
		return preserveHostPlan{summary: fmt.Sprintf("off (%s)", reason)}
	}
	switch {
	case in.flag == sourceFlagOn:
		return preserveHostPlan{all: true, summary: "on for every port (" + sourceFlagOn + ")"}
	case in.flag == sourceFlagOff:
		return off(sourceFlagOff)
	case in.fileOn:
		return preserveHostPlan{all: true, summary: "on for every port (" + sourceFileOn + ")"}
	case in.autoDisable:
		return off(sourceFileAutoDisabled)
	case liferayPort > 0:
		return preserveHostPlan{ports: []int{liferayPort},
			summary: fmt.Sprintf("on for %d (Liferay detected)", liferayPort)}
	default:
		return preserveHostPlan{summary: "off (default; no Liferay port detected)"}
	}
}

func currentPreserveHostPlan(cfg *config.ClientConfig) preserveHostPlan {
	return planPreserveHost(preserveHostInputsFor(cfg), facts.liferayPort)
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
