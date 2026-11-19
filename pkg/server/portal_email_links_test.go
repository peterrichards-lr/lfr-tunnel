package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// System emails link to the portal, which is V2 at /portal (#2528). Every emitter builds the link
// as the portal base URL + PortalV2Base, and the templates render whatever they are given -- so
// both halves are checked: each template carries the link, and the link a real email carries is
// served V2 rather than V1 or a redirect.

func TestEveryEmailTemplateRendersThePortalLink(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	const link = "https://tunnel.example.com" + PortalV2Base

	checked := 0
	err := fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, readErr := templatesFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(raw), ".PortalLink") {
			return nil
		}
		parts := strings.Split(path, "/") // templates/<lang>/<name>
		body, renderErr := srv.renderEmailTemplate(parts[1], parts[2], map[string]interface{}{tmplPortalLink: link})
		if renderErr != nil {
			t.Errorf("%s: %v", path, renderErr)
			return nil
		}
		if !strings.Contains(body, link) {
			t.Errorf("%s does not carry the portal link %s", path, link)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fixture: the walk found the templates that link the portal (nine, all English today).
	if checked < 9 {
		t.Fatalf("only %d templates carry PortalLink; the walk is not reading the templates", checked)
	}
}

func TestAnEmailsPortalLinkOpensV2(t *testing.T) {
	srv, mail, cleanup := setupTestServer(t)
	defer cleanup()

	expires := time.Now().Add(48 * time.Hour)
	user := &db.User{ID: "u@example.com", Email: "u@example.com", FirstName: "U", Role: "user", Status: db.UserStatusApproved}
	if err := srv.sendTokenExpiringEmail(user, &db.PersonalAccessToken{Name: "ci", TokenPrefix: "lfr_pat_abcd", ExpiresAt: &expires}); err != nil {
		t.Fatalf("sending: %v", err)
	}
	emails := mail.getSentEmails()
	if len(emails) != 1 {
		t.Fatalf("want one email, got %d", len(emails))
	}
	link := srv.getPortalBaseURL(nil) + PortalV2Base
	if body := emails[0].HtmlBody + emails[0].TextBody; !strings.Contains(body, `href="`+link+`"`) {
		t.Fatalf("the email does not link %s:\n%.600s", link, body)
	}

	req := httptest.NewRequest(http.MethodGet, link, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if !isV2Document(rec) {
		t.Errorf("GET %s: status %d Location %q, want V2's app", link, rec.Code, rec.Header().Get("Location"))
	}
}
