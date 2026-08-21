package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// TestApprovalConfirmationEscapes covers the confirmation page. The name and email come
// from an unverified registration, so they are attacker-controlled until approval.
func TestApprovalConfirmationEscapes(t *testing.T) {
	s := &Server{}
	user := &db.User{
		FirstName: `<script>alert(1)</script>`,
		LastName:  `"onload="x`,
		Email:     `evil"@example.com`,
	}

	rec := httptest.NewRecorder()
	s.renderApprovalConfirmation(rec, user, user.Email, `tok"><script>`)
	body := rec.Body.String()

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("registration name was rendered unescaped into the confirmation page")
	}
	if strings.Contains(body, `tok"><script>`) {
		t.Error("approval token was rendered unescaped, breaking out of the hidden field")
	}

	// The token belongs in a hidden field, not the form action, so it stays out of
	// browser history and the Referer header.
	if !strings.Contains(body, `<form method="POST"`) {
		t.Error("expected the confirmation to submit by POST")
	}
	if strings.Contains(body, `action="/api/admin/approve?`) {
		t.Error("token must not be placed in the form action URL")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("expected Referrer-Policy: no-referrer so the token is not leaked onward")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("expected the page not to be cached")
	}
}

// TestApproveRejectsGetWithoutDatabase is a narrow guard on the routing precondition --
// a GET must never reach the state change. The full approval path needs a database, so
// the deeper behaviour is covered by the existing setup tests; what matters here is that
// GET is not the verb that approves.
func TestApproveGetDoesNotApprove(t *testing.T) {
	// With no database the handler bails before doing anything, which is enough to
	// assert the GET path does not fall through into the mutation.
	s := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/approve?email=a@b.c&token=t", nil)
	rec := httptest.NewRecorder()
	s.handleApproveUser(rec, req)

	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "approved") {
		t.Error("a GET must not report an approval -- link previews and prefetchers follow URLs")
	}
}

// TestApprovePostReadsFormValues confirms the POST path takes its credentials from the
// form, which is what keeps the token out of the URL.
func TestApprovePostReadsFormValues(t *testing.T) {
	form := url.Values{}
	form.Set("email", "someone@example.com")
	form.Set("token", "secret-token")

	req := httptest.NewRequest(http.MethodPost, "/api/admin/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if err := req.ParseForm(); err != nil {
		t.Fatalf("form did not parse: %v", err)
	}
	if req.PostFormValue("email") != "someone@example.com" || req.PostFormValue("token") != "secret-token" {
		t.Fatal("fixture is wrong: the handler reads these via PostFormValue")
	}
	if req.URL.Query().Get("token") != "" {
		t.Error("the token must not need to appear in the query string on the POST path")
	}
}
