package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Identifying Liferay by what it says it is (#2571).
//
// #2560 turns preserve_host on for the Liferay port, but the client used to know that port only
// by guessing: a workspace folder (any Gradle project qualified) or a container name or image.
// A standalone Tomcat bundle, and any port named with -ports, was never identified, so it got
// localhost links -- the SE guide's own standalone-bundle case.
//
// Liferay names itself. MainServlet adds a Liferay-Portal response header to every request it
// serves, unless http.header.version.verbosity is "off" (liferay-portal:
// portal-impl/src/com/liferay/portal/internal/servlet/MainServlet.java). So one GET to a local
// port, with the LOCAL Host, answers the question directly. The local Host matters: a public
// Host Liferay has not accepted draws a 500 from ValidHostNameFilter, which carries no header.

// liferayPortalHeader is the response header Liferay's MainServlet sets.
const liferayPortalHeader = "Liferay-Portal"

// LiferayIdentity is the verdict of one probe.
type LiferayIdentity int

const (
	// LiferayUnknown: nothing answered, or what answered may still be Liferay booting. Ask again
	// later, and meanwhile fall back to the heuristics.
	LiferayUnknown LiferayIdentity = iota
	// LiferayIdentified: the port answered with the Liferay-Portal header.
	LiferayIdentified
	// LiferayNotIdentified: the port answered as a working server without the header. Either not
	// Liferay, or Liferay with the header switched off -- which is why a strong heuristic (the
	// container image) still stands against this verdict, and a weak one does not.
	LiferayNotIdentified
)

// liferayProbeTimeout bounds one probe. Liferay renders its home page for an anonymous GET, which
// is not instant on a cold portal, so this is generous; it never delays visitor traffic, because
// probes run off the request path.
const liferayProbeTimeout = 10 * time.Second

// ProbeLiferay asks the server on host:port what it is, with one GET to "/" carrying the local
// Host. Redirects are not followed: Liferay sets the header on the redirect response too.
// Returns the verdict and, when identified, the header's value (the product name, or more).
func ProbeLiferay(ctx context.Context, rt http.RoundTripper, host string, port int) (LiferayIdentity, string) {
	if rt == nil {
		rt = http.DefaultTransport
	}
	hostHeader := getHostHeaderValue(host, port)
	ctx, cancel := context.WithTimeout(ctx, liferayProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostHeader+"/", nil)
	if err != nil {
		return LiferayUnknown, ""
	}
	req.Host = hostHeader
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return LiferayUnknown, ""
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Draining a Liferay probe response: %v", err))
	}
	if err := resp.Body.Close(); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Closing a Liferay probe response: %v", err))
	}
	if v := strings.TrimSpace(resp.Header.Get(liferayPortalHeader)); v != "" {
		return LiferayIdentified, v
	}
	if stillStarting(resp.StatusCode) {
		return LiferayUnknown, ""
	}
	return LiferayNotIdentified, ""
}

// stillStarting reports whether a status without the header can still be Liferay on its way up.
// Tomcat answers before the portal is deployed, and those answers are 404 (nothing at "/" yet)
// or 5xx; a server answering anything else is up, and is not Liferay with the header on.
func stillStarting(status int) bool {
	return status == http.StatusNotFound || status >= http.StatusInternalServerError
}

// LiferayWatch keeps asking ports what they are until each one answers or the watch gives up.
// It exists because a tunnel is usually started beside a Liferay that is still booting -- which
// takes minutes -- so a single probe at startup would miss the very case it is for.
type LiferayWatch struct {
	// Host is the local target host the ports are reached on.
	Host string
	// Ports are the candidate local ports.
	Ports []int
	// Interval is the pause between rounds; Deadline is how long the watch runs in total.
	Interval, Deadline time.Duration
	// Transport, when set, replaces http.DefaultTransport (tests).
	Transport http.RoundTripper
	// OnIdentified is called once per port the header identifies.
	OnIdentified func(port int, header string)
	// OnNotIdentified is called once per port that answered without it.
	OnNotIdentified func(port int)
}

// Run probes until every port is decided, the deadline passes, or ctx ends. It returns the ports
// still undecided. Each callback runs at most once per port.
func (w LiferayWatch) Run(ctx context.Context) []int {
	pending := map[int]bool{}
	for _, p := range w.Ports {
		if p > 0 {
			pending[p] = true
		}
	}
	deadline := time.Now().Add(w.Deadline)
	for len(pending) > 0 {
		for _, p := range sortedPorts(pending) {
			switch verdict, header := ProbeLiferay(ctx, w.Transport, w.Host, p); verdict {
			case LiferayIdentified:
				delete(pending, p)
				if w.OnIdentified != nil {
					w.OnIdentified(p, header)
				}
			case LiferayNotIdentified:
				delete(pending, p)
				if w.OnNotIdentified != nil {
					w.OnNotIdentified(p)
				}
			}
		}
		if len(pending) == 0 || !time.Now().Add(w.Interval).Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return sortedPorts(pending)
		case <-time.After(w.Interval):
		}
	}
	return sortedPorts(pending)
}

func sortedPorts(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// MarkLiferayPort records a port identified as Liferay after startup, and turns preserve_host on
// for it when preserve is true (#2571). The proxy reads PreservesHostFor on every request, so a
// port identified mid-run takes effect from the next request; nothing is reconnected.
func (e *InterceptorEngine) MarkLiferayPort(port int, preserve bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.liferayPorts == nil {
		e.liferayPorts = map[int]bool{}
	}
	e.liferayPorts[port] = true
	if preserve {
		if e.preserveHostPorts == nil {
			e.preserveHostPorts = map[int]bool{}
		}
		e.preserveHostPorts[port] = true
	}
}

// UnmarkLiferayPort withdraws a port a weak heuristic identified, once the port itself answered
// without the header (#2571): automatic preserve_host is turned off for it too. An explicit
// every-port preserve_host is unaffected.
func (e *InterceptorEngine) UnmarkLiferayPort(port int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.liferayPorts, port)
	delete(e.preserveHostPorts, port)
}
