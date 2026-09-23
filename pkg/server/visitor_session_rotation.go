package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"lfr-tunnel/pkg/db"
)

// Changing the visitor session-signing key without ending a single session (#2195).
//
// #2181 gave the fleet a shared, persisted key and deliberately left the shape rotation needs:
// the accepted set is a SET verified without short-circuiting, only the current generation
// mints, every key carries a generation id, and the id travels on the frame. Nothing in it
// could CHANGE the key. A key that can never be rotated is a key that lives forever.
//
// THREE PHASES, and the third one is the whole point:
//
//  1. DISTRIBUTE. Central mints a new generation and adds it to the accepted set, leaving the
//     current generation minting. It pushes the set to every connected node and waits for each
//     to acknowledge that it now HOLDS the new generation.
//  2. COMMIT. Only once every connected node has acknowledged, central makes the new generation
//     current and tells the fleet to switch minting. The previous generation stays accepted.
//  3. RETIRE. A separate, later step drops a generation from the accepted set.
//
// Retirement is a phase rather than part of the commit because a session cookie lives 24 hours
// (visitorSessionCookieLifetime). If the previous key stopped verifying at the commit, every
// visitor holding one would be bounced to the passcode page at that instant -- which is #2181
// again, on the rotation's cadence, and is exactly the outcome the owner ruled out. So
// retirement lags the commit by longer than a cookie can live.
//
// "ALL NODES READY" MEANS EVERY CURRENTLY-CONNECTED NODE, never every configured one. edge-us
// and edge-sa power off nightly (edge-sync: "Every Edge is powered off nightly"), so a commit
// gated on all configured nodes would fail closed every night while appearing to work. A node
// that was asleep is handed the current set at its own handshake, which is #2181's existing
// push and needs nothing added here.
//
// FAIL CLOSED means the SWITCH does not happen: the current generation is not moved, the old
// key goes on minting, and the reason is recorded in admin_audit_log. It does not mean the
// distributed generation is clawed back -- a generation nothing has ever signed with is an
// extra key in the accepted set that changes no behaviour, and a rollback that can itself fail
// is more moving parts than the invariant needs. pruneUncommittedGenerations is what bounds it.

const (
	// visitorSessionCookieLifetime is how long a visitor's session cookie is valid, and is the
	// number createSessionCookie stamps into every cookie it signs.
	//
	// Named here because the retirement lag is derived from it. If these two ever drift apart,
	// retirement starts logging visitors out and nothing says so.
	visitorSessionCookieLifetime = 24 * time.Hour

	// visitorSessionRotationInterval is the periodic cadence -- "roughly daily", per the issue.
	visitorSessionRotationInterval = 24 * time.Hour

	// visitorSessionRetirementLag is how long after a commit a generation may be dropped from
	// the accepted set.
	//
	// It is the cookie lifetime PLUS one rotation interval, and the second term is not padding.
	// A node whose commit push failed goes on minting with the outgoing generation until
	// something corrects it, and the thing that corrects it is either its next handshake or the
	// next rotation run's push -- at most one interval away. A cookie minted in that window
	// lives a further cookie lifetime. Retiring any sooner would end sessions that were minted
	// legitimately, which is the defect this whole design exists to avoid.
	visitorSessionRetirementLag = visitorSessionCookieLifetime + visitorSessionRotationInterval

	// visitorSessionRotationSettingKey is the admin_settings row holding the rotation's
	// bookkeeping: when the next periodic rotation is due, which generations are scheduled for
	// retirement, and what the last rotation did.
	//
	// A SECOND row, not more fields on visitor_session_secrets. The key set is the thing a
	// gateway cannot serve a tunnel without; bookkeeping is not. Keeping them apart means an
	// unreadable schedule costs a re-anchored timer rather than a fleet-wide re-prompt, and
	// storedVisitorSessionSecrets.valid() -- which decides whether the keys survive a restart --
	// does not have to learn about rotation at all.
	//
	// IT HOLDS NO KEY MATERIAL. Generation ids only, for the same reason the ack frame carries
	// only ids.
	visitorSessionRotationSettingKey = "visitor_session_rotation"

	// visitorSessionSecretAckFrameType carries a node's acknowledgement UP the edge control
	// channel. Named once so the sender, central's switch and the tests cannot drift apart over
	// a string literal -- the #1245 failure, where central sent a frame every edge logged as
	// unknown for weeks.
	visitorSessionSecretAckFrameType = "visitor_session_secrets_ack"

	// Triggers. The owner asked specifically to be able to tell these apart after the fact.
	visitorSessionTriggerManual   = "manual"
	visitorSessionTriggerPeriodic = "periodic"

	// visitorSessionPeriodicActor is the audit actor_id a periodic rotation is recorded under.
	//
	// The trigger is stated in the audit details too, but actor_id is the field
	// ListAuditEntries can FILTER on, and "show me every rotation nobody asked for" is the
	// query that separates a system fault from somebody watching a screen. Prose in a details
	// column is not queryable.
	visitorSessionPeriodicActor = "system:scheduler"

	// Outcomes.
	visitorSessionOutcomeCommitted = "committed"
	visitorSessionOutcomeAborted   = "aborted"

	// Audit actions.
	visitorSessionAuditRotated = "session_secret.rotated"
	visitorSessionAuditAborted = "session_secret.rotation_aborted"
	visitorSessionAuditRetired = "session_secret.retired"
)

// Tunables, package vars so a test can drive the engine without waiting on real time. Written
// only by tests; read through the accessors below.
var (
	// visitorSessionAckTimeout is how long a phase waits for every connected node to
	// acknowledge before it gives up and fails closed.
	visitorSessionAckTimeout = 10 * time.Second

	// visitorSessionAckPollInterval is how often the runner re-checks the acknowledgements it
	// has. Polling rather than a channel because the reader is one goroutine waiting on a
	// bounded set, and the read pump that records an ack must never block behind it.
	visitorSessionAckPollInterval = 25 * time.Millisecond

	// visitorSessionRotationCheckInterval is how often the scheduler compares the wall clock
	// against the persisted due time. Short relative to the interval so a restart's first
	// overdue tick fires promptly rather than up to a day late.
	visitorSessionRotationCheckInterval = time.Minute

	visitorSessionTunableMu sync.RWMutex
)

func visitorSessionAckTunables() (timeout, poll time.Duration) {
	visitorSessionTunableMu.RLock()
	defer visitorSessionTunableMu.RUnlock()
	return visitorSessionAckTimeout, visitorSessionAckPollInterval
}

// visitorSessionAck is one node's report of the generations it holds.
type visitorSessionAck struct {
	// CurrentID is the generation that node is MINTING with.
	CurrentID string
	// Accepted is every generation it will VERIFY against, including its own node-local
	// bootstrap key.
	Accepted []string
	// At is when central received it, which is what makes an acknowledgement answer "since the
	// push" rather than "at some point".
	At time.Time
}

// holds reports whether this node accepts the named generation.
func (a visitorSessionAck) holds(generation string) bool {
	for _, id := range a.Accepted {
		if id == generation {
			return true
		}
	}
	return false
}

// visitorSessionAckState is central's record of what each node last reported.
//
// In memory only, and deliberately so: it is evidence about a live connection, and a value that
// survived a restart would be a claim about a node nobody has spoken to since.
type visitorSessionAckState struct {
	mu     sync.RWMutex
	latest map[string]visitorSessionAck
}

func (v *visitorSessionAckState) note(nodeID string, ack visitorSessionAck) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.latest == nil {
		v.latest = make(map[string]visitorSessionAck)
	}
	v.latest[nodeID] = ack
}

func (v *visitorSessionAckState) get(nodeID string) (visitorSessionAck, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ack, ok := v.latest[nodeID]
	return ack, ok
}

// visitorSessionNodeStatus names one node a phase could not get an acknowledgement from, and
// why. On an abort the reason is the whole value of the audit event.
type visitorSessionNodeStatus struct {
	NodeID string `json:"node_id"`
	Why    string `json:"why"`
}

// visitorSessionRotationOutcome is what one rotation attempt did. It is persisted (so the
// portal can read back the last outcome across a restart), returned by the admin API, and
// rendered into the audit entry.
//
// No key material, by construction: generation ids only.
type visitorSessionRotationOutcome struct {
	At                 time.Time                  `json:"at"`
	Trigger            string                     `json:"trigger"`
	Actor              string                     `json:"actor,omitempty"`
	Outcome            string                     `json:"outcome"`
	Generation         string                     `json:"generation,omitempty"`
	PreviousGeneration string                     `json:"previous_generation,omitempty"`
	Acknowledged       []string                   `json:"acknowledged"`
	Unacknowledged     []visitorSessionNodeStatus `json:"unacknowledged"`
	Reason             string                     `json:"reason,omitempty"`
}

// committed reports whether the fleet actually switched.
func (o visitorSessionRotationOutcome) committed() bool {
	return o.Outcome == visitorSessionOutcomeCommitted
}

// visitorSessionRotationState is the persisted bookkeeping.
type visitorSessionRotationState struct {
	// NextRotationAt is an absolute instant, not a countdown. That is the whole answer to "a
	// central restart must neither skip nor double-fire a scheduled rotation": an in-memory
	// timer restarts at zero on every deploy and a daily rotation would never fire on a control
	// plane that deploys more often than daily.
	NextRotationAt time.Time `json:"next_rotation_at"`
	// Retirements maps a generation id to the instant it may be dropped from the accepted set.
	Retirements map[string]time.Time `json:"retirements,omitempty"`
	// Last is the last attempt, committed or aborted. Persisted so the portal can show it after
	// a restart -- a silently-aborting rotation that is invisible is the pattern this repo
	// keeps finding.
	Last *visitorSessionRotationOutcome `json:"last,omitempty"`
}

// reportVisitorSessionSecretsUpstream is the EDGE half: it tells central which generations this
// node now holds, reading them back out of the live proxy handler (#2195).
//
// Called after a successful apply and never before one. Read back rather than echoed: an
// acknowledgement that repeated the frame it was sent would be satisfied by a node that received
// the keys and refused them, and that node is precisely the one the commit gate exists to catch
// (§5c -- "ask what the pre-fix code does to your assertion").
//
// A failure is logged and nothing is retried. Central's phase times out, names this node, and
// fails closed; retrying into a connection that just failed a write would only delay that.
func (s *Server) reportVisitorSessionSecretsUpstream() {
	if s.proxyHandler == nil {
		return
	}
	current, accepted := s.proxyHandler.VisitorSessionGenerations()

	s.edgeUplinkMu.RLock()
	conn := s.edgeUplink
	s.edgeUplinkMu.RUnlock()
	if conn == nil {
		slog.Info("[Edge Control] Cannot acknowledge the visitor session keys: no control connection")
		return
	}
	if err := conn.WriteJSON(visitorSessionAckFrame(current, accepted)); err != nil {
		slog.Info(fmt.Sprintf("[Edge Control] Could not acknowledge the visitor session keys: %v", err))
		return
	}
	slog.Info(fmt.Sprintf("[Edge Control] Acknowledged the visitor session keys: %d accepted, minting with generation %s", len(accepted), current))
}

// visitorSessionAckFrame builds the acknowledgement that travels up the control channel.
//
// One function so the frame a test inspects is the frame production sends, rather than a copy of
// it assembled beside it (§5c rule 4). What is NOT on it is the point: no SessionSecrets field,
// so there is no path by which key material can travel back up a channel that never needs to
// see it.
func visitorSessionAckFrame(current string, accepted []string) ControlMessage {
	return ControlMessage{
		Type:                     visitorSessionSecretAckFrameType,
		CurrentSessionSecretID:   current,
		AcceptedSessionSecretIDs: accepted,
	}
}

// noteVisitorSessionSecretAck is CENTRAL's half, called from the edge control channel's read
// pump.
//
// It touches no database, which is what makes it safe on that pump: the pump is a bare goroutine
// bgWG knows nothing about, so Stop can cancel, wait and close the database while it is still
// running (#1833). The rotation runner is a tracked goroutine and is where the audit write
// happens.
func (s *Server) noteVisitorSessionSecretAck(nodeID, currentID string, accepted []string) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return
	}
	s.visitorSessionAcks.note(nodeID, visitorSessionAck{
		CurrentID: strings.TrimSpace(currentID),
		Accepted:  append([]string(nil), accepted...),
		At:        time.Now(),
	})
}

// connectedEdgeNodeIDs is the roster a rotation is gated on: every node holding a live control
// connection right now, sorted so the audit entry reads the same way twice.
//
// NEVER the configured node list. That distinction is constraint 2 and it is the difference
// between a rotation that works and one that fails closed every night between 00:00 and 08:00
// while appearing to work correctly.
func (s *Server) connectedEdgeNodeIDs() []string {
	s.edgeClientsMu.RLock()
	connected := make([]string, 0, len(s.edgeClients))
	for nodeID, conn := range s.edgeClients {
		if conn == nil {
			continue
		}
		connected = append(connected, nodeID)
	}
	s.edgeClientsMu.RUnlock()
	sort.Strings(connected)
	return connected
}

// loadVisitorSessionRotationState reads the bookkeeping row, returning a zero state when there
// is none or when it cannot be read.
//
// An unreadable row is a re-anchored schedule, never a refusal to serve: the keys live in a
// different row and are unaffected, and treating a corrupt timestamp as fatal would take the
// control plane down over bookkeeping.
func loadVisitorSessionRotationState(database *db.DB) visitorSessionRotationState {
	var state visitorSessionRotationState
	if database == nil {
		return state
	}
	raw, exists, err := database.GetAdminSettingOptional(visitorSessionRotationSettingKey)
	if err != nil {
		slog.Warn(fmt.Sprintf("[Session] Could not read the session-key rotation schedule: %v", err))
		return state
	}
	if !exists || raw == "" {
		return state
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		slog.Warn(fmt.Sprintf("[Session] The stored session-key rotation schedule is unreadable and will be re-anchored: %v", err))
		return visitorSessionRotationState{}
	}
	return state
}

// saveVisitorSessionRotationState persists the bookkeeping row.
func saveVisitorSessionRotationState(database *db.DB, state visitorSessionRotationState) error {
	if database == nil {
		return fmt.Errorf("no database: only the control plane owns the rotation schedule")
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("could not encode the rotation schedule: %w", err)
	}
	return database.SetAdminSetting(visitorSessionRotationSettingKey, string(payload))
}

// initVisitorSessionRotation anchors the schedule on a control plane that has never had one.
//
// The first start deliberately schedules the first rotation an INTERVAL away rather than
// immediately. A rotation on every start would fire on every deploy, and central deploys
// constantly -- which is a rotation cadence nobody chose, driven by an event that has nothing to
// do with the key's age.
func (s *Server) initVisitorSessionRotation(database *db.DB) {
	if database == nil {
		return
	}
	state := loadVisitorSessionRotationState(database)
	if !state.NextRotationAt.IsZero() {
		slog.Info(fmt.Sprintf("[Session] Next visitor session-key rotation is due %s", state.NextRotationAt.UTC().Format(time.RFC3339)))
		return
	}
	state.NextRotationAt = time.Now().UTC().Add(visitorSessionRotationInterval)
	if err := saveVisitorSessionRotationState(database, state); err != nil {
		slog.Error(fmt.Sprintf("[Session] Could not anchor the visitor session-key rotation schedule: %v", err))
		return
	}
	slog.Info(fmt.Sprintf("[Session] Visitor session-key rotation scheduled, first run due %s", state.NextRotationAt.Format(time.RFC3339)))
}

// watchVisitorSessionRotation is the periodic trigger (#2195).
//
// A sweep against a PERSISTED due instant rather than a time.Ticker, which is the whole of how a
// central restart neither skips nor double-fires:
//
//   - It cannot DOUBLE-FIRE, because the due time is advanced and written to the database
//     BEFORE the rotation runs. A restart -- during the rotation or after it -- reads a due time
//     in the future and waits.
//   - It cannot SKIP, because the due time is an absolute instant rather than a countdown that
//     restarts at zero. Central being down across it means the first tick after it comes back
//     finds the time already past and fires.
//   - A long outage fires ONCE, not once per missed interval, because the advance is
//     now+interval rather than due+interval. Rotating five times to catch up on five missed days
//     would exhaust the accepted set's bound and end sessions, which is a worse answer than one
//     rotation on a key that is five days old.
func (s *Server) watchVisitorSessionRotation(ctx context.Context) {
	if s.db == nil {
		return
	}
	ticker := time.NewTicker(visitorSessionRotationCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepVisitorSessionRotation(ctx, time.Now().UTC())
		}
	}
}

// sweepVisitorSessionRotation is the body of the watch, separated so a test can drive it with an
// explicit clock instead of waiting on a ticker -- the shape sweepEdgeMetricsDelivery and
// sweepExpiredDiagnosticsCommands already use.
func (s *Server) sweepVisitorSessionRotation(ctx context.Context, now time.Time) {
	if s.db == nil {
		return
	}
	// Retirement first, and on every tick rather than only inside a rotation. A fleet whose
	// rotations keep aborting must still retire the generations an EARLIER rotation committed,
	// or a key that stopped minting days ago goes on verifying forever.
	s.retireVisitorSessionGenerations(now)

	state := loadVisitorSessionRotationState(s.db)
	if state.NextRotationAt.IsZero() {
		state.NextRotationAt = now.Add(visitorSessionRotationInterval)
		if err := saveVisitorSessionRotationState(s.db, state); err != nil {
			slog.Error(fmt.Sprintf("[Session] Could not anchor the visitor session-key rotation schedule: %v", err))
		}
		return
	}
	if now.Before(state.NextRotationAt) {
		return
	}

	// Advanced and PERSISTED before the rotation runs. This is the no-double-fire guarantee,
	// and it costs at most one deferred rotation if the process dies mid-run -- against a
	// rotation storm on a control plane that crash-loops, which would burn a generation per
	// restart and hit the accepted set's bound.
	state.NextRotationAt = now.Add(visitorSessionRotationInterval)
	if err := saveVisitorSessionRotationState(s.db, state); err != nil {
		// Not run. A rotation whose due time could not be moved would fire again on the very
		// next tick, every minute, until the write succeeded.
		slog.Error(fmt.Sprintf("[Session] Skipping the periodic session-key rotation: the schedule could not be advanced: %v", err))
		return
	}
	s.RotateVisitorSessionSecret(ctx, visitorSessionTriggerPeriodic, visitorSessionPeriodicActor, nil)
}

// pruneUncommittedGenerations drops generations left behind by a rotation that did not finish.
//
// The invariant every generation in the stored set satisfies is: it is the CURRENT one, or it
// has a scheduled RETIREMENT, or it is a distributed-but-never-committed generation from an
// attempt that aborted or crashed. The third kind is the one pruned here, and pruning it is safe
// for one reason only -- nothing ever MINTED with it, because minting follows the current
// generation and it was never current, so no cookie in the world verifies against it.
//
// Without this the accepted set would creep upwards on every abort and eventually hit
// maxAcceptedVisitorSessionSecrets, at which point rotation would abort permanently for a reason
// no log line would explain.
func pruneUncommittedGenerations(stored storedVisitorSessionSecrets, retirements map[string]time.Time) storedVisitorSessionSecrets {
	kept := make([]VisitorSessionSecret, 0, len(stored.Secrets))
	for _, secret := range stored.Secrets {
		if secret.ID == stored.CurrentID {
			kept = append(kept, secret)
			continue
		}
		if _, scheduled := retirements[secret.ID]; scheduled {
			kept = append(kept, secret)
			continue
		}
		slog.Info(fmt.Sprintf("[Session] Dropping generation %s: it was distributed but never committed, so nothing was signed with it", secret.ID))
	}
	stored.Secrets = kept
	return stored
}

// retireVisitorSessionGenerations is PHASE THREE: it drops generations whose retirement time has
// come.
//
// This is the step that finally invalidates an old key, and it is separate from the commit
// because a session cookie outlives the commit by up to visitorSessionCookieLifetime. Retiring
// at the commit would bounce every visitor holding a cookie signed with the outgoing key to the
// passcode page at that instant -- #2181 on the rotation's cadence, and the outcome the owner
// explicitly ruled out.
//
// The current generation is never retired, whatever the schedule says.
func (s *Server) retireVisitorSessionGenerations(now time.Time) {
	if s.db == nil {
		return
	}
	state := loadVisitorSessionRotationState(s.db)
	if len(state.Retirements) == 0 {
		return
	}
	stored := s.visitorSessionSecrets.get()
	if len(stored.Secrets) == 0 {
		return
	}

	due := make([]string, 0, len(state.Retirements))
	for id, at := range state.Retirements {
		if id == stored.CurrentID {
			// Defensive, and it would be a bug to hit: a generation is only scheduled when it
			// STOPS being current. Retiring the minting key would leave the fleet signing with
			// a key nothing accepts.
			continue
		}
		if !now.Before(at) {
			due = append(due, id)
		}
	}
	if len(due) == 0 {
		return
	}
	sort.Strings(due)

	retired := make(map[string]bool, len(due))
	for _, id := range due {
		retired[id] = true
	}
	kept := make([]VisitorSessionSecret, 0, len(stored.Secrets))
	for _, secret := range stored.Secrets {
		if retired[secret.ID] {
			continue
		}
		kept = append(kept, secret)
	}
	if len(kept) == len(stored.Secrets) {
		// Scheduled but not present: an earlier retirement already removed it. Clear the
		// schedule entries so they stop being re-evaluated every tick.
		for _, id := range due {
			delete(state.Retirements, id)
		}
		if err := saveVisitorSessionRotationState(s.db, state); err != nil {
			slog.Warn(fmt.Sprintf("[Session] Could not clear a stale retirement schedule: %v", err))
		}
		return
	}
	stored.Secrets = kept

	if err := s.applyAndPersistVisitorSessionSecrets(s.db, stored); err != nil {
		// Kept, not dropped. An over-long retirement costs nothing a visitor can see; a
		// half-applied one ends sessions.
		slog.Error(fmt.Sprintf("[Session] Could not retire session-key generation(s) %s: %v", strings.Join(due, ", "), err))
		return
	}
	for _, id := range due {
		delete(state.Retirements, id)
	}
	if err := saveVisitorSessionRotationState(s.db, state); err != nil {
		slog.Warn(fmt.Sprintf("[Session] Retired generation(s) %s but could not update the schedule: %v", strings.Join(due, ", "), err))
	}

	// Told to the fleet, not only applied here. An edge holding a retired key would go on
	// honouring sessions this control plane has decided are over, which is the split brain
	// edge-sync exists to prevent.
	for _, nodeID := range s.connectedEdgeNodeIDs() {
		if err := s.pushVisitorSessionSecrets(nodeID); err != nil {
			// Converges at the node's next handshake, which re-sends the whole set.
			slog.Info(fmt.Sprintf("[Session] %s was not told about the retirement: %v", nodeID, err))
		}
	}

	slog.Info(fmt.Sprintf("[Session] Retired visitor session-key generation(s) %s; %d generation(s) still accepted, minting with %s",
		strings.Join(due, ", "), len(stored.Secrets), stored.CurrentID))
	s.auditVisitorSessionRotation(visitorSessionAuditRetired, visitorSessionPeriodicActor, strings.Join(due, ", "),
		fmt.Sprintf("Retired visitor session-key generation(s) %s, %s after they stopped minting. Sessions signed with them no longer verify. %d generation(s) still accepted, minting with %s.",
			strings.Join(due, ", "), visitorSessionRetirementLag, len(stored.Secrets), stored.CurrentID), nil)
}

// RotateVisitorSessionSecret runs one rotation end to end and reports what it did (#2195).
//
// Synchronous, because the caller is either the scheduler's own goroutine or an admin waiting on
// an HTTP response, and both want the outcome rather than an acknowledgement that something has
// been queued. Bounded by visitorSessionAckTimeout.
//
// r is the HTTP request for a manual rotation, used only to record the caller's address on the
// audit entry; nil for a periodic one.
func (s *Server) RotateVisitorSessionSecret(ctx context.Context, trigger, actor string, r *http.Request) visitorSessionRotationOutcome {
	now := time.Now().UTC()
	outcome := visitorSessionRotationOutcome{
		At:             now,
		Trigger:        trigger,
		Actor:          actor,
		Acknowledged:   []string{},
		Unacknowledged: []visitorSessionNodeStatus{},
	}

	// One rotation at a time. A manual trigger landing on top of the periodic one would have
	// two runners minting generations into the same bounded set and gating on each other's
	// acknowledgements.
	if !s.visitorSessionRotationMu.TryLock() {
		return s.abortVisitorSessionRotation(outcome, "a rotation is already running on this control plane", r)
	}
	defer s.visitorSessionRotationMu.Unlock()

	if s.db == nil {
		return s.abortVisitorSessionRotation(outcome, "only the control plane can rotate the visitor session keys, and this node has no database", r)
	}

	state := loadVisitorSessionRotationState(s.db)
	if state.Retirements == nil {
		state.Retirements = make(map[string]time.Time)
	}

	stored := pruneUncommittedGenerations(s.visitorSessionSecrets.get(), state.Retirements)
	if len(stored.Secrets) == 0 || stored.CurrentID == "" {
		return s.abortVisitorSessionRotation(outcome, "this control plane holds no visitor session keys to rotate from", r)
	}
	outcome.PreviousGeneration = stored.CurrentID

	if len(stored.Secrets)+1 > maxAcceptedVisitorSessionSecrets {
		return s.abortVisitorSessionRotation(outcome, fmt.Sprintf(
			"a new generation would make %d accepted keys, more than the %d a node holds; generation(s) %s are still waiting to retire",
			len(stored.Secrets)+1, maxAcceptedVisitorSessionSecrets, strings.Join(generationIDs(stored.Secrets), ", ")), r)
	}

	// ---- PHASE 1: DISTRIBUTE ----
	//
	// The new generation joins the accepted set; CurrentID is deliberately untouched, so every
	// node goes on minting with the outgoing key until the commit. That is what makes an abort
	// here cost nothing.
	fresh, err := newVisitorSessionSecret()
	if err != nil {
		return s.abortVisitorSessionRotation(outcome, fmt.Sprintf("a new generation could not be minted: %v", err), r)
	}
	outcome.Generation = fresh.ID

	distributed := storedVisitorSessionSecrets{
		CurrentID: stored.CurrentID,
		Secrets:   append(append([]VisitorSessionSecret{}, stored.Secrets...), fresh),
	}
	// Snapshotted BEFORE the push, so an acknowledgement recorded afterwards is provably about
	// this set and not a stale one from the node's last handshake.
	pushedAt := time.Now()
	if err := s.applyAndPersistVisitorSessionSecrets(s.db, distributed); err != nil {
		return s.abortVisitorSessionRotation(outcome, fmt.Sprintf("the new generation could not be established on the control plane: %v", err), r)
	}

	targets, failedPush := s.pushVisitorSessionSecretsToFleet()

	acked, missing := s.awaitVisitorSessionAcks(ctx, targets, pushedAt, failedPush, func(ack visitorSessionAck) bool {
		return ack.holds(fresh.ID)
	})
	outcome.Acknowledged = acked
	outcome.Unacknowledged = missing
	if len(missing) > 0 {
		// FAIL CLOSED. CurrentID was never moved, so the outgoing generation is still the one
		// every node mints with and not one visitor session is affected. The distributed
		// generation stays in the accepted set until the next attempt prunes it -- nothing has
		// signed with it, so it verifies nothing and changes no behaviour.
		return s.abortVisitorSessionRotation(outcome, fmt.Sprintf(
			"%d of %d connected node(s) did not confirm they hold generation %s, so the switch did not happen and generation %s goes on minting",
			len(missing), len(targets), fresh.ID, stored.CurrentID), r)
	}

	// ---- PHASE 2: COMMIT ----
	if err := s.commitVisitorSessionGeneration(ctx, distributed, fresh, targets); err != nil {
		return s.abortVisitorSessionRotation(outcome, fmt.Sprintf("the switch to generation %s could not be recorded, so it did not happen: %v", fresh.ID, err), r)
	}

	// ---- PHASE 3 IS SCHEDULED, NOT RUN ----
	//
	// The outgoing generation keeps verifying for longer than a cookie can live. retire happens
	// on a later sweep; doing it here is the defect.
	state.Retirements[stored.CurrentID] = now.Add(visitorSessionRetirementLag)
	outcome.Outcome = visitorSessionOutcomeCommitted
	state.Last = &outcome
	if err := saveVisitorSessionRotationState(s.db, state); err != nil {
		// The switch stands; only the bookkeeping failed. Said out loud because a lost
		// retirement schedule means the outgoing generation verifies until something else
		// notices it.
		slog.Error(fmt.Sprintf("[Session] Rotated to generation %s but could not record the retirement of %s: %v", fresh.ID, stored.CurrentID, err))
	}

	slog.Info(fmt.Sprintf("[Session] Visitor session-key rotation (%s) committed: minting with generation %s, %d node(s) acknowledged, %s retires %s",
		trigger, fresh.ID, len(acked), stored.CurrentID, now.Add(visitorSessionRetirementLag).Format(time.RFC3339)))
	s.auditVisitorSessionRotation(visitorSessionAuditRotated, auditActorFor(trigger, actor), fresh.ID, outcome.auditDetails(), r)
	return outcome
}

// pushVisitorSessionSecretsToFleet sends the current key set to every connected node, returning
// the roster it targeted and, per node, why a push did not go out.
//
// EVERY CURRENTLY-CONNECTED NODE -- never every configured one. edge-us and edge-sa are powered
// off nightly, and a rotation gated on them would fail closed every night while appearing to
// work. A node that was asleep is handed the set at its own handshake instead.
func (s *Server) pushVisitorSessionSecretsToFleet() (targets []string, failures map[string]string) {
	targets = s.connectedEdgeNodeIDs()
	failures = make(map[string]string, len(targets))
	for _, nodeID := range targets {
		if err := s.pushVisitorSessionSecrets(nodeID); err != nil {
			failures[nodeID] = fmt.Sprintf("the key set could not be sent: %v", err)
		}
	}
	return targets, failures
}

// commitVisitorSessionGeneration is PHASE TWO: it makes the distributed generation the one the
// fleet mints with.
//
// The outgoing generation stays ACCEPTED. That is the verify-many/mint-one property #2181 built,
// and it is the reason a visitor holding a cookie signed a minute ago is not logged out here.
//
// A node that does not confirm the switch is logged, not fatal: readiness was decided in phase
// one, the switch is already persisted, and the outgoing generation is still accepted
// everywhere -- so such a node mints with a key the whole fleet still honours until its next
// handshake or the next rotation's push corrects it. That window is precisely what the second
// term of visitorSessionRetirementLag pays for.
func (s *Server) commitVisitorSessionGeneration(ctx context.Context, distributed storedVisitorSessionSecrets, fresh VisitorSessionSecret, targets []string) error {
	committed := storedVisitorSessionSecrets{CurrentID: fresh.ID, Secrets: distributed.Secrets}
	committedAt := time.Now()
	if err := s.applyAndPersistVisitorSessionSecrets(s.db, committed); err != nil {
		return err
	}

	for _, nodeID := range targets {
		if err := s.pushVisitorSessionSecrets(nodeID); err != nil {
			slog.Warn(fmt.Sprintf("[Session] %s was not told to switch to generation %s: %v", nodeID, fresh.ID, err))
		}
	}
	switched, notSwitched := s.awaitVisitorSessionAcks(ctx, targets, committedAt, nil, func(ack visitorSessionAck) bool {
		return ack.CurrentID == fresh.ID
	})
	slog.Info(fmt.Sprintf("[Session] %d of %d connected node(s) confirmed they are minting with generation %s", len(switched), len(targets), fresh.ID))
	for _, node := range notSwitched {
		slog.Info(fmt.Sprintf("[Session] %s has not yet confirmed it is minting with generation %s (%s); it will be re-told at its next handshake", node.NodeID, fresh.ID, node.Why))
	}
	return nil
}

// abortVisitorSessionRotation records a rotation that did not switch, and returns it.
//
// The reason is the whole value of the event, so it is carried on the outcome, written into the
// audit details and persisted for the portal to read back.
func (s *Server) abortVisitorSessionRotation(outcome visitorSessionRotationOutcome, reason string, r *http.Request) visitorSessionRotationOutcome {
	outcome.Outcome = visitorSessionOutcomeAborted
	outcome.Reason = reason
	slog.Warn(fmt.Sprintf("[Session] Visitor session-key rotation aborted (%s): %s", outcome.Trigger, reason))
	if s.db != nil {
		state := loadVisitorSessionRotationState(s.db)
		state.Last = &outcome
		if err := saveVisitorSessionRotationState(s.db, state); err != nil {
			slog.Warn(fmt.Sprintf("[Session] Could not record the aborted rotation: %v", err))
		}
	}
	s.auditVisitorSessionRotation(visitorSessionAuditAborted, auditActorFor(outcome.Trigger, outcome.Actor), outcome.Generation, outcome.auditDetails(), r)
	return outcome
}

// auditActorFor resolves the audit actor_id.
//
// A periodic rotation is filed under a reserved id rather than under whoever happened to deploy
// last, so "which rotations did nobody ask for" is a query against actor_id -- the field
// ListAuditEntries can filter on -- rather than a search through prose.
func auditActorFor(trigger, actor string) string {
	if trigger == visitorSessionTriggerManual && strings.TrimSpace(actor) != "" {
		return actor
	}
	return visitorSessionPeriodicActor
}

// auditDetails renders one rotation into the sentence that goes in admin_audit_log.
//
// Everything the issue asks to be recorded is here: the trigger, the actor on a manual run, the
// outcome, the generation moved to, which nodes acknowledged, and which did not AND WHY. On an
// abort the reason comes last because it is the part somebody is reading the entry for.
//
// Generation ids only; a key never reaches this string.
func (o visitorSessionRotationOutcome) auditDetails() string {
	var b strings.Builder
	if o.Trigger == visitorSessionTriggerManual {
		fmt.Fprintf(&b, "Manual rotation requested by %s.", o.Actor)
	} else {
		b.WriteString("Periodic rotation, on the control plane's own schedule.")
	}
	if o.committed() {
		fmt.Fprintf(&b, " Committed: minting moved from generation %s to %s.", o.PreviousGeneration, o.Generation)
		fmt.Fprintf(&b, " Generation %s stays verifiable for %s so sessions already signed with it survive.", o.PreviousGeneration, visitorSessionRetirementLag)
	} else {
		fmt.Fprintf(&b, " Aborted: no switch happened and generation %s goes on minting.", o.PreviousGeneration)
	}
	if len(o.Acknowledged) > 0 {
		fmt.Fprintf(&b, " Acknowledged by %s.", strings.Join(o.Acknowledged, ", "))
	} else {
		b.WriteString(" No edge node acknowledged (none was connected, or none answered).")
	}
	if len(o.Unacknowledged) > 0 {
		parts := make([]string, 0, len(o.Unacknowledged))
		for _, node := range o.Unacknowledged {
			parts = append(parts, fmt.Sprintf("%s (%s)", node.NodeID, node.Why))
		}
		fmt.Fprintf(&b, " Not acknowledged by %s.", strings.Join(parts, "; "))
	}
	if o.Reason != "" {
		fmt.Fprintf(&b, " Reason: %s.", o.Reason)
	}
	return b.String()
}

// awaitVisitorSessionAcks waits for every named node to report a state satisfying want, and
// returns who did and who did not with the reason.
//
// Two things make a node stop being awaited without failing the phase:
//
//   - It DISCONNECTED. A node that has gone is no longer a currently-connected node, which is
//     what readiness is defined against, and it is handed the current set at its next handshake
//     -- #2181's existing push. Blocking a commit on a node that powered off mid-rotation would
//     reintroduce constraint 2's failure by the back door.
//   - The context was cancelled. Stop is shutting the control plane down and a half-finished
//     rotation is fail-closed by construction.
func (s *Server) awaitVisitorSessionAcks(ctx context.Context, nodes []string, since time.Time, pushFailures map[string]string, want func(visitorSessionAck) bool) (acknowledged []string, unacknowledged []visitorSessionNodeStatus) {
	acknowledged = []string{}
	unacknowledged = []visitorSessionNodeStatus{}
	if len(nodes) == 0 {
		return acknowledged, unacknowledged
	}

	timeout, poll := visitorSessionAckTunables()
	deadline := time.Now().Add(timeout)
	pending := make(map[string]bool, len(nodes))
	for _, nodeID := range nodes {
		pending[nodeID] = true
	}

	for len(pending) > 0 {
		stillConnected := make(map[string]bool)
		for _, nodeID := range s.connectedEdgeNodeIDs() {
			stillConnected[nodeID] = true
		}
		for nodeID := range pending {
			if !stillConnected[nodeID] {
				// Gone, not silent. Recorded in the outcome so the audit entry says what
				// happened, but not a reason to abort.
				delete(pending, nodeID)
				slog.Info(fmt.Sprintf("[Session] %s dropped its control connection during the rotation; it will be told the keys at its next handshake", nodeID))
				continue
			}
			ack, seen := s.visitorSessionAcks.get(nodeID)
			if !seen || !ack.At.After(since) || !want(ack) {
				continue
			}
			acknowledged = append(acknowledged, nodeID)
			delete(pending, nodeID)
		}
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}

	for nodeID := range pending {
		why := "it did not answer before the rotation's deadline"
		if reason, failed := pushFailures[nodeID]; failed {
			why = reason
		}
		unacknowledged = append(unacknowledged, visitorSessionNodeStatus{NodeID: nodeID, Why: why})
	}
	sort.Strings(acknowledged)
	sort.Slice(unacknowledged, func(i, j int) bool { return unacknowledged[i].NodeID < unacknowledged[j].NodeID })
	return acknowledged, unacknowledged
}

// generationIDs lists a set's generation ids. Ids are labels, not secrets.
func generationIDs(secrets []VisitorSessionSecret) []string {
	ids := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		ids = append(ids, secret.ID)
	}
	return ids
}

// auditVisitorSessionRotation writes one rotation entry to admin_audit_log, synchronously.
//
// Deliberately not s.writeAudit, which fires a goroutine and drops the error, for the reason
// auditDiagnostics gives: on an abort THIS ENTRY IS THE ONLY RECORD that a rotation was
// attempted and did not happen, and a trail with silent gaps is worse than one known to be
// incomplete. A failure is logged loudly and does not fail the rotation.
func (s *Server) auditVisitorSessionRotation(action, actor, generation, details string, r *http.Request) {
	if s.db == nil {
		return
	}
	ip := ""
	if r != nil {
		ip = s.clientIP(r)
	}
	if err := s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     action,
		TargetType: "session_secret",
		TargetID:   generation,
		Details:    details,
		IPAddress:  ip,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		slog.Error("[Audit] Could not record a visitor session-key rotation",
			"action", action, "actor", actor, "generation", generation, "error", err)
	}
}
