package client

import (
	"testing"
)

// Access control could never be saved (#2098). Reproduced against a live client by posting back
// the values it already held:
//
//	payload:  {"passcode":"test","whitelist_ips":"","access_mode":"public"}
//	HTTP 400  Invalid assigned subdomain format
//
// Two faults, and the first fired before the second was reached.

// FAULT 1. engine.SubdomainAss is set from regResp.SubdomainPrefix -- the prefix ALONE -- so
// splitting it on "." always produced one part and every save 400'd.
func TestTheSubdomainTheEngineActuallyHolds(t *testing.T) {
	prefix, domain, err := splitAssignedHost("peters", []string{"https://peters.lfr-demo.se"})
	if err != nil {
		t.Fatalf("the reported defect: a bare prefix is rejected: %v", err)
	}
	if prefix != "peters" || domain != "lfr-demo.se" {
		t.Errorf("got (%q, %q), want (\"peters\", \"lfr-demo.se\")", prefix, domain)
	}
}

// The pair is derived from the public URLs, never guessed from the shape of the string.
//
// The first version trusted any assignment containing a dot as prefix.domain. That is exactly
// wrong for a custom domain, and the test below caught it: "vanity.example.com" split into
// ("vanity", "example.com"), a plausible pair addressing somebody else's reservation.
func TestADottedAssignmentIsNotSplitOnFaith(t *testing.T) {
	// Matches a public URL, so it resolves -- via the URL, not via the dot.
	prefix, domain, err := splitAssignedHost("peters", []string{"https://peters.lfr-demo.se"})
	if err != nil || prefix != "peters" || domain != "lfr-demo.se" {
		t.Fatalf("got (%q, %q, %v)", prefix, domain, err)
	}

	// Nothing to derive from: refused rather than split.
	if _, _, err := splitAssignedHost("peters.lfr-demo.se", nil); err == nil {
		t.Error("an assignment was split on the strength of containing a dot, with no public " +
			"URL to confirm it. That is how a custom domain becomes the wrong reservation.")
	}
}

// The custom-domain case is out of scope, and must say so rather than guess a pair that would
// address somebody else's reservation.
func TestACustomDomainFailsWithAnExplanation(t *testing.T) {
	_, _, err := splitAssignedHost("vanity.example.com", []string{"https://vanity.example.com"})
	if err == nil {
		t.Fatal("a custom domain produced a (prefix, domain) pair; the reservation is keyed on " +
			"that pair, so guessing one addresses the wrong reservation")
	}
}

func TestNoAssignmentYetIsReportedPlainly(t *testing.T) {
	if _, _, err := splitAssignedHost("  ", nil); err == nil {
		t.Error("an empty assignment should be refused, not split into empty strings")
	}
}
