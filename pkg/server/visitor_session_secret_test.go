package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// A visitor who has entered a passcode is sent back to the passcode page whenever the tunnel
// moves gateway or the serving gateway restarts (#2181). The cookie is HMAC'd with a key each
// gateway process generated for itself, so the node that now holds the lease reads a perfectly
// good session as a forgery.
//
// The property every test here is written against is one sentence: A COOKIE MINTED BY ONE NODE
// VERIFIES ON ANOTHER. Not "the frame was sent", not "the field is populated" -- those are true
// of a design that still logs the visitor out.

// fleetKeys builds a key set with the given generation ids, each with a distinct key.
func fleetKeys(t *testing.T, ids ...string) []VisitorSessionSecret {
	t.Helper()
	secrets := make([]VisitorSessionSecret, 0, len(ids))
	for _, id := range ids {
		key := sha256.Sum256([]byte("test-material-" + id))
		secrets = append(secrets, VisitorSessionSecret{ID: id, Key: hex.EncodeToString(key[:])})
	}
	return secrets
}

// aGateway is a proxy handler as a freshly started gateway process has one: a node-local
// bootstrap key and nothing else. Each call is a DIFFERENT process, which is the whole subject.
func aGateway(t *testing.T) *ProxyHandler {
	t.Helper()
	p := NewProxyHandler(nil, &config.ServerConfig{Domains: []string{"lfr-demo.se"}})
	if len(p.sessionBootstrap.key) == 0 {
		t.Fatal("the fixture has no bootstrap key, so it is not the gateway this test means")
	}
	return p
}

// THE PROPERTY. Two gateways told the same fleet keys honour each other's sessions.
func TestASessionMintedOnOneGatewayIsHonouredOnAnother(t *testing.T) {
	keys := fleetKeys(t, "gen-1")

	minting := aGateway(t)
	serving := aGateway(t)
	for _, p := range []*ProxyHandler{minting, serving} {
		if err := p.SetVisitorSessionSecrets(keys, "gen-1"); err != nil {
			t.Fatalf("the fleet keys were refused: %v", err)
		}
	}

	cookie, ok := minting.createSessionCookie("peters")
	if !ok {
		t.Fatal("the minting gateway would not sign a session at all")
	}
	if !serving.verifySessionCookie(cookie, "peters") {
		t.Error("a session minted on one gateway was rejected by another holding the same fleet key -- " +
			"this is #2181: the visitor is sent back to the passcode page by the failover")
	}
}

// The control that makes the test above mean something. Without the fleet key each gateway
// signs with its own, and the session dies at the move -- the behaviour before this change.
//
// If this ever goes green the fixture is sharing a key it should not be, and the test above is
// passing for a reason that has nothing to do with the mechanism it names.
func TestWithoutAFleetKeyASessionDiesAtTheMove(t *testing.T) {
	minting := aGateway(t)
	serving := aGateway(t)

	cookie, ok := minting.createSessionCookie("peters")
	if !ok {
		t.Fatal("the minting gateway would not sign a session at all")
	}
	if !minting.verifySessionCookie(cookie, "peters") {
		t.Fatal("a gateway did not honour its own session; the fixture is broken, not the subject")
	}
	if serving.verifySessionCookie(cookie, "peters") {
		t.Error("two gateways that were never told a fleet key accepted each other's sessions -- " +
			"they must have the same node-local key, so this fixture cannot tell a shared key from a per-process one")
	}
}

// Verify-many, mint-one. A key that has stopped minting must go on verifying, or a rotation
// would end every session in flight -- #2181 again, on the rotation's cadence.
func TestAGenerationThatStoppedMintingStillVerifies(t *testing.T) {
	both := fleetKeys(t, "gen-1", "gen-2")

	before := aGateway(t)
	if err := before.SetVisitorSessionSecrets(both[:1], "gen-1"); err != nil {
		t.Fatalf("gen-1 was refused: %v", err)
	}
	cookie, ok := before.createSessionCookie("peters")
	if !ok {
		t.Fatal("the gateway would not sign a session with gen-1")
	}

	after := aGateway(t)
	if err := after.SetVisitorSessionSecrets(both, "gen-2"); err != nil {
		t.Fatalf("the rotated set was refused: %v", err)
	}
	if !after.verifySessionCookie(cookie, "peters") {
		t.Error("a session signed with gen-1 was rejected by a node that still ACCEPTS gen-1 and " +
			"only mints with gen-2 -- verification is following the minting key instead of the accepted set")
	}

	// And it mints with the current generation, not with the one it merely accepts.
	rotated, ok := after.createSessionCookie("peters")
	if !ok {
		t.Fatal("the rotated gateway would not sign a session")
	}
	if before.verifySessionCookie(rotated, "peters") {
		t.Error("a node holding only gen-1 accepted a session the rotated node minted, so the " +
			"rotated node is still signing with gen-1 rather than with the generation it was told is current")
	}
}

// The gap between a gateway starting and central telling it the fleet key. Sessions minted in
// it are not portable -- that is the old bug, bounded to a few seconds -- but they must not
// stop working on the node that issued them the moment the fleet key lands.
func TestTheNodeLocalKeySurvivesTheFleetKeyArriving(t *testing.T) {
	p := aGateway(t)
	early, ok := p.createSessionCookie("peters")
	if !ok {
		t.Fatal("a gateway with only its bootstrap key would not sign a session")
	}

	if err := p.SetVisitorSessionSecrets(fleetKeys(t, "gen-1"), "gen-1"); err != nil {
		t.Fatalf("the fleet keys were refused: %v", err)
	}
	if !p.verifySessionCookie(early, "peters") {
		t.Error("a session minted before the fleet key arrived stopped working on the node that " +
			"minted it -- being told the fleet key logged out every visitor already through the door")
	}

	later, ok := p.createSessionCookie("peters")
	if !ok {
		t.Fatal("the gateway would not sign a session after being told the fleet key")
	}
	other := aGateway(t)
	if err := other.SetVisitorSessionSecrets(fleetKeys(t, "gen-1"), "gen-1"); err != nil {
		t.Fatalf("the fleet keys were refused: %v", err)
	}
	if !other.verifySessionCookie(later, "peters") {
		t.Error("once told the fleet key the gateway went on minting with its node-local key, so " +
			"its sessions still do not survive a move")
	}
}

// A gateway with no key at all mints nothing. A zeroed key would sign every cookie with a value
// anyone can compute, which is worse than asking for the passcode again.
func TestAGatewayWithNoKeyMintsNoSession(t *testing.T) {
	p := aGateway(t)
	p.sessionMu.Lock()
	p.sessionCurrent = visitorSessionKey{}
	p.sessionKeys = nil
	p.sessionBootstrap = visitorSessionKey{}
	p.sessionMu.Unlock()

	if _, ok := p.createSessionCookie("peters"); ok {
		t.Error("a gateway holding no signing key still minted a session cookie")
	}
	if p.verifySessionCookie("peters:9999999999:deadbeef", "peters") {
		t.Error("a gateway holding no signing key accepted a session cookie")
	}
}

// The correct passcode on a gateway that cannot sign: the visitor is challenged again and told
// it is the gateway, never handed a cookie nothing can verify.
func TestTheCorrectPasscodeWithNoKeyIssuesNoCookie(t *testing.T) {
	p := aGateway(t)
	p.sessionMu.Lock()
	p.sessionCurrent = visitorSessionKey{}
	p.sessionKeys = nil
	p.sessionBootstrap = visitorSessionKey{}
	p.sessionMu.Unlock()

	lease := &TunnelLease{SubdomainPrefix: "peters"}
	lease.SetAccessControls(HashPasscode("s3cret-passcode"), "", "passcode")

	form := url.Values{"passcode": {"s3cret-passcode"}, "redirect_uri": {"/"}}
	r := httptest.NewRequest(http.MethodPost, "/lfr-tunnel-verify", strings.NewReader(form.Encode()))
	r.Host = "peters.lfr-demo.se"
	r.RemoteAddr = "198.51.100.9:40000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	if p.checkAccessControls(w, r, lease, "peters.lfr-demo.se") {
		t.Error("the request was let through without a session")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "lfr_tunnel_session" {
			t.Fatal("a session cookie was issued by a gateway with no key to sign it with")
		}
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected the passcode page (401), got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Incorrect passcode") {
		t.Error("the visitor was told their passcode was wrong; it was right, and the gateway was the problem")
	}
}

// Every guard on the applied set, and the state after each refusal: a node that rejects a set
// keeps the one it had, because a working node beats a node holding something nothing agrees
// with.
func TestAMalformedKeySetIsRefusedAndTheOldSetKept(t *testing.T) {
	good := fleetKeys(t, "gen-1")
	tooMany := fleetKeys(t, "a", "b", "c", "d", "e")

	cases := []struct {
		name    string
		secrets []VisitorSessionSecret
		current string
		wants   string
	}{
		{"no keys", nil, "gen-1", "no visitor session signing keys were supplied"},
		{"more keys than the bound", tooMany, "a", "more than the 4 this node accepts"},
		{"no current generation named", good, "", "no current visitor session key generation was named"},
		{"current generation absent from the set", good, "gen-9", "is not among the 1 supplied"},
		{"a key with no generation id", []VisitorSessionSecret{{ID: "", Key: good[0].Key}}, "gen-1", "no generation id"},
		{"the reserved node-local id", []VisitorSessionSecret{{ID: nodeLocalSessionKeyID, Key: good[0].Key}}, nodeLocalSessionKeyID, "reserved generation id"},
		{"a key that is not hex", []VisitorSessionSecret{{ID: "gen-1", Key: "zzzz"}}, "gen-1", "not valid hex"},
		{"a key of the wrong length", []VisitorSessionSecret{{ID: "gen-1", Key: "0011"}}, "gen-1", "is 2 bytes, not 32"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := aGateway(t)
			if err := p.SetVisitorSessionSecrets(good, "gen-1"); err != nil {
				t.Fatalf("the good set was refused: %v", err)
			}
			cookie, ok := p.createSessionCookie("peters")
			if !ok {
				t.Fatal("the gateway would not sign a session with the good set")
			}

			err := p.SetVisitorSessionSecrets(tc.secrets, tc.current)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			// The message, not merely "an error": every case here is refused by a
			// different guard, and "it errored" is satisfied by any of them.
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("refused for the wrong reason: got %q, wanted it to mention %q", err, tc.wants)
			}
			if !p.verifySessionCookie(cookie, "peters") {
				t.Error("the refusal discarded the keys the node already had, so live sessions were dropped over a bad frame")
			}
		})
	}
}

// Central owns the key and PERSISTS it. A key regenerated per central process would move the
// bug rather than fix it: central restarts on every deploy.
func TestCentralKeepsItsKeyAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "central.db")

	first, err := db.Open(path)
	if err != nil {
		t.Fatalf("could not open the database: %v", err)
	}
	before, err := loadOrCreateVisitorSessionSecrets(first)
	if err != nil {
		t.Fatalf("the first process could not establish a key: %v", err)
	}

	// A SECOND handle on the same file, which is what a restarted control plane is: pkg/db
	// exposes no Close, so the restart is modelled by a fresh Open against the same path
	// rather than by a mirror of the load logic (§5c rule 4 -- test production, not a copy).
	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("could not reopen the database: %v", err)
	}
	after, err := loadOrCreateVisitorSessionSecrets(second)
	if err != nil {
		t.Fatalf("the second process could not read the key: %v", err)
	}

	if after.CurrentID != before.CurrentID {
		t.Fatalf("central minted a new key generation on restart (%s -> %s), so every visitor session dies with every deploy",
			before.CurrentID, after.CurrentID)
	}

	// Asserted through the property rather than by comparing strings: a cookie the first
	// process signed has to be honoured by the second.
	minting := aGateway(t)
	if err := minting.SetVisitorSessionSecrets(before.Secrets, before.CurrentID); err != nil {
		t.Fatalf("the first process's keys were refused: %v", err)
	}
	cookie, ok := minting.createSessionCookie("peters")
	if !ok {
		t.Fatal("the first process would not sign a session")
	}

	restarted := aGateway(t)
	if err := restarted.SetVisitorSessionSecrets(after.Secrets, after.CurrentID); err != nil {
		t.Fatalf("the second process's keys were refused: %v", err)
	}
	if !restarted.verifySessionCookie(cookie, "peters") {
		t.Error("a session signed before a control-plane restart was rejected after it")
	}
}

// A corrupt stored value is replaced rather than fatal, and the replacement is usable.
func TestACorruptStoredKeySetIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "central.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("could not open the database: %v", err)
	}

	if err := database.SetAdminSetting(visitorSessionSecretSettingKey, "{not json"); err != nil {
		t.Fatalf("could not seed the corrupt row: %v", err)
	}
	stored, err := loadOrCreateVisitorSessionSecrets(database)
	if err != nil {
		t.Fatalf("a corrupt row stopped the control plane establishing a key: %v", err)
	}
	if !stored.valid() {
		t.Fatal("the replacement key set is not usable")
	}

	p := aGateway(t)
	if err := p.SetVisitorSessionSecrets(stored.Secrets, stored.CurrentID); err != nil {
		t.Fatalf("the replacement keys were refused: %v", err)
	}
	if _, ok := p.createSessionCookie("peters"); !ok {
		t.Error("a gateway given the replacement keys still would not sign a session")
	}
}

// The frame has to stay well inside the control channel's read limit: gorilla CLOSES a
// connection that exceeds it, and this channel also carries kicks, schedules and blacklist
// pushes. A session key must never be able to take those down.
func TestTheKeyFrameStaysInsideTheControlChannelReadLimit(t *testing.T) {
	payload, err := json.Marshal(ControlMessage{
		Type:                   visitorSessionSecretFrameType,
		SessionSecrets:         fleetKeys(t, "a", "b", "c", "d"),
		CurrentSessionSecretID: "d",
	})
	if err != nil {
		t.Fatalf("could not encode a full key frame: %v", err)
	}
	if len(payload) > edgeControlReadLimit/8 {
		t.Errorf("a full key frame is %d bytes, uncomfortably close to the %d byte read limit that closes the connection",
			len(payload), edgeControlReadLimit)
	}
}

// The signing key must not reach a log line. This repo has closed two credential-leak issues
// recently (#2135, #2137) and both were a struct that found its way into a format string.
func TestAKeyDoesNotRenderItself(t *testing.T) {
	secret := fleetKeys(t, "gen-1")[0]
	rendered := secret.String()
	if strings.Contains(rendered, secret.Key) {
		t.Errorf("the signing key rendered itself verbatim: %s", rendered)
	}
	if !strings.Contains(rendered, "gen-1") {
		t.Errorf("the generation id was redacted too; it is a label, not a secret: %s", rendered)
	}
}

// End to end over the real control channel, with the real symbols on both sides: a control
// plane with a database and an edge with none. The edge is never configured with a key and has
// no way to read one -- being told over this channel is the only route.
func TestAnEdgeIsToldTheKeysOnItsHandshake(t *testing.T) {
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"lfr-demo.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "usedge-mysecrettokenvalue"
	tokenHash := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHash[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create the control plane: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		controlSrv.Stop()
	}()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	cfgEdge := config.DefaultServerConfig()
	cfgEdge.DBPath = "" // an edge has no database, which is the whole reason this frame exists
	cfgEdge.Domains = []string{"lfr-demo.se"}
	cfgEdge.ControlPlaneURL = ts.URL
	cfgEdge.EdgeToken = edgeToken
	cfgEdge.DisableBackupScheduler = true

	edgeSrv, err := NewServer(cfgEdge)
	if err != nil {
		t.Fatalf("failed to create the edge: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		edgeSrv.Stop()
	}()
	if edgeSrv.db != nil {
		t.Fatal("the edge fixture has a database, so it does not exercise the case this frame exists for")
	}

	// A session minted on the control plane before the edge has been told anything.
	cookie, ok := controlSrv.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the control plane would not sign a session")
	}
	if edgeSrv.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Fatal("the edge honoured the control plane's session before the handshake; the two must " +
			"already share a key, and this test cannot then tell a push from a coincidence")
	}

	waitUntil(t, stated("the edge to be told the visitor session keys over its control channel"), func() bool {
		return edgeSrv.proxyHandler.verifySessionCookie(cookie, "peters")
	})

	// And back the other way: the edge mints with what it was told, so a visitor who entered
	// the passcode on the edge stays signed in when the tunnel fails back to central.
	edgeCookie, ok := edgeSrv.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the edge would not sign a session after being told the keys")
	}
	if !controlSrv.proxyHandler.verifySessionCookie(edgeCookie, "peters") {
		t.Error("a session minted on the edge was rejected by the control plane -- the edge is " +
			"still minting with its own key, so a failback logs the visitor out")
	}
}
