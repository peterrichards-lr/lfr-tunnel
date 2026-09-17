package client

import (
	"fmt"
	"reflect"
	"sort"
	"time"
)

// ClientSettings is the declarative block a gateway advertises on /api/version so tuning
// decisions reach clients without a release (#1948).
//
// Deliberately NOT a command channel. pkg/server/diagnostics_transport.go records why, and it
// still holds: a general "run this" channel to every client is a foothold, and an admin account
// is not a safe place to put one. What travels here is a small set of NAMED numbers the client
// already had constants for, each clamped into a range the client chose. A value a client
// interprets within bounds it set is a different risk class from remote execution, and this is
// the line that keeps it there.
//
// A zero or absent field means the gateway has no opinion, and the client's own default
// applies. That is what makes the block safe to extend: an older gateway sends fewer fields,
// a newer client reads fewer than it knows about, and neither case is an error.
//
// Adding a field here REQUIRES adding it to settingBounds below. That is enforced by
// TestEverySettingIsBounded, not by convention -- an unbounded advertised value is precisely
// the defect this design exists to prevent.
type ClientSettings struct {
	// ReconnectSeconds is how long to keep trying to reattach to the same gateway before
	// handing control back for region failover. Previously advertised as the top-level
	// client_reconnect_seconds (#1946), which is still read as a fallback.
	ReconnectSeconds int `json:"reconnect_seconds,omitempty"`
	// HeartbeatSeconds is the /api/tunnel-status tick. It decides both how fast this client
	// notices a lease eviction and how much load the fleet puts on a gateway, which is
	// exactly the sort of number that wants correcting from the server side.
	HeartbeatSeconds int `json:"heartbeat_seconds,omitempty"`
}

// settingBound is one advertised setting's default and the range a gateway may move it within.
//
// The bounds live on the CLIENT, not in the advertisement. A gateway that could send its own
// bounds could send useless ones, which is the same as having none -- the point is that the
// client decides what it is willing to be talked into.
type settingBound struct {
	// def applies when nothing is advertised, or when what was advertised is not usable.
	def int
	min int
	max int
	// why records what going outside the range would actually break, so a later change to a
	// bound is a decision rather than a nudge.
	why string
}

// settingBounds is keyed on the JSON field name, which is what crosses the wire, so a rename
// that forgot this table fails TestEverySettingIsBounded rather than silently unbounding a
// setting.
var settingBounds = map[string]settingBound{
	"reconnect_seconds": {
		def: 60, min: 20, max: 180,
		why: "below the minimum the client gives up inside a routine gateway restart, which is the defect #1946 fixed; above the maximum an unsignalled outage starves region failover for minutes",
	},
	"heartbeat_seconds": {
		def: 5, min: 2, max: 60,
		why: "below the minimum the fleet floods the gateway it is reporting to; above the maximum a lease eviction goes unnoticed for longer than the reconnect window, so failover is delayed by the very signal meant to trigger it",
	},
}

// clampSetting resolves one advertised value against its bounds, and reports whether the
// advertised value was usable as sent.
//
// Zero, negative and out-of-range all resolve to something safe rather than to an error: a
// typo in one server config must not be able to stop clients working across the fleet. That is
// the whole reason this clamps instead of validating -- a validation failure would need a
// behaviour for "invalid", and every candidate behaviour is worse than using a sane number.
func clampSetting(name string, advertised int) (int, bool) {
	b, known := settingBounds[name]
	if !known {
		// Unreachable while TestEverySettingIsBounded passes. Returning the advertised
		// value here would be the one path that applies an unbounded number, so it returns
		// nothing instead.
		return 0, false
	}
	if advertised <= 0 {
		return b.def, false
	}
	if advertised < b.min {
		return b.min, false
	}
	if advertised > b.max {
		return b.max, false
	}
	return advertised, true
}

// ResolvedSettings is what the client actually runs with: every value bounded, and a note of
// anything the gateway asked for that had to be adjusted.
type ResolvedSettings struct {
	ReconnectWindow   time.Duration
	HeartbeatInterval time.Duration
	// Adjustments describes each advertised value that was not used as sent. Reported rather
	// than silently corrected, because a gateway config with a typo in it is a thing somebody
	// needs to find out about -- and the client is the only place the effect is visible.
	Adjustments []string
}

// ResolveSettings applies the bounds to an advertised block.
//
// legacyReconnectSeconds is the pre-block top-level field (#1946). The block wins when it
// carries a value, so a gateway can move to the block without every client having to; a
// gateway sending only the old field still steers clients that understand the new one.
func ResolveSettings(s *ClientSettings, legacyReconnectSeconds int) ResolvedSettings {
	advertised := map[string]int{
		"reconnect_seconds": legacyReconnectSeconds,
		"heartbeat_seconds": 0,
	}
	if s != nil {
		if s.ReconnectSeconds > 0 {
			advertised["reconnect_seconds"] = s.ReconnectSeconds
		}
		if s.HeartbeatSeconds > 0 {
			advertised["heartbeat_seconds"] = s.HeartbeatSeconds
		}
	}

	out := ResolvedSettings{}
	var names []string
	for name := range advertised {
		names = append(names, name)
	}
	// Sorted so the adjustment list is stable, which is what makes it assertable and makes
	// two clients' logs comparable.
	sort.Strings(names)

	resolved := map[string]int{}
	for _, name := range names {
		value, asSent := clampSetting(name, advertised[name])
		resolved[name] = value
		if advertised[name] > 0 && !asSent {
			out.Adjustments = append(out.Adjustments, fmt.Sprintf(
				"%s: the gateway advertised %d, using %d -- %s", name, advertised[name], value, settingBounds[name].why))
		}
	}

	out.ReconnectWindow = time.Duration(resolved["reconnect_seconds"]) * time.Second
	out.HeartbeatInterval = time.Duration(resolved["heartbeat_seconds"]) * time.Second
	return out
}

// settingFieldNames lists the wire names of every field on ClientSettings, by reflection, so
// the bounds test enumerates the type rather than a hand-written list that can go stale.
func settingFieldNames() []string {
	var names []string
	t := reflect.TypeOf(ClientSettings{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				tag = tag[:j]
				break
			}
		}
		if tag != "" {
			names = append(names, tag)
		}
	}
	return names
}
