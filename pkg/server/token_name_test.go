package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// #2348: a token name was checked only for being non-empty, so it could carry line breaks, tabs,
// NUL and any length into emails, both portals, audit rows and the client's advice lines.

func TestValidateTokenName(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		err            error
	}{
		{"ordinary", "Work Laptop", "Work Laptop", nil},
		{"non-ASCII is fine", "café ✓ — ビルド", "café ✓ — ビルド", nil},
		{"trimmed", "  CI  ", "CI", nil},
		{"exactly the limit", strings.Repeat("é", maxTokenNameLength), strings.Repeat("é", maxTokenNameLength), nil},
		{"one over, counted in characters not bytes", strings.Repeat("é", maxTokenNameLength+1), "", errTokenNameTooLong},
		{"empty", "", "", errTokenNameRequired},
		{"only spaces", "   ", "", errTokenNameRequired},
		{"line feed", "a\nb", "", errTokenNameControl},
		{"carriage return", "a\rb", "", errTokenNameControl},
		{"tab", "a\tb", "", errTokenNameControl},
		{"NUL", "a\x00b", "", errTokenNameControl},
		{"DEL", "a\x7fb", "", errTokenNameControl},
		{"C1 next-line", "a\u0085b", "", errTokenNameControl},
		{"header injection shape", "name\r\nBcc: someone@example.com", "", errTokenNameControl},
		{"line separator, which IsControl misses", "a\u2028b", "", errTokenNameControl},
		{"paragraph separator", "a\u2029b", "", errTokenNameControl},
		{"right-to-left override", "invoice\u202Etxt.exe", "", errTokenNameControl},
		{"right-to-left isolate", "a\u2067b", "", errTokenNameControl},
		{"left-to-right mark", "a\u200Eb", "", errTokenNameControl},
		{"Arabic letter mark", "a\u061Cb", "", errTokenNameControl},
		{"ZWJ emoji sequence is a real name", "laptop 👩\u200D💻", "laptop 👩\u200D💻", nil},
		{"ZWNJ is ordinary in Persian", "می\u200Cخواهم", "می\u200Cخواهم", nil},
		{"a trailing newline from a paste is trimmed, not refused", "Work Laptop\n", "Work Laptop", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateTokenName(tc.in)
			if err != tc.err || got != tc.want {
				t.Errorf("validateTokenName(%q) = (%q, %v), want (%q, %v)", tc.in, got, err, tc.want, tc.err)
			}
		})
	}
}

// The routed create path refuses with a 400 that says why, and stores nothing.
func TestCreatingATokenRefusesABadName(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, session := userWithSession(t, srv, "dev@example.com", "developer")

	post := func(name string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]interface{}{"name": name, "expires_in_days": 30})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "http://example.com/api/tokens", bytes.NewBuffer(body))
		req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
		rec := httptest.NewRecorder()
		srv.handleCreateToken(rec, req)
		return rec
	}

	for _, bad := range []string{"a\nb", "a\tb", strings.Repeat("x", maxTokenNameLength+1)} {
		rec := post(bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: got %d, want 400", bad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Token name") {
			t.Errorf("name %q: the refusal does not say what was wrong: %s", bad, rec.Body.String())
		}
	}
	if pats, err := srv.db.ListPATs(dev.ID); err != nil || len(pats) != 0 {
		t.Fatalf("a refused name must store nothing; found %d token(s) (%v)", len(pats), err)
	}

	// The control: an ordinary name is accepted, and stored trimmed.
	if rec := post("  Work Laptop  "); rec.Code != http.StatusCreated {
		t.Fatalf("an ordinary name was refused: %d %s", rec.Code, rec.Body.String())
	}
	pats, err := srv.db.ListPATs(dev.ID)
	if err != nil || len(pats) != 1 || pats[0].Name != "Work Laptop" {
		t.Errorf("expected one token named %q, got %+v (%v)", "Work Laptop", pats, err)
	}
}

// The unrouted create path applies the same rule, so wiring it up later cannot reopen the gap.
func TestThePortalServiceCreatePathRefusesABadNameToo(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresDisabled, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	dev, _ := userWithSession(t, srv, "dev@example.com", "developer")

	// A real expiry on both calls, so the name is the only difference between them. "" means
	// "never", which this policy refuses -- a refusal for the wrong reason.
	expires := time.Now().AddDate(0, 0, 30).UTC().Format(time.RFC3339)
	if _, _, err := srv.portalService.CreateToken(dev, "a\nb", expires, "127.0.0.1"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("portalService.CreateToken must refuse a name with a line break as an invalid request; got %v", err)
	}
	if _, _, err := srv.portalService.CreateToken(dev, "Work Laptop", expires, "127.0.0.1"); err != nil {
		t.Errorf("portalService.CreateToken refused an ordinary name: %v", err)
	}
}

// Both portals cap the input at the server's limit. Read from source, both arms, so the three
// numbers cannot drift -- a portal allowing more than the server accepts lets a user type a name
// that is then refused, and V1/V2 must behave identically.
func TestBothPortalsCapTheTokenNameAtTheServersLimit(t *testing.T) {
	want := fmt.Sprintf("%d", maxTokenNameLength)
	for _, tc := range []struct {
		file    string
		pattern string
	}{
		{"dashboard.html", `id="token-name"[^>]*maxlength="(\d+)"`},
		{filepath.Join("..", "..", "ui", "src", "pages", "Dashboard.tsx"), `id="token-name-label"[\s\S]{0,300}?maxLength=\{(\d+)\}`},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("reading %s: %v", tc.file, err)
		}
		m := regexp.MustCompile(tc.pattern).FindSubmatch(src)
		if m == nil {
			t.Errorf("%s: the token name input has no length cap", tc.file)
			continue
		}
		if string(m[1]) != want {
			t.Errorf("%s caps the token name at %s, the server at %s", tc.file, m[1], want)
		}
	}
}
