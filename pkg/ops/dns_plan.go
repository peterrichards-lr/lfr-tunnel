package ops

import (
	"context"
	"fmt"
)

// DomainPlan is the read-only result of diffing one DomainSpec against a
// Provider's live state.
type DomainPlan struct {
	Domain      string
	ZoneExists  bool
	ZoneID      string   // empty if ZoneExists is false
	NameServers []string // populated when ZoneExists is true
	Changes     []Change
}

// HasChanges reports whether applying this plan would do anything.
func (p *DomainPlan) HasChanges() bool {
	for _, c := range p.Changes {
		if c.Action != ActionNoop {
			return true
		}
	}
	return false
}

// BuildPlan is entirely read-only: it never creates a zone or mutates a
// record, so it's safe to call from `dns plan`.
func BuildPlan(ctx context.Context, provider Provider, spec DomainSpec) (*DomainPlan, error) {
	zone, exists, err := provider.LookupZone(ctx, spec.Zone)
	if err != nil {
		return nil, fmt.Errorf("looking up zone %s: %w", spec.Zone, err)
	}

	plan := &DomainPlan{Domain: spec.Zone, ZoneExists: exists}

	if !exists {
		// Nothing to list yet; every desired record will show as a CREATE,
		// which is exactly what `dns apply` would do once it creates the zone.
		plan.Changes = Reconcile(spec.Records, nil, provider.Name())
		return plan, nil
	}

	plan.ZoneID = zone.ID
	plan.NameServers = zone.NameServers

	current, err := provider.ListRecords(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("listing records for zone %s: %w", spec.Zone, err)
	}

	plan.Changes = Reconcile(spec.Records, current, provider.Name())
	return plan, nil
}

// ApplyResult is the outcome of applying one domain's plan.
type ApplyResult struct {
	Domain         string
	ZoneWasCreated bool
	ZoneID         string
	NameServers    []string
	Created        int
	Updated        int
	Noop           int
	Err            error // set by the caller when this domain's apply fails; see dns.go's per-domain continue-on-error loop
}

// ApplyPlan performs the mutation described by an already-built plan:
// creating the zone first if BuildPlan found it absent, then applying every
// non-noop change. Changes are applied in the order they appear in the plan.
func ApplyPlan(ctx context.Context, provider Provider, spec DomainSpec, plan *DomainPlan) (*ApplyResult, error) {
	result := &ApplyResult{Domain: spec.Zone}

	var zone ZoneRef
	if plan.ZoneExists {
		zone = ZoneRef{Domain: spec.Zone, ID: plan.ZoneID, NameServers: plan.NameServers}
	} else {
		created, err := provider.CreateZone(ctx, spec.Zone)
		if err != nil {
			return result, fmt.Errorf("creating zone %s: %w", spec.Zone, err)
		}
		zone = created
		result.ZoneWasCreated = true
	}
	result.ZoneID = zone.ID
	result.NameServers = zone.NameServers

	for _, change := range plan.Changes {
		switch change.Action {
		case ActionNoop:
			result.Noop++
		case ActionCreate:
			if err := provider.ApplyChange(ctx, zone, change); err != nil {
				return result, fmt.Errorf("applying create for %s %s in %s: %w", change.Desired.Name, change.Desired.Type, spec.Zone, err)
			}
			result.Created++
		case ActionUpdate:
			if err := provider.ApplyChange(ctx, zone, change); err != nil {
				return result, fmt.Errorf("applying update for %s %s in %s: %w", change.Desired.Name, change.Desired.Type, spec.Zone, err)
			}
			result.Updated++
		}
	}

	return result, nil
}
