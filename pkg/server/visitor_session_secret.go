package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"lfr-tunnel/pkg/db"

	"github.com/gorilla/websocket"
)

// A visitor who has entered a tunnel's passcode is given a signed cookie, and until #2181 that
// signature was made with a key each gateway PROCESS generated for itself. Every failover,
// failback and gateway restart therefore sent the visitor back to the passcode page: the node
// that now held the lease had never seen the key the cookie was signed with, so it could only
// read a valid cookie as a forgery.
//
// Central owns the key instead, persists it, and hands it to each edge over the authenticated
// edge control channel. That is the shape edge-sync prescribes for state an edge cannot work
// out for itself -- an edge has no database, so being TOLD is the only route, and this channel
// already carries access control, schedules, roster fingerprints, quota enforcement and kicks.
// Nothing new comes to rest on an edge: the key lives in memory there, like everything else it
// is told, and is re-sent on every handshake.
//
// Central persisting it is the half that is easy to miss. A key regenerated on each CENTRAL
// process would only move the bug from "survives a failover" to "survives a control-plane
// restart", and central restarts for every deploy.

// visitorSessionSecretSettingKey is the admin_settings row central keeps the key set in. Only
// central has a database; an edge never reads this.
const visitorSessionSecretSettingKey = "visitor_session_secrets"

// visitorSessionSecretBytes is the length of one signing key, matching the SHA-256 block that
// HMACs it.
const visitorSessionSecretBytes = 32

// maxAcceptedVisitorSessionSecrets bounds how many keys a node will hold at once.
//
// Verification tries every accepted key, so the bound is what stops a malformed or hostile
// frame from making each visitor request arbitrarily expensive. Four leaves room for a
// current key, the one it replaced, and a prepare/commit rotation in flight -- the rotation
// mechanism itself is a separate issue, and this file deliberately implements none of it.
const maxAcceptedVisitorSessionSecrets = 4

// nodeLocalSessionKeyID is the generation id of the key a gateway makes for itself at startup.
//
// It is the pre-#2181 behaviour, kept as a floor rather than as the design: a node that has
// not been told the fleet key still mints a cookie that IT can verify, exactly as before, so a
// new gateway against an older control plane degrades to the old bug instead of refusing every
// correct passcode. It is never the current key once central has spoken, and it stays in the
// accepted set so cookies minted during the gap keep working on the node that issued them.
//
// The id is deliberately fixed and recognisable: a node-local key is a DIFFERENT key on every
// node, so two nodes sharing this id is not a collision to resolve, it is the statement that
// neither has been told anything yet.
const nodeLocalSessionKeyID = "node-local"

// visitorSessionSecretFrameType is the control-channel frame carrying the key set from central
// down to an edge. Named once so the sender, the edge's switch and the tests cannot drift apart
// over a string literal -- the same reason nodeSetFrameType exists.
const visitorSessionSecretFrameType = "visitor_session_secrets"

// VisitorSessionSecret is one accepted cookie-signing key, as central stores it and as it
// travels down the edge control channel.
//
// ID is a GENERATION label, not a secret, and is safe to log: it exists so a later rotation
// protocol can name a generation unambiguously ("commit to N") rather than inferring one from
// position in a list. Key is the secret and is never logged -- see String below.
type VisitorSessionSecret struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// String redacts the key.
//
// This repo has closed two credential-leak issues recently (#2135, #2137), and the shape both
// had in common was a struct that reached a log line through %v long after the line was
// written. A signing key is only useful to an attacker verbatim, so the type refuses to render
// itself. json.Marshal is unaffected, which is what the control channel needs.
func (s VisitorSessionSecret) String() string {
	return fmt.Sprintf("VisitorSessionSecret{ID:%s, Key:[redacted]}", s.ID)
}

// storedVisitorSessionSecrets is the whole key set: every key a node should ACCEPT, plus which
// one it should MINT with.
//
// Verify-many, mint-one is the property that makes rotation possible later without
// reintroducing this issue. A session cookie lives 24 hours, so a rotation that invalidated the
// previous key at the moment it stopped minting with it would end every visitor session in
// flight -- #2181 again, on whatever cadence the rotation ran. Verification has to outlive
// minting by at least the cookie lifetime, which means the accepted set is a set and not a
// value, from the first line of this design rather than as a later widening.
type storedVisitorSessionSecrets struct {
	CurrentID string                 `json:"current_id"`
	Secrets   []VisitorSessionSecret `json:"secrets"`
}

// valid reports whether a decoded set is usable: a current id, at least one key, every key
// well-formed, and the current id actually present among them.
func (s storedVisitorSessionSecrets) valid() bool {
	if s.CurrentID == "" || len(s.Secrets) == 0 || len(s.Secrets) > maxAcceptedVisitorSessionSecrets {
		return false
	}
	current := false
	for _, secret := range s.Secrets {
		if secret.ID == "" {
			return false
		}
		key, err := hex.DecodeString(secret.Key)
		if err != nil || len(key) != visitorSessionSecretBytes {
			return false
		}
		if secret.ID == s.CurrentID {
			current = true
		}
	}
	return current
}

// newVisitorSessionSecret mints one key with a fresh generation id.
func newVisitorSessionSecret() (VisitorSessionSecret, error) {
	key := make([]byte, visitorSessionSecretBytes)
	if _, err := rand.Read(key); err != nil {
		return VisitorSessionSecret{}, fmt.Errorf("could not read randomness for a visitor session signing key: %w", err)
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return VisitorSessionSecret{}, fmt.Errorf("could not read randomness for a visitor session key id: %w", err)
	}
	return VisitorSessionSecret{ID: hex.EncodeToString(id), Key: hex.EncodeToString(key)}, nil
}

// loadOrCreateVisitorSessionSecrets reads central's key set, creating and persisting one the
// first time.
//
// It returns the set to use alongside any error, because those are separate questions: a key
// that could not be WRITTEN is still a key this process can sign with, and refusing to start
// the control plane over a cookie key would be a far larger outage than the one re-prompt a
// non-durable key eventually costs. The caller applies the set and logs the error.
func loadOrCreateVisitorSessionSecrets(database *db.DB) (storedVisitorSessionSecrets, error) {
	if database == nil {
		return storedVisitorSessionSecrets{}, fmt.Errorf("no database: only the control plane owns visitor session keys")
	}

	raw, exists, err := database.GetAdminSettingOptional(visitorSessionSecretSettingKey)
	if err != nil {
		return storedVisitorSessionSecrets{}, fmt.Errorf("could not read the stored visitor session keys: %w", err)
	}
	if exists && raw != "" {
		var stored storedVisitorSessionSecrets
		if jsonErr := json.Unmarshal([]byte(raw), &stored); jsonErr == nil && stored.valid() {
			return stored, nil
		}
		// Replaced rather than fatal, and said out loud rather than silently: the cost of a
		// replacement is that every visitor enters the passcode once more, and that is worth
		// knowing about when it happens.
		slog.Warn("[Session] The stored visitor session signing keys are unreadable; a new set will replace them and visitors will be asked for their passcode once more.")
	}

	secret, err := newVisitorSessionSecret()
	if err != nil {
		return storedVisitorSessionSecrets{}, err
	}
	stored := storedVisitorSessionSecrets{CurrentID: secret.ID, Secrets: []VisitorSessionSecret{secret}}

	payload, err := json.Marshal(stored)
	if err != nil {
		return stored, fmt.Errorf("could not encode the visitor session keys for storage: %w", err)
	}
	if err := database.SetAdminSetting(visitorSessionSecretSettingKey, string(payload)); err != nil {
		return stored, fmt.Errorf("could not persist the visitor session keys: %w", err)
	}
	return stored, nil
}

// visitorSessionSecretState is central's copy of the key set, held so the control channel can
// hand it to a node that connects later without a database read per handshake.
//
// Late joiners are the normal case, not the exception: edge-us and edge-sa are powered off
// nightly and are absent for hours. Nothing here waits for a quorum or for all configured
// nodes to be present -- a node is told at ITS handshake, whenever that happens.
type visitorSessionSecretState struct {
	mu     sync.RWMutex
	stored storedVisitorSessionSecrets
}

func (v *visitorSessionSecretState) set(stored storedVisitorSessionSecrets) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stored = stored
}

func (v *visitorSessionSecretState) get() storedVisitorSessionSecrets {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.stored
}

// initVisitorSessionSecrets loads central's key set and applies it to this node's own proxy
// handler, which serves tunnels exactly as an edge does.
//
// A node with no database is an edge: it owns nothing here and waits to be told.
func (s *Server) initVisitorSessionSecrets(database *db.DB) {
	if database == nil {
		return
	}
	stored, err := loadOrCreateVisitorSessionSecrets(database)
	if err != nil {
		slog.Error(fmt.Sprintf("[Session] Visitor session signing keys: %v", err))
	}
	if len(stored.Secrets) == 0 {
		return
	}
	s.visitorSessionSecrets.set(stored)
	if s.proxyHandler == nil {
		return
	}
	if err := s.proxyHandler.SetVisitorSessionSecrets(stored.Secrets, stored.CurrentID); err != nil {
		slog.Error(fmt.Sprintf("[Session] Visitor session signing keys were not applied: %v", err))
		return
	}
	slog.Info(fmt.Sprintf("[Session] Visitor session signing keys ready: %d accepted, minting with generation %s", len(stored.Secrets), stored.CurrentID))
}

// SendVisitorSessionSecrets hands one edge node the key set it needs to mint and verify visitor
// session cookies (#2181).
//
// Called on every handshake, which is what makes a node that was powered off all night converge
// on its own the moment it comes back -- the same convergence SendEdgeSchedule and
// BroadcastNodeSet already rely on, and the reason a miss here is logged rather than retried.
func (s *Server) SendVisitorSessionSecrets(nodeID string) {
	if err := s.pushVisitorSessionSecrets(nodeID); err != nil {
		// Logged, never retried. The handshake re-sends, which is what makes a node converge
		// on its own; a miss here costs the node nothing it will not be told again.
		slog.Info(fmt.Sprintf("[Edge WS] Visitor session keys for %s were not sent: %v", nodeID, err))
	}
}

// pushVisitorSessionSecrets is SendVisitorSessionSecrets with the failure returned rather than
// only logged (#2195).
//
// The rotation engine needs the answer: a push that did not go out is a node that cannot
// acknowledge, and the whole point of a prepare/commit gate is to be able to say WHICH node and
// WHY rather than to wait out a timeout with no explanation.
func (s *Server) pushVisitorSessionSecrets(nodeID string) error {
	stored := s.visitorSessionSecrets.get()
	if len(stored.Secrets) == 0 {
		// Not the control plane, or the key set could not be established. Either way there is
		// nothing true to say, and saying nothing leaves the edge on its node-local key rather
		// than on no key at all.
		return fmt.Errorf("this node holds no visitor session keys to send")
	}

	payload, err := json.Marshal(ControlMessage{
		Type:                   visitorSessionSecretFrameType,
		SessionSecrets:         stored.Secrets,
		CurrentSessionSecretID: stored.CurrentID,
	})
	if err != nil {
		return fmt.Errorf("the visitor session keys could not be encoded: %w", err)
	}

	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()
	if !exists || conn == nil {
		return fmt.Errorf("it has no control connection")
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("the control channel write failed: %w", err)
	}
	// Counts and generation ids only. The keys themselves never reach a log line.
	slog.Info(fmt.Sprintf("[Edge WS] Told %s the visitor session keys: %d accepted, minting with generation %s", nodeID, len(stored.Secrets), stored.CurrentID))
	return nil
}

// applyAndPersistVisitorSessionSecrets makes one key set authoritative on central: validated,
// applied to central's own proxy handler, then written to admin_settings (#2195).
//
// The order is the design. Validation happens inside SetVisitorSessionSecrets, so applying
// first means a set central itself would refuse is never written to the database -- the
// alternative persists a row that every node including this one rejects on the next restart.
// Persisting second means a write failure leaves a fleet that agrees with itself and a database
// one rotation behind, which the next rotation corrects.
func (s *Server) applyAndPersistVisitorSessionSecrets(database *db.DB, stored storedVisitorSessionSecrets) error {
	if database == nil {
		return fmt.Errorf("no database: only the control plane owns visitor session keys")
	}
	if !stored.valid() {
		return fmt.Errorf("the key set is not usable: %d keys, current generation %q", len(stored.Secrets), stored.CurrentID)
	}
	if s.proxyHandler != nil {
		if err := s.proxyHandler.SetVisitorSessionSecrets(stored.Secrets, stored.CurrentID); err != nil {
			return fmt.Errorf("the control plane refused its own key set: %w", err)
		}
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("could not encode the visitor session keys for storage: %w", err)
	}
	if err := database.SetAdminSetting(visitorSessionSecretSettingKey, string(payload)); err != nil {
		return fmt.Errorf("could not persist the visitor session keys: %w", err)
	}
	s.visitorSessionSecrets.set(stored)
	return nil
}
