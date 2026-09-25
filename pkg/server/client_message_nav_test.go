package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A message a CLIENT receives must not send the reader to a portal location only some readers have.
//
// #2236: the custom-domain refusal was reworded to "add it under Custom Domains in the portal".
// True in V2. In V1, `custom-domains` is an ADMIN_ONLY_TAB, so a non-admin has no such nav item --
// and non-admins are the only people who can ever see that refusal, because admin and owner
// auto-reserve and never reach it. The message named a place, for the wrong arm, to the only
// audience it addresses.
//
// The guard is derived rather than hand-listed: V1 states which tabs are admin-only and states
// each tab's label, so the set of labels a client-facing message must not name follows from the
// source. A twelfth admin tab added later is covered by being written.
func adminOnlyV1NavLabels(t *testing.T) []string {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("static", "dashboard.js"))
	if err != nil {
		t.Fatalf("reading the V1 portal: %v", err)
	}
	text := string(src)

	block := regexp.MustCompile(`(?s)const ADMIN_ONLY_TABS = \[(.*?)\]`).FindStringSubmatch(text)
	if block == nil {
		t.Fatal("ADMIN_ONLY_TABS is gone from dashboard.js; this guard is describing code that " +
			"has moved and needs revisiting rather than deleting")
	}
	adminTabs := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([a-z0-9-]+)'`).FindAllStringSubmatch(block[1], -1) {
		adminTabs[m[1]] = true
	}
	if len(adminTabs) < 5 {
		t.Fatalf("only %d admin-only tab(s) were derived; the parse is broken, so a green result "+
			"here would mean nothing was checked", len(adminTabs))
	}

	var labels []string
	for _, m := range regexp.MustCompile(`nav: 'nav-([a-z0-9-]+)', label: '([^']+)'`).FindAllStringSubmatch(text, -1) {
		if adminTabs[m[1]] {
			labels = append(labels, m[2])
		}
	}
	if len(labels) == 0 {
		t.Fatal("no labels were matched to admin-only tabs; the derivation is broken")
	}
	return labels
}

func TestNoClientFacingRefusalNamesAnAdminOnlyPortalTab(t *testing.T) {
	labels := adminOnlyV1NavLabels(t)

	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	// The messages a CLIENT is shown, not every string in the file: a line that both mentions the
	// portal and is handed to RegisterResponse is one a user reads in their terminal.
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "RegisterResponse{") || !strings.Contains(line, "portal") {
			continue
		}
		for _, label := range labels {
			if strings.Contains(line, label) {
				t.Errorf("a client-facing refusal names the portal tab %q, which V1 hides from "+
					"non-admins (ADMIN_ONLY_TABS) -- and non-admins are the only readers of this "+
					"message, because admin and owner auto-reserve and never see it.\n\n  %s\n\n"+
					"Name the action or a location both arms show, not a top-level tab (#2236).",
					label, strings.TrimSpace(line))
			}
		}
	}
}
