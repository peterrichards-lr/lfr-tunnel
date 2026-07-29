package ops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// Route53API is the minimal subset of *route53.Client this adapter needs.
// Hand-declaring it (rather than depending on the concrete *route53.Client)
// is what makes Route53Provider unit-testable without real AWS calls — the
// SDK v2 doesn't ship an official Route53 mock.
type Route53API interface {
	ListHostedZonesByName(ctx context.Context, in *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
	CreateHostedZone(ctx context.Context, in *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
	GetHostedZone(ctx context.Context, in *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	ListResourceRecordSets(ctx context.Context, in *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ChangeResourceRecordSets(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	ChangeTagsForResource(ctx context.Context, in *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error)
}

// Route53Provider implements Provider against Amazon Route53.
type Route53Provider struct {
	Client Route53API
}

// NewRoute53Provider builds a Route53Provider using the AWS SDK's default
// credential chain (AWS_PROFILE / SSO / env vars / ~/.aws/config) — no
// project-specific credential handling is introduced here; using Route53 at
// all remains entirely opt-in via -provider route53.
func NewRoute53Provider(ctx context.Context) (*Route53Provider, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &Route53Provider{Client: route53.NewFromConfig(cfg)}, nil
}

func (p *Route53Provider) Name() string { return "route53" }

// LookupZone must exact-match the returned hosted zone name — Route53's
// ListHostedZonesByName is a lexical "starts here" listing API, not an exact
// filter, so a non-empty result does not by itself mean the zone exists.
func (p *Route53Provider) LookupZone(ctx context.Context, domain string) (ZoneRef, bool, error) {
	fqdn := ensureTrailingDot(domain)

	out, err := p.Client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName: aws.String(fqdn),
	})
	if err != nil {
		return ZoneRef{}, false, fmt.Errorf("listing route53 hosted zones for %s: %w", domain, err)
	}

	if len(out.HostedZones) == 0 || !route53NameMatches(aws.ToString(out.HostedZones[0].Name), fqdn) {
		return ZoneRef{}, false, nil
	}

	zoneID := aws.ToString(out.HostedZones[0].Id)
	nameServers, err := p.nameServersForZone(ctx, zoneID)
	if err != nil {
		return ZoneRef{}, false, err
	}

	return ZoneRef{Domain: domain, ID: zoneID, NameServers: nameServers}, true, nil
}

func (p *Route53Provider) nameServersForZone(ctx context.Context, zoneID string) ([]string, error) {
	out, err := p.Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(zoneID)})
	if err != nil {
		return nil, fmt.Errorf("getting route53 hosted zone %s: %w", zoneID, err)
	}
	if out.DelegationSet == nil {
		return nil, nil
	}
	return out.DelegationSet.NameServers, nil
}

// route53NameMatches compares two fully-qualified, trailing-dot zone names
// case-insensitively. Route53 zone IDs returned by CreateHostedZone/
// GetHostedZone are prefixed ("/hostedzone/XXXX"), which the caller must
// strip before using them elsewhere — this function only compares names.
func route53NameMatches(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

func (p *Route53Provider) CreateZone(ctx context.Context, domain string) (ZoneRef, error) {
	out, err := p.Client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(ensureTrailingDot(domain)),
		CallerReference: aws.String(fmt.Sprintf("lfr-tunnel-ops-dns-%d", time.Now().UnixNano())),
	})
	if err != nil {
		return ZoneRef{}, fmt.Errorf("creating route53 hosted zone for %s: %w", domain, err)
	}

	var nameServers []string
	if out.DelegationSet != nil {
		nameServers = out.DelegationSet.NameServers
	}

	return ZoneRef{
		Domain:      domain,
		ID:          stripHostedZoneIDPrefix(aws.ToString(out.HostedZone.Id)),
		NameServers: nameServers,
	}, nil
}

func stripHostedZoneIDPrefix(id string) string {
	return strings.TrimPrefix(id, "/hostedzone/")
}

// TagZone applies the given tags to a hosted zone, replacing any existing
// value for keys that already exist. tags is a plain map rather than
// something spec-driven -- resource tagging is an AWS-account-management
// concern, not a DNS record, so it stays out of the provider-agnostic
// Record/Spec types entirely; callers (see dns.go) source tag values from
// their own environment (e.g. Liferay's LFR_TAG_* convention) and only ever
// invoke this for the Route53 provider specifically.
func (p *Route53Provider) TagZone(ctx context.Context, zoneID string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}

	addTags := make([]types.Tag, 0, len(tags))
	for k, v := range tags {
		addTags = append(addTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	_, err := p.Client.ChangeTagsForResource(ctx, &route53.ChangeTagsForResourceInput{
		ResourceId:   aws.String(stripHostedZoneIDPrefix(zoneID)),
		ResourceType: types.TagResourceTypeHostedzone,
		AddTags:      addTags,
	})
	if err != nil {
		return fmt.Errorf("tagging route53 hosted zone %s: %w", zoneID, err)
	}
	return nil
}

func (p *Route53Provider) ListRecords(ctx context.Context, zone ZoneRef) ([]ProviderRecord, error) {
	var records []ProviderRecord
	input := &route53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zone.ID)}

	for {
		out, err := p.Client.ListResourceRecordSets(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("listing route53 record sets for zone %s: %w", zone.ID, err)
		}

		for _, rrs := range out.ResourceRecordSets {
			if rrs.Type == types.RRTypeNs || rrs.Type == types.RRTypeSoa {
				continue
			}
			records = append(records, route53RRSetToProviderRecord(zone.Domain, rrs))
		}

		if !out.IsTruncated {
			break
		}
		input.StartRecordName = out.NextRecordName
		input.StartRecordType = out.NextRecordType
	}

	return records, nil
}

func (p *Route53Provider) ApplyChange(ctx context.Context, zone ZoneRef, change Change) error {
	rrs := recordToRoute53RRSet(zone.Domain, change.Desired)

	_, err := p.Client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zone.ID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					// Route53's UPSERT is idempotent by name+type regardless of
					// whether Reconcile decided CREATE or UPDATE — the
					// CREATE/UPDATE distinction only matters for plan-output
					// readability, not for which API call gets made.
					Action:            types.ChangeActionUpsert,
					ResourceRecordSet: &rrs,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("applying route53 change for %s %s: %w", change.Desired.Name, change.Desired.Type, err)
	}
	return nil
}

func toRoute53Name(zoneDomain, name string) string {
	if name == "@" || name == "" {
		return ensureTrailingDot(zoneDomain)
	}
	return name + "." + ensureTrailingDot(zoneDomain)
}

func fromRoute53Name(zoneDomain, r53Name string) string {
	// Route53 always returns the wildcard label octal-escaped ("\052")
	// in API responses, even though "*" is accepted on write -- confirmed
	// against a live zone. Un-escape it before comparing against the spec's
	// literal "*", or the wildcard record never matches on read and shows
	// as a spurious CREATE on every subsequent plan/apply.
	r53Name = strings.Replace(r53Name, `\052`, "*", 1)

	trimmed := strings.TrimSuffix(r53Name, ".")
	zoneTrimmed := strings.TrimSuffix(zoneDomain, ".")
	if strings.EqualFold(trimmed, zoneTrimmed) {
		return "@"
	}
	return TrimSuffixFold(trimmed, "."+zoneTrimmed)
}

func ensureTrailingDot(host string) string {
	if strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

// toRoute53Value renders a canonical, unquoted Record.Value into the exact
// wire format Route53 expects for its ResourceRecord.Value: CNAME/MX targets
// get a trailing dot, MX gets its priority folded into the same string
// (Route53 has no separate priority field), and TXT gets wrapped in literal
// double quotes.
func toRoute53Value(r Record) string {
	switch r.Type {
	case RecordTypeCNAME:
		return ensureTrailingDot(r.Value)
	case RecordTypeMX:
		priority := 0
		if r.Priority != nil {
			priority = *r.Priority
		}
		return fmt.Sprintf("%d %s", priority, ensureTrailingDot(r.Value))
	case RecordTypeTXT:
		return `"` + strings.ReplaceAll(r.Value, `"`, `\"`) + `"`
	default: // A, AAAA
		return r.Value
	}
}

// fromRoute53Value is the inverse of toRoute53Value.
func fromRoute53Value(recordType RecordType, value string) (val string, priority *int) {
	switch recordType {
	case RecordTypeCNAME:
		return strings.TrimSuffix(value, "."), nil
	case RecordTypeMX:
		parts := strings.SplitN(value, " ", 2)
		if len(parts) != 2 {
			return value, nil
		}
		p, err := strconv.Atoi(parts[0])
		if err != nil {
			return value, nil
		}
		target := strings.TrimSuffix(parts[1], ".")
		return target, &p
	case RecordTypeTXT:
		unquoted := strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
		unquoted = strings.ReplaceAll(unquoted, `\"`, `"`)
		return unquoted, nil
	default: // A, AAAA
		return value, nil
	}
}

func recordToRoute53RRSet(zoneDomain string, r Record) types.ResourceRecordSet {
	ttl := int64(r.TTL)
	return types.ResourceRecordSet{
		Name: aws.String(toRoute53Name(zoneDomain, r.Name)),
		Type: types.RRType(r.Type),
		TTL:  &ttl,
		ResourceRecords: []types.ResourceRecord{
			{Value: aws.String(toRoute53Value(r))},
		},
	}
}

func route53RRSetToProviderRecord(zoneDomain string, rrs types.ResourceRecordSet) ProviderRecord {
	rec := Record{
		Name: fromRoute53Name(zoneDomain, aws.ToString(rrs.Name)),
		Type: RecordType(rrs.Type),
	}
	if rrs.TTL != nil {
		rec.TTL = int(*rrs.TTL)
	}
	if len(rrs.ResourceRecords) > 0 {
		val, priority := fromRoute53Value(rec.Type, aws.ToString(rrs.ResourceRecords[0].Value))
		rec.Value = val
		rec.Priority = priority
	}
	return ProviderRecord{Record: rec}
}
