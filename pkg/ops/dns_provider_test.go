package ops

import "testing"

func intPtr(v int) *int { return &v }

func TestReconcile_CreateWhenMissing(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}
	changes := Reconcile(desired, nil, "cloudflare")

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != ActionCreate {
		t.Errorf("expected ActionCreate, got %s", changes[0].Action)
	}
	if changes[0].Current != nil {
		t.Errorf("expected nil Current for a create, got %+v", changes[0].Current)
	}
}

func TestReconcile_UpdateWhenValueDiffers(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.5", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}, ProviderID: "abc"}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected 1 ActionUpdate change, got %+v", changes)
	}
	if changes[0].Current == nil || changes[0].Current.ProviderID != "abc" {
		t.Errorf("expected Current to carry through the existing ProviderID, got %+v", changes[0].Current)
	}
}

func TestReconcile_UpdateWhenTTLDiffers(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 300}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected 1 ActionUpdate change for a TTL diff, got %+v", changes)
	}
}

func TestReconcile_UpdateWhenPriorityDiffers(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: intPtr(20)}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: intPtr(10)}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected 1 ActionUpdate change for a priority diff, got %+v", changes)
	}
}

func TestReconcile_NoopWhenDesiredPriorityNilMatchesProviderZero(t *testing.T) {
	// A spec MX record that omits priority renders as "0 <target>" on write
	// (toRoute53Value), so the provider always echoes back an explicit 0 on
	// read. This must match, not perpetually differ.
	desired := []Record{{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: intPtr(0)}}}

	changes := Reconcile(desired, current, "route53")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected nil desired priority to match an explicit provider-returned 0, got %+v", changes)
	}
}

func TestReconcile_UpdateWhenDesiredPriorityNilDiffersFromNonZero(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeMX, Value: "tunnel.example.com", TTL: 120, Priority: intPtr(10)}}}

	changes := Reconcile(desired, current, "route53")

	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected nil desired priority (resolves to 0) to differ from an existing non-zero priority, got %+v", changes)
	}
}

func TestReconcile_NoopWhenIdentical(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected 1 ActionNoop change, got %+v", changes)
	}
}

func TestReconcile_IgnoresUnknownExistingRecords(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}
	current := []ProviderRecord{
		{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}},
		{Record: Record{Name: "stale-leftover", Type: RecordTypeA, Value: "9.9.9.9", TTL: 120}},
	}

	changes := Reconcile(desired, current, "cloudflare")

	// Proves the no-delete design decision: a stray current record absent
	// from `desired` must never surface as a Change (there is no
	// ActionDelete to emit it as).
	if len(changes) != 1 {
		t.Fatalf("expected exactly 1 change (the desired record only), got %d: %+v", len(changes), changes)
	}
	if changes[0].Desired.Name != "@" {
		t.Errorf("expected the single change to be for the desired '@' record, got %+v", changes[0])
	}
}

func TestReconcile_MatchesRecordNameCaseInsensitively(t *testing.T) {
	desired := []Record{{Name: "Tunnel", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "tunnel", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected a case-differing name to still match existing state, got %+v", changes)
	}
}

func TestReconcile_DifferentTypeSameNameNotMatched(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeAAAA, Value: "::1", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionCreate {
		t.Fatalf("expected an A record at the same name to not satisfy the desired AAAA record, got %+v", changes)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestReconcile_UpdateWhenProxiedDiffersOnCloudflare(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120, Cloudflare: CloudflareOpts{Proxied: boolPtr(true)}}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120, Cloudflare: CloudflareOpts{Proxied: boolPtr(false)}}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionUpdate {
		t.Fatalf("expected 1 ActionUpdate change for a proxied diff on cloudflare, got %+v", changes)
	}
}

func TestReconcile_NoopWhenProxiedNilTreatedAsFalse(t *testing.T) {
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120, Cloudflare: CloudflareOpts{Proxied: boolPtr(false)}}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected unset desired Proxied (nil) to be treated as false, matching an explicit false, got %+v", changes)
	}
}

func TestReconcile_IgnoresProxiedOnNonCloudflareProvider(t *testing.T) {
	// A spec shared across providers (e.g. lfr-demo-production.yaml, read for
	// both Cloudflare and Route53) may set `cloudflare: {proxied: true}` on a
	// record. Route53 has no concept of Proxied and never populates it, so
	// this must never surface as a spurious diff when reconciling against a
	// non-Cloudflare provider.
	desired := []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120, Cloudflare: CloudflareOpts{Proxied: boolPtr(true)}}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	changes := Reconcile(desired, current, "route53")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected Proxied to be ignored entirely on a non-cloudflare provider, got %+v", changes)
	}
}

func TestReconcile_IgnoresProxiedOnRecordTypesThatDontSupportIt(t *testing.T) {
	// TXT/MX records have no Cloudflare proxy concept; recordToCFPayload
	// never sets Proxied for them, so the comparison must not apply here
	// even on the cloudflare provider.
	desired := []Record{{Name: "@", Type: RecordTypeTXT, Value: "v=spf1 -all", TTL: 120, Cloudflare: CloudflareOpts{Proxied: boolPtr(true)}}}
	current := []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeTXT, Value: "v=spf1 -all", TTL: 120}}}

	changes := Reconcile(desired, current, "cloudflare")

	if len(changes) != 1 || changes[0].Action != ActionNoop {
		t.Fatalf("expected Proxied to be ignored for a TXT record even on cloudflare, got %+v", changes)
	}
}
