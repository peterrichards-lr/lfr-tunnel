package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"

	"github.com/gorilla/websocket"
	"sync"
)

// edgeTunableMu guards the four tunables below (#1370).
//
// They are plain vars that only tests ever write -- but the write races a live read, and no
// amount of teardown ordering in the test fixes it. handleEdgeControlWS reads them AFTER
// upgrading the connection, and httptest.Server.Close() stops tracking a connection once it
// is hijacked, so Close() does not wait for that handler and establishes no happens-before
// with the test's deferred restore.
//
// Hoisting the read out of the spawned goroutine (the fix #1328 used in pkg/client) is not
// sufficient here for that reason: it moved the read from one untracked goroutine to another.
// Synchronising the access is what actually removes it.
//
// All four are guarded, not just the two the issue listed. edgeClientReadDeadline was first
// left alone on the grounds that runEdgeControlChannel runs under Server.bgWG, so Stop() waits
// for it -- but the read sits in its PongHandler CLOSURE, which gorilla/websocket invokes from
// the inner reader goroutine, and that one is not under bgWG. "The enclosing function is
// tracked" is not the same claim as "this read is tracked".
var edgeTunableMu sync.RWMutex

// edgeControlReadDeadline is how long the control plane waits for any frame
// (data or Ping) from an edge before considering the connection dead. Overridable
// in tests so the reconnect-loop fix can be verified without a real 60s wait.
// Read via edgeTunables(); written only by tests, via setEdgeControlReadDeadline.
var edgeControlReadDeadline = 60 * time.Second

// edgeClientReadDeadline is the mirror-image of edgeControlReadDeadline for the
// edge's own outbound connection: how long an edge waits for any frame (data or
// Pong) from the control plane before considering the connection dead. Overridable
// in tests for the same reason. See #911.
var edgeClientReadDeadline = 75 * time.Second

// edgeClientPingInterval is how often an edge sends a keepalive Ping to the control
// plane on its outbound connection. Overridable in tests so edgeClientReadDeadline's
// fix can be verified without a real 30s+ wait.
var edgeClientPingInterval = 30 * time.Second

// edgeHealthPingInterval is how often the control plane sends its OWN Ping to each
// connected edge, purely to time the Pong for RTT (see handleEdgeControlWS's PongHandler
// and #976) -- independent of edgeClientPingInterval above, which is the edge's existing
// keepalive in the other direction and was never used for timing. Overridable in tests.
// Read via edgeTunables(); written only by tests, via setEdgeHealthPingInterval.
var edgeHealthPingInterval = 20 * time.Second

// edgeTunables reads the control-plane side's guarded values together. One call per connection,
// at the top of the connection's lifetime, so the values stay fixed for that connection --
// which is what the code already assumed and is why a single read site is enough.
func edgeTunables() (readDeadline, healthPingInterval time.Duration) {
	edgeTunableMu.RLock()
	defer edgeTunableMu.RUnlock()
	return edgeControlReadDeadline, edgeHealthPingInterval
}

// edgeClientTunables is edgeTunables for the edge's own outbound connection.
func edgeClientTunables() (readDeadline, pingInterval time.Duration) {
	edgeTunableMu.RLock()
	defer edgeTunableMu.RUnlock()
	return edgeClientReadDeadline, edgeClientPingInterval
}

// nodeSchedule is an edge's own stop/start window as told to it by central (#1276).
// Enabled false means the node is not on a schedule and the times carry no meaning.
type nodeSchedule struct {
	Enabled   bool
	StopTime  string // "HH:MM" in Timezone
	StartTime string // "HH:MM" in Timezone
	Timezone  string // IANA name, e.g. "Asia/Kolkata"
}

// ControlMessage represents the JSON schema for websocket communication.
type ControlMessage struct {
	Type     string `json:"type"`
	Nonce    string `json:"nonce,omitempty"`
	Response string `json:"response,omitempty"`
	IP       string `json:"ip,omitempty"`
	// BanExpiresAt carries when a blacklist entry lapses, so an edge expires it in step with
	// the control plane (#1353). Absent means the ban does not expire, which is what a manual
	// ban by an admin is -- and what every ban was before this existed, so an older edge that
	// ignores this field simply keeps the previous behaviour.
	BanExpiresAt *time.Time `json:"ban_expires_at,omitempty"`
	NodeID       string     `json:"node_id,omitempty"`
	Action       string     `json:"action,omitempty"`
	// Access control pushed to edges when the portal changes it (#1329). An edge has no
	// database, so being told is the only way an edit reaches it -- otherwise turning a
	// passcode on would leave every edge serving the tunnel unprotected until the client
	// reconnected (#1367).
	Passcode         string            `json:"passcode,omitempty"`
	WhitelistIPs     string            `json:"whitelist_ips,omitempty"`
	AccessMode       string            `json:"access_mode,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	Duration         int               `json:"duration,omitempty"`
	UserID           string            `json:"user_id,omitempty"`
	Subdomain        string            `json:"subdomain,omitempty"`
	FullHost         string            `json:"full_host,omitempty"`
	TargetURL        string            `json:"target_url,omitempty"`
	SecondsRemaining int               `json:"seconds_remaining,omitempty"`
	ShutdownAt       int64             `json:"shutdown_at,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	// Schedule fields, carried by the "node_schedule" frame (#1276). ScheduleEnabled has no
	// omitempty: false is meaningful here -- it is how central tells a node it has come off
	// the rota, and dropping it would leave the edge believing its last known schedule.
	ScheduleEnabled   bool   `json:"schedule_enabled"`
	ScheduleStopTime  string `json:"schedule_stop_time,omitempty"`
	ScheduleStartTime string `json:"schedule_start_time,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	// Metrics carries byte deltas UPWARDS, from an edge to the control plane (#1958) --
	// the first field on this message that travels in that direction. An edge has no
	// database, so this is the only way traffic it served reaches tunnel_metrics. See
	// edge_metrics.go for why it rides this channel rather than a connection of its own.
	Metrics []EdgeByteDelta `json:"metrics,omitempty"`
	// NodeSet carries central's node-set fingerprint down to every edge (#1960), so an edge
	// can echo it on its own clients' heartbeats. omitempty is safe here, unlike on
	// ScheduleEnabled above: a string decodes to "" whether it was sent empty or omitted, so
	// an edge told the empty fingerprint clears what it was holding either way.
	NodeSet string `json:"node_set,omitempty"`
	// RateLimit carries the requests-per-second a bandwidth-quota throttle holds a user's
	// tunnels to (#1959), on the "quota_enforcement" frame below. Central is the counting
	// authority -- an edge has no database -- so an edge learns a user is over their
	// allowance only by being told.
	RateLimit int `json:"rate_limit,omitempty"`
	// Diagnostics fields (#1991). DiagRequestID travels in BOTH directions -- down on
	// diagnostics_collect, back up on diagnostics_ack -- and is the capability that
	// authorises everything it appears on, so it is the one field both frames share.
	//
	// There is deliberately no field naming the command to run. collect_logs is the only
	// command there is (diagnostics_transport.go), the frame type is the verb, and an edge
	// synthesises it locally: a forwarding frame with a verb field would be the general
	// "run this on every client" channel that file exists to refuse.
	DiagRequestID string `json:"diag_request_id,omitempty"`
	// DiagExpiresInSecs is what REMAINS of the window central opened, not a TTL for the edge
	// to start. One clock, held by the node that issued the request.
	DiagExpiresInSecs int `json:"diag_expires_in,omitempty"`
}

// nodeSetFrameType is the control-channel frame that carries central's roster fingerprint
// down to the edges. Named separately so the sender, the edge's switch and the tests cannot
// drift apart over a string literal.
const nodeSetFrameType = "node_set"

// handleEdgeControlWS handles control plane WebSocket connections from Edge nodes.
func (s *Server) handleEdgeControlWS(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		http.Error(w, "missing node_id", http.StatusBadRequest)
		return
	}
	version := r.URL.Query().Get("version")

	// The edge's address, resolved through the same trust boundary as every other client
	// address (#1818). This used to read X-Real-IP directly and, failing that, take the
	// LEFTMOST X-Forwarded-For entry -- which is the caller-supplied end, because nginx's
	// $proxy_add_x_forwarded_for appends. So any caller able to reach this endpoint could
	// name the address recorded for a node. clientIPFrom walks the list right-to-left and
	// only consults either header when the immediate peer is in trusted_proxies; otherwise
	// it returns the peer, which is the same fallback the old `else` branch had.
	//
	// No behaviour change for this deployment: nginx proxies to 127.0.0.1, which is the
	// default trusted set, so X-Real-IP is honoured exactly as before. What changes is a
	// request arriving from an UNTRUSTED peer, where the header is now ignored rather than
	// believed -- and edgeIPs is not merely displayed, it is a routing target
	// (server.go returns "http://" + s.edgeIPs[nodeID]).
	clientIP := s.clientIP(r)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to upgrade WebSocket: %v", err))
		return
	}

	// 1. Generate challenge nonce
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce) //nolint:errcheck
	nonceStr := hex.EncodeToString(nonce)

	challenge := ControlMessage{
		Type:  "challenge",
		Nonce: nonceStr,
	}

	if err := conn.WriteJSON(challenge); err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to send challenge to %s: %v", nodeID, err))
		_ = conn.Close() //nolint:errcheck
		return
	}

	// 2. Wait for auth response
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	var authMsg ControlMessage
	if err := conn.ReadJSON(&authMsg); err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to read auth message from %s: %v", nodeID, err))
		_ = conn.Close() //nolint:errcheck
		return
	}

	if authMsg.Type != "auth" {
		slog.Info(fmt.Sprintf("[Edge WS] Expected auth message from %s, got %s", nodeID, authMsg.Type))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unexpected message type"}) //nolint:errcheck
		_ = conn.Close()                                                                           //nolint:errcheck
		return
	}

	// 3. Verify HMAC response
	var nodeConfig *config.EdgeNodeConfig
	for _, node := range s.edgeNodes() {
		if node.ID == nodeID {
			nodeConfig = &node
			break
		}
	}

	if nodeConfig == nil {
		slog.Info(fmt.Sprintf("[Edge WS] Unknown edge node ID: %s", nodeID))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unknown node_id"}) //nolint:errcheck
		_ = conn.Close()                                                                   //nolint:errcheck
		return
	}

	// The token hash is the HMAC KEY here, not a value to compare, so a rotation has to be tried
	// against every accepted hash. Miss this and an edge presenting the incoming token passes
	// /api/internal/* and then silently fails to establish its control channel -- losing schedule
	// pushes, shutdown warnings and lease kicks, which is worse than not rotating at all (#1491).
	respMAC, err := hex.DecodeString(authMsg.Response)
	authOK := false
	if err == nil {
		for _, accepted := range nodeConfig.AcceptedTokenHashes() {
			keyBytes, decodeErr := hex.DecodeString(accepted)
			if decodeErr != nil {
				slog.Info(fmt.Sprintf("[Edge WS] Ignoring an unparseable token hash configured for %s", nodeID))
				continue
			}
			mac := hmac.New(sha256.New, keyBytes)
			mac.Write([]byte(nonceStr))
			// Every candidate is compared even once one has matched, so the time taken does not
			// report WHICH hash was the match -- during a rotation that would leak whether an
			// edge is still on the old token.
			if subtle.ConstantTimeCompare(respMAC, mac.Sum(nil)) == 1 {
				authOK = true
			}
		}
	}

	if !authOK {
		slog.Info(fmt.Sprintf("[Edge WS] HMAC verification failed for %s", nodeID))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "invalid signature"}) //nolint:errcheck
		_ = conn.Close()                                                                     //nolint:errcheck
		return
	}

	// Reset read deadline
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Authenticated! Register edge client
	s.edgeClientsMu.Lock()
	if oldConn, exists := s.edgeClients[nodeID]; exists {
		_ = oldConn.WriteJSON(ControlMessage{Type: "replaced", Reason: "new connection established"}) //nolint:errcheck
		_ = oldConn.Close()                                                                           //nolint:errcheck
	}
	registered := &safeConn{conn: conn}
	s.edgeClients[nodeID] = registered
	if version != "" {
		s.edgeVersions[nodeID] = version
	} else {
		s.edgeVersions[nodeID] = "Unknown"
	}
	s.edgeIPs[nodeID] = clientIP
	s.edgeClientsMu.Unlock()

	slog.Info(fmt.Sprintf("[Edge WS] Edge node %s successfully authenticated.", nodeID))
	// Written through the registered safeConn, not the raw conn. The moment the lock
	// above is released, broadcasts such as SendEdgeKickAll can take this node from
	// s.edgeClients and write to it; going direct to conn here would bypass
	// safeConn.mu and interleave two frames on the same socket (issue #1125).
	_ = registered.WriteJSON(ControlMessage{Type: "auth_success"}) //nolint:errcheck

	// Tell the node its own schedule as soon as it is registered, so a node that has just
	// restarted -- or has just been deployed to -- knows about its own downtime without
	// waiting for the next change (#1276). Sent from a goroutine because SendEdgeSchedule
	// takes edgeClientsMu, which this path has only just released, and because a slow write
	// must not hold up the read pump starting below.
	go func() {
		s.edgeHealthMu.RLock()
		h := s.edgeHealth[nodeID]
		s.edgeHealthMu.RUnlock()
		s.SendEdgeSchedule(nodeID, nodeSchedule{
			Enabled:   h.ScheduleEnabled,
			StopTime:  h.ScheduleStopTime,
			StartTime: h.ScheduleStartTime,
			Timezone:  h.Timezone,
		})
		// This node connecting IS a roster change -- it has just moved from
		// regions_unavailable to regions on /api/version -- so every edge is told, not
		// just this one. The broadcast reaches the node that just registered too, which
		// is what makes a restarted or freshly deployed edge current immediately rather
		// than at the next change (#1960), so no separate send is needed for it.
		s.BroadcastNodeSet()
	}()

	// pingStop signals the RTT-ping goroutine below to exit once the read pump's defer
	// runs -- it has no other way to notice the connection is gone, since it only ever
	// writes and never reads.
	pingStop := make(chan struct{})

	// One synchronised read for this connection, passed into the goroutines below (#1370).
	// See edgeTunableMu for why the lock is required and hoisting alone was not.
	//
	// Freezing the values per connection is not a behaviour change: nothing in production writes
	// either var, so there is no live update to miss.
	readDeadline, healthPingInterval := edgeTunables()

	// Start read pump to keep alive and detect disconnects
	go func() {
		defer func() {
			close(pingStop)
			s.edgeClientsMu.Lock()
			// Only tear down if this connection is still the registered one. A node that
			// reconnected already has a newer, live connection under the same ID, and
			// removing it here would strand a healthy node (the #1147 shape, control-channel
			// edition).
			wasActive := false
			if activeConn, exists := s.edgeClients[nodeID]; exists && activeConn.conn == conn {
				wasActive = true
				delete(s.edgeClients, nodeID)
				delete(s.edgeVersions, nodeID)
				delete(s.edgeIPs, nodeID)
				// Drop the reporting record with the connection (#1980), and only under the
				// same guard: a node that legitimately went away -- a scheduled power-off --
				// would otherwise keep its last frame time and read as "stalled" on its next
				// connect. A signal that cries wolf at every power window gets muted.
				s.edgeMetricsSeen.Forget(nodeID)
			}
			s.edgeClientsMu.Unlock()

			// The same guard has to cover the health write. It used to be unconditional, so
			// a superseded connection's cleanup marked the node Offline even though a newer
			// connection was live and registered -- and nothing writes "Online" on
			// registration, so that stale status stuck. Central logged
			// "successfully authenticated" and then reported the node Offline for eight and
			// a half minutes (#1271).
			if wasActive {
				s.edgeHealthMu.Lock()
				if h, exists := s.edgeHealth[nodeID]; exists {
					h.Status = "Offline"
					h.ErrorMessage = "Control connection disconnected"
					s.edgeHealth[nodeID] = h
				}
				s.edgeHealthMu.Unlock()
			}
			s.edgePingMu.Lock()
			delete(s.edgePingSentAt, nodeID)
			s.edgePingMu.Unlock()
			_ = conn.Close() //nolint:errcheck
			slog.Info(fmt.Sprintf("[Edge WS] Edge node %s disconnected.", nodeID))

			// A node dropping is a roster change for everyone still connected: it has
			// just moved from regions to regions_unavailable (#1960). Under the same
			// wasActive guard as the teardown above, because a superseded connection's
			// cleanup removed nothing and so changed no roster -- broadcasting there
			// would announce a change that did not happen on every reconnect.
			//
			// In a goroutine so a slow or dead peer cannot hold up this cleanup, which
			// still has the health write and the ping map behind it.
			if wasActive {
				go s.BroadcastNodeSet()
			}
		}()

		// Set read limit and pong handler. 512 bytes was ample while the only thing an edge
		// ever sent was its auth response; it now also reports byte deltas up this channel
		// (#1958), and a frame over the limit makes gorilla close the connection -- which
		// would let telemetry take down kicks, schedules and blacklist pushes with it. The
		// edge chunks its reports far below this bound; the bound stays finite so a
		// misbehaving node still cannot make us buffer without limit.
		conn.SetReadLimit(edgeControlReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline)) //nolint:errcheck
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(readDeadline)) //nolint:errcheck
			// Answers our own RTT-ping below, not the edge's keepalive Ping (that one
			// gets a Pong reply from the PingHandler further down, never from us
			// sending a Ping ourselves) -- see #976. A miss (no recorded send time,
			// e.g. a stray Pong right after reconnect) just leaves latency unchanged
			// rather than recording a bogus value.
			s.edgePingMu.Lock()
			sentAt, ok := s.edgePingSentAt[nodeID]
			s.edgePingMu.Unlock()
			if ok {
				s.updateEdgeLatencyFromPing(nodeID, time.Since(sentAt).Milliseconds())
			}
			return nil
		})

		// The edge sends its own Ping every 30s (runEdgeControlChannel), not a Pong,
		// so the PongHandler above never actually fires — nothing ever prompts the
		// edge to send a Pong back to us, and this server never sends a Ping of its
		// own either. Left with only the PongHandler as a reset mechanism, the
		// initial deadline set above is a one-shot timer that always expires,
		// forcing a disconnect/reconnect every ~60s regardless of how alive the
		// connection actually is. gorilla/websocket handles incoming Ping/Pong
		// frames internally inside ReadMessage/NextReader and never surfaces them
		// to the caller, so resetting the deadline only *after* ReadMessage returns
		// would not fire on those pings either — a custom PingHandler is what
		// actually observes them. Replicate the default handler's pong reply here
		// too, since registering a custom handler replaces it entirely.
		conn.SetPingHandler(func(appData string) error {
			_ = conn.SetReadDeadline(time.Now().Add(readDeadline)) //nolint:errcheck
			err := conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
			if err == websocket.ErrCloseSent {
				return nil
			} else if e, ok := err.(net.Error); ok && e.Temporary() { //nolint:staticcheck
				return nil
			}
			return err
		})

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			_ = conn.SetReadDeadline(time.Now().Add(readDeadline)) //nolint:errcheck

			// Inbound frames used to be discarded unread -- this channel only ever carried
			// traffic downwards. An edge now reports the bytes its leases carried up it
			// (#1958). Parsed best-effort on purpose: an unparseable or unknown frame is
			// ignored and the connection kept, because a telemetry problem must never cost
			// this node its kicks, schedules or blacklist updates.
			var inbound ControlMessage
			if err := json.Unmarshal(data, &inbound); err != nil {
				slog.Info(fmt.Sprintf("[Edge WS] Ignoring an unparseable frame from %s: %v", nodeID, err))
				continue
			}
			switch inbound.Type {
			case edgeMetricsFrameType:
				// nodeID is this connection's authenticated identity, not anything the
				// payload claims, so an edge cannot file its traffic under another node.
				//
				// Stamped BEFORE queueEdgeMetrics, which returns early on an empty batch:
				// the empty frame IS the liveness signal (#1980), so recording delivery
				// inside the queueing path would drop exactly the frames that prove an idle
				// edge is still alive.
				s.edgeMetricsSeen.Note(nodeID, len(inbound.Metrics), time.Now())
				s.queueEdgeMetrics(nodeID, inbound.Metrics)
			case diagnosticsAckFrameType:
				// A client on this edge acknowledged a collection request (#1991). The
				// delivery audit entry is written here because the audit log is here --
				// an edge has no database, so an ack it could not relay would be an ack
				// that never happened.
				s.recordForwardedDiagnosticsAck(nodeID, inbound.DiagRequestID)
			}
		}
	}()

	// Send our own periodic Ping so the PongHandler above has something to time (#976) --
	// the edge's existing keepalive Ping (runEdgeControlChannel, other direction) was never
	// usable for this since we only reply to it, we don't time it. WriteControl is safe to
	// call concurrently with the read pump's WriteControl (PongHandler, above) and with any
	// WriteJSON/WriteMessage via safeConn elsewhere -- gorilla/websocket exempts
	// WriteControl from its single-writer restriction.
	go func() {
		ticker := time.NewTicker(healthPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingStop:
				return
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.edgePingMu.Lock()
				s.edgePingSentAt[nodeID] = time.Now()
				s.edgePingMu.Unlock()
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()
}

// BroadcastAccessControlUpdate pushes changed access-control rules for a subdomain to every
// connected Edge node, so a portal edit takes effect there on the next request rather than on
// the client's next reconnect.
//
// Empty values are meaningful here -- clearing a passcode is an edit like any other -- so the
// message carries the subdomain in a field that is always sent, and the edge applies whatever
// it is given rather than merging.
func (s *Server) BroadcastAccessControlUpdate(subdomain, passcode, whitelistIPs, accessMode string) {
	msg := ControlMessage{
		Type:         "access_control_update",
		Subdomain:    subdomain,
		Passcode:     passcode,
		WhitelistIPs: whitelistIPs,
		AccessMode:   accessMode,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()
	for nodeID, conn := range s.edgeClients {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to push access control for %s to %s: %v", subdomain, nodeID, err))
		}
	}
}

// BroadcastBlacklistUpdate pushes an IP blacklist update to all connected Edge nodes.
func (s *Server) BroadcastBlacklistUpdate(action, ip string, expiresAt *time.Time) {
	msg := ControlMessage{
		Type:         "blacklist_update",
		Action:       action, // "add" or "remove"
		IP:           ip,
		BanExpiresAt: expiresAt,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	for _, conn := range s.edgeClients {
		_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
	}
}

// BroadcastMaintenance pushes a maintenance mode event to all connected Edge nodes.
func (s *Server) BroadcastMaintenance(action string, duration int, reason string) {
	msg := ControlMessage{
		Type:     "maintenance_trigger",
		Action:   action, // "enable" or "disable"
		Duration: duration,
		Reason:   reason,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	for _, conn := range s.edgeClients {
		_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
	}
}

// BroadcastRouteUpdate pushes a routing table update to all connected Edge nodes (issue #1249).
func (s *Server) BroadcastRouteUpdate(action, fullHost, targetURL, nodeID string) {
	msg := ControlMessage{
		Type:      "route_update",
		Action:    action, // "add" or "remove"
		FullHost:  fullHost,
		TargetURL: targetURL,
		NodeID:    nodeID,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	for _, conn := range s.edgeClients {
		_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
	}
}

// SendEdgeSchedule tells one edge node what its own stop/start window is (#1276).
//
// An edge cannot work this out for itself: the provisioner sidecar that knows it runs only
// beside central. Pushing it means the node can answer questions about its own downtime --
// including warning its own clients -- without a round trip to central, and without central
// being reachable at the moment it matters.
//
// A miss is logged rather than retried. The next handshake pushes the schedule again, so a
// node that is briefly disconnected converges on its own.
func (s *Server) SendEdgeSchedule(nodeID string, sched nodeSchedule) {
	msg := ControlMessage{
		Type:              "node_schedule",
		NodeID:            nodeID,
		ScheduleEnabled:   sched.Enabled,
		ScheduleStopTime:  sched.StopTime,
		ScheduleStartTime: sched.StartTime,
		Timezone:          sched.Timezone,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		slog.Error(fmt.Sprintf("[Edge WS] Could not encode the schedule for %s: %v", nodeID, err))
		return
	}

	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()
	if !exists {
		slog.Info(fmt.Sprintf("[Edge WS] Schedule for %s not sent: it has no control connection", nodeID))
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		slog.Warn(fmt.Sprintf("[Edge WS] Schedule for %s failed to send: %v", nodeID, err))
		return
	}
	if sched.Enabled {
		slog.Info(fmt.Sprintf("[Edge WS] Told %s its schedule: stops %s, starts %s (%s)", nodeID, sched.StopTime, sched.StartTime, sched.Timezone))
		return
	}
	slog.Info(fmt.Sprintf("[Edge WS] Told %s it is not on a schedule", nodeID))
}

// setOwnSchedule records what central has told this node about its own downtime.
func (s *Server) setOwnSchedule(sched nodeSchedule) {
	s.maintMutex.Lock()
	defer s.maintMutex.Unlock()
	s.ownSchedule = sched
}

// OwnSchedule returns this node's own stop/start window as last told by central. The zero
// value -- Enabled false -- means either that this node is not scheduled or that central has
// not told it yet; both mean "no known downtime", which is the safe reading for a caller.
func (s *Server) OwnSchedule() nodeSchedule {
	s.maintMutex.RLock()
	defer s.maintMutex.RUnlock()
	return s.ownSchedule
}

// BroadcastNodeSet pushes central's node-set fingerprint to every connected edge (#1960).
//
// An edge holds no roster -- it is configured with no edge_nodes and has no database -- so
// before this it answered its own clients' heartbeats with no fingerprint at all, and a client
// that had elected an edge while some other edge was asleep was never told the topology moved.
// Central is the only node that knows, so central decides and the edges carry it: the same
// shape RateLimit and the node schedule already use, rather than a second mechanism.
//
// The value is computed ONCE here, outside the loop, and for two reasons. It is the same roster
// for every recipient, so recomputing it per edge would let two edges be told different things
// about one moment; and nodeSetFingerprint takes edgeClientsMu itself, so computing it inside
// the loop below would re-enter a lock this function already holds.
//
// A miss is logged, not retried. Every edge is told again the moment any edge connects or
// disconnects, and a reconnecting edge is told on its handshake, so a node that missed one
// frame converges on its own -- the same convergence SendEdgeSchedule relies on.
func (s *Server) BroadcastNodeSet() {
	fingerprint := s.nodeSetFingerprint()
	payload, err := json.Marshal(ControlMessage{Type: nodeSetFrameType, NodeSet: fingerprint})
	if err != nil {
		slog.Error(fmt.Sprintf("[Edge WS] Could not encode the node set: %v", err))
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()
	for nodeID, conn := range s.edgeClients {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to push the node set to %s: %v", nodeID, err))
		}
	}
}

// setUpstreamNodeSet records the roster fingerprint central last pushed to this node.
func (s *Server) setUpstreamNodeSet(fingerprint string) {
	s.maintMutex.Lock()
	defer s.maintMutex.Unlock()
	s.upstreamNodeSet = fingerprint
}

// UpstreamNodeSet returns the roster fingerprint central last pushed to this node, or "" when
// central has not told it -- which is also what a control plane, which needs no telling,
// reports. Exported for the same reason OwnSchedule is: it is the edge half of a two-node
// contract, and a test that can only observe one half cannot exercise the pair.
func (s *Server) UpstreamNodeSet() string {
	s.maintMutex.RLock()
	defer s.maintMutex.RUnlock()
	return s.upstreamNodeSet
}

// BroadcastNodeShutdownWarning sends a shutdown warning notification to a specific edge node or all edge nodes.
func (s *Server) BroadcastNodeShutdownWarning(nodeID string, secondsRemaining int, reason string) {
	shutdownAt := time.Now().Unix() + int64(secondsRemaining)
	msg := ControlMessage{
		Type:             "node_shutdown_warning",
		NodeID:           nodeID,
		Action:           "shutdown_warning",
		SecondsRemaining: secondsRemaining,
		ShutdownAt:       shutdownAt,
		Reason:           reason,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		slog.Error(fmt.Sprintf("[Edge WS] Could not encode a shutdown warning for %s: %v", nodeID, err))
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	// Every outcome is logged, including the misses. This used to send silently, so an
	// operator could not tell a warning that fired from one that never did -- and the two
	// were indistinguishable for weeks while the edge-side receiver was unreleased and
	// discarding every frame. Establishing which had happened took a live test against a
	// real scheduled stop (#1245).
	if nodeID != "" {
		conn, exists := s.edgeClients[nodeID]
		if !exists {
			slog.Warn(fmt.Sprintf("[Edge WS] Shutdown warning for %s not sent: it has no control connection (stopping in %ds: %s)", nodeID, secondsRemaining, reason))
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			slog.Warn(fmt.Sprintf("[Edge WS] Shutdown warning for %s failed to send: %v", nodeID, err))
			return
		}
		slog.Info(fmt.Sprintf("[Edge WS] Sent shutdown warning to %s: %ds remaining (%s)", nodeID, secondsRemaining, reason))
		return
	}

	// No nodeID means every connected edge -- used for control-plane-wide events rather
	// than a single node's schedule.
	sent := 0
	for id, conn := range s.edgeClients {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			slog.Warn(fmt.Sprintf("[Edge WS] Shutdown warning to %s failed to send: %v", id, err))
			continue
		}
		sent++
	}
	slog.Info(fmt.Sprintf("[Edge WS] Sent shutdown warning to %d edge node(s): %ds remaining (%s)", sent, secondsRemaining, reason))
}

// sendEdgeWSKick sends a lease kick message to a specific Edge node via WebSocket.
// Returns true if the message was sent successfully.
func (s *Server) sendEdgeWSKick(nodeID, subdomain string) bool {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists {
		return false
	}

	msg := ControlMessage{
		Type:      "lease_kick",
		Subdomain: subdomain,
	}

	err := conn.WriteJSON(msg)
	return err == nil
}

// sendEdgeWSHeaders sends a lease headers update to a specific Edge node via WebSocket.
// Returns true if the message was sent successfully.
func (s *Server) sendEdgeWSHeaders(nodeID, fullHost string, headers map[string]string) bool {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists {
		return false
	}

	msg := ControlMessage{
		Type:      "lease_headers",
		Subdomain: fullHost,
		Headers:   headers,
	}

	err := conn.WriteJSON(msg)
	return err == nil
}

// broadcastQuotaEnforcement tells every connected edge what the bandwidth quota now says
// about one user (#1959).
//
// Broadcast rather than targeted, unlike sendEdgeWSKick, and that is the decision worth
// stating: a user can hold leases on several edges at once, and s.edgeLeases is central's
// cache of where they are rather than the truth. A frame naming a user that an edge has never
// heard of is a no-op there, so sending to all of them costs one small frame per node and
// removes the failure where a stale cache leaves one edge serving an over-quota tunnel at
// full speed. An empty userID means every user, which is what a period rollover is.
//
// Best-effort by design. A node that is disconnected simply does not receive it -- the quota
// fails OPEN across a partition, see quota.go -- and the next sweep after it reconnects sends
// the frame again, because the standing it was computed from has not changed.
func (s *Server) broadcastQuotaEnforcement(userID, action string, rateLimit int) {
	msg := ControlMessage{
		Type:      "quota_enforcement",
		UserID:    userID,
		Action:    action,
		RateLimit: rateLimit,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		slog.Error(fmt.Sprintf("[Edge WS] Could not encode a quota %s for %q: %v", action, userID, err))
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	sent := 0
	for id, conn := range s.edgeClients {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			// Named, not swallowed: an edge that did not get this keeps serving the
			// user at full speed, and the operator should be able to see which one.
			slog.Warn(fmt.Sprintf("[Edge WS] Quota %s for %q failed to reach %s: %v", action, userID, id, err))
			continue
		}
		sent++
	}
	if sent > 0 {
		slog.Info(fmt.Sprintf("[Edge WS] Sent quota %s for %q to %d edge node(s)", action, userID, sent))
	}
}

// SendEdgeRestart sends a restart command to a specific edge node.
func (s *Server) SendEdgeRestart(nodeID string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type: "restart",
	}
	return conn.WriteJSON(msg)
}

// SendEdgeMaintenance sends a maintenance mode trigger to a specific edge node.
func (s *Server) SendEdgeMaintenance(nodeID string, action string, duration int, reason string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type:     "maintenance_trigger",
		Action:   action,
		Duration: duration,
		Reason:   reason,
	}
	return conn.WriteJSON(msg)
}

// SendEdgeKickAll kicks all active leases/tunnels on a specific edge node.
func (s *Server) SendEdgeKickAll(nodeID string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type:      "lease_kick",
		Subdomain: "*",
	}
	return conn.WriteJSON(msg)
}

// CloseEdgeControlConn forcibly closes the control WebSocket connection for a specific edge node.
func (s *Server) CloseEdgeControlConn(nodeID string) {
	s.edgeClientsMu.Lock()
	conn, exists := s.edgeClients[nodeID]
	if exists && conn != nil && conn.conn != nil {
		_ = conn.conn.Close() //nolint:errcheck
	}
	s.edgeClientsMu.Unlock()
}

// kickAllLocalLeases terminates all tunnels hosted locally on this server instance.
func (s *Server) kickAllLocalLeases() {
	if s.registry == nil {
		return
	}
	leases := s.registry.ListLeases()
	for _, l := range leases {
		slog.Info(fmt.Sprintf("[Edge Control] Terminating lease for %s", l.FullHost))
		s.registry.KickLease(l.SubdomainPrefix)
	}
}

// edgeNodeIDFromToken derives an edge's own node ID from its configured edge token, which
// is shaped "<node-id>-<secret>".
//
// Extracted so the control channel and the lease registry cannot disagree about who this
// gateway is -- the registry previously did not know at all and stamped every lease
// "control" (issue #1167).
func edgeNodeIDFromToken(token string) string {
	parts := strings.Split(token, "-")
	nodeID := ""
	if len(parts) > 1 {
		nodeID = strings.Join(parts[:len(parts)-1], "-")
	} else if len(parts) == 1 {
		nodeID = parts[0]
	}
	if nodeID == "" {
		nodeID = "edge"
	}
	return nodeID
}

// backoffOrStop pauses d before the next reconnect attempt, reporting false when the
// server is shutting down. A bare time.Sleep on these paths kept runEdgeControlChannel
// alive for up to ten seconds after Stop had cancelled its context -- still reading the
// deadline tunables that Stop's caller is entitled to tear down (issue #1131).
func (s *Server) backoffOrStop(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// runEdgeControlChannel manages the Edge Node's client control WebSocket connection.
func (s *Server) runEdgeControlChannel() {
	lostAt := time.Time{}

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		u, err := url.Parse(s.cfg.ControlPlaneURL)
		if err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Invalid ControlPlaneURL: %v", err))
			if !s.backoffOrStop(10 * time.Second) {
				return
			}
			continue
		}

		nodeID := edgeNodeIDFromToken(s.cfg.EdgeToken)

		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		wsURL := fmt.Sprintf("%s://%s/api/internal/edge-control-ws?node_id=%s&version=%s", scheme, u.Host, nodeID, url.QueryEscape(config.Version))

		slog.Info(fmt.Sprintf("[Edge Control] Connecting to Control Plane at %s...", wsURL))

		// A COPY of the default dialer, not the pointer (#1370). websocket.DefaultDialer is a
		// *Dialer, so assigning it and then setting fields mutates gorilla's package-level
		// global. Two Servers with overlapping lifetimes race on it -- write/write between two
		// runEdgeControlChannel goroutines, and write/read against gorilla reading the same
		// fields inside DialContext. Found by 20 iterations under -race; a single run and a
		// single CI run both passed.
		//
		// It is also wrong independently of the race: mutating the global leaves every other
		// user of DefaultDialer in this process with NetDialContext forcing tcp4.
		dialerCopy := *websocket.DefaultDialer
		dialer := &dialerCopy
		dialer.HandshakeTimeout = 5 * time.Second
		// Force IPv4 for this outbound connection (see #911): on dual-stack edges, the
		// default dialer prefers IPv6 when both are available, but at least one edge
		// region's IPv6 path to the control plane has exhibited a ~75s idle-connection
		// timeout at an intermediate network hop, causing needless reconnect churn. Every
		// edge always has a guaranteed IPv4 Elastic IP (IPv6 is opt-in), so this is safe
		// across all regions, not just the affected one.
		dialer.NetDialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		}

		if s.cfg.InsecureSkipVerify {
			dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}

		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Connection failed: %v", err))
			if lostAt.IsZero() {
				lostAt = time.Now()
			} else if time.Since(lostAt) > 3*time.Minute {
				slog.Info("[Edge Control] Connection lost for >3 minutes. Terminating all active tunnels...")
				s.kickAllLocalLeases()
			}
			if !s.backoffOrStop(10 * time.Second) {
				return
			}
			continue
		}

		// Connected! Reset lostAt timer
		lostAt = time.Time{}

		// 1. Receive challenge
		var challengeMsg ControlMessage
		if err := conn.ReadJSON(&challengeMsg); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to read challenge: %v", err))
			_ = conn.Close() //nolint:errcheck
			if !s.backoffOrStop(5 * time.Second) {
				return
			}
			continue
		}

		if challengeMsg.Type != "challenge" {
			slog.Info(fmt.Sprintf("[Edge Control] Expected challenge message, got %s", challengeMsg.Type))
			_ = conn.Close() //nolint:errcheck
			if !s.backoffOrStop(5 * time.Second) {
				return
			}
			continue
		}

		// 2. Calculate HMAC response using sha256(EdgeToken)
		key := sha256.Sum256([]byte(s.cfg.EdgeToken))
		mac := hmac.New(sha256.New, key[:])
		mac.Write([]byte(challengeMsg.Nonce))
		respHex := hex.EncodeToString(mac.Sum(nil))

		authMsg := ControlMessage{
			Type:     "auth",
			Response: respHex,
		}
		if err := conn.WriteJSON(authMsg); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to send auth response: %v", err))
			_ = conn.Close() //nolint:errcheck
			if !s.backoffOrStop(5 * time.Second) {
				return
			}
			continue
		}

		// 3. Receive auth result
		var authResult ControlMessage
		if err := conn.ReadJSON(&authResult); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to read auth result: %v", err))
			_ = conn.Close() //nolint:errcheck
			if !s.backoffOrStop(5 * time.Second) {
				return
			}
			continue
		}

		if authResult.Type != "auth_success" {
			slog.Info(fmt.Sprintf("[Edge Control] Authentication failed: %s", authResult.Reason))
			_ = conn.Close() //nolint:errcheck
			if !s.backoffOrStop(10 * time.Second) {
				return
			}
			continue
		}

		slog.Info("[Edge Control] Successfully connected and authenticated with Control Plane.")
		// From here until the read loop exits this edge can carry sessions. /api/healthz
		// reports this so clients stop electing an edge whose HTTP is up but whose
		// control channel is not (issue #1145).
		s.edgeControlConnected.Store(true)

		// The read loop below resets its 75s read deadline before each blocking read,
		// but that only actually gets hit once a real ControlMessage arrives -- and the
		// control plane only sends one on real events (blacklist updates, maintenance
		// triggers, lease kicks, etc.), not on a fixed schedule. During an idle period
		// with no such events, nothing refreshes the deadline except this edge's own
		// outgoing Ping (sent every 30s below) getting a Pong back -- but gorilla/
		// websocket handles incoming Pong frames internally and never surfaces them to
		// the caller unless a PongHandler is registered (see #911; same class of bug the
		// server side already had to work around for the equivalent Ping case). Without
		// this, any edge idle for >75s hits the deadline and reconnects regardless of
		// network conditions -- confirmed as the actual root cause of edge-apac's ~75s
		// reconnect cycling, not a network-path timeout as originally suspected. Other
		// edges apparently receive enough incidental real ControlMessage traffic
		// (broadcasts) to keep resetting the deadline before it fires; edge-apac's idle
		// periods are long enough to expose the missing handler.
		// One synchronised read per connection (#1370). The PongHandler below is invoked by
		// gorilla/websocket from the reader goroutine, so it must close over a value rather
		// than read the package var itself.
		clientReadDeadline, clientPingInterval := edgeClientTunables()

		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(clientReadDeadline)) //nolint:errcheck
			return nil
		})

		// Start ticker to send ping messages
		ticker := time.NewTicker(clientPingInterval)
		pingErrChan := make(chan error, 1)

		// connDone bounds both per-connection goroutines below to the life of this
		// connection. The ping goroutine used to exit only on a write error or on
		// server shutdown, so every read-side failure stranded one: ticker.Stop() halts
		// deliveries but leaves it parked on a channel that can no longer fire. An edge
		// reconnecting on the 75s deadline leaked one per cycle (issue #1131).
		connDone := make(chan struct{})

		// This node's own writer for the connection, so the keepalive above and the metrics
		// reporter below cannot interleave two frames on the same socket (#1958). The same
		// reason handleEdgeControlWS wraps its side in one (#1125). Published on the server
		// so Stop can make a final byte report over it before the process goes.
		uplink := &safeConn{conn: conn}
		s.setEdgeUplink(uplink)

		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// WriteControl rather than SetWriteDeadline+WriteMessage: identical frame
					// and identical 5s deadline, but gorilla exempts WriteControl from its
					// single-writer restriction, so the keepalive no longer has to take turns
					// with the metrics report. The control plane's own RTT ping does the same.
					if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
						pingErrChan <- err
						return
					}
				case <-connDone:
					return
				case <-s.ctx.Done():
					return
				}
			}
		}()

		// Report the bytes this edge's leases have carried, for as long as this connection
		// lives (#1958). Scoped to the connection rather than to the process because a
		// report is only meaningful while there is somewhere to send it -- and because
		// nothing is taken off the leases while the channel is down, so an outage costs
		// resolution, not totals: the first report after it covers the whole gap.
		if s.edgeMetrics != nil {
			go func() {
				metricsTicker := time.NewTicker(s.edgeMetricsInterval())
				defer metricsTicker.Stop()
				for {
					select {
					case <-metricsTicker.C:
						s.edgeMetrics.Collect(s.registry)
						s.edgeMetrics.Flush(uplink)
					case <-connDone:
						return
					case <-s.ctx.Done():
						return
					}
				}
			}()
		}

		// One reader for the connection's lifetime. Spawning one per loop iteration
		// orphaned a goroutine still blocked in ReadJSON whenever pingErrChan won the
		// select, leaving it on a connection the loop then closed and replaced, and it
		// split SetReadDeadline and ReadJSON across two goroutines. gorilla/websocket
		// permits one reader and one writer; this had neither (issue #1131).
		type controlRead struct {
			msg ControlMessage
			err error
		}
		// The send selects on connDone as well: readCh holds one message, so a read that
		// completes just as pingErrChan wins the select below would otherwise park here
		// on a full buffer nobody will drain.
		readCh := make(chan controlRead, 1)
		go func() {
			for {
				var msg ControlMessage
				_ = conn.SetReadDeadline(time.Now().Add(clientReadDeadline)) //nolint:errcheck
				err := conn.ReadJSON(&msg)
				select {
				case readCh <- controlRead{msg: msg, err: err}:
				case <-connDone:
					return
				}
				if err != nil {
					return
				}
			}
		}()

		// Read loop
		for {
			var msg ControlMessage
			var readErr error
			select {
			case res := <-readCh:
				msg, readErr = res.msg, res.err
			case pingErr := <-pingErrChan:
				readErr = pingErr
			case <-s.ctx.Done():
				readErr = s.ctx.Err()
			}

			if readErr != nil {
				slog.Info(fmt.Sprintf("[Edge Control] Connection closed or read failed: %v", readErr))
				break
			}

			switch msg.Type {
			case "restart":
				slog.Info("[Edge Control] Restart request received from Control Plane. Exiting...")
				os.Exit(1)
			case "route_update":
				s.remoteRoutesMu.Lock()
				if s.remoteRoutes == nil {
					s.remoteRoutes = make(map[string]string)
				}
				switch msg.Action {
				case "add":
					slog.Info(fmt.Sprintf("[Edge Control] Route added for %s -> %s (node: %s)", msg.FullHost, msg.TargetURL, msg.NodeID))
					s.remoteRoutes[msg.FullHost] = msg.TargetURL
				case "remove":
					slog.Info(fmt.Sprintf("[Edge Control] Route removed for %s", msg.FullHost))
					delete(s.remoteRoutes, msg.FullHost)
				}
				s.remoteRoutesMu.Unlock()
			case "access_control_update":
				updated := s.registry.SetAccessControlsForSubdomain(msg.Subdomain, msg.Passcode, msg.WhitelistIPs, msg.AccessMode)
				slog.Info(fmt.Sprintf("[Edge Control] Access control for %s applied to %d lease(s)", msg.Subdomain, updated))
			case "blacklist_update":
				switch msg.Action {
				case "add":
					slog.Info(fmt.Sprintf("[Edge Control] Blacklisting IP: %s, %s", msg.IP, describeBanDuration(msg.BanExpiresAt)))
					// Stored with the control plane's expiry, so the ban lifts here at the same
					// moment it lifts there rather than outliving it (#1353).
					s.cacheBan(msg.IP, msg.BanExpiresAt)
				case "remove":
					slog.Info(fmt.Sprintf("[Edge Control] Unblacklisting IP: %s", msg.IP))
					s.blacklist.Delete(msg.IP)
				}
			case "maintenance_trigger":
				s.maintMutex.Lock()
				switch msg.Action {
				case "enable":
					slog.Info(fmt.Sprintf("[Edge Control] Maintenance enabled: %s (duration: %d mins)", msg.Reason, msg.Duration))
					s.maintenanceMode = true
					s.kickAllLocalLeases()
				case "disable":
					slog.Info("[Edge Control] Maintenance disabled.")
					s.maintenanceMode = false
				}
				s.maintMutex.Unlock()
			case "node_shutdown_warning":
				// Central warns a specific node ahead of a scheduled stop. Recorded here
				// and handed to clients on their next tunnel-status heartbeat, which is
				// the only channel they already listen on -- their tunnel itself is a
				// chisel connection owned by the library, with no frame channel of its
				// own (#1238).
				s.maintMutex.Lock()
				s.pendingShutdownAt = msg.ShutdownAt
				s.pendingShutdownReason = msg.Reason
				s.maintMutex.Unlock()
				slog.Info(fmt.Sprintf("[Edge Control] Node shutdown warning: %ds remaining (%s)", msg.SecondsRemaining, msg.Reason))
				// Report now rather than waiting for the interval (#1958). This node is about
				// to be powered off -- every edge is, nightly -- and it has no database to
				// leave anything in. The graceful stop flushes too, but an EC2 stop is not
				// always graceful, and flushing at the announcement bounds what a hard stop
				// inside the warning window can take with it.
				s.flushEdgeMetrics()
			case "node_schedule":
				// Central telling this node its own stop/start window (#1276). Held in
				// memory only -- the next handshake re-sends it, so there is nothing to
				// persist and nothing to go stale across a restart.
				sched := nodeSchedule{
					Enabled:   msg.ScheduleEnabled,
					StopTime:  msg.ScheduleStopTime,
					StartTime: msg.ScheduleStartTime,
					Timezone:  msg.Timezone,
				}
				s.setOwnSchedule(sched)
				// Logged so a node's own view of its downtime is visible in its own
				// journal, rather than only in central's. Establishing what central
				// believed about a schedule used to require reading central's logs and
				// inferring the rest (#1245).
				if sched.Enabled {
					slog.Info(fmt.Sprintf("[Edge Control] Control plane says this node stops at %s and starts at %s (%s)", sched.StopTime, sched.StartTime, sched.Timezone))
				} else {
					slog.Info("[Edge Control] Control plane says this node is not on a shutdown schedule")
				}
			case nodeSetFrameType:
				// Central telling this node what its roster hashes to (#1960). Held in
				// memory only, like the schedule above: the next handshake re-sends it,
				// and a value that survived a restart would be a fingerprint nothing had
				// checked against the live roster.
				//
				// Not logged per frame. This arrives on every edge connect and disconnect
				// across the whole fleet, and the value is meaningless to a human reader --
				// twelve hex characters whose only property is that it changes. The change
				// a person cares about is on the client side, where node_set_changed is
				// already logged with both values.
				s.setUpstreamNodeSet(msg.NodeSet)
			case diagnosticsCollectFrameType:
				// Central forwarding an admin's collection request for a user THIS node
				// serves (#1991). Held in memory only, like the schedule and the node set
				// above, and handed to the client on its next tunnel-status heartbeat --
				// the only channel a NATed client listens on, and only from the gateway
				// actually serving it, which is what made this hop necessary.
				s.queueForwardedDiagnosticsCollect(msg.UserID, msg.DiagRequestID,
					time.Duration(msg.DiagExpiresInSecs)*time.Second)
			case "lease_kick":
				if msg.Subdomain == "*" || msg.Subdomain == "" {
					slog.Info("[Edge Control] Kicking ALL leases on this edge node")
					s.kickAllLocalLeases()
				} else {
					slog.Info(fmt.Sprintf("[Edge Control] Kicking lease for subdomain %s", msg.Subdomain))
					s.registry.KickLease(msg.Subdomain)
				}
			case "quota_enforcement":
				// The edge half of the cumulative bandwidth quota (#1959). An edge holds
				// no database and does no counting; central decides and this applies.
				//
				// Every branch is idempotent and none of them can fail the connection --
				// telemetry and policy share this channel with kicks and schedules, and
				// dropping it would take those with it.
				switch msg.Action {
				case "throttle":
					applied := s.registry.SetQuotaRateLimitForUser(msg.UserID, msg.RateLimit)
					slog.Warn(fmt.Sprintf("[Edge Control] Bandwidth quota: %d lease(s) for user %s throttled to %d rps", applied, msg.UserID, msg.RateLimit))
				case "stop":
					stopped := 0
					for _, subdomain := range s.registry.LeaseSubdomainsForUser(msg.UserID) {
						if s.registry.KickLease(subdomain) {
							stopped++
						}
					}
					slog.Warn(fmt.Sprintf("[Edge Control] Bandwidth quota: %d lease(s) for user %s terminated; the allowance is used up", stopped, msg.UserID))
				case "release":
					if msg.UserID == "" {
						// A period rollover. Every lease this node holds goes back
						// to what it was granted at registration.
						restored := 0
						for _, l := range s.registry.ListLeases() {
							restored += s.registry.ClearQuotaRateLimitForUser(l.UserID)
						}
						slog.Info(fmt.Sprintf("[Edge Control] Bandwidth quota period reset; %d lease(s) restored", restored))
					} else {
						restored := s.registry.ClearQuotaRateLimitForUser(msg.UserID)
						slog.Info(fmt.Sprintf("[Edge Control] Bandwidth quota lifted for user %s; %d lease(s) restored", msg.UserID, restored))
					}
				default:
					slog.Warn(fmt.Sprintf("[Edge Control] Ignoring unknown quota action %q", msg.Action))
				}
			case "lease_headers":
				slog.Info(fmt.Sprintf("[Edge Control] Updating custom headers for lease %s", msg.Subdomain))
				if err := s.registry.UpdateLeaseHeaders(msg.Subdomain, msg.Headers); err != nil {
					slog.Error(fmt.Sprintf("[Edge Control] Failed to update lease headers for %s: %v", msg.Subdomain, err))
				}
			default:
				// Ignoring a frame this build has no case for is the correct behaviour --
				// an older node must tolerate a newer control plane rather than die on it.
				// Doing so *silently* is not: central sent node_shutdown_warning every
				// scheduled stop for weeks and every edge discarded it here, because the
				// case handling it shipped after the release the fleet was running. From
				// the outside that was indistinguishable from central never sending at all
				// (#1245). A version skew that changes behaviour has to say so.
				slog.Warn(fmt.Sprintf("[Edge Control] Ignoring unknown message type %q from the control plane -- this node is likely running an older version than central", msg.Type))
			}
		}

		s.edgeControlConnected.Store(false)
		// Withdrawn before the close, so a Stop racing this reconnect cannot try to report
		// over a connection that is about to go (#1958). Nothing is lost by that: whatever
		// was pending stays pending for the next connection.
		s.setEdgeUplink(nil)
		// Release both goroutines, then close: the reader may be parked in ReadJSON,
		// which only Close unblocks.
		close(connDone)
		ticker.Stop()
		_ = conn.Close() //nolint:errcheck
		lostAt = time.Now()
	}
}

// setEdgeControlReadDeadline sets the guarded tunable and returns the previous value, so a test
// can restore it with `defer setEdgeControlReadDeadline(setEdgeControlReadDeadline(d))` and have
// both the write and the restore hold the lock (#1370).
func setEdgeControlReadDeadline(d time.Duration) time.Duration {
	edgeTunableMu.Lock()
	defer edgeTunableMu.Unlock()
	prev := edgeControlReadDeadline
	edgeControlReadDeadline = d
	return prev
}

// setEdgeHealthPingInterval is setEdgeControlReadDeadline's counterpart for the RTT-ping
// interval.
func setEdgeHealthPingInterval(d time.Duration) time.Duration {
	edgeTunableMu.Lock()
	defer edgeTunableMu.Unlock()
	prev := edgeHealthPingInterval
	edgeHealthPingInterval = d
	return prev
}

// setEdgeClientTunables sets the edge-side pair and returns a restore func, so a test can do
// `defer setEdgeClientTunables(d, i)()` with both the override and the restore under the lock
// (#1370). A pair rather than two setters because the two tests that touch these always set
// both, and restoring only one of them is not a state worth making expressible.
func setEdgeClientTunables(readDeadline, pingInterval time.Duration) func() {
	edgeTunableMu.Lock()
	prevDeadline := edgeClientReadDeadline
	prevInterval := edgeClientPingInterval
	edgeClientReadDeadline = readDeadline
	edgeClientPingInterval = pingInterval
	edgeTunableMu.Unlock()

	return func() {
		edgeTunableMu.Lock()
		edgeClientReadDeadline = prevDeadline
		edgeClientPingInterval = prevInterval
		edgeTunableMu.Unlock()
	}
}
