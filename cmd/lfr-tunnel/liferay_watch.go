package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// Identifying the Liferay port by its Liferay-Portal response header (#2571), for every way the
// ports were chosen -- including a native bundle found by the port probe and ports named with
// -ports, which no heuristic identifies.

// liferayWatchInterval and liferayWatchDeadline pace the background watch. Variables so tests
// run in milliseconds. Fifteen minutes covers a cold Liferay start with room to spare.
var (
	liferayWatchInterval = 5 * time.Second
	liferayWatchDeadline = 15 * time.Minute
)

// liferayWatchCandidates are the local ports to ask. None when a strong heuristic (the container
// image or LDM's label) already identified the portal: asking adds nothing, and a Liferay with
// the header switched off would otherwise be withdrawn.
func liferayWatchCandidates(mappings []client.PortMapping) []int {
	if facts.liferayPort > 0 && !facts.liferayWeak {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, m := range mappings {
		if m.LocalPort > 0 && !seen[m.LocalPort] {
			seen[m.LocalPort] = true
			out = append(out, m.LocalPort)
		}
	}
	return out
}

// autoPreserveAllowed reports whether automatic preserve_host may act: nothing explicit was set
// (#2560 precedence -- an explicit on already covers every port, and an explicit off wins).
func autoPreserveAllowed(cfg *config.ClientConfig) bool {
	in := preserveHostInputsFor(cfg)
	return in.flag == "" && !in.fileOn && !in.autoDisable
}

// newLiferayWatch builds the watch for this run. Split from startLiferayWatch so tests can run it
// synchronously against httptest servers.
func newLiferayWatch(engine *client.InterceptorEngine, cfg *config.ClientConfig) (client.LiferayWatch, bool) {
	candidates := facts.liferayCandidates
	if len(candidates) == 0 {
		return client.LiferayWatch{}, false
	}
	allowed := autoPreserveAllowed(cfg)
	weak, weakPort := facts.liferayWeak, facts.liferayPort
	return client.LiferayWatch{
		Host:     engine.TargetHost,
		Ports:    candidates,
		Interval: liferayWatchInterval,
		Deadline: liferayWatchDeadline,
		OnIdentified: func(port int, header string) {
			if weak && port == weakPort {
				slog.Debug(fmt.Sprintf("[Client] Port %d confirmed as Liferay (%s: %s)", port, "Liferay-Portal", header))
				return
			}
			engine.MarkLiferayPort(port, allowed)
			if allowed {
				slog.Info(fmt.Sprintf("[Client] Liferay identified on port %d (Liferay-Portal: %s): preserve_host is now on for it, so Liferay generates links for the public hostname.", port, header))
			} else {
				slog.Info(fmt.Sprintf("[Client] Liferay identified on port %d (Liferay-Portal: %s); preserve_host stays as configured.", port, header))
			}
		},
		OnNotIdentified: func(port int) {
			if weak && port == weakPort {
				engine.UnmarkLiferayPort(port)
				slog.Info(fmt.Sprintf("[Client] Port %d answered without Liferay's Liferay-Portal header, so it is not treated as Liferay: automatic preserve_host is off for it. If it is Liferay with http.header.version.verbosity=off, pass -preserve-host.", port))
			}
		},
	}, true
}

// startLiferayWatch runs the watch in the background. It never touches visitor traffic: it only
// sends its own GETs, and the proxy picks up a change on its next request.
func startLiferayWatch(ctx context.Context, engine *client.InterceptorEngine, cfg *config.ClientConfig) {
	w, ok := newLiferayWatch(engine, cfg)
	if !ok {
		return
	}
	go func() {
		if left := w.Run(ctx); len(left) > 0 {
			slog.Debug(fmt.Sprintf("[Client] Stopped watching %v for Liferay's header: no definite answer within %s", left, liferayWatchDeadline))
		}
	}()
}

// liferayWatchSummary rewrites the startup line's preserve_host value while the watch decides,
// so it says what will happen rather than a final "off" that is about to change.
func liferayWatchSummary(cfg *config.ClientConfig, planned string) string {
	if !autoPreserveAllowed(cfg) || len(facts.liferayCandidates) == 0 {
		return planned
	}
	if facts.liferayWeak && facts.liferayPort > 0 {
		return fmt.Sprintf("on for %d (Liferay workspace; confirmed or withdrawn by the port's Liferay-Portal header)", facts.liferayPort)
	}
	if facts.liferayPort == 0 {
		return fmt.Sprintf("off until Liferay is identified: watching %s for its Liferay-Portal header", joinInts(facts.liferayCandidates))
	}
	return planned
}

func joinInts(ports []int) string {
	s := ""
	for i, p := range ports {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprint(p)
	}
	return s
}
