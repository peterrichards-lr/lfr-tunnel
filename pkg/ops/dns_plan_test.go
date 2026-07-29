package ops

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	lookupZoneFunc   func(ctx context.Context, domain string) (ZoneRef, bool, error)
	createZoneFunc   func(ctx context.Context, domain string) (ZoneRef, error)
	listRecordsFunc  func(ctx context.Context, zone ZoneRef) ([]ProviderRecord, error)
	applyChangeFunc  func(ctx context.Context, zone ZoneRef, change Change) error
	createZoneCalls  int
	applyChangeCalls int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) LookupZone(ctx context.Context, domain string) (ZoneRef, bool, error) {
	return f.lookupZoneFunc(ctx, domain)
}

func (f *fakeProvider) CreateZone(ctx context.Context, domain string) (ZoneRef, error) {
	f.createZoneCalls++
	return f.createZoneFunc(ctx, domain)
}

func (f *fakeProvider) ListRecords(ctx context.Context, zone ZoneRef) ([]ProviderRecord, error) {
	return f.listRecordsFunc(ctx, zone)
}

func (f *fakeProvider) ApplyChange(ctx context.Context, zone ZoneRef, change Change) error {
	f.applyChangeCalls++
	return f.applyChangeFunc(ctx, zone, change)
}

func TestBuildPlan_NewZone(t *testing.T) {
	p := &fakeProvider{
		lookupZoneFunc: func(ctx context.Context, domain string) (ZoneRef, bool, error) {
			return ZoneRef{}, false, nil
		},
	}
	spec := DomainSpec{Zone: "example.com", Records: []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	plan, err := BuildPlan(context.Background(), p, spec)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	if plan.ZoneExists {
		t.Error("expected ZoneExists=false for a brand new zone")
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ActionCreate {
		t.Fatalf("expected a single ActionCreate change, got %+v", plan.Changes)
	}
}

func TestBuildPlan_ExistingZoneAllNoop(t *testing.T) {
	zone := ZoneRef{Domain: "example.com", ID: "zone-1", NameServers: []string{"ns1.example.com"}}
	p := &fakeProvider{
		lookupZoneFunc: func(ctx context.Context, domain string) (ZoneRef, bool, error) {
			return zone, true, nil
		},
		listRecordsFunc: func(ctx context.Context, z ZoneRef) ([]ProviderRecord, error) {
			return []ProviderRecord{{Record: Record{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}, nil
		},
	}
	spec := DomainSpec{Zone: "example.com", Records: []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}

	plan, err := BuildPlan(context.Background(), p, spec)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	if !plan.ZoneExists || plan.ZoneID != "zone-1" {
		t.Fatalf("expected existing zone-1, got %+v", plan)
	}
	if plan.HasChanges() {
		t.Errorf("expected no changes, got %+v", plan.Changes)
	}
}

func TestApplyPlan_CreatesZoneOnlyWhenMissing(t *testing.T) {
	p := &fakeProvider{
		createZoneFunc: func(ctx context.Context, domain string) (ZoneRef, error) {
			return ZoneRef{Domain: domain, ID: "new-zone", NameServers: []string{"ns1", "ns2"}}, nil
		},
		applyChangeFunc: func(ctx context.Context, zone ZoneRef, change Change) error { return nil },
	}
	spec := DomainSpec{Zone: "example.com", Records: []Record{{Name: "@", Type: RecordTypeA, Value: "1.2.3.4", TTL: 120}}}
	plan := &DomainPlan{Domain: "example.com", ZoneExists: false, Changes: Reconcile(spec.Records, nil)}

	result, err := ApplyPlan(context.Background(), p, spec, plan)
	if err != nil {
		t.Fatalf("ApplyPlan failed: %v", err)
	}
	if p.createZoneCalls != 1 {
		t.Errorf("expected CreateZone to be called exactly once, got %d", p.createZoneCalls)
	}
	if !result.ZoneWasCreated || result.ZoneID != "new-zone" {
		t.Errorf("expected ZoneWasCreated with id new-zone, got %+v", result)
	}
	if result.Created != 1 {
		t.Errorf("expected 1 created record, got %d", result.Created)
	}

	// Existing-zone case: CreateZone must not be called again.
	p2 := &fakeProvider{
		createZoneFunc: func(ctx context.Context, domain string) (ZoneRef, error) {
			t.Fatal("CreateZone should not be called when the zone already exists")
			return ZoneRef{}, nil
		},
		applyChangeFunc: func(ctx context.Context, zone ZoneRef, change Change) error { return nil },
	}
	existingPlan := &DomainPlan{Domain: "example.com", ZoneExists: true, ZoneID: "existing-zone", Changes: []Change{{Desired: Record{Name: "@", Type: RecordTypeA}, Action: ActionUpdate}}}
	if _, err := ApplyPlan(context.Background(), p2, spec, existingPlan); err != nil {
		t.Fatalf("ApplyPlan failed: %v", err)
	}
	if p2.createZoneCalls != 0 {
		t.Errorf("expected CreateZone to never be called for an existing zone, got %d calls", p2.createZoneCalls)
	}
}

func TestApplyPlan_SkipsNoopChanges(t *testing.T) {
	p := &fakeProvider{
		applyChangeFunc: func(ctx context.Context, zone ZoneRef, change Change) error {
			t.Fatalf("ApplyChange should never be called for a noop change, got %+v", change)
			return nil
		},
	}
	spec := DomainSpec{Zone: "example.com"}
	plan := &DomainPlan{
		Domain:     "example.com",
		ZoneExists: true,
		ZoneID:     "zone-1",
		Changes:    []Change{{Desired: Record{Name: "@", Type: RecordTypeA}, Action: ActionNoop}},
	}

	result, err := ApplyPlan(context.Background(), p, spec, plan)
	if err != nil {
		t.Fatalf("ApplyPlan failed: %v", err)
	}
	if result.Noop != 1 || result.Created != 0 || result.Updated != 0 {
		t.Errorf("expected 1 noop and 0 created/updated, got %+v", result)
	}
}

func TestApplyPlan_PropagatesApplyChangeError(t *testing.T) {
	p := &fakeProvider{
		applyChangeFunc: func(ctx context.Context, zone ZoneRef, change Change) error {
			return errors.New("boom")
		},
	}
	spec := DomainSpec{Zone: "example.com"}
	plan := &DomainPlan{
		Domain:     "example.com",
		ZoneExists: true,
		ZoneID:     "zone-1",
		Changes:    []Change{{Desired: Record{Name: "@", Type: RecordTypeA}, Action: ActionCreate}},
	}

	_, err := ApplyPlan(context.Background(), p, spec, plan)
	if err == nil {
		t.Fatal("expected ApplyPlan to propagate the ApplyChange error")
	}
}
