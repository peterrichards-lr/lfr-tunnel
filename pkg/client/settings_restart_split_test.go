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

// A launch-claimed field must show the RUNNING value, not the saved one.
//
// The two halves of this issue pull in opposite directions and this is where they meet. #2088
// says an editable field must show what a save writes, or edits appear to revert. #1211 says a
// field must never sit empty while the tunnel is up -- a client launched from flags usually has
// no config file, so its saved values are blank.
//
// Both are satisfied by splitting on whether the field can be edited at all: claimed fields
// cannot, so they show what is in force; the rest show what a save would write. The E2E suite
// caught this by failing on an empty #cfg-server-url, which is exactly the state #1211 fixed.
func TestLaunchClaimedFieldsShowTheRunningValue(t *testing.T) {
	page := dashboard(t)

	start := strings.Index(page, "const claimedBy = launchOverrides[key];")
	if start < 0 {
		t.Fatal("the claimed-field branch is gone")
	}
	branch := page[start:min(len(page), start+1400)]

	if !strings.Contains(branch, "eff[key]") {
		t.Error("a launch-claimed field does not read the running value, so a client started " +
			"from flags with no config file shows an empty read-only box while its tunnel is " +
			"up and serving traffic (#1211)")
	}
	assign := regexp.MustCompile(`el\.value = running|el\.checked = !!running`)
	if !assign.MatchString(branch) {
		t.Error("the running value is read but never shown")
	}
}

// renderSettingsView assigns the /api/config response to `cfg`. Reading a field off any other name
// throws a ReferenceError, and the surrounding try/catch swallows it into "Failed to load
// configuration" -- so the whole Settings tab comes up empty and the console is the only place
// that says why.
//
// That shipped in this very change: annotateSettings was called with `data.launch_overrides`
// when no `data` existed in that scope, and every Go test here passed, because they read the
// page as TEXT and text cannot throw. The Playwright suite caught it by loading the page.
func TestLoadConfigReadsTheResponseOffTheNameItAssigned(t *testing.T) {
	page := dashboard(t)

	start := strings.Index(page, "async function renderSettingsView()")
	if start < 0 {
		t.Fatal("renderSettingsView is gone; this test asserts about code that no longer exists")
	}
	// From the function start to the next top-level declaration after it. The window has to be
	// generous: this function builds a large template literal before it fetches anything.
	body := page[start:]
	if next := regexp.MustCompile(`\n        (async )?function `).FindStringIndex(body[30:]); next != nil {
		body = body[:next[0]+30]
	}

	assigned := regexp.MustCompile(`const (\w+) = await res\.json\(\);`).FindStringSubmatch(body)
	if assigned == nil {
		t.Fatal("renderSettingsView no longer assigns the response, so this guard cannot check it")
	}
	name := assigned[1]

	for _, field := range []string{"launch_overrides", "can_restart", "effective"} {
		uses := regexp.MustCompile(`(\w+)\.`+field).FindAllStringSubmatch(body, -1)
		for _, u := range uses {
			if u[1] != name {
				t.Errorf("renderSettingsView reads %s.%s, but the response was assigned to %q.\n"+
					"That is a ReferenceError at runtime, swallowed by the try/catch into "+
					"\"Failed to load configuration\" -- the Settings tab comes up blank.",
					u[1], field, name)
			}
		}
	}
}

// Every settings field must sit inside exactly one of the two sections (#2110).
//
// #2088 established the distinction and labelled it with a muted caption inside the same grid,
// which is what prompted "I do not believe it is very intuitive which settings require a restart
// and which don't". A caption does not bound anything, so a field could sit visually between the
// two groups and belong to neither.
//
// Asserts the GROUPING rather than the wording: the headings can be reworded freely, but a field
// that falls outside both sections fails.
func TestEverySettingsFieldSitsInOneOfTheTwoSections(t *testing.T) {
	page := dashboard(t)

	restartAt := strings.Index(page, `data-i18n="client_settings_group_restart"`)
	liveAt := strings.Index(page, `data-i18n="client_settings_group_live"`)
	saveAt := strings.Index(page, `onclick="saveSettings()"`)

	if restartAt < 0 || liveAt < 0 || saveAt < 0 {
		t.Fatal("the two section headings and the save button no longer all exist; this test " +
			"is asserting about a layout that has changed")
	}
	if restartAt >= liveAt || liveAt >= saveAt {
		t.Fatalf("sections are out of order: restart=%d live=%d save=%d", restartAt, liveAt, saveAt)
	}

	restartFields := regexp.MustCompile(`id="(cfg-[a-z-]+)"`).
		FindAllStringSubmatch(page[restartAt:liveAt], -1)
	liveFields := regexp.MustCompile(`id="(cfg-[a-z-]+)"`).
		FindAllStringSubmatch(page[liveAt:saveAt], -1)

	found := map[string]string{}
	for _, m := range restartFields {
		found[m[1]] = "restart"
	}
	for _, m := range liveFields {
		found[m[1]] = "live"
	}

	// The fields whose group is decided by behaviour, not taste: these are written to
	// config.yaml and read only at startup, so they cannot be anywhere but the restart section.
	for _, id := range []string{
		"cfg-server-url", "cfg-auth-token", "cfg-subdomain", "cfg-target-host",
		"cfg-dest-port", "cfg-preserve-host", "cfg-insecure-skip-verify", "cfg-log-dir",
	} {
		switch found[id] {
		case "restart":
		case "":
			t.Errorf("%s is in neither section -- a field outside both groups tells the user "+
				"nothing about when their change takes effect", id)
		default:
			t.Errorf("%s is in the %q section, but it is only read at startup", id, found[id])
		}
	}

	// ...and the one the handler applies to the running engine.
	if found["cfg-maintenance-path"] != "live" {
		t.Errorf("cfg-maintenance-path is in %q; the handler writes engine.MaintenancePath, so "+
			"it applies without a restart", found["cfg-maintenance-path"])
	}
}
