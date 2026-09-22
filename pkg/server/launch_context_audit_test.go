package server

import (
	"strings"
	"testing"
)

// How the client was started, in the audit line — so the portal can say why a tunnel is where it
// is (#2148).
//
// region_source says a region was PROBED; it cannot say whether a routing flag was given at all.
// A client started with no routing flag and one pinned with -pin differ, and the failback prober
// only ever watches the region resolved at startup — so "was -prefer-region given" is the
// question that was unanswerable on 2026-09-21 and had to be inferred from repeated log entries.

// THE constraint: names, never values. -passcode, -basic-auth and -token are all flags, and a
// record carrying their values would be the credential leak #2135 and #2137 just closed.
func TestTheAuditLineCarriesFlagNamesAndNeverValues(t *testing.T) {
	const secret = "hunter2-the-actual-passcode"

	// What a client sends: names only. The value is here purely to prove it cannot appear.
	detail := launchContextDetail(
		[]string{"passcode", "prefer-region", "gui"},
		map[string]string{"auth_token": "LFT_CLIENT_TOKEN"},
	)

	if strings.Contains(detail, secret) {
		t.Fatalf("the audit line contains a flag VALUE: %s", detail)
	}
	for _, want := range []string{"-passcode", "-prefer-region", "-gui"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the audit line does not name %s: %s", want, detail)
		}
	}
	if !strings.Contains(detail, "auth_token=LFT_CLIENT_TOKEN") {
		t.Errorf("an env-claimed setting is not reported: %s", detail)
	}
}

// A setting a FLAG claimed is already in the flag list. Repeating it doubles the line for no new
// information, so only env-claimed settings appear in the second part.
func TestAFlagClaimedSettingIsNotRepeatedAsEnv(t *testing.T) {
	detail := launchContextDetail(
		[]string{"subdomain"},
		map[string]string{"subdomain": "-subdomain", "auth_token": "LFT_CLIENT_TOKEN"},
	)
	if strings.Contains(detail, "subdomain=-subdomain") {
		t.Errorf("a flag-claimed setting was repeated in the env list: %s", detail)
	}
	if !strings.Contains(detail, "-subdomain") {
		t.Errorf("the flag itself is missing: %s", detail)
	}
	if !strings.Contains(detail, "auth_token=LFT_CLIENT_TOKEN") {
		t.Errorf("the env-claimed setting is missing: %s", detail)
	}
}

// A client that predates this sends nothing. The line must be unchanged for them rather than
// carrying an "unknown" nobody can act on.
func TestAnOlderClientLeavesTheLineAlone(t *testing.T) {
	if got := launchContextDetail(nil, nil); got != "" {
		t.Errorf("an older client added %q to the audit line", got)
	}
	if got := launchContextDetail([]string{}, map[string]string{}); got != "" {
		t.Errorf("empty values added %q to the audit line", got)
	}
}

// The distinction this exists to draw: "no routing flag given" must be visibly different from
// "-prefer-region given", because that is what decides whether a client fails back.
func TestNoRoutingFlagIsDistinguishableFromOne(t *testing.T) {
	withFlag := launchContextDetail([]string{"prefer-region"}, nil)
	without := launchContextDetail([]string{"background"}, nil)

	if withFlag == without {
		t.Fatal("a client that named a region and one that did not produce the same audit line")
	}
	if strings.Contains(without, "prefer-region") {
		t.Errorf("a flag that was not given is reported: %s", without)
	}
}

// Field separators must survive a hostile flag name, or one client could forge extra fields into
// somebody's audit row. sanitizeSessionContextField is what the rest of this line already relies
// on; this asserts it is applied here too.
func TestAHostileFlagNameCannotForgeAuditFields(t *testing.T) {
	detail := launchContextDetail(
		[]string{"gui; node fake-edge; region source forged"},
		nil,
	)
	if strings.Contains(detail, "node fake-edge") {
		t.Errorf("a flag name injected audit fields: %s", detail)
	}
}
