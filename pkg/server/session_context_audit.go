package server

import (
	"fmt"
	"sort"
	"strings"
)

// Per-session client provenance, recorded in the tunnel.start audit line (#2001).
//
// The client already sends client_version and client_os on every registration, and the
// server already stores them -- on the USER record, as last_client_version /
// last_client_os. That is a single mutable field, overwritten by the next registration,
// so it answers "what is this user on now" and cannot answer "what were they on when it
// broke". #1987 needed the second question and the field had already been overwritten by
// the upgrade that fixed the problem.
//
// Nothing new is collected here. The server is already holding these values at the moment
// it writes tunnel.start; this stops throwing them away. No protocol change, no new table
// (admin_audit_log is retained and already rendered in both portal arms), and no new
// consent question -- it is the user's own client reporting its own version.
//
// Two more facts ride along from the same request for the same reason:
//
//   - the accepting node. tunnel_metrics.node_id records which gateway served a session's
//     TRAFFIC; nothing recorded which gateway ACCEPTED it, and after a failover those are
//     different gateways.
//   - the region source. Already on the registration payload and already stored in
//     client_region_source, but joining that back to one session is awkward; in the audit
//     line it is simply there.
//
// What this does NOT fix: a client too old to send client_version reports nothing, and no
// server-side change can reach it. Those sessions get an explicit "client version not
// reported", never a blank that reads like a recorded value.

// clientVersionUnreported is what the detail says when the client sent no version.
//
// Deliberately a sentence, not an empty string or a placeholder like "-" or "unknown
// version": a reader scanning audit rows must not be able to mistake an unreported
// version for a recorded one, and a downstream grep for a version must not match this.
const clientVersionUnreported = "client version not reported"

// unknownSessionContextValue is used for the node and the region source when the request
// does not carry them. Unlike the version, these are server-side facts, so "unknown" here
// means the server could not name it rather than that a client withheld it.
const unknownSessionContextValue = "unknown"

// maxSessionContextField caps one field before it reaches the database.
//
// client_version, client_os and region_source are all attacker-controlled: any client can
// send any string on an endpoint that authenticates but does not otherwise bound them.
// The same reasoning and the same bound as recordRegionSource.
const maxSessionContextField = 64

// sanitizeSessionContextField makes one attacker-controlled value safe to concatenate
// into an audit detail.
//
// The detail is free text rendered as a single row in both portal arms and emitted into
// CSV exports, so a newline or a semicolon in a client-supplied value could forge what
// looks like a second record. Anything outside a conservative printable set becomes '?'
// rather than being dropped, so tampering stays visible instead of being silently
// normalised away.
func sanitizeSessionContextField(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) > maxSessionContextField {
		v = v[:maxSessionContextField]
	}
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(".-_+/: ", r):
			b.WriteRune(r)
		default:
			b.WriteRune('?')
		}
	}
	return b.String()
}

// sessionContextDetail renders the per-session suffix appended to a tunnel.start detail.
//
// Shape (one bracketed group, semicolon-separated, so a reader and a grep both cope):
//
//	[client v1.48.35 on darwin; node control; region source probe]
//	[client version not reported; node edge-eu; region source unknown]
func sessionContextDetail(clientVersion, clientOS, nodeID, regionSource string) string {
	clientVersion = sanitizeSessionContextField(clientVersion)
	clientOS = sanitizeSessionContextField(clientOS)
	nodeID = sanitizeSessionContextField(nodeID)
	regionSource = sanitizeSessionContextField(regionSource)

	var client string
	switch {
	case clientVersion != "" && clientOS != "":
		client = fmt.Sprintf("client %s on %s", clientVersion, clientOS)
	case clientVersion != "":
		client = "client " + clientVersion
	case clientOS != "":
		client = fmt.Sprintf("%s (%s)", clientVersionUnreported, clientOS)
	default:
		client = clientVersionUnreported
	}

	if nodeID == "" {
		nodeID = unknownSessionContextValue
	}
	if regionSource == "" {
		regionSource = unknownSessionContextValue
	}

	return fmt.Sprintf("[%s; node %s; region source %s]", client, nodeID, regionSource)
}

// launchContextDetail renders how the client was started, for the same audit line (#2148).
//
// Answers the question "why is this tunnel HERE", which region source alone cannot: it says a
// region was probed, not whether a routing flag was given at all. A client started with no
// routing flag and one pinned with -pin both differ from one that named a region, and the
// failback prober only ever watches the region resolved at STARTUP.
//
// Empty for a client that predates this, so the line is unchanged for them rather than carrying
// an "unknown" nobody can act on.
//
// Every value here is a flag or environment-variable NAME. The client sends no flag values, and
// this must never start printing any: -passcode, -basic-auth and -token are all flags.
func launchContextDetail(flags []string, overrides map[string]string) string {
	var parts []string

	if len(flags) > 0 {
		safe := make([]string, 0, len(flags))
		for _, f := range flags {
			// Rejected outright, not sanitised. A flag name is [a-z0-9-] and nothing else, so
			// anything containing other characters is not a flag this binary defines and has no
			// business in an audit row. sanitizeSessionContextField only neutralises the field
			// SEPARATORS, which stops a forged field but still prints the attacker's text --
			// misleading to whoever reads the row, and needless when the alphabet is this narrow.
			if isPlausibleFlagName(f) {
				safe = append(safe, "-"+f)
			}
		}
		if len(safe) > 0 {
			parts = append(parts, "flags "+strings.Join(safe, " "))
		}
	}

	// Only the settings an ENV VAR claimed. A flag-claimed setting is already in the list above,
	// and repeating it would double the length of the line for no new information.
	var fromEnv []string
	for key, source := range overrides {
		if strings.HasPrefix(source, "-") {
			continue
		}
		k := sanitizeSessionContextField(key)
		v := sanitizeSessionContextField(source)
		if k != "" && v != "" {
			fromEnv = append(fromEnv, k+"="+v)
		}
	}
	if len(fromEnv) > 0 {
		sort.Strings(fromEnv)
		parts = append(parts, "env "+strings.Join(fromEnv, " "))
	}

	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}

// isPlausibleFlagName reports whether a reported flag name could be a flag this binary defines.
//
// The client sends these, and a client is not trusted: it can send any string it likes. Rather
// than mangle a hostile one into something that still reads as text, anything outside a flag's
// actual alphabet is dropped.
func isPlausibleFlagName(name string) bool {
	if name == "" || len(name) > 40 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
