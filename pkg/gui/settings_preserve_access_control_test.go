package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Settings tab must not erase anything the caller did not send (#1762, #1793, #2056).
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

// requestAssign matches an unguarded copy of ANY decoded request field onto the config -- the
// shape of the defect, not the fields it has happened to occupy.
//
// Widened from (Passcode|RateLimit) in #2056. Scoping it to the two fields that had already
// burned someone is the same mistake one level up: it passed while every OTHER field in both
// handlers was still a plain value, and a POST of `{}` to a live client duly zeroed server_url,
// subdomain, target_host, ports and preserve_host. The class is "an omitted field is
// indistinguishable from an empty one", not "passcode and rate_limit specifically".
var requestAssign = regexp.MustCompile(`cfg\.[A-Za-z]+\s*=\s*req\.[A-Za-z]+\b`)

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
		// what a renamed field looks like -- so require the guards themselves to be present.
		//
		// Every field the form can send, not a sample: a sample is how the previous version of
		// this guard passed over eight unguarded fields.
		// Requires the nil check to be ATTACHED to the assignment it protects, not merely
		// present somewhere in the file. Two earlier versions of this were weaker:
		//
		//   * the first required the literal "req.ServerURL != nil", which broke the moment
		//     the checks moved onto a method whose receiver is `q` -- the guard failed while
		//     the property was perfectly intact. It was testing the spelling.
		//   * the second dropped the receiver but accepted the field being mentioned ANYWHERE.
		//     `anyFieldSet()` mentions every field, so replacing a guarded assignment in
		//     applyTo with `cfg.TargetHost = ""` still passed -- measured, not supposed.
		//
		// Matching the pair is what makes it about the behaviour: the assignment must be the
		// dereference of the pointer whose nil-ness was just checked.
		for _, field := range []string{
			"ServerURL", "TargetHost", "DestPort", "Subdomain",
			"PreserveHost", "InsecureSkipVerify", "Passcode", "RateLimit",
		} {
			// No backreference: Go's RE2 has none, so the receiver is matched loosely on both
			// sides rather than required to be the same token. Still far stronger than a
			// mention -- the check and the assignment must be adjacent and about this field.
			pair := regexp.MustCompile(
				`if\s+\w+\.` + field + `\s*!=\s*nil\s*\{\s*\n\s*cfg\.\w+\s*=\s*[^\n]*\.` + field + `\b`)
			if !pair.MatchString(string(src)) {
				t.Errorf("%s does not guard the assignment of %s on its own nil check -- an "+
					"omitted field must mean \"leave it alone\", not \"clear it\". The check and "+
					"the assignment have to be the same field, together", rel, field)
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
