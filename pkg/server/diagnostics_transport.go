package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// Getting a collection request from an admin to a client (#1763, part 2 of #1696).
//
// #1696 landed the consent model and an endpoint that enforces it completely and then answers
// 501, because nothing carried the request anywhere. This is the carrier.
//
// THE TRANSPORT IS THE HEARTBEAT, not a new channel. The issue proposed modelling this on
// edge_control_ws.go, and that model cannot apply: an edge is a server with a public address
// that dials the control plane and holds a socket open, while a client sits behind NAT and a
// firewall -- which is the entire reason the tunnel exists. The gateway cannot initiate anything
// to a client.
//
// It does not need to. pkg/client/interceptor.go POSTs /api/tunnel-status every five seconds,
// authenticated by its session token, and already reads and acts on the response body. #1238
// reached this conclusion first, for the shutdown warning, and said so: "the heartbeat is the
// right carrier: it is the one gateway-to-client channel the client already listens to and
// already acts on, so nothing new has to be opened or polled." A command rides the same way.
//
// Three constraints the wire imposes, all of them load-bearing:
//
//   - The success body must never carry a top-level "status" field. The client's
//     gatewayHasNoLease treats {"status":"ok"} as "this gateway holds no lease for me" and
//     re-registers. A command that used that key would tear down the tunnel it was asking about.
//   - The client reads the body through io.LimitReader(resp.Body, 512). A command plus a pending
//     shutdown warning has to fit in 512 bytes or the JSON truncates and neither parses. Hence
//     the short field names, and the cap of one command per heartbeat.
//   - Only the SERVING gateway's response is parsed (`if pingURL == serverURL`). Central's reply
//     is ignored for edge-hosted sessions, so central cannot deliver to them directly.
//
// Delivery is at-least-once: a queued command rides every heartbeat until the client acks it or
// it expires. The client dedupes on the id, which is cheaper than making the gateway certain a
// single delivery arrived.
const (
	// diagnosticsCommandCollectLogs is the only command. Deliberately the only one: a general
	// "run this" channel to every client is a foothold, and an admin account is not a safe
	// place to put one. It carries no path -- the client resolves its own log files.
	diagnosticsCommandCollectLogs = "collect_logs"

	// diagnosticsCommandTTL bounds how long a queued command can sit unclaimed.
	//
	// This is a consent guarantee, not tidiness. #1696's whole design is that consent is
	// enforced when the request is issued, so a withdrawal mid-session is honoured because the
	// request never goes out. Queueing introduces a gap that property did not have: a command
	// sitting for a day could be delivered after consent was withdrawn. Five minutes bounds the
	// gap, and deliverDiagnosticsCommands re-checks consent at delivery as well, so the
	// guarantee ends up stronger than it was rather than weaker.
	diagnosticsCommandTTL = 5 * time.Minute

	// diagnosticsAuditDelivered records that the command actually reached a client -- the
	// request audit (#1696) only records that an admin asked. Written on the client's ack,
	// because an ack is evidence of arrival and a write to a socket is not.
	diagnosticsAuditDelivered = "diagnostics.collect_delivered"

	// diagnosticsAuditExpired records a request that never reached anyone. Without it a
	// collection that silently never happened is indistinguishable from one that did.
	diagnosticsAuditExpired = "diagnostics.collect_expired"
)

// diagnosticsCommand is one queued instruction. The JSON field names are short because the
// client caps its read of this body at 512 bytes.
type diagnosticsCommand struct {
	ID   string `json:"id"`
	Type string `json:"t"`

	// Unexported: these are the gateway's bookkeeping and must not reach the client.
	userID      string
	requestedBy string
	queuedAt    time.Time
}

// queueDiagnosticsCollect records a collection request for a user and returns it.
//
// The caller has already established consent; this does not re-check, because the check belongs
// at the authorisation point (handleAdminDiagnosticsCollect) and duplicating it here would make
// two places able to disagree about who may collect.
func (s *Server) queueDiagnosticsCollect(userID, requestedBy string) *diagnosticsCommand {
	cmd := &diagnosticsCommand{
		ID:          newDiagnosticsCommandID(),
		Type:        diagnosticsCommandCollectLogs,
		userID:      userID,
		requestedBy: requestedBy,
		queuedAt:    time.Now().UTC(),
	}

	s.diagCommandsMu.Lock()
	defer s.diagCommandsMu.Unlock()
	if s.diagCommands == nil {
		s.diagCommands = make(map[string][]*diagnosticsCommand)
	}
	// One outstanding command per user. A second request while the first is unclaimed replaces
	// it rather than queueing behind it: an admin clicking twice wants the logs once, and two
	// commands would produce two uploads of the same files.
	s.diagCommands[userID] = []*diagnosticsCommand{cmd}
	return cmd
}

// pendingDiagnosticsCommands returns the commands to ride this user's next heartbeat, dropping
// any that have expired. Expiry is audited by the caller, which has the request context.
func (s *Server) pendingDiagnosticsCommands(userID string) (live []*diagnosticsCommand, expired []*diagnosticsCommand) {
	if userID == "" {
		return nil, nil
	}
	s.diagCommandsMu.Lock()
	defer s.diagCommandsMu.Unlock()

	queued := s.diagCommands[userID]
	if len(queued) == 0 {
		return nil, nil
	}

	cutoff := time.Now().UTC().Add(-diagnosticsCommandTTL)
	var keep []*diagnosticsCommand
	for _, c := range queued {
		if c.queuedAt.Before(cutoff) {
			expired = append(expired, c)
			continue
		}
		keep = append(keep, c)
	}
	if len(keep) == 0 {
		delete(s.diagCommands, userID)
	} else {
		s.diagCommands[userID] = keep
	}

	// One per heartbeat: the client's 512-byte read has to hold this and a shutdown warning.
	if len(keep) > 1 {
		keep = keep[:1]
	}
	return keep, expired
}

// ackDiagnosticsCommand removes an acknowledged command and returns it, or nil if it was already
// acked or expired. Returning it lets the caller audit the delivery with the original requester.
func (s *Server) ackDiagnosticsCommand(userID, id string) *diagnosticsCommand {
	if userID == "" || id == "" {
		return nil
	}
	s.diagCommandsMu.Lock()
	defer s.diagCommandsMu.Unlock()

	queued := s.diagCommands[userID]
	for i, c := range queued {
		if c.ID != id {
			continue
		}
		rest := append(queued[:i:i], queued[i+1:]...)
		if len(rest) == 0 {
			delete(s.diagCommands, userID)
		} else {
			s.diagCommands[userID] = rest
		}
		return c
	}
	return nil
}

// hasPendingDiagnosticsCommand reports whether a user already has one outstanding, so the admin
// endpoint can say "already requested" instead of queueing a duplicate.
func (s *Server) hasPendingDiagnosticsCommand(userID string) bool {
	s.diagCommandsMu.Lock()
	defer s.diagCommandsMu.Unlock()
	return len(s.diagCommands[userID]) > 0
}

// newDiagnosticsCommandID returns a short random id. Short because of the 512-byte budget, random
// because it is the client's dedupe key across at-least-once delivery. It is not a secret: it
// authorises nothing on its own, and the session token is what proves who is asking.
func newDiagnosticsCommandID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Failing closed here would refuse a support request because the OS RNG hiccuped. The
		// timestamp is unique enough for a dedupe key with a five-minute TTL.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000")))[:12]
	}
	return hex.EncodeToString(b)
}

// diagnosticsCommandsForSession returns the commands to attach to this heartbeat's response.
//
// Consent is re-checked here, at delivery, as well as at the authorisation point. That is not
// belt-and-braces: queueing introduced a window #1696's design did not have, where a request
// authorised at T could be delivered at T+n after the user withdrew at T+1. Re-checking closes
// it, and means the guarantee the feature makes -- "a withdrawal is honoured from the moment it
// is made" -- survives the transport being asynchronous.
//
// The database read only happens when something is actually queued, which is close to never.
// Reading a user on every heartbeat would be a query per client per five seconds.
func (s *Server) diagnosticsCommandsForSession(leases []*TunnelLease, r *http.Request) []*diagnosticsCommand {
	userID := ""
	for _, l := range leases {
		if l != nil && l.UserID != "" {
			userID = l.UserID
			break
		}
	}
	if userID == "" {
		return nil
	}

	live, expired := s.pendingDiagnosticsCommands(userID)

	for _, c := range expired {
		// Audited rather than dropped: a collection that silently never happened looks
		// exactly like one that did, which is the failure #1824 is named for.
		s.auditDiagnostics(c.requestedBy, diagnosticsAuditExpired, "user", c.userID,
			fmt.Sprintf("Collection request expired undelivered after %s; the client did not acknowledge it", diagnosticsCommandTTL), r)
	}
	if len(live) == 0 {
		return nil
	}

	if s.db != nil {
		target, err := s.db.GetUser(userID)
		if err != nil || target == nil || !diagnosticsCollectionAllowed(target) {
			// Consent withdrawn (or unreadable) since the request. Drop the command and say
			// so, rather than delivering it or leaving it to expire quietly.
			for _, c := range live {
				s.ackDiagnosticsCommand(c.userID, c.ID)
				s.auditDiagnostics(c.requestedBy, diagnosticsAuditRefused, "user", c.userID,
					"Queued collection dropped before delivery: consent is no longer in place", r)
			}
			return nil
		}
	}
	return live
}

// recordDiagnosticsAcks turns a client's acknowledgement into the delivery audit entry.
func (s *Server) recordDiagnosticsAcks(leases []*TunnelLease, ids []string, r *http.Request) {
	userID := ""
	for _, l := range leases {
		if l != nil && l.UserID != "" {
			userID = l.UserID
			break
		}
	}
	if userID == "" {
		return
	}
	// Bounded: the ack list arrives from a client, and one heartbeat can only ever have been
	// handed one command.
	if len(ids) > 4 {
		ids = ids[:4]
	}
	for _, id := range ids {
		cmd := s.ackDiagnosticsCommand(userID, id)
		if cmd == nil {
			// Already acked, expired, or never ours. Not an error: at-least-once delivery
			// means a client can legitimately ack the same id twice.
			continue
		}
		s.auditDiagnostics(cmd.requestedBy, diagnosticsAuditDelivered, "user", cmd.userID,
			fmt.Sprintf("Client acknowledged collection request %s", cmd.ID), r)
	}
}
