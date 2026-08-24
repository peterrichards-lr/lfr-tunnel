package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// dnsPublisher keeps a tunnel's public name pointed at the gateway that is actually serving
// it (#1247).
//
// Since #1285 a tunnel is issued at the apex -- peters.lfr-demo.se, with no region in it --
// so the name alone no longer says which node holds it. The wildcard record sends everything
// to the control plane, which is right only while the control plane is the one serving. For
// an edge-held tunnel a visitor has to reach the edge, and a specific record beats the
// wildcard, so publishing one is what makes an edge-held tunnel reachable at all.
//
// Nothing here knows what a hosted zone is. DNS writing lives in pkg/ops as ops tooling
// (dns_route53.go, dns_cloudflare.go); calling a cloud DNS API from lfr-tunneld would give the
// most sensitive host in the deployment a new credential and a new blast radius, and would put
// one provider's identity into provider-neutral code -- the thing #1015/#1016 pushed out. So
// this mirrors powerHook (pkg/ops/power.go): an operator-supplied executable with a documented
// argument protocol, and a reference implementation shipped beside it.
//
// Contract:
//
//	<hook> upsert <fqdn> <target>   publish or replace the record, exit 0 on success
//	<hook> delete <fqdn>            withdraw it, exit 0 on success
//
// target is a hostname that already resolves to the serving gateway, so a hook is free to
// write a CNAME, an ALIAS, or to resolve it and write an A record -- whichever its provider
// prefers. Everything else the script needs comes from its own environment.
type dnsPublisher struct {
	// path is the hook script. Empty means this is not configured, which is the same as
	// never setting it up: every method becomes a no-op and DNS is left entirely alone.
	path string
	// grace is how long a withdrawal waits before it actually deletes anything. A lease is
	// cleaned up on any disconnect, including a laptop sleeping or a wifi blip, and the
	// sweep that does it runs on a timer -- deleting immediately would hammer the provider
	// on ordinary reconnect churn and blank the record every time a client hiccuped.
	grace time.Duration

	mu sync.Mutex
	// published records the target last written for each name, so re-registering an
	// unchanged tunnel does not call the provider again.
	published map[string]string
	// gen is bumped by every request for a name. A delayed withdrawal carries the value it
	// saw, and abandons itself if anything has happened since -- which is what makes a
	// planned move safe: the new gateway's upsert lands, and the old gateway's pending
	// delete then knows it is stale rather than deleting the record that just replaced it.
	gen map[string]uint64

	// run executes the hook. A field so tests can exercise the debouncing, the withdrawal
	// grace and the migration ordering without a script on disk.
	run func(action string, args ...string) error
}

// dnsHookTimeout bounds a single hook invocation. Provider APIs are slow enough to need more
// than a couple of seconds and never slow enough to need minutes; a hook that hangs past this
// is broken, and holding the record open forever helps nobody.
const dnsHookTimeout = 60 * time.Second

// defaultDNSWithdrawGrace is the withdrawal delay when the operator has not chosen one. Long
// enough to cover a client restarting or moving between gateways, short enough that a name
// left pointing at a dead node is corrected promptly.
const defaultDNSWithdrawGrace = 90 * time.Second

func newDNSPublisher(path string, grace time.Duration) *dnsPublisher {
	if grace <= 0 {
		grace = defaultDNSWithdrawGrace
	}
	p := &dnsPublisher{
		path:      path,
		grace:     grace,
		published: make(map[string]string),
		gen:       make(map[string]uint64),
	}
	p.run = p.exec
	return p
}

func (p *dnsPublisher) configured() bool { return p != nil && p.path != "" }

// exec invokes the hook. Its stderr is passed through to ours, so whatever it has to say about
// a failure reaches the operator unedited rather than being summarised into a log line.
func (p *dnsPublisher) exec(action string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dnsHookTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.path, append([]string{action}, args...)...)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("dns hook %q %s %s: %w", p.path, action, strings.Join(args, " "), err)
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		slog.Info(fmt.Sprintf("[DNS] %s %s: %s", action, strings.Join(args, " "), trimmed))
	}
	return nil
}

// Publish points fqdn at target, cancelling any withdrawal still waiting out its grace period.
// A no-op when the name already points there, so a client reconnecting to the same gateway
// costs nothing.
func (p *dnsPublisher) Publish(fqdn, target string) {
	if !p.configured() || fqdn == "" || target == "" {
		return
	}

	p.mu.Lock()
	g := p.gen[fqdn] + 1
	p.gen[fqdn] = g
	unchanged := p.published[fqdn] == target
	p.mu.Unlock()

	if unchanged {
		return
	}

	go func() {
		if err := p.run("upsert", fqdn, target); err != nil {
			slog.Info(fmt.Sprintf("[DNS] Failed to publish %s -> %s: %v", fqdn, target, err))
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		// Only the most recent request gets to record what the name now points at. An older
		// invocation finishing late must not overwrite a newer one's answer.
		if p.gen[fqdn] == g {
			p.published[fqdn] = target
			slog.Info(fmt.Sprintf("[DNS] Published %s -> %s", fqdn, target))
		}
	}()
}

// Withdraw removes fqdn after the grace period, on behalf of the gateway that was serving it.
//
// target is that gateway, and the record is only removed if it still points there. A gateway
// can only give up a name it currently holds: during a planned move the client registers on
// the new gateway before the old one tears its lease down, so the old gateway's withdrawal
// routinely arrives *after* the name has legitimately been repointed. Without this it deleted
// the record that had just replaced it, and the tunnel was live with no way to reach it.
//
// Once the record is gone the wildcard takes over again, so the name falls back to the control
// plane's offline page rather than to NXDOMAIN.
func (p *dnsPublisher) Withdraw(fqdn, target string) {
	if !p.configured() || fqdn == "" {
		return
	}

	p.mu.Lock()
	if _, known := p.published[fqdn]; !known {
		// Never published by this process, so there is nothing of ours to remove. Deleting
		// anyway would take out a static record an operator put there by hand.
		p.mu.Unlock()
		return
	}
	g := p.gen[fqdn] + 1
	p.gen[fqdn] = g
	p.mu.Unlock()

	time.AfterFunc(p.grace, func() { p.withdrawNow(fqdn, target, g) })
}

// withdrawNow performs the delete if the name is still this gateway's to give up. Split out
// from Withdraw so both conditions are testable without waiting out a grace period.
func (p *dnsPublisher) withdrawNow(fqdn, target string, g uint64) {
	p.mu.Lock()
	if p.gen[fqdn] != g {
		// Reconnected, or moved to another gateway, while this was waiting.
		p.mu.Unlock()
		return
	}
	if current, ok := p.published[fqdn]; ok && target != "" && current != target {
		// The name has moved on to another gateway. Not ours to remove.
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	if err := p.run("delete", fqdn); err != nil {
		slog.Info(fmt.Sprintf("[DNS] Failed to withdraw %s: %v", fqdn, err))
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen[fqdn] == g {
		delete(p.published, fqdn)
		delete(p.gen, fqdn)
		slog.Info(fmt.Sprintf("[DNS] Withdrew %s", fqdn))
	}
}

// dnsTargetForEdge returns the hostname visitors should be sent to for a tunnel held by the
// named edge node: the node's own configured URL, which is the one address central already
// knows resolves to it. Empty when the node has no URL, in which case there is nothing
// truthful to publish and the caller leaves DNS alone.
func (s *Server) dnsTargetForEdge(nodeID string) string {
	for _, node := range s.cfg.EdgeNodes {
		if node.ID == nodeID {
			return hostFromURL(node.URL)
		}
	}
	return ""
}

// dnsTargetForCentral returns the hostname for a tunnel this control plane is serving itself.
// Publishing this rather than simply withdrawing matters on a planned move: it replaces the
// edge's record immediately instead of relying on that edge's withdrawal having arrived.
func (s *Server) dnsTargetForCentral() string {
	if h := hostFromURL(s.cfg.CentralURL); h != "" {
		return h
	}
	if len(s.cfg.Domains) > 0 {
		return "tunnel." + s.cfg.Domains[0]
	}
	return ""
}

// hostFromURL pulls the hostname out of a configured URL, tolerating one written without a
// scheme -- operators write both, and url.Parse reads a bare host as a path.
func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// publishTunnelDNS points a tunnel's public name at the gateway serving it. Custom domains are
// skipped: those live in a zone the tunnel operator does not control, so the hook would fail
// on every attempt, and the user's own CNAME is what points them here in the first place.
func (s *Server) publishTunnelDNS(fqdn, target string) {
	if !s.dns.configured() || s.isCustomDomain(fqdn) {
		return
	}
	s.dns.Publish(fqdn, target)
}

// withdrawTunnelDNS starts the grace-period countdown on a tunnel's public name, on behalf of
// the gateway that was serving it. See dnsPublisher.Withdraw for why the caller has to say
// which gateway is giving the name up rather than just naming the record.
func (s *Server) withdrawTunnelDNS(fqdn, target string) {
	if !s.dns.configured() || s.isCustomDomain(fqdn) {
		return
	}
	s.dns.Withdraw(fqdn, target)
}
