package ops

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RecordType is a DNS resource record type.
type RecordType string

const (
	RecordTypeA     RecordType = "A"
	RecordTypeAAAA  RecordType = "AAAA"
	RecordTypeCNAME RecordType = "CNAME"
	RecordTypeTXT   RecordType = "TXT"
	RecordTypeMX    RecordType = "MX"
)

// CloudflareOpts holds Cloudflare-specific record options. Other providers
// ignore this field entirely; its zero value is always safe.
type CloudflareOpts struct {
	// Proxied controls Cloudflare's orange/grey cloud. nil is treated as
	// false (grey-cloud/DNS-only) when building the Cloudflare API payload.
	Proxied *bool `yaml:"proxied,omitempty"`
}

// Record is one provider-agnostic desired DNS resource record. Name is
// relative to the zone apex ("@" denotes the apex itself). Value is
// canonical and UNQUOTED (bare IP literal, CNAME/MX target hostname without
// a trailing dot, raw TXT text) — each Provider adapter is responsible for
// whatever wire-format quoting/trailing-dot convention its API requires.
type Record struct {
	Name       string         `yaml:"name"`
	Type       RecordType     `yaml:"type"`
	Value      string         `yaml:"value"`
	TTL        int            `yaml:"ttl"`
	Priority   *int           `yaml:"priority,omitempty"`
	Cloudflare CloudflareOpts `yaml:"cloudflare,omitempty"`
}

// DomainSpec is the full desired record set for one zone.
type DomainSpec struct {
	Zone    string   `yaml:"zone"`
	Records []Record `yaml:"records"`
}

// Spec is the top-level declarative DNS specification: one or more
// DomainSpecs, each independently planned/applied against a Provider.
type Spec struct {
	Domains []DomainSpec `yaml:"domains"`
}

// LoadSpecFile reads a YAML spec file, substitutes "${KEY}"-style
// placeholders in every record's Value string using vars, and unmarshals the
// result. Record Name is never substituted -- no spec uses a placeholder
// there, and record names are structural (used to match against provider
// state), not the sort of value that should vary by deployment. This is the
// only spec-construction logic in this package — no domain, deployment, or
// provider is hardcoded here; callers supply their own YAML file plus
// whatever variables it references (typically IPV4/IPV6).
func LoadSpecFile(path string, vars map[string]string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("reading spec file %s: %w", path, err)
	}

	var spec Spec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return Spec{}, fmt.Errorf("parsing spec file %s: %w", path, err)
	}

	for di := range spec.Domains {
		for ri := range spec.Domains[di].Records {
			rec := &spec.Domains[di].Records[ri]
			resolved, err := substitutePlaceholders(rec.Value, vars)
			if err != nil {
				return Spec{}, fmt.Errorf("domain %s, record %s %s: %w", spec.Domains[di].Zone, rec.Name, rec.Type, err)
			}
			rec.Value = resolved
		}
	}

	return spec, nil
}

// substitutePlaceholders replaces every "${KEY}" occurrence in s with
// vars[KEY]. It is an error for a placeholder to reference a key that isn't
// present in vars — a silently-empty substitution in a DNS record value
// would be worse than a hard failure at load time.
func substitutePlaceholders(s string, vars map[string]string) (string, error) {
	var b strings.Builder
	for {
		start := strings.Index(s, "${")
		if start == -1 {
			b.WriteString(s)
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			b.WriteString(s)
			break
		}
		end += start

		b.WriteString(s[:start])
		key := s[start+2 : end]
		val, ok := vars[key]
		if !ok {
			return "", fmt.Errorf("undefined placeholder ${%s}", key)
		}
		b.WriteString(val)
		s = s[end+1:]
	}
	return b.String(), nil
}
