package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// A struct that can reach a browser must not publish a credential (#2137).
//
// TunnelLease had `json:"session_token"`, and the admin users list embeds whole leases as
// active_tunnels -- so every active tunnel's session token was serialised to any admin or owner
// loading that page. The token is not a hash of a credential, it IS one: /api/deregister
// authenticates on nothing else, so holding it is enough to tear down that session's tunnels.
//
// Asserted by MARSHALLING rather than by reading the tag, because the tag is not the only way to
// publish a field -- an embedded struct, a custom MarshalJSON or a map built from the lease would
// all reintroduce it without the tag changing.
func TestALeaseNeverSerialisesItsSessionToken(t *testing.T) {
	const secret = "a-real-session-token-value"
	// Plaintext "user:password": proxy.go compares the decoded Authorization header against
	// this byte for byte, so it is a working login and not a hash of one.
	const basicAuthSecret = "admin:hunter2"
	lease := &TunnelLease{
		UserID:          "user-1",
		SubdomainPrefix: "peters",
		FullHost:        "peters.lfr-demo.se",
		SessionToken:    secret,
		BasicAuth:       basicAuthSecret,
		LocalPort:       8080,
	}

	encoded, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("marshalling a lease: %v", err)
	}
	body := string(encoded)

	if strings.Contains(body, secret) {
		t.Errorf("a marshalled lease contains the session token:\n    %s\n"+
			"/api/deregister authenticates on that value alone, so publishing it hands every "+
			"viewer the means to drop any tunnel they can see.", body)
	}
	if strings.Contains(body, "session_token") {
		t.Errorf("a marshalled lease still carries a session_token key:\n    %s", body)
	}
	if strings.Contains(body, basicAuthSecret) || strings.Contains(body, "basic_auth") {
		t.Errorf("a marshalled lease contains the Basic Auth credential:\n    %s\n"+
			"It is stored as plaintext user:password and compared byte for byte, so this is a "+
			"working login for every protected tunnel.", body)
	}

	// The fields the portal legitimately shows must survive, or this "fix" is just breakage.
	for _, want := range []string{"peters", "peters.lfr-demo.se", "user-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("a marshalled lease no longer carries %q; the admin view needs it", want)
		}
	}
}

// The same rule applied to the payload that actually goes out, so the two cannot drift: a future
// response could embed the lease differently and reintroduce the field.
func TestTheAdminUsersPayloadCarriesNoSessionToken(t *testing.T) {
	const secret = "another-session-token"
	type adminUserResponse struct {
		ActiveTunnels []*TunnelLease `json:"active_tunnels"`
	}

	encoded, err := json.Marshal(adminUserResponse{
		ActiveTunnels: []*TunnelLease{{
			SubdomainPrefix: "gatxdemo",
			FullHost:        "gatxdemo.lfr-demo.se",
			SessionToken:    secret,
		}},
	})
	if err != nil {
		t.Fatalf("marshalling the payload: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("active_tunnels carries the session token: %s", encoded)
	}
}
