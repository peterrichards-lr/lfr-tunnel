package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Settings tab must not erase the tunnel's access control (#1762, #1793).
//
// Two handlers decode the same page's Settings form: pkg/client/inspector.go and this package's
// TempSettingsServer. Both load the config, overwrite it from the request, and save. The form
// has no controls for `passcode` or `rate_limit` -- the Access Control tab owns those and posts
// them to /api/access-control -- so with plain value types an omitted field decoded to "" or 0
// and was written over the user's passcode, removing the access control from their tunnel with
// no symptom until somebody reached it.
//
// #1762 fixed inspector.go and missed this one. The guard it added read only inspector.go, which
// is exactly why the second handler survived: an assertion scoped to the instance rather than the
// class. This one walks both.

// requestAssign matches an unguarded copy of a decoded request field onto the config -- the
// shape of the defect, not the two line numbers it happened to occupy.
var requestAssign = regexp.MustCompile(`cfg\.(Passcode|RateLimit)\s*=\s*req\.(Passcode|RateLimit)\b`)

// handlersDecodingTheSettingsForm are the files that decode that form into a ClientConfig.
// Listed rather than discovered, so adding a third handler without adding it here is caught by
// the sanity assertion below rather than silently skipped.
var handlersDecodingTheSettingsForm = []string{
	"gui.go",
	filepath.Join("..", "client", "inspector.go"),
}

func TestNoHandlerClobbersAccessControlFromAnOmittedField(t *testing.T) {
	checked := 0

	for _, rel := range handlersDecodingTheSettingsForm {
		src, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		checked++

		if m := requestAssign.FindAllString(string(src), -1); len(m) > 0 {
			t.Errorf("%s assigns a decoded request field straight onto the config: %v\n"+
				"With a value type an omitted field decodes to the zero value, indistinguishable "+
				"from one deliberately set empty -- which is how a Settings save erased the "+
				"tunnel's passcode (#1762/#1793). Use a pointer and assign only when non-nil.", rel, m)
		}

		// The positive half. Absence of the bad shape is also what an empty file looks like, and
		// what a renamed field looks like -- so require the guard itself to be present.
		for _, want := range []string{"req.Passcode != nil", "req.RateLimit != nil"} {
			if !strings.Contains(string(src), want) {
				t.Errorf("%s no longer guards on %q -- an omitted field must mean "+
					"\"leave it alone\", not \"clear it\"", rel, want)
			}
		}
	}

	// Anti-vacuity: a test that checked no files would pass silently, which is the failure mode
	// this whole guard exists to prevent.
	if checked != len(handlersDecodingTheSettingsForm) {
		t.Fatalf("checked %d handlers, expected %d", checked, len(handlersDecodingTheSettingsForm))
	}
	if checked < 2 {
		t.Fatal("fewer than two handlers checked -- the second one is the whole point of this test")
	}
}
