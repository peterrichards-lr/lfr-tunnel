package client

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Saving from the Settings tab used to erase the tunnel's passcode and rate limit (#1762).
//
// The Settings form posted `passcode: ""` and `rate_limit: 0` — fields it has no control for,
// owned by the Access Control tab — and the handler assigned both unconditionally. So setting a
// passcode and then saving anything unrelated removed the access control from the tunnel, with
// no symptom until someone reached it.
//
// Asserted on the DECODE, because the defect is a decode-level one: with plain value types, an
// absent field and a field set empty are indistinguishable, so removing them from the form is
// not on its own a fix. Any future caller that omits them would erase them again.
//
// AuthToken above these two already carried a guard against the same hazard, which is what
// makes this an oversight rather than a design choice.

// configPostRequest mirrors the anonymous request struct in inspector.go's /api/config handler.
// It has to be kept in step by hand; the assertions below fail loudly if the JSON tags drift.
type configPostRequest struct {
	Passcode  *string `json:"passcode"`
	RateLimit *int    `json:"rate_limit"`
}

func TestConfigPost_OmittedFieldsAreDistinguishableFromEmpty(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantPasscode  bool // was a passcode present in the payload at all
		wantRateLimit bool
	}{
		{
			// What the Settings tab now sends: neither field mentioned.
			name:          "Settings save omits both",
			body:          `{"server_url":"https://gw.example.com","subdomain":"demo"}`,
			wantPasscode:  false,
			wantRateLimit: false,
		},
		{
			// The exact payload that caused the bug. Even now, a caller that sends these
			// explicitly is taken at its word -- which is correct, and is why the fix is at
			// the decode rather than only in the form.
			name:          "explicit empty is still honoured",
			body:          `{"passcode":"","rate_limit":0}`,
			wantPasscode:  true,
			wantRateLimit: true,
		},
		{
			name:          "a real passcode is carried through",
			body:          `{"passcode":"hunter2","rate_limit":64}`,
			wantPasscode:  true,
			wantRateLimit: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req configPostRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("decoding %s: %v", tc.body, err)
			}

			if got := req.Passcode != nil; got != tc.wantPasscode {
				t.Errorf("passcode present = %v, want %v -- an omitted passcode must not read "+
					"as a request to clear it, or an unrelated save removes the tunnel's access control",
					got, tc.wantPasscode)
			}
			if got := req.RateLimit != nil; got != tc.wantRateLimit {
				t.Errorf("rate_limit present = %v, want %v", got, tc.wantRateLimit)
			}
		})
	}
}

// The form half. Belt and braces: the decode fix makes omission safe, and the form should not
// have been claiming to own these in the first place.
//
// The literals are distinctive -- the Access Control form posts `passcode: passcode`, a
// variable -- so their absence is a meaningful assertion rather than a coincidence.
func TestSettingsFormDoesNotPostAccessControlFields(t *testing.T) {
	page, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("reading the Inspector page: %v", err)
	}

	for _, forbidden := range []string{`passcode: ""`, "rate_limit: 0"} {
		if strings.Contains(string(page), forbidden) {
			t.Errorf("the Settings form still posts %s -- it has no control for it, the Access "+
				"Control tab owns it, and sending it erases the user's value (#1762)", forbidden)
		}
	}
}

// The decode test above documents the SEMANTICS of pointer-vs-value, but it decodes into its
// own mirror of the request struct — so on its own it would keep passing if inspector.go
// reverted to value types. This asserts the production code, which is the thing that matters.
//
// Source-level rather than by driving the handler: /api/config's closure calls
// config.SaveClientConfig(""), which writes to the real ~/.lfr-tunnel/config.yaml. A test that
// exercised it end to end would edit the developer's own configuration. The same idiom is used
// by cmd/lfr-tunnel/hooks_wiring_test.go for a call site that cannot be unit-driven.
func TestConfigHandlerKeepsPasscodeAndRateLimitOptional(t *testing.T) {
	src, err := os.ReadFile("inspector.go")
	if err != nil {
		t.Fatalf("reading inspector.go: %v", err)
	}
	code := string(src)

	// Matched on type-and-tag rather than exact spacing: gofmt realigns the struct whenever a
	// neighbouring field name changes length, and a test that broke on that would be noise.
	for _, want := range []string{
		"*string `json:\"passcode\"`",
		"*int    `json:\"rate_limit\"`",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("the /api/config request struct no longer declares %s.\n"+
				"With a value type, an omitted field decodes to the zero value and is "+
				"indistinguishable from one deliberately set empty -- which is how a Settings "+
				"save erased the tunnel's passcode (#1762).", want)
		}
	}

	for _, want := range []string{
		"if req.Passcode != nil {",
		"if req.RateLimit != nil {",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("the /api/config handler no longer guards on %q -- an omitted field must "+
				"mean \"leave it alone\", not \"clear it\" (#1762)", want)
		}
	}
}
