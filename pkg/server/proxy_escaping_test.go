package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The visitor-facing proxy pages render values chosen by whoever made the request, and they are
// reached before a tunnel is identified: the WAF branch runs before the lease lookup, and the
// passcode page renders on a *failed* passcode. These tests pin the escaping (#1323) and the
// redirect validation (#1324).

func TestSafeRedirectPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty falls back to root", "", "/"},
		{"root is kept", "/", "/"},
		{"ordinary path is kept", "/dashboard", "/dashboard"},
		{"query string survives", "/page?tab=2&x=1", "/page?tab=2&x=1"},
		{"fragment survives", "/page#section", "/page#section"},

		// Each of these leaves the hostname, which is what the passcode gate exists to control.
		{"absolute https is refused", "https://example.invalid/", "/"},
		{"absolute http is refused", "http://example.invalid/", "/"},
		{"protocol-relative is refused", "//example.invalid", "/"},
		{"backslash form is refused", "/\\example.invalid", "/"},
		{"scheme-relative with creds is refused", "https://user@example.invalid", "/"},
		{"a bare path without a leading slash is refused", "example.invalid", "/"},
		{"javascript scheme is refused", "javascript:alert(1)", "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeRedirectPath(tc.in); got != tc.want {
				t.Errorf("safeRedirectPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderPage_EscapesEveryValue(t *testing.T) {
	out := renderPage(`<span>{{.Host}}</span>`, map[string]string{
		"{{.Host}}": `<script>alert(1)</script>`,
	})

	if strings.Contains(out, "<script>") {
		t.Errorf("a script tag survived into the page: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected the value to be escaped, got %q", out)
	}
}

// passcode.html puts RedirectURI inside value="...", so escaping only the angle brackets would
// still let a quote close the attribute and start an event handler.
func TestRenderPage_EscapesQuotesForAttributeContext(t *testing.T) {
	out := renderPage(`<input value="{{.RedirectURI}}">`, map[string]string{
		"{{.RedirectURI}}": `/x" onmouseover="alert(1)`,
	})

	if strings.Contains(out, `" onmouseover="`) {
		t.Errorf("the value broke out of the attribute: %q", out)
	}
	if !strings.Contains(out, "&#34;") {
		t.Errorf("expected the quote to be escaped, got %q", out)
	}
}

// A chain of ReplaceAll calls re-scans text it has already substituted, so a value containing
// another page's placeholder would be expanded on a later pass -- letting the caller choose
// where a different value lands. A single-pass replacer matches the original template only.
func TestRenderPage_DoesNotExpandPlaceholdersFoundInValues(t *testing.T) {
	out := renderPage(`<p>{{.RedirectURI}}</p><p>{{.Error}}</p>`, map[string]string{
		"{{.RedirectURI}}": "{{.Error}}",
		"{{.Error}}":       "the-error-text",
	})

	if strings.Count(out, "the-error-text") != 1 {
		t.Errorf("a placeholder inside a value was expanded; got %q", out)
	}
}

func TestServeBlockedPage_EscapesAttackerControlledValues(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.serveBlockedPage(w, req, "example.com", "cat", "reason", `<script>alert(1)</script>`)

	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("the client IP was rendered unescaped on the WAF blocked page")
	}
}

func TestServeUnauthorizedIPPage_EscapesTheIP(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.serveUnauthorizedIPPage(w, req, "example.com", `<img src=x onerror=alert(1)>`)

	if strings.Contains(w.Body.String(), "<img src=x onerror=alert(1)>") {
		t.Error("the client IP was rendered unescaped on the unauthorized-IP page")
	}
}

// The passcode page is the one that renders on a *wrong* passcode, so everything it echoes is
// reachable without knowing the secret.
func TestServePasscodePage_NeutralisesTheRedirectURI(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.servePasscodePage(w, req, "example.com", `/x" onmouseover="alert(1)`, "Incorrect passcode.")

	body := w.Body.String()
	if strings.Contains(body, `" onmouseover="`) {
		t.Error("the redirect_uri broke out of the hidden input's value attribute")
	}
	if !strings.Contains(body, "&#34;") {
		t.Error("expected the quotes in the redirect_uri to be escaped")
	}
}

// Kept separate from the escaping case above because the two defences answer different attacks.
// A value like `/x" onmouseover=...` stays a same-host path -- it is not an open redirect, and
// escaping is what makes it harmless. An absolute URL is the open redirect, and escaping would
// do nothing about it; only reducing it to a relative path does.
func TestServePasscodePage_RejectsAnOffsiteRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.servePasscodePage(w, req, "example.com", "https://example.invalid/", "")

	body := w.Body.String()
	if strings.Contains(body, "example.invalid") {
		t.Error("an off-site redirect target was carried into the form")
	}
	if !strings.Contains(body, `name="redirect_uri" value="/"`) {
		t.Error("expected the off-site redirect_uri to be replaced with the site root")
	}
}

func TestServePasscodePage_KeepsAnOrdinaryRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.servePasscodePage(w, req, "example.com", "/reports?tab=2", "")

	if !strings.Contains(w.Body.String(), `value="/reports?tab=2"`) {
		t.Error("a legitimate relative redirect was not preserved")
	}
}

func TestServeOfflinePage_EscapesTheHost(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.serveOfflinePage(w, req, `<script>alert(1)</script>`, 404)

	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Error("the requested host was rendered unescaped on the offline page")
	}
}
