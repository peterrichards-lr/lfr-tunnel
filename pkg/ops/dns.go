package ops

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DnsCommand is the entrypoint for `lfr-tunnel-ops dns <plan|apply> [flags]`.
// It reconciles a declarative YAML DNS spec (see LoadSpecFile) against a
// pluggable Provider (Cloudflare or Route53, chosen explicitly via
// -provider -- neither is a default). This package contains no hardcoded
// domain, deployment, or provider preference: the spec file supplied via
// -spec is the only source of what records exist.
func DnsCommand(args []string) {
	if len(args) < 1 {
		printDnsUsage()
		os.Exit(1)
	}

	action := args[0]
	rest := args[1:]

	switch action {
	case "plan":
		runDnsPlan(rest)
	case "apply":
		runDnsApply(rest)
	default:
		fmt.Printf("Unknown dns action: %q\n", action)
		printDnsUsage()
		os.Exit(1)
	}
}

func printDnsUsage() {
	fmt.Println("Usage: lfr-tunnel-ops dns <plan|apply> -provider <cloudflare|route53> -spec <path> [flags]")
}

type dnsFlags struct {
	provider string
	specPath string
	domain   string
	ipv4     string
	ipv6     string
	detectIP bool
	out      string
	yes      bool
}

func parseDnsFlags(action string, args []string) *dnsFlags {
	fs := flag.NewFlagSet("dns "+action, flag.ExitOnError)
	f := &dnsFlags{}
	fs.StringVar(&f.provider, "provider", "", "DNS provider: cloudflare or route53 (required; neither is a default)")
	fs.StringVar(&f.specPath, "spec", "", "path to a YAML DNS spec file (required)")
	fs.StringVar(&f.domain, "domain", "", "comma-separated subset of domains from the spec file to target (default: all)")
	fs.StringVar(&f.ipv4, "ipv4", "", "value substituted for ${IPV4} placeholders in the spec")
	fs.StringVar(&f.ipv6, "ipv6", "", "value substituted for ${IPV6} placeholders in the spec")
	fs.BoolVar(&f.detectIP, "detect-ip", false, "auto-detect IPv4/IPv6 via https://api.ipify.org -- NEVER use this from an operator laptop, only from the target server itself")
	fs.StringVar(&f.out, "out", "text", "output format: text or json")
	if action == "apply" {
		fs.BoolVar(&f.yes, "yes", false, "skip the interactive typed confirmation prompt")
	}
	// flag.ExitOnError already exits the process on a parse error.
	_ = fs.Parse(args) //nolint:errcheck

	return f
}

func runDnsPlan(args []string) {
	f := parseDnsFlags("plan", args)
	ctx := context.Background()
	spec, provider := loadSpecAndProvider(ctx, f)

	plans := make([]*DomainPlan, 0, len(spec.Domains))
	for _, domainSpec := range spec.Domains {
		plan, err := BuildPlan(ctx, provider, domainSpec)
		CheckFatal(err, fmt.Sprintf("building plan for %s", domainSpec.Zone))
		plans = append(plans, plan)
	}

	renderPlans(plans, f.out)
}

func runDnsApply(args []string) {
	f := parseDnsFlags("apply", args)
	ctx := context.Background()
	spec, provider := loadSpecAndProvider(ctx, f)

	plans := make([]*DomainPlan, 0, len(spec.Domains))
	totalChanges := 0
	for _, domainSpec := range spec.Domains {
		plan, err := BuildPlan(ctx, provider, domainSpec)
		CheckFatal(err, fmt.Sprintf("building plan for %s", domainSpec.Zone))
		plans = append(plans, plan)
		for _, c := range plan.Changes {
			if c.Action != ActionNoop {
				totalChanges++
			}
		}
	}

	renderPlans(plans, "text")

	if totalChanges == 0 {
		// Tagging is independent of record-level changes: an
		// already-fully-reconciled zone still needs (re-)tagging on every
		// run, and ApplyPlan (which normally triggers tagging below) is
		// never reached in this branch.
		for _, plan := range plans {
			if plan.ZoneExists {
				tagRoute53ZoneIfApplicable(ctx, provider, plan.Domain, plan.ZoneID)
			}
		}
		fmt.Println("No changes to apply.")
		return
	}

	if !f.yes {
		fmt.Printf("Type the provider name (%s) to confirm applying %d change(s) across %d domain(s): ", f.provider, totalChanges, len(spec.Domains))
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n') //nolint:errcheck
		if strings.TrimSpace(line) != f.provider {
			fmt.Println("Confirmation did not match. Aborting.")
			os.Exit(1)
		}
	}

	// Each domain is applied independently: a failure on one domain does not
	// abort the rest, so e.g. a transient throttle on one zone doesn't leave
	// another half-applied and silently unreported.
	results := make([]*ApplyResult, 0, len(spec.Domains))
	anyErr := false
	for i, domainSpec := range spec.Domains {
		result, err := ApplyPlan(ctx, provider, domainSpec, plans[i])
		if err != nil {
			result.Err = err
			anyErr = true
		} else {
			tagRoute53ZoneIfApplicable(ctx, provider, result.Domain, result.ZoneID)
		}
		results = append(results, result)
	}

	for _, r := range results {
		if r.Err != nil {
			fmt.Printf("=== %s: FAILED: %v ===\n", r.Domain, r.Err)
			continue
		}
		fmt.Printf("=== %s: created=%d updated=%d noop=%d zone_created=%v zone_id=%s ===\n", r.Domain, r.Created, r.Updated, r.Noop, r.ZoneWasCreated, r.ZoneID)
		if len(r.NameServers) > 0 {
			fmt.Printf("    name servers: %s\n", strings.Join(r.NameServers, ", "))
		}
	}

	if anyErr {
		os.Exit(1)
	}
}

func loadSpecAndProvider(ctx context.Context, f *dnsFlags) (Spec, Provider) {
	if f.provider == "" {
		CheckFatal(fmt.Errorf("-provider is required (cloudflare or route53); neither is a default"), "parsing flags")
	}
	if f.specPath == "" {
		CheckFatal(fmt.Errorf("-spec is required"), "parsing flags")
	}

	ipv4, ipv6 := f.ipv4, f.ipv6
	if f.detectIP {
		fmt.Println("WARNING: -detect-ip resolves the CALLER's public IP, not the target server's. Never use this from an operator laptop -- only run it from the target server itself.")
		detectedV4, detectedV6, err := detectPublicIPs(ctx)
		CheckFatal(err, "detecting public IP")
		if ipv4 == "" {
			ipv4 = detectedV4
		}
		if ipv6 == "" {
			ipv6 = detectedV6
		}
	}

	vars := map[string]string{}
	if ipv4 != "" {
		vars["IPV4"] = ipv4
	}
	if ipv6 != "" {
		vars["IPV6"] = ipv6
	}

	spec, err := LoadSpecFile(f.specPath, vars)
	CheckFatal(err, "loading spec file")

	domains, err := filterDomains(spec.Domains, f.domain)
	CheckFatal(err, "filtering domains")
	spec.Domains = domains

	provider, err := newProvider(ctx, f.provider)
	CheckFatal(err, "initializing provider")

	return spec, provider
}

// tagRoute53ZoneIfApplicable applies Liferay's optional LFR_TAG_* resource
// tagging convention (see scripts/liferay/aws/liferay-tags.env.example, already used
// by scripts/common/provision-aws-ec2.sh for EC2) to a hosted zone. This is
// deliberately NOT part of the generic Provider interface -- tagging is an
// AWS-account-management concern with no Cloudflare equivalent, so it's
// applied here via a type assertion, only for Route53, and only when at
// least one LFR_TAG_* variable is actually set (an OSS user who never
// touches those env vars sees no behavior change at all). Safe to call on
// every apply, not just newly created zones: ChangeTagsForResource just
// upserts the given keys.
func tagRoute53ZoneIfApplicable(ctx context.Context, provider Provider, domain, zoneID string) {
	r53, ok := provider.(*Route53Provider)
	if !ok {
		return
	}
	tags := liferayTagsFromEnv()
	if len(tags) == 0 {
		return
	}
	if err := r53.TagZone(ctx, zoneID, tags); err != nil {
		fmt.Printf("WARNING: failed to tag zone %s for %s: %v\n", zoneID, domain, err)
	}
}

// liferayTagsFromEnv mirrors scripts/common/provision-aws-ec2.sh's tag-key mapping
// exactly (Project/Owner/Team/CostCenter), so the same liferay-tags.env file
// already used for EC2 provisioning drives Route53 zone tagging too, with no
// new configuration mechanism introduced.
func liferayTagsFromEnv() map[string]string {
	tags := map[string]string{}
	if v := os.Getenv("LFR_TAG_PROJECT"); v != "" {
		tags["Project"] = v
	}
	if v := os.Getenv("LFR_TAG_OWNER"); v != "" {
		tags["Owner"] = v
	}
	if v := os.Getenv("LFR_TAG_TEAM"); v != "" {
		tags["Team"] = v
	}
	if v := os.Getenv("LFR_TAG_COST_CENTER"); v != "" {
		tags["CostCenter"] = v
	}
	return tags
}

func filterDomains(domains []DomainSpec, filter string) ([]DomainSpec, error) {
	if filter == "" {
		return domains, nil
	}

	wanted := make(map[string]bool)
	for _, w := range strings.Split(filter, ",") {
		wanted[strings.TrimSpace(w)] = true
	}

	out := make([]DomainSpec, 0, len(domains))
	for _, d := range domains {
		if wanted[d.Zone] {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no domains in the spec file matched -domain %q", filter)
	}
	return out, nil
}

// newProvider is the only place a provider name maps to a concrete
// implementation. Neither branch is preferred: -provider must be supplied
// explicitly by the caller.
func newProvider(ctx context.Context, name string) (Provider, error) {
	switch name {
	case "cloudflare":
		token := GetEnvOrDefault("LFT_CLOUDFLARE_API_TOKEN", "")
		if token == "" {
			return nil, fmt.Errorf("LFT_CLOUDFLARE_API_TOKEN must be set to use -provider cloudflare")
		}
		return NewCloudflareProvider(token), nil
	case "route53":
		return NewRoute53Provider(ctx)
	default:
		return nil, fmt.Errorf("unknown -provider %q (supported: cloudflare, route53)", name)
	}
}

// detectPublicIPs mirrors scripts/liferay/vm6/cloudflare-ddns.sh's external-echo-service
// fallback. Both lookups are best-effort: either may come back empty if that
// address family isn't available, and that's left to the caller to handle.
func detectPublicIPs(ctx context.Context) (ipv4, ipv6 string, err error) {
	v4, errV4 := httpGetString(ctx, "https://api.ipify.org")
	v6, errV6 := httpGetString(ctx, "https://api6.ipify.org")
	if errV4 != nil && errV6 != nil {
		return "", "", fmt.Errorf("failed to detect public IPv4 (%v) and IPv6 (%v)", errV4, errV6)
	}
	return v4, v6, nil
}

func httpGetString(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func renderPlans(plans []*DomainPlan, format string) {
	if format == "json" {
		b, err := json.MarshalIndent(plans, "", "  ")
		CheckFatal(err, "marshaling plan output")
		fmt.Println(string(b))
		return
	}

	for _, p := range plans {
		fmt.Printf("=== %s ===\n", p.Domain)
		if !p.ZoneExists {
			fmt.Println("  zone does not exist yet -- will be created")
		} else {
			fmt.Printf("  zone id: %s\n", p.ZoneID)
			if len(p.NameServers) > 0 {
				fmt.Printf("  name servers: %s\n", strings.Join(p.NameServers, ", "))
			}
		}
		for _, c := range p.Changes {
			fmt.Printf("  [%s] %s %s -> %s (%s)\n", c.Action, c.Desired.Name, c.Desired.Type, c.Desired.Value, c.Reason)
		}
	}
}
