package ops

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type fakeRoute53Client struct {
	listHostedZonesByNameFunc    func(ctx context.Context, in *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
	createHostedZoneFunc         func(ctx context.Context, in *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
	getHostedZoneFunc            func(ctx context.Context, in *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	listResourceRecordSetsFunc   func(ctx context.Context, in *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	changeResourceRecordSetsFunc func(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	changeTagsForResourceFunc    func(ctx context.Context, in *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error)
}

func (f *fakeRoute53Client) ListHostedZonesByName(ctx context.Context, in *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
	return f.listHostedZonesByNameFunc(ctx, in, optFns...)
}

func (f *fakeRoute53Client) CreateHostedZone(ctx context.Context, in *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
	return f.createHostedZoneFunc(ctx, in, optFns...)
}

func (f *fakeRoute53Client) GetHostedZone(ctx context.Context, in *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return f.getHostedZoneFunc(ctx, in, optFns...)
}

func (f *fakeRoute53Client) ListResourceRecordSets(ctx context.Context, in *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	return f.listResourceRecordSetsFunc(ctx, in, optFns...)
}

func (f *fakeRoute53Client) ChangeResourceRecordSets(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return f.changeResourceRecordSetsFunc(ctx, in, optFns...)
}

func (f *fakeRoute53Client) ChangeTagsForResource(ctx context.Context, in *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error) {
	return f.changeTagsForResourceFunc(ctx, in, optFns...)
}

func TestRoute53Provider_LookupZone_ExactNameMatch(t *testing.T) {
	client := &fakeRoute53Client{
		listHostedZonesByNameFunc: func(ctx context.Context, in *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
			if aws.ToString(in.DNSName) != "example.com." {
				t.Errorf("expected DNSName 'example.com.', got %q", aws.ToString(in.DNSName))
			}
			return &route53.ListHostedZonesByNameOutput{
				HostedZones: []types.HostedZone{{Id: aws.String("/hostedzone/Z123"), Name: aws.String("example.com.")}},
			}, nil
		},
		getHostedZoneFunc: func(ctx context.Context, in *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
			return &route53.GetHostedZoneOutput{DelegationSet: &types.DelegationSet{NameServers: []string{"ns1.example.com", "ns2.example.com"}}}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	zone, exists, err := provider.LookupZone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupZone failed: %v", err)
	}
	if !exists {
		t.Fatal("expected exists=true for an exact-matching zone")
	}
	if zone.ID != "/hostedzone/Z123" {
		t.Errorf("expected zone ID to be returned as-is from the API, got %q", zone.ID)
	}
	if len(zone.NameServers) != 2 {
		t.Errorf("expected 2 name servers, got %+v", zone.NameServers)
	}
}

func TestRoute53Provider_LookupZone_RejectsLexicalNeighbor(t *testing.T) {
	// ListHostedZonesByName is a lexical "starts here" list, not an exact
	// filter — a request for "example.com" can come back with a non-empty
	// but different zone (e.g. "example.computer.") lexically sorted next
	// to it. LookupZone must not treat that as a match.
	client := &fakeRoute53Client{
		listHostedZonesByNameFunc: func(ctx context.Context, in *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
			return &route53.ListHostedZonesByNameOutput{
				HostedZones: []types.HostedZone{{Id: aws.String("/hostedzone/ZOTHER"), Name: aws.String("example.computer.")}},
			}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	_, exists, err := provider.LookupZone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupZone failed: %v", err)
	}
	if exists {
		t.Fatal("expected exists=false when the API returns a lexically-neighboring, non-matching zone")
	}
}

func TestRoute53Provider_CreateZone(t *testing.T) {
	var gotCallerRef, gotName string
	client := &fakeRoute53Client{
		createHostedZoneFunc: func(ctx context.Context, in *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
			gotCallerRef = aws.ToString(in.CallerReference)
			gotName = aws.ToString(in.Name)
			return &route53.CreateHostedZoneOutput{
				HostedZone:    &types.HostedZone{Id: aws.String("/hostedzone/ZNEW")},
				DelegationSet: &types.DelegationSet{NameServers: []string{"ns1", "ns2", "ns3", "ns4"}},
			}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	zone, err := provider.CreateZone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("CreateZone failed: %v", err)
	}
	if gotName != "example.com." {
		t.Errorf("expected trailing-dot zone name, got %q", gotName)
	}
	if gotCallerRef == "" {
		t.Error("expected a non-empty CallerReference")
	}
	if zone.ID != "ZNEW" {
		t.Errorf("expected zone ID with prefix stripped ('ZNEW'), got %q", zone.ID)
	}
	if len(zone.NameServers) != 4 {
		t.Errorf("expected 4 name servers, got %+v", zone.NameServers)
	}
}

func TestRoute53Provider_ListRecords_FiltersNSAndSOAAndDecodesWireFormat(t *testing.T) {
	client := &fakeRoute53Client{
		listResourceRecordSetsFunc: func(ctx context.Context, in *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
			ttl120 := int64(120)
			ttl86400 := int64(86400)
			return &route53.ListResourceRecordSetsOutput{
				ResourceRecordSets: []types.ResourceRecordSet{
					{Name: aws.String("example.com."), Type: types.RRTypeA, TTL: &ttl120, ResourceRecords: []types.ResourceRecord{{Value: aws.String("1.2.3.4")}}},
					// Route53 always returns the wildcard label octal-escaped,
					// even though "*" is accepted on write -- confirmed against
					// a live zone. This must decode back to the literal "*".
					{Name: aws.String(`\052.example.com.`), Type: types.RRTypeA, TTL: &ttl120, ResourceRecords: []types.ResourceRecord{{Value: aws.String("1.2.3.4")}}},
					{Name: aws.String("example.com."), Type: types.RRTypeNs, TTL: &ttl86400, ResourceRecords: []types.ResourceRecord{{Value: aws.String("ns1.awsdns.com.")}}},
					{Name: aws.String("example.com."), Type: types.RRTypeSoa, TTL: &ttl86400, ResourceRecords: []types.ResourceRecord{{Value: aws.String("ignored")}}},
					{Name: aws.String("example.com."), Type: types.RRTypeMx, TTL: &ttl120, ResourceRecords: []types.ResourceRecord{{Value: aws.String("10 tunnel.example.com.")}}},
					{Name: aws.String("_dmarc.example.com."), Type: types.RRTypeTxt, TTL: &ttl120, ResourceRecords: []types.ResourceRecord{{Value: aws.String(`"v=DMARC1; p=reject;"`)}}},
				},
				IsTruncated: false,
			}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	records, err := provider.ListRecords(context.Background(), ZoneRef{Domain: "example.com", ID: "Z123"})
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("expected 4 records after filtering NS/SOA, got %d: %+v", len(records), records)
	}

	wildcard := records[1]
	if wildcard.Name != "*" {
		t.Errorf("expected the octal-escaped wildcard name to decode to the literal '*', got %+v", wildcard)
	}

	mx := records[2]
	if mx.Type != RecordTypeMX || mx.Value != "tunnel.example.com" || mx.Priority == nil || *mx.Priority != 10 {
		t.Errorf("unexpected decoded MX record: %+v", mx)
	}

	txt := records[3]
	if txt.Name != "_dmarc" || txt.Value != "v=DMARC1; p=reject;" {
		t.Errorf("unexpected decoded TXT record: %+v", txt)
	}
}

func TestRoute53Provider_ListRecords_NameDerivationIsCaseInsensitive(t *testing.T) {
	// DNS names are case-insensitive. The suffix-trim in fromRoute53Name must
	// still correctly derive the zone-relative name even if the zone domain
	// or the record name differ in case.
	client := &fakeRoute53Client{
		listResourceRecordSetsFunc: func(ctx context.Context, in *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
			ttl120 := int64(120)
			return &route53.ListResourceRecordSetsOutput{
				ResourceRecordSets: []types.ResourceRecordSet{
					{Name: aws.String("Tunnel.Example.COM."), Type: types.RRTypeA, TTL: &ttl120, ResourceRecords: []types.ResourceRecord{{Value: aws.String("1.2.3.4")}}},
				},
				IsTruncated: false,
			}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	records, err := provider.ListRecords(context.Background(), ZoneRef{Domain: "example.com", ID: "Z123"})
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(records) != 1 || records[0].Name != "Tunnel" {
		t.Fatalf("expected the zone suffix to be stripped case-insensitively, leaving 'Tunnel', got %+v", records)
	}
}

func TestRoute53Provider_ApplyChange_AlwaysUpserts(t *testing.T) {
	for _, action := range []ChangeAction{ActionCreate, ActionUpdate} {
		var gotAction types.ChangeAction
		var gotName, gotValue string
		var gotTTL int64
		client := &fakeRoute53Client{
			changeResourceRecordSetsFunc: func(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
				c := in.ChangeBatch.Changes[0]
				gotAction = c.Action
				gotName = aws.ToString(c.ResourceRecordSet.Name)
				gotValue = aws.ToString(c.ResourceRecordSet.ResourceRecords[0].Value)
				gotTTL = aws.ToInt64(c.ResourceRecordSet.TTL)
				return &route53.ChangeResourceRecordSetsOutput{}, nil
			},
		}
		provider := &Route53Provider{Client: client}
		change := Change{
			Desired: Record{Name: "tunnel", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120},
			Action:  action,
		}

		if err := provider.ApplyChange(context.Background(), ZoneRef{Domain: "example.com", ID: "Z123"}, change); err != nil {
			t.Fatalf("ApplyChange (%s) failed: %v", action, err)
		}
		if gotAction != types.ChangeActionUpsert {
			t.Errorf("expected route53 action always UPSERT regardless of plan action %s, got %s", action, gotAction)
		}
		if gotName != "tunnel.example.com." {
			t.Errorf("expected trailing-dot fully-qualified name, got %q", gotName)
		}
		if gotValue != "1.2.3.4" {
			t.Errorf("expected raw A record value, got %q", gotValue)
		}
		if gotTTL != 120 {
			t.Errorf("expected TTL 120, got %d", gotTTL)
		}
	}
}

func TestRoute53Provider_ApplyChange_TXTValueIsQuoted(t *testing.T) {
	var gotValue string
	client := &fakeRoute53Client{
		changeResourceRecordSetsFunc: func(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
			gotValue = aws.ToString(in.ChangeBatch.Changes[0].ResourceRecordSet.ResourceRecords[0].Value)
			return &route53.ChangeResourceRecordSetsOutput{}, nil
		},
	}
	provider := &Route53Provider{Client: client}
	change := Change{Desired: Record{Name: "@", Type: RecordTypeTXT, Value: "v=spf1 -all", TTL: 120}, Action: ActionCreate}

	if err := provider.ApplyChange(context.Background(), ZoneRef{Domain: "example.com", ID: "Z123"}, change); err != nil {
		t.Fatalf("ApplyChange failed: %v", err)
	}
	if gotValue != `"v=spf1 -all"` {
		t.Errorf("expected literal-quoted TXT value, got %q", gotValue)
	}
}

func TestRoute53Provider_ApplyChange_MXValueFormatsAsPriorityAndTarget(t *testing.T) {
	var gotValue string
	client := &fakeRoute53Client{
		changeResourceRecordSetsFunc: func(ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
			gotValue = aws.ToString(in.ChangeBatch.Changes[0].ResourceRecordSet.ResourceRecords[0].Value)
			return &route53.ChangeResourceRecordSetsOutput{}, nil
		},
	}
	provider := &Route53Provider{Client: client}
	priority := 10
	change := Change{Desired: Record{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: &priority}, Action: ActionCreate}

	if err := provider.ApplyChange(context.Background(), ZoneRef{Domain: "example.com", ID: "Z123"}, change); err != nil {
		t.Fatalf("ApplyChange failed: %v", err)
	}
	if gotValue != "10 tunnel.example.com." {
		t.Errorf("expected '10 tunnel.example.com.', got %q", gotValue)
	}
}

func TestRoute53Provider_TagZone(t *testing.T) {
	var gotResourceID string
	var gotResourceType types.TagResourceType
	var gotTags map[string]string
	client := &fakeRoute53Client{
		changeTagsForResourceFunc: func(ctx context.Context, in *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error) {
			gotResourceID = aws.ToString(in.ResourceId)
			gotResourceType = in.ResourceType
			gotTags = map[string]string{}
			for _, tag := range in.AddTags {
				gotTags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
			}
			return &route53.ChangeTagsForResourceOutput{}, nil
		},
	}
	provider := &Route53Provider{Client: client}

	err := provider.TagZone(context.Background(), "/hostedzone/Z123", map[string]string{"Project": "lfr-tunnel"})
	if err != nil {
		t.Fatalf("TagZone failed: %v", err)
	}
	if gotResourceID != "Z123" {
		t.Errorf("expected the '/hostedzone/' prefix stripped, got %q", gotResourceID)
	}
	if gotResourceType != types.TagResourceTypeHostedzone {
		t.Errorf("expected ResourceType hostedzone, got %q", gotResourceType)
	}
	if gotTags["Project"] != "lfr-tunnel" {
		t.Errorf("expected Project=lfr-tunnel tag, got %+v", gotTags)
	}
}

func TestRoute53Provider_TagZone_NoTagsIsNoop(t *testing.T) {
	client := &fakeRoute53Client{
		changeTagsForResourceFunc: func(ctx context.Context, in *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error) {
			t.Fatal("ChangeTagsForResource should not be called when there are no tags")
			return nil, nil
		},
	}
	provider := &Route53Provider{Client: client}

	if err := provider.TagZone(context.Background(), "Z123", nil); err != nil {
		t.Fatalf("TagZone failed: %v", err)
	}
}
