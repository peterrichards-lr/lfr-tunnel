package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Liferay's "Invalid host name" 500 (#2561).
//
// Until the public hostname is a Virtual Host, or matches virtual.hosts.valid.hosts, Liferay's
// ValidHostNameFilter throws `Invalid host name <host>` and the request is answered by
// errors/code.jsp -- the <error-page> for EVERY uncaught exception. So the body identifies
// nothing, and matching on it would be a guess (measured on liferay/dxp:2026.q1.14-lts; see the
// liferay-target-testing skill). What does identify it is a differential probe: repeat the same
// GET to the same local port with Host set to the local target. If that is not a 500, the only
// thing that changed was the hostname, and the hostname is what is being refused.

// HostAcceptance is the outcome of a host-acceptance probe.
type HostAcceptance int

const (
	// HostAcceptanceUnknown: the probe could not decide -- a request failed, or the local
	// Host also got a 500, so the 500 is not about the hostname. Never reported as either
	// of the other two.
	HostAcceptanceUnknown HostAcceptance = iota
	// HostAccepted: the public hostname did not get a 500.
	HostAccepted
	// HostRejected: the public hostname got a 500 and the local one did not.
	HostRejected
)

// HostProbeResult is what a probe found, with the statuses it was decided from (0 = no answer).
type HostProbeResult struct {
	Verdict      HostAcceptance
	PublicStatus int
	LocalStatus  int
	// Detail says why the verdict is Unknown; empty otherwise.
	Detail string
}

// hostProbeTimeout bounds each probe request. Liferay's first request after start can be slow,
// but a probe that hangs for longer than this is not going to answer the question.
const hostProbeTimeout = 15 * time.Second

// probeStatus sends one GET to rawURL with the given Host and returns the status code. The body
// is drained and closed; only the status matters, and the body is generic anyway.
func probeStatus(ctx context.Context, rt http.RoundTripper, rawURL, host string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, hostProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Host = host
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Draining a host probe response: %v", err))
	}
	if err := resp.Body.Close(); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Closing a host probe response: %v", err))
	}
	return resp.StatusCode, nil
}

// confirmHostRejection is the second half of the probe, for a request that already got a 500
// with the public Host: the same URL again with Host set to the local target.
func confirmHostRejection(ctx context.Context, rt http.RoundTripper, rawURL, localHost string) HostProbeResult {
	result := HostProbeResult{PublicStatus: http.StatusInternalServerError}
	status, err := probeStatus(ctx, rt, rawURL, localHost)
	result.LocalStatus = status
	switch {
	case err != nil:
		result.Detail = fmt.Sprintf("the same request with Host %s failed (%v), so the 500 could not be attributed", localHost, err)
	case status == http.StatusInternalServerError:
		result.Detail = fmt.Sprintf("the same request with Host %s also got a 500, so the application is failing for another reason, not refusing the hostname", localHost)
	default:
		result.Verdict = HostRejected
	}
	return result
}

// ProbeHostAcceptance asks the local target at baseURL (scheme://host:port) whether it accepts
// publicHost, by sending GET baseURL+path with Host: publicHost and, on a 500, again with
// Host: localHost.
func ProbeHostAcceptance(ctx context.Context, rt http.RoundTripper, baseURL, path, publicHost, localHost string) HostProbeResult {
	if path == "" {
		path = "/"
	}
	rawURL := strings.TrimRight(baseURL, "/") + path
	status, err := probeStatus(ctx, rt, rawURL, publicHost)
	if err != nil {
		return HostProbeResult{Detail: fmt.Sprintf("%s did not answer (%v)", baseURL, err)}
	}
	if status != http.StatusInternalServerError {
		return HostProbeResult{Verdict: HostAccepted, PublicStatus: status}
	}
	return confirmHostRejection(ctx, rt, rawURL, localHost)
}

// LiferayHostHint is the fix for a hostname Liferay refuses, filled in with that hostname.
func LiferayHostHint(publicHost string) string {
	host := hostWithoutPort(publicHost)
	valid := host
	if wildcard := wildcardFor(host); wildcard != "" {
		valid = fmt.Sprintf("%s (or %s, which covers every subdomain and survives a subdomain change)", host, wildcard)
	}
	return fmt.Sprintf("Fix it either way: set %s as a Virtual Host (Control Panel > Instance Settings > "+
		"Virtual Hosts), or add %s to virtual.hosts.valid.hosts in portal-ext.properties", host, valid)
}

// wildcardFor is the virtual.hosts.valid.hosts wildcard covering host's siblings, or "" when the
// host has no parent domain worth naming (a bare name, an IP).
func wildcardFor(host string) string {
	if net.ParseIP(host) != nil {
		return ""
	}
	labels := strings.Split(host, ".")
	if len(labels) < 3 {
		return ""
	}
	return "*." + strings.Join(labels[1:], ".")
}

func hostWithoutPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// Field names of the host_rejected session-log event.
const (
	hostRejectedFieldPort        = "port"
	hostRejectedFieldLocalStatus = "local_status"
)

// emitHostRejectionHint prints the runtime hint. A variable so a test can capture it.
var emitHostRejectionHint = func(msg string) { slog.Warn(msg) }

// maybeProbeHostRejection runs the differential probe for a 500 answered to req on targetPort,
// at most once per public hostname, in the background. The visitor's response is neither
// delayed nor changed.
//
// Only where preserve_host is on for the port: without it the target was asked for the local
// Host, not the public one, so the 500 cannot be a hostname refusal.
func (e *InterceptorEngine) maybeProbeHostRejection(rt http.RoundTripper, req *http.Request, targetPort int) {
	if req.Method != http.MethodGet || !e.PreservesHostFor(targetPort) {
		return
	}
	publicHost := hostWithoutPort(req.Host)
	if publicHost == "" || isLoopbackHostname(publicHost) {
		return
	}
	if _, already := e.hostProbes.LoadOrStore(publicHost, true); already {
		return
	}
	probeURL := *req.URL
	localHost := getHostHeaderValue(e.TargetHost, targetPort)
	liferay := e.IsLiferayPort(targetPort)
	go func() {
		result := confirmHostRejection(context.Background(), rt, probeURL.String(), localHost)
		if result.Verdict != HostRejected {
			slog.Debug(fmt.Sprintf("[Client] 500 for %s on port %d is not a hostname refusal: %s", publicHost, targetPort, result.Detail))
			return
		}
		what := fmt.Sprintf("Liferay on port %d", targetPort)
		if !liferay {
			what = fmt.Sprintf("The application on port %d", targetPort)
		}
		emitHostRejectionHint(fmt.Sprintf("[Liferay] %s answers 500 for %s but %d for %s: it has not accepted this hostname "+
			"(Liferay logs \"Invalid host name %s\"). %s.",
			what, publicHost, result.LocalStatus, localHost, publicHost, LiferayHostHint(publicHost)))
		e.LogEvent("warn", "host_rejected", map[string]any{
			"host":                       publicHost,
			hostRejectedFieldPort:        targetPort,
			hostRejectedFieldLocalStatus: result.LocalStatus,
		})
	}()
}
