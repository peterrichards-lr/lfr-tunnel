package ops

import (
	"context"
	"fmt"
)

// ZoneRef identifies a zone at a specific provider.
type ZoneRef struct {
	Domain      string
	ID          string   // provider opaque zone id (no trailing dot)
	NameServers []string // populated by LookupZone/CreateZone when known
}

// ProviderRecord is a record as currently reported by a provider, plus
// whatever opaque handle is needed to mutate it. Adapters must report Name
// using the same zone-relative convention as Record.Name ("@" for the zone
// apex), so Reconcile can match desired and current records by Name+Type.
type ProviderRecord struct {
	Record
	ProviderID string // e.g. Cloudflare's dns_record id; unused by Route53 (UPSERT addresses by name+type)
}

// ChangeAction describes what Reconcile decided should happen to a record.
type ChangeAction string

const (
	ActionCreate ChangeAction = "CREATE"
	ActionUpdate ChangeAction = "UPDATE"
	ActionNoop   ChangeAction = "NOOP"
	// Deliberately no ActionDelete in v1: Reconcile never proposes removing a
	// record that exists at the provider but isn't in the desired spec. See
	// the plan's risk list — this must be addressed before this tool can
	// fully replace the DDNS cron scripts' stale-record cleanup behavior.
)

// Change is one row of a reconciliation plan.
type Change struct {
	Zone    string
	Desired Record
	Current *ProviderRecord // nil if it doesn't exist yet
	Action  ChangeAction
	Reason  string
}

// Provider is the pluggable DNS backend contract. Every method must be
// idempotent / safe to call repeatedly — this is what would let `dns apply`
// eventually double as a DDNS-cron-style reconciliation loop (not wired up
// yet). Cloudflare and Route53 implement this exact same interface; neither
// is privileged over the other.
type Provider interface {
	// Name identifies the provider, e.g. "cloudflare" or "route53".
	Name() string

	// LookupZone is read-only: no mutation, safe to call from `dns plan`.
	// Returns exists=false (not an error) when the zone simply doesn't exist yet.
	LookupZone(ctx context.Context, domain string) (zone ZoneRef, exists bool, err error)

	// CreateZone is mutating; only ever called from ApplyPlan, and only
	// after LookupZone has confirmed the zone is absent.
	CreateZone(ctx context.Context, domain string) (ZoneRef, error)

	// ListRecords returns the provider's current records for the zone. NS
	// and SOA records must be filtered out by the adapter before returning.
	ListRecords(ctx context.Context, zone ZoneRef) ([]ProviderRecord, error)

	// ApplyChange performs one CREATE or UPDATE. Never called with ActionNoop.
	ApplyChange(ctx context.Context, zone ZoneRef, change Change) error
}

// Reconcile is the single, provider-agnostic diff engine shared by every
// adapter: providers only need to expose ListRecords/ApplyChange, and this
// comparison logic (name+type match, then value/ttl/priority equality)
// lives here once rather than being duplicated per provider. providerName
// (e.g. "cloudflare", "route53") gates provider-specific comparisons such as
// Cloudflare's Proxied flag, which has no equivalent on other providers.
func Reconcile(desired []Record, current []ProviderRecord, providerName string) []Change {
	changes := make([]Change, 0, len(desired))

	for _, want := range desired {
		existing := findMatching(current, want)

		if existing == nil {
			changes = append(changes, Change{
				Desired: want,
				Current: nil,
				Action:  ActionCreate,
				Reason:  "record does not exist",
			})
			continue
		}

		if reason, differs := diff(want, existing.Record, providerName); differs {
			changes = append(changes, Change{
				Desired: want,
				Current: existing,
				Action:  ActionUpdate,
				Reason:  reason,
			})
			continue
		}

		changes = append(changes, Change{
			Desired: want,
			Current: existing,
			Action:  ActionNoop,
			Reason:  "matches desired state",
		})
	}

	return changes
}

func findMatching(current []ProviderRecord, want Record) *ProviderRecord {
	for i := range current {
		if current[i].Name == want.Name && current[i].Type == want.Type {
			return &current[i]
		}
	}
	return nil
}

func diff(want, have Record, providerName string) (string, bool) {
	if want.Value != have.Value {
		return fmt.Sprintf("value differs: %s -> %s", have.Value, want.Value), true
	}
	if want.TTL != have.TTL {
		return fmt.Sprintf("ttl differs: %d -> %d", have.TTL, want.TTL), true
	}
	if !priorityEqual(want.Priority, have.Priority) {
		return fmt.Sprintf("priority differs: %s -> %s", priorityString(have.Priority), priorityString(want.Priority)), true
	}
	// Proxied is a Cloudflare-only concept (orange/grey cloud) and only
	// applies to A/AAAA/CNAME records -- other providers never populate it,
	// so comparing it unconditionally would falsely flag every record on a
	// non-Cloudflare provider as differing whenever the spec sets
	// `cloudflare: {proxied: ...}` for a spec shared across providers.
	if providerName == "cloudflare" && proxiedSupported(want.Type) {
		wantProxied, haveProxied := resolveProxied(want.Cloudflare.Proxied), resolveProxied(have.Cloudflare.Proxied)
		if wantProxied != haveProxied {
			return fmt.Sprintf("proxied differs: %t -> %t", haveProxied, wantProxied), true
		}
	}
	return "", false
}

func proxiedSupported(t RecordType) bool {
	return t == RecordTypeA || t == RecordTypeAAAA || t == RecordTypeCNAME
}

// resolveProxied mirrors recordToCFPayload's own nil-handling: an unset
// Proxied is treated as false (grey-cloud/DNS-only).
func resolveProxied(p *bool) bool {
	return p != nil && *p
}

func priorityEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func priorityString(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
}
