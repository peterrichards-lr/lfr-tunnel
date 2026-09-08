// Package nettrust holds the width rule for a forwarded-header trust set, stated once (#1792,
// #1801).
//
// A "forwarded-header trust set" is any hand-configured list of addresses whose X-Real-IP /
// X-Forwarded-For this fleet will believe. There are two of them, on two different boundaries,
// and they are configured in two different files:
//
//   - nginx's set_real_ip_from, rendered by pkg/ops from -trusted-proxy and the DNS spec, which
//     decides which peer may rewrite $remote_addr before the gateway ever sees the request;
//   - trusted_proxies in the server config, read by pkg/server's parseTrustedProxies, which
//     decides whose forwarding headers clientIPFrom will believe AT ALL -- a second boundary
//     that holds whatever nginx in front of it does, and holds even when there is no nginx.
//
// Both reopen the forgeable path #1325 closed when they are widened, and both are judged here.
// This package exists so that the two cannot be judged by two predicates that drift apart: the
// rule was written for the first boundary in #1792 and pkg/server could not reach it, since
// pkg/server must not import pkg/ops (#1801).
package nettrust

import (
	"fmt"
	"net/netip"
	"strings"
)

// The rule, enforced rather than asserted (#1792).
//
// pkg/ops's renderRealIPBlock doc comment and the rendered config both say every entry must be an
// exact address, and until #1792 nothing checked it: `-trusted-proxy 0.0.0.0/0` rendered a config
// that passed `nginx -t`, looked entirely normal, and let EVERY caller assert a visitor address in
// X-Forwarded-For. The gateway's own trusted_proxies had the same hole until #1801.
//
// # Why not simply refuse every CIDR
//
// A /32 or /128 is exactly one host and so is identical to a bare address; refusing it would be
// refusing a spelling, not a risk. And nginx's set_real_ip_from genuinely takes prefixes: a
// deployment whose forwarders are a load-balancer subnet rather than one box is a real shape, and
// there is no way to enumerate an autoscaled LB's addresses in advance.
//
// # The boundary that is actually load-bearing
//
// It is NOT prefix length. A /24 is not safer than a /8 in any way that matters; both were
// candidate thresholds in #1792 and both are arbitrary. What decides the risk is whether an
// attacker can obtain a source address inside the range, because that -- and only that -- is what
// a trust set grants on. So the rule is:
//
//	an entry must name exactly one host, OR lie entirely inside address space
//	that no host on the internet can occupy.
//
// A prefix inside RFC1918, CGNAT, link-local, loopback or IPv6 ULA space is reachable only from
// inside the operator's own network, so trusting it grants nothing to anyone who is not already
// there. A prefix covering more than one GLOBALLY ROUTABLE address grants it to whoever ends up
// holding an address in that range -- which for 0.0.0.0/0 and ::/0 is the entire internet, and
// for 203.0.113.0/24 is every other tenant of that block.
//
// Containment is checked against the WHOLE prefix, not its network address: 10.0.0.0/7 starts in
// RFC1918 space and runs straight out of it into 11.0.0.0/8.
//
// This is deliberately more permissive than "exact addresses only", which is what the fleet
// itself uses -- central is one elastic IP and the peer entries are literal A/AAAA records, so
// nothing in this deployment needs the private-range allowance. It exists so the guard refuses
// what is dangerous rather than what is merely unfamiliar.
var privateBlocks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),     // RFC1918
	netip.MustParsePrefix("172.16.0.0/12"),  // RFC1918
	netip.MustParsePrefix("192.168.0.0/16"), // RFC1918
	netip.MustParsePrefix("100.64.0.0/10"),  // RFC6598 CGNAT
	netip.MustParsePrefix("169.254.0.0/16"), // RFC3927 link-local
	netip.MustParsePrefix("127.0.0.0/8"),    // loopback
	netip.MustParsePrefix("fc00::/7"),       // RFC4193 unique local
	netip.MustParsePrefix("fe80::/10"),      // IPv6 link-local
	netip.MustParsePrefix("::1/128"),        // IPv6 loopback
}

// WidthGuidance is the remedy, written once so the render-time refusal, the startup refusal and
// the two drift findings cannot say different things about the same rule.
//
// Worded for the rule rather than for one of its boundaries: #1792 phrased it in terms of
// set_real_ip_from because nginx was the only enforcement site, and the same sentence is now
// quoted by a gateway refusing to start over trusted_proxies, where set_real_ip_from is not the
// mechanism and naming it would send the operator to the wrong file (#1801).
const WidthGuidance = "a trusted-forwarder entry lets ANY peer whose source address falls " +
	"inside the range assert an arbitrary visitor address in X-Real-IP / X-Forwarded-For, so a " +
	"routable range reopens the forgeable path #1325 closed for everything inside it (#1792). " +
	"Name the forwarder's exact address -- a bare IP, a /32 or a /128. A wider prefix is accepted " +
	"only when it lies entirely inside space no internet host can occupy (RFC1918, CGNAT " +
	"100.64.0.0/10, link-local, loopback, IPv6 ULA fc00::/7), e.g. a load-balancer subnet inside " +
	"your own VPC"

// TooWide reports why an entry may not appear in a forwarded-header trust set, or "" if it may.
//
// Returns "" for anything that is not an address or prefix at all -- a hostname, a URL, nginx's
// `unix:` form. Those are separate defects with their own guards (setup-edge-vps.sh shape-checks
// the flag, a hostname never matches a peer address so an nginx block is merely inert, and
// pkg/server's parseTrustedProxies drops an unparseable entry, which narrows its trusted set),
// and reporting them here would make a width check fail for a reason that has nothing to do with
// width.
func TooWide(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ""
	}
	if _, err := netip.ParseAddr(entry); err == nil {
		return "" // a bare address is exactly one host
	}
	p, err := netip.ParsePrefix(entry)
	if err != nil {
		return ""
	}
	p = p.Masked()
	if p.IsSingleIP() {
		return "" // /32 or /128: the same one host, spelled as a prefix
	}
	for _, private := range privateBlocks {
		// Bits() first so this is containment of the whole prefix, not just of its base address.
		if private.Bits() <= p.Bits() && private.Contains(p.Addr()) {
			return ""
		}
	}
	return fmt.Sprintf("%q is a /%d range rather than a single host, and it reaches globally "+
		"routable address space", entry, p.Bits())
}
