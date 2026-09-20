package client

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Settings panel used to repopulate six fields from the RUNNING session, so an edit
// vanished on refresh and the save looked broken when it had worked (#2088).
//
// These read the shipped page, because that is the artefact: dashboard.html is embedded and
// served as-is, so a regression here is a regression in the product with nothing else to catch
// it.

func dashboard(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("reading dashboard.html: %v", err)
	}
	return string(body)
}

// The reported defect: an edit must survive a refresh.
func TestRestartFieldsArePopulatedFromTheSavedConfig(t *testing.T) {
	page := dashboard(t)

	for _, id := range []string{
		"cfg-server-url", "cfg-auth-token", "cfg-dest-port",
		"cfg-target-host", "cfg-subdomain", "cfg-preserve-host",
	} {
		assign := regexp.MustCompile(`getElementById\('` + regexp.QuoteMeta(id) + `'\)\.(value|checked)\s*=\s*([^;]+);`)
		m := assign.FindStringSubmatch(page)
		if m == nil {
			t.Errorf("%s is never populated", id)
			continue
		}
		if strings.Contains(m[2], "pick(") {
			t.Errorf("%s is populated with pick(), which prefers the RUNNING value over the "+
				"saved one.\nThat is the defect: you edit the field, save, refresh, and the "+
				"running session writes your edit back out of the box.", id)
		}
	}
}

// pick() is gone, and must stay gone. It existed only to prefer the running value over the
// saved one, which is precisely the behaviour that made saves look broken -- leaving the helper
// in place invites the next person to reach for it.
func TestTheHelperThatPreferredTheRunningValueIsGone(t *testing.T) {
	page := dashboard(t)

	if regexp.MustCompile(`const pick\s*=`).MatchString(page) {
		t.Error("pick() is back. It returns the RUNNING value whenever the engine has one, so " +
			"any field populated through it reverts on refresh (#2088).")
	}

	// PREMISE: the effective values are still fetched, because they are still shown beside the
	// fields. If they stopped being read, the assertion above would pass on a page that simply
	// no longer knows what is in force.
	if !strings.Contains(page, "cfg.effective") {
		t.Error("the page no longer reads cfg.effective at all, so it cannot tell the user " +
			"which value is actually serving traffic")
	}
}

// A field claimed by a flag or an environment variable can never be changed here, because the
// restart reuses the same argv and the claim is re-applied on every start.
func TestLaunchClaimedFieldsAreMadeReadOnly(t *testing.T) {
	page := dashboard(t)

	if !strings.Contains(page, "launchOverrides[key]") {
		t.Fatal("the panel never consults launch_overrides, so a field a flag owns is still " +
			"rendered as an editable box that silently never takes effect")
	}
	if !regexp.MustCompile(`readOnly = true|disabled = true`).MatchString(page) {
		t.Error("nothing is made read-only or disabled, so the claim is not acted on")
	}
	if !strings.Contains(page, "client_setting_fixed_at_launch") {
		t.Error("a claimed field is locked without saying what claimed it, which is worse than " +
			"leaving it editable -- the user cannot tell why")
	}
}

// A restart drops live connections, so it is offered, never assumed.
func TestTheRestartIsConfirmedBeforeItHappens(t *testing.T) {
	page := dashboard(t)

	offer := strings.Index(page, "async function offerRestart()")
	if offer < 0 {
		t.Fatal("nothing offers a restart, so the settings that need one are saved with no way " +
			"to apply them")
	}
	body := page[offer:min(len(page), offer+1200)]

	confirmAt := strings.Index(body, "confirm(")
	postAt := strings.Index(body, "/api/restart")
	if confirmAt < 0 {
		t.Error("offerRestart never asks. A restart interrupts every tunnelled connection and " +
			"must not follow from pressing Save.")
	}
	if postAt >= 0 && confirmAt >= 0 && confirmAt > postAt {
		t.Error("the restart is requested BEFORE the confirmation is read")
	}
	if !strings.Contains(body, "client_settings_apply_next_start") {
		t.Error("declining the restart says nothing. The user needs to be told the settings are " +
			"saved and will apply at the next start.")
	}
}

// Only when something that needs a restart actually changed.
func TestNoRestartIsOfferedForLiveOnlyChanges(t *testing.T) {
	page := dashboard(t)

	gate := regexp.MustCompile(`restartNeededFields\.length > 0 && changedRestartField`)
	if !gate.MatchString(page) {
		t.Error("the restart offer is not gated on a restart-needing field having changed.\n" +
			"Prompting after every save teaches people to dismiss the prompt, which is how a " +
			"prompt that matters gets ignored.")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
