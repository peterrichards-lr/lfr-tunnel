package nettrust

import (
	"strings"
	"testing"
)

// The width rule for a trusted forwarder, enforced rather than merely asserted (#1792, #1801).
//
// Before this, pkg/ops's renderRealIPBlock doc comment and the rendered config both insisted every
// entry be an exact address and nothing checked it, so `-trusted-proxy 0.0.0.0/0` produced a
// config that passed `nginx -t`, read as entirely normal, and let every caller on the internet
// assert a visitor address in X-Forwarded-For -- the forgeable path #1325 closed, reopened. The
// gateway's own trusted_proxies had the same hole on its own boundary until #1801.
//
// What the rule is NOT is a prefix-length threshold. A /24 is no safer than an /8 in any way that
// matters. It is whether an attacker can obtain a source address inside the range, which is the
// only thing a trust set grants on -- so exactly one host is fine at any spelling, and a wider
// prefix is fine only where no internet host can occupy it.
func TestTooWide(t *testing.T) {
	accepted := []string{
		"203.0.113.7",        // a bare address: one host
		"2001:db8::1",        // the same, IPv6
		"203.0.113.7/32",     // one host, spelled as a prefix -- identical to the bare form
		"2001:db8::1/128",    // the same, IPv6
		"127.0.0.1",          // the loopback entry every documented deployment carries
		"10.0.0.0/8",         // RFC1918: a proxy subnet nobody outside the network can join
		"172.16.5.0/24",      // RFC1918, inside 172.16.0.0/12
		"192.168.1.0/24",     // RFC1918
		"100.64.0.0/10",      // RFC6598 CGNAT
		"169.254.1.0/24",     // link-local
		"fd12:3456::/32",     // IPv6 ULA, inside fc00::/7
		"fe80::/10",          // IPv6 link-local
		"::1/128",            // IPv6 loopback, the other half of the documented default
		"tunnel.example.com", // not an address at all: a different defect, with its own guard
		"",
	}
	for _, entry := range accepted {
		if reason := TooWide(entry); reason != "" {
			t.Errorf("%q must be accepted, got %q", entry, reason)
		}
	}

	refused := []string{
		"0.0.0.0/0",      // the whole internet
		"::/0",           // the whole internet, IPv6
		"203.0.113.0/24", // a routable /24: every other tenant of that block
		"8.8.8.0/31",     // only two hosts, but two hosts an operator does not own
		"2001:db8::/64",  // a routable IPv6 prefix
		"0.0.0.0/1",      // half the internet, which is not half as safe
		// Containment is of the WHOLE prefix, not of its base address: this one starts inside
		// RFC1918 space and runs straight out of it into 11.0.0.0/8.
		"10.0.0.0/7",
	}
	for _, entry := range refused {
		reason := TooWide(entry)
		if reason == "" {
			t.Errorf("%q trusts more than one host in routable space and must be refused", entry)
			continue
		}
		// Assert the cause, not that a non-empty string came back: the reason is what the render
		// refusal, the startup refusal and both drift findings quote at the operator, and it is
		// useless if it does not say which entry is the problem.
		if !strings.Contains(reason, entry) {
			t.Errorf("the reason for refusing %q must name it, got %q", entry, reason)
		}
	}
}

// WidthGuidance is quoted verbatim by four call sites in two packages, and each of them asserts
// the operator is told which rule was broken. Pinning it here means a rewrite that drops the
// issue reference fails once, next to the rule, rather than four times a long way from it.
func TestWidthGuidanceCarriesTheRuleAndTheRemedy(t *testing.T) {
	for _, want := range []string{"#1325", "#1792", "/32", "/128", "RFC1918", "fc00::/7"} {
		if !strings.Contains(WidthGuidance, want) {
			t.Errorf("the guidance must mention %q so the operator can act on it, got %q", want, WidthGuidance)
		}
	}
}
