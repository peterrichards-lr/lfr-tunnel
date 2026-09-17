package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Getting a collection request to an EDGE-served client, and its result back (#1991).
//
// #1763 made the heartbeat the carrier, and that is still the only way to reach a client: it
// sits behind NAT and a firewall, and only the SERVING gateway's heartbeat body is read
// (interceptor.go's `pingURL == serverURL`). What #1763 could not do was reach a client whose
// serving gateway is an edge. s.diagCommands is an in-memory map on whichever gateway handled
// the admin request, and that is always central -- so an edge's diagnosticsCommandsForSession
// always returned nil, and diagnosticsReachability told the admin so rather than queueing into
// a void.
//
// This is the missing hop, in the shape #1960 established for the node set: central decides,
// the EXISTING edge control channel carries it, the edge applies it. Three things travel, and
// the direction of each is the design:
//
//  1. DOWN, to ONE edge (SendEdgeSchedule's shape, not BroadcastNodeSet's): the request id, the
//     user, and how long the edge may keep offering it.
//  2. UP, on the same connection: the client's acknowledgement. #1960 needed nothing in this
//     direction; this does, because diagnosticsAuditDelivered is the evidence that a collection
//     actually reached somebody and the audit log lives on central. An edge has no database, so
//     an ack it could not relay would be an ack that never happened. Reported the way
//     edge_metrics.go reports byte deltas -- same connection, never a second one.
//  3. UP, over HTTP: the collected bundle itself. Not on the control channel, because a bundle
//     is up to 6 MB and edgeControlReadLimit is 128 KB -- a frame over the limit makes gorilla
//     close the connection, and telemetry must never be able to drop a tunnel. It rides
//     /api/internal/edge-diagnostics with the edge token, which is what
//     /api/internal/edge-audit-log already does for exactly the same reason: something measured
//     at the edge that only central can store.
//
// WHAT IS DELIBERATELY NOT HERE: a command name on the wire. diagnostics_transport.go states
// why collect_logs is the only command -- "a general 'run this' channel to every client is a
// foothold, and an admin account is not a safe place to put one" -- and a forwarding frame
// carrying an arbitrary verb would be that channel, one field at a time. The frame type IS the
// verb. The edge synthesises diagnosticsCommandCollectLogs locally and there is no field an
// operator, an admin or a compromised control plane could put a second command in.
//
// The request id is the capability throughout. It is 48 random bits with a five-minute life,
// central issues it, and every path up -- the ack frame and the bundle upload -- is authorised
// by presenting it rather than by naming a user. So an edge cannot dequeue or satisfy a
// collection for a user it does not serve without guessing the id, and nothing up here has to
// be trusted to name the right user.

const (
	// diagnosticsCollectFrameType carries one collection request from central down to the
	// single edge serving that user. Named once so the sender, the edge's switch and the
	// tests cannot drift apart over a string literal -- the #1245 failure, where central sent
	// a frame every edge logged as unknown for weeks.
	diagnosticsCollectFrameType = "diagnostics_collect"

	// diagnosticsAckFrameType carries the client's acknowledgement back up. The second frame
	// on this channel to travel upwards, after edge_metrics.
	diagnosticsAckFrameType = "diagnostics_ack"

	// diagnosticsAckGrace is how long past a command's deadline central waits before
	// recording it undelivered.
	//
	// It exists because the deadline and the evidence of delivery move at different speeds.
	// The edge stops offering a command at the deadline central gave it; a client acks on its
	// next heartbeat, up to five seconds later, and the edge relays that up the control
	// channel. Without the grace, a command handed over in the last moments of its window
	// would be audited "expired undelivered" while its ack was still in flight -- which is
	// precisely the wrong entry, since it says a collection did not happen when it did.
	diagnosticsAckGrace = 30 * time.Second

	// diagnosticsAckBuffer is how many relayed acknowledgements central will hold while the
	// tracked worker gets to them.
	//
	// Small on purpose. One ack exists per outstanding collection request, a request is a
	// deliberate admin action with a five-minute life, and the worker does one audit write per
	// ack -- so a full buffer means something is wrong rather than busy. Dropping is then the
	// right answer and is logged: the alternative is blocking the control channel's read pump,
	// which would stall every other frame on that connection, including the byte deltas.
	diagnosticsAckBuffer = 64

	// errInvalidEdgeToken is the body a relay gets when its token is not one this gateway
	// knows. Named because goconst counts this literal's five package-wide occurrences
	// against the newest file, which is this one -- the four older sites in server.go still
	// spell it inline and are deliberately left alone here (#1655: nothing became more
	// duplicated, the count simply acquired a new home).
	errInvalidEdgeToken = "invalid edge token"

	// diagnosticsExpirySweepInterval is how often central looks for commands that ran out.
	//
	// A sweep is needed at all because the lazy expiry in pendingDiagnosticsCommands only
	// runs when that user's client heartbeats THIS gateway, and an edge-served client's
	// heartbeat never reaches central's queue. Left to that path alone, a forwarded request
	// that was never picked up would sit in central's map forever: never delivered, never
	// audited expired, and still blocking the admin's next attempt with "already requested".
	diagnosticsExpirySweepInterval = 30 * time.Second
)

// isEdgeNode reports whether this gateway is an edge.
//
// The same pair of settings that decides it everywhere else in this package
// (newEdgeMetricsReporterFor, resolveRemoteRouteForHost, server.go's registry.SetNodeID), so
// there is one answer to "is this an edge" rather than a second, subtly different one.
func (s *Server) isEdgeNode() bool {
	return s.cfg != nil && s.cfg.ControlPlaneURL != "" && s.cfg.EdgeToken != ""
}

// SendEdgeDiagnosticsCollect forwards one collection request to the edge serving a user, and
// reports whether it reached a live control connection.
//
// Shaped on SendEdgeSchedule rather than on BroadcastNodeSet: this is addressed to one node,
// and sending it to every edge would hand a user's request id to gateways that do not serve
// them. The return value is the point of difference from both -- a schedule that misses is
// re-sent at the next handshake and converges on its own, whereas this has a five-minute life
// and an admin waiting on it, so the caller has to be able to say "not sent" out loud instead
// of reporting a queued request that went nowhere.
func (s *Server) SendEdgeDiagnosticsCollect(nodeID, userID, requestID string, expiresIn time.Duration) bool {
	if nodeID == "" || userID == "" || requestID == "" {
		return false
	}
	seconds := int(expiresIn / time.Second)
	if seconds <= 0 {
		return false
	}

	// No command name on the wire. See the file comment: the frame type is the verb, and the
	// edge synthesises the one command that exists.
	payload, err := json.Marshal(ControlMessage{
		Type:              diagnosticsCollectFrameType,
		UserID:            userID,
		DiagRequestID:     requestID,
		DiagExpiresInSecs: seconds,
	})
	if err != nil {
		slog.Error(fmt.Sprintf("[Edge WS] Could not encode the collection request for %s: %v", nodeID, err))
		return false
	}

	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()
	if !exists {
		slog.Warn(fmt.Sprintf("[Edge WS] Collection request %s not sent: %s has no control connection", requestID, nodeID))
		return false
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		slog.Warn(fmt.Sprintf("[Edge WS] Collection request %s to %s failed to send: %v", requestID, nodeID, err))
		return false
	}
	slog.Info(fmt.Sprintf("[Edge WS] Forwarded collection request %s to %s, valid for %ds", requestID, nodeID, seconds))
	return true
}

// queueForwardedDiagnosticsCollect is the edge's half: it queues what central forwarded so the
// edge's own heartbeat hands it to the client.
//
// The command TYPE is not read from the frame because it is not on the frame. This is where the
// single permitted command is synthesised, so widening what an edge can be told to run would
// take a deliberate change here rather than a new value in a field.
//
// requestedBy is deliberately left empty. The admin's identity belongs in the audit log, the
// audit log is on central, and an edge has no use for it -- spreading it to four regions to be
// carried back unchanged would be data movement with no reader.
func (s *Server) queueForwardedDiagnosticsCollect(userID, requestID string, expiresIn time.Duration) {
	if userID == "" || requestID == "" {
		slog.Info("[Edge Control] Ignoring a collection request with no user or no request id")
		return
	}
	// Clamped to the one TTL this feature has. Expiry is central's decision -- the edge is
	// told what remains of the window rather than starting a clock of its own -- but an edge
	// must not be able to be told to hold somebody's collection request open for a week, so
	// the constant bounds what the wire can ask for.
	if expiresIn <= 0 || expiresIn > diagnosticsCommandTTL {
		expiresIn = diagnosticsCommandTTL
	}

	now := time.Now().UTC()
	cmd := &diagnosticsCommand{
		ID:        requestID,
		Type:      diagnosticsCommandCollectLogs,
		userID:    userID,
		queuedAt:  now,
		expiresAt: now.Add(expiresIn),
	}

	s.diagCommandsMu.Lock()
	if s.diagCommands == nil {
		s.diagCommands = make(map[string][]*diagnosticsCommand)
	}
	// One outstanding command per user, the same rule queueDiagnosticsCollect applies on
	// central: a second request replaces the first rather than queueing behind it.
	s.diagCommands[userID] = []*diagnosticsCommand{cmd}
	s.diagCommandsMu.Unlock()

	slog.Info(fmt.Sprintf("[Edge Control] Holding collection request %s for a client on this node, for up to %s", requestID, expiresIn))
}

// reportDiagnosticsAckUpstream tells central that a client on this edge acknowledged a request,
// reporting whether the frame actually went out.
//
// The caller drops the command only on a true return. A failure here has to leave the command
// queued: the client acks on every arrival, so an unreported ack is retried on the next
// heartbeat, and the alternative -- dropping it and hoping -- ends with central auditing
// "expired undelivered" for a collection the client definitely received.
func (s *Server) reportDiagnosticsAckUpstream(requestID string) bool {
	if requestID == "" {
		return false
	}
	s.edgeUplinkMu.RLock()
	conn := s.edgeUplink
	s.edgeUplinkMu.RUnlock()
	if conn == nil {
		slog.Info(fmt.Sprintf("[Edge Control] Cannot report the acknowledgement of %s yet: no control connection. Keeping it for the next heartbeat.", requestID))
		return false
	}
	// No user id on the wire: central resolves the user from the request id it issued, so an
	// edge never names whose command it is acknowledging.
	if err := conn.WriteJSON(ControlMessage{Type: diagnosticsAckFrameType, DiagRequestID: requestID}); err != nil {
		slog.Info(fmt.Sprintf("[Edge Control] Failed to report the acknowledgement of %s, keeping it for the next heartbeat: %v", requestID, err))
		return false
	}
	slog.Info(fmt.Sprintf("[Edge Control] Reported that a client acknowledged collection request %s", requestID))
	return true
}

// diagnosticsAck is one acknowledgement an edge relayed, on its way from the control
// channel's read pump to the worker that writes the audit entry (#1991).
type diagnosticsAck struct {
	// nodeID is the connection's authenticated identity, captured at receipt rather than
	// resolved later, so the entry names the edge that actually relayed it.
	nodeID    string
	requestID string
}

// queueForwardedDiagnosticsAck hands an ack to the tracked worker, from the read pump.
//
// It must not touch the database itself. The read pump is a bare `go` on a connection that
// outlives no particular request and is not counted by bgWG, so Stop can cancel, wait and close
// the database while it is still running -- #1833, which
// TestNoUntrackedGoroutineReachesTheDatabase gates. The metrics frame arriving on the same pump
// already queues rather than writes, for the same reason.
//
// Non-blocking, because the pump reads every frame on that connection: blocking here to wait for
// an audit write would stall the byte deltas behind it.
func (s *Server) queueForwardedDiagnosticsAck(nodeID, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || s.diagAcks == nil {
		return
	}
	select {
	case s.diagAcks <- diagnosticsAck{nodeID: nodeID, requestID: requestID}:
	default:
		// Logged rather than swallowed: the entry this drops is the evidence a collection
		// reached somebody, so its absence would otherwise look like a client that never
		// acknowledged.
		slog.Warn(fmt.Sprintf("[Edge WS] Dropped the acknowledgement of %s relayed by %s: %d are already waiting to be recorded",
			requestID, nodeID, diagnosticsAckBuffer))
	}
}

// recordForwardedDiagnosticsAck is central's half: an edge has relayed a client's ack, so the
// delivery audit entry gets written here, where the log is.
//
// nodeID is the connection's authenticated identity rather than anything the frame claims --
// the same rule the metrics frame follows -- and it is used only to say who relayed it. The
// request id is what authorises the claim: it is unguessable, central issued it, and it was
// only ever sent to the one edge serving that user. An edge cannot use this to dismiss a
// collection for somebody else's user without already knowing an id it was never told.
func (s *Server) recordForwardedDiagnosticsAck(nodeID, requestID string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	cmd := s.claimDiagnosticsCommandByID(requestID)
	if cmd == nil {
		// Already satisfied -- by the upload, which normally beats the ack -- or expired, or
		// never ours. At-least-once delivery means an id can legitimately be acked twice.
		return
	}
	s.auditDiagnostics(cmd.requestedBy, diagnosticsAuditDelivered, "user", cmd.userID,
		fmt.Sprintf("Client acknowledged collection request %s, relayed by edge node %s", cmd.ID, nodeID), nil)
}

// postDiagnosticsBundleToControlPlane relays an uploaded bundle from an edge to central, which
// is the only node with somewhere to put it.
//
// The client's bytes are passed through unchanged rather than decoded and re-encoded: the edge
// has no reason to look inside somebody's logs, and re-serialising several megabytes to add
// nothing would be work done for no reader.
func (s *Server) postDiagnosticsBundleToControlPlane(body []byte) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(s.cfg.ControlPlaneURL, "/")+"/api/internal/edge-diagnostics", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	// Longer than the 5s the register proxy uses: this is up to 6 MB rather than a few
	// hundred bytes, and the client is already waiting on a 60s timeout of its own.
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	// Bounded: this is a remote response, and the caller only passes it back to the client.
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, answer, nil
}

// handleEdgeDiagnosticsUpload accepts a bundle an edge has relayed on a client's behalf.
//
// The edge token authenticates the relay; the REQUEST ID authorises the upload, exactly as it
// does on the direct path (diagnostics_store.go). Central looks the id up in its own queue and
// takes the user from what it finds, so a relayed upload cannot name a user, cannot be filed
// against somebody who was never asked, and cannot arrive for a collection nobody requested.
func (s *Server) handleEdgeDiagnosticsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if _, authorised := s.authorisedEdgeNode(r.Header.Get("X-Edge-Token")); !authorised {
		// apiError rather than a map literal, for the same reason the message is a constant:
		// the "error" key has 93 package-wide occurrences and goconst attributes them to the
		// newest file (#1655).
		respondJSON(w, http.StatusUnauthorized, apiError{Error: errInvalidEdgeToken})
		return
	}
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	var payload bundleUpload
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBundleUploadBytes)).Decode(&payload); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	cmd := s.claimDiagnosticsCommandByID(strings.TrimSpace(payload.RequestID))
	if cmd == nil {
		http.Error(w, `{"error":"No collection was requested, or it has already been satisfied"}`, http.StatusForbidden)
		return
	}
	s.storeDiagnosticsBundles(w, r, cmd, payload, "relayed by an edge node")
}

// watchDiagnosticsExpiry retires collection requests nobody picked up (#1991).
//
// A sweep, for the same reason watchEdgeMetricsDelivery is one: the event is an ABSENCE. Before
// this, expiry only happened when the user's client heartbeated the gateway holding the queue,
// and a forwarded request is held by central while the heartbeats go to an edge -- so nothing
// would ever have retired one.
func (s *Server) watchDiagnosticsExpiry(ctx context.Context) {
	ticker := time.NewTicker(diagnosticsExpirySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ack := <-s.diagAcks:
			// Written here rather than on the read pump that received it: this goroutine is
			// tracked, so Stop waits for it before closing the database.
			s.recordForwardedDiagnosticsAck(ack.nodeID, ack.requestID)
		case <-ticker.C:
			s.sweepExpiredDiagnosticsCommands(time.Now().UTC())
		}
	}
}

// sweepExpiredDiagnosticsCommands is the body of the watch, separated so a test can drive it
// with an explicit clock instead of waiting on a ticker (the shape sweepEdgeMetricsDelivery
// already uses).
//
// The grace is what keeps ONE clock authoritative. The deadline is set once, by central, when
// the request is made; the edge is told what remains of it and never computes its own. All this
// adds is a wait for evidence that was already in flight -- not a second opinion about when the
// window closed.
func (s *Server) sweepExpiredDiagnosticsCommands(now time.Time) {
	s.diagCommandsMu.Lock()
	var expired []*diagnosticsCommand
	for userID, queued := range s.diagCommands {
		var keep []*diagnosticsCommand
		for _, c := range queued {
			if now.After(c.deadline().Add(diagnosticsAckGrace)) {
				expired = append(expired, c)
				continue
			}
			keep = append(keep, c)
		}
		if len(keep) == 0 {
			delete(s.diagCommands, userID)
			continue
		}
		s.diagCommands[userID] = keep
	}
	s.diagCommandsMu.Unlock()

	for _, c := range expired {
		// Audited rather than dropped. A collection that silently never happened looks
		// exactly like one that did, and on this path -- an edge-served user -- there is no
		// heartbeat arriving here to notice it any other way.
		s.auditDiagnostics(c.requestedBy, diagnosticsAuditExpired, "user", c.userID,
			fmt.Sprintf("Collection request expired undelivered after %s; the client did not acknowledge it", diagnosticsCommandTTL), nil)
	}
}

// relayDiagnosticsUpload is an EDGE accepting a client's bundle and passing it to central
// (#1991).
//
// The edge is where the upload arrives -- the client posts to whichever gateway serves it -- and
// central is the only node with a store. Before this the edge answered 501 and the client logged
// an upload failure, so an edge-served collection reached the client, was collected and redacted,
// and then evaporated. An admin saw a request that produced nothing, which is indistinguishable
// from a client that ignored it.
//
// The same two-key authorisation as the direct path: the session token says who is uploading,
// and the request id says the gateway asked for it. Both are checked HERE, on the node that
// holds the lease and the command, before a byte crosses to central.
func (s *Server) relayDiagnosticsUpload(w http.ResponseWriter, r *http.Request) {
	sessionToken := strings.TrimSpace(r.Header.Get("X-Session-Token"))
	if sessionToken == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	userID := ""
	for _, l := range s.registry.GetSessionLeases(sessionToken) {
		if l != nil && l.UserID != "" {
			userID = l.UserID
			break
		}
	}
	if userID == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Read whole rather than streamed, and relayed byte for byte. The edge has no reason to
	// look inside somebody's logs beyond finding the request id, and re-encoding several
	// megabytes to change nothing would be work with no reader.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBundleUploadBytes))
	if err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}
	var envelope struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	// The command is the authorisation, exactly as on the direct path, and claiming it here
	// makes the relay idempotent: a client retrying after a timeout finds it already
	// satisfied rather than sending the same logs twice.
	cmd := s.ackDiagnosticsCommand(userID, strings.TrimSpace(envelope.RequestID))
	if cmd == nil {
		http.Error(w, `{"error":"No collection was requested, or it has already been satisfied"}`, http.StatusForbidden)
		return
	}

	status, answer, err := s.postDiagnosticsBundleToControlPlane(body)

	// A transient failure and a refusal are different things, and conflating them is how a
	// feature ends up retrying something that can never succeed -- or dropping something that
	// would have worked on the next attempt.
	if err != nil || status >= http.StatusInternalServerError {
		// Transient: the control plane is unreachable or broken. Put the command back, with
		// its ORIGINAL deadline. The collection has not happened, and a command that is gone
		// cannot be retried -- the client would be waiting on an upload nobody asks for
		// again, and central would audit the request expired undelivered when it had in fact
		// been delivered and then dropped here.
		s.requeueDiagnosticsCommand(cmd)
		if err != nil {
			slog.Warn(fmt.Sprintf("[Diagnostics] Could not relay collection %s to the control plane, keeping the request open: %v", cmd.ID, err))
		} else {
			slog.Warn(fmt.Sprintf("[Diagnostics] The control plane answered HTTP %d for collection %s; keeping the request open", status, cmd.ID))
		}
		http.Error(w, `{"error":"The control plane could not accept the upload"}`, http.StatusBadGateway)
		return
	}
	if status != http.StatusOK {
		// A refusal, and it is central's to make -- withdrawn consent is the one that
		// matters, and an edge has no database to evaluate it with. Passed through
		// unchanged and NOT requeued: retrying cannot change the answer, and holding the
		// request open would keep offering the client a collection that will be refused
		// every time. The client logs the status it was given, which is the real one.
		slog.Info(fmt.Sprintf("[Diagnostics] The control plane refused collection %s with HTTP %d; the request is closed", cmd.ID, status))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, werr := w.Write(answer); werr != nil {
			slog.Debug(fmt.Sprintf("[Diagnostics] Could not write the relayed refusal for %s: %v", cmd.ID, werr))
		}
		return
	}

	slog.Info(fmt.Sprintf("[Diagnostics] Relayed collection %s to the control plane (%d bytes)", cmd.ID, len(body)))
	// Central's own answer, passed back unchanged. The alternative -- this node composing a
	// bundleUploadResult of its own -- would mean reporting a stored count and a retention
	// period it never observed, and a relay that invents its reply is a relay that can be
	// wrong about whether anything was kept.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(answer); err != nil {
		slog.Debug(fmt.Sprintf("[Diagnostics] Could not write the relayed reply for %s: %v", cmd.ID, err))
	}
}
