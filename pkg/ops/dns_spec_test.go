package ops

import (
	"testing"
)

func TestLoadSpecFile_MultiDomainAndPlaceholderSubstitution(t *testing.T) {
	spec, err := LoadSpecFile("testdata/example_spec.yaml", map[string]string{
		"IPV4": "203.0.113.10",
		"IPV6": "2001:db8::1",
	})
	if err != nil {
		t.Fatalf("LoadSpecFile failed: %v", err)
	}

	if len(spec.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(spec.Domains))
	}
	if spec.Domains[0].Zone != "example.com" {
		t.Errorf("expected first zone example.com, got %s", spec.Domains[0].Zone)
	}
	if spec.Domains[1].Zone != "example.org" {
		t.Errorf("expected second zone example.org, got %s", spec.Domains[1].Zone)
	}

	records := spec.Domains[0].Records
	if len(records) != 6 {
		t.Fatalf("expected 6 records for example.com, got %d", len(records))
	}

	apexA := records[0]
	if apexA.Name != "@" || apexA.Type != RecordTypeA || apexA.Value != "203.0.113.10" || apexA.TTL != 120 {
		t.Errorf("unexpected apex A record: %+v", apexA)
	}

	apexAAAA := records[1]
	if apexAAAA.Value != "2001:db8::1" {
		t.Errorf("expected AAAA value substituted, got %q", apexAAAA.Value)
	}

	spf := records[4]
	if spf.Type != RecordTypeTXT || spf.Value != "v=spf1 ip4:203.0.113.10 ip6:2001:db8::1 -all" {
		t.Errorf("unexpected SPF TXT record: %+v", spf)
	}

	tunnel := records[3]
	if tunnel.Cloudflare.Proxied == nil || *tunnel.Cloudflare.Proxied != false {
		t.Errorf("expected tunnel record cloudflare.proxied=false, got %+v", tunnel.Cloudflare)
	}
}

func TestLoadSpecFile_UndefinedPlaceholderIsError(t *testing.T) {
	_, err := LoadSpecFile("testdata/missing_var_spec.yaml", map[string]string{
		"IPV4": "203.0.113.10",
	})
	if err == nil {
		t.Fatal("expected an error for an undefined placeholder, got nil")
	}
}

func TestLoadSpecFile_MissingFile(t *testing.T) {
	_, err := LoadSpecFile("testdata/does_not_exist.yaml", nil)
	if err == nil {
		t.Fatal("expected an error for a missing spec file, got nil")
	}
}
