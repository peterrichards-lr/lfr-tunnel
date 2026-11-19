package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2522: a user whose client is below min_client_version is told in the portal (V2's banner,
// from /api/me) and once by email when their warning window opens -- not only by the CLI.

// setupMinVersionNoticeServer is a gateway with a floor of v1.45.0 and a mail sender to watch.
func setupMinVersionNoticeServer(t *testing.T) (*Server, *mockMailSender, func()) {
	t.Helper()
	srv, mail, cleanup := setupTestServer(t)
	srv.cfg.MinClientVersion = "v1.45.0"
	return srv, mail, cleanup
}

// userOnClient is an approved user whose client last reported version (empty: never seen).
func userOnClient(t *testing.T, srv *Server, email, version string) *db.User {
	t.Helper()
	u := &db.User{ID: email, Email: email, FirstName: "Pat", Role: "user", Status: db.UserStatusApproved, LastClientVersion: version}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	if version != "" {
		if err := srv.db.UpdateUser(u); err != nil {
			t.Fatalf("recording %s's client version: %v", email, err)
		}
	}
	return u
}

func getMe(t *testing.T, srv *Server, email string) map[string]interface{} {
	t.Helper()
	session := generateToken(16)
	srv.portalMap.Store("admin_session_"+session, PortalSessionData{Email: email, ExpiresAt: time.Now().Add(time.Hour)})
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	rec := httptest.NewRecorder()
	srv.handleGetMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("/api/me: %v", err)
	}
	return body
}

func TestMeReportsAClientBelowTheFloor(t *testing.T) {
	srv, _, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	user := userOnClient(t, srv, "old@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 11)

	mv, ok := getMe(t, srv, user.Email)["min_version"].(map[string]interface{})
	if !ok {
		t.Fatal("/api/me carries no min_version for a client below the floor")
	}
	for k, want := range map[string]string{
		"client_version": "v1.40.0", "min_version": "v1.45.0", "phase": GracePhaseWarning, "upgrade_command": UpgradeCommand,
	} {
		if mv[k] != want {
			t.Errorf("min_version.%s = %v, want %q", k, mv[k], want)
		}
	}
	if mv["deadline"] == nil || mv["deadline"] == "" {
		t.Error("min_version carries no deadline inside the warning window")
	}
}

// Looking at the portal must not start anybody's window: only the client registering does.
func TestReadingMeDoesNotStartTheWindow(t *testing.T) {
	srv, _, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	user := userOnClient(t, srv, "unseen@example.com", "v1.40.0")

	mv, ok := getMe(t, srv, user.Email)["min_version"].(map[string]interface{})
	if !ok || mv["phase"] != GracePhaseGrace || mv["deadline"] != nil {
		t.Fatalf("before the window starts, want phase grace with no deadline, got %v", mv)
	}
	if seen, err := srv.db.GetFirstSeen(user.ID, MinVersionDocumentID, srv.cfg.MinClientVersion); err != nil || !seen.IsZero() {
		t.Fatalf("reading /api/me recorded a first sight (%v, %v): it would start the grace window", seen, err)
	}
	// Control: the recording path the client's registration uses does start it.
	srv.minVersionState(user, user.LastClientVersion, true)
	if seen, err := srv.db.GetFirstSeen(user.ID, MinVersionDocumentID, srv.cfg.MinClientVersion); err != nil || seen.IsZero() {
		t.Fatalf("control: recording a first sight did not store one (%v), so the check above proves nothing", err)
	}
}

func TestMeSaysNothingForACurrentDevOrUnknownClient(t *testing.T) {
	srv, _, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	for _, version := range []string{"v1.45.0", "v1.52.9", "dev", ""} {
		user := userOnClient(t, srv, "c"+strings.ReplaceAll(version, ".", "")+"@example.com", version)
		if mv, present := getMe(t, srv, user.Email)["min_version"]; present {
			t.Errorf("client %q: min_version = %v, want it absent", version, mv)
		}
	}
}

func warningsTo(mail *mockMailSender, email string) int {
	n := 0
	for _, e := range mail.getSentEmails() {
		if e.To == email && strings.Contains(e.Subject, "upgrade your Liferay Tunnel client") {
			n++
		}
	}
	return n
}

func TestTheVersionWarningIsEmailedOncePerFloor(t *testing.T) {
	srv, mail, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	user := userOnClient(t, srv, "warned@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 11)

	srv.warnUsersEnteringMinVersionWindow()
	if got := warningsTo(mail, user.Email); got != 1 {
		t.Fatalf("emails after the first sweep = %d, want 1", got)
	}
	e := mail.getSentEmails()[0]
	for _, want := range []string{"v1.40.0", "v1.45.0", UpgradeCommand, PortalV2Base} {
		if !strings.Contains(e.HtmlBody+e.TextBody, want) {
			t.Errorf("the email does not name %q", want)
		}
	}

	srv.warnUsersEnteringMinVersionWindow()
	if got := warningsTo(mail, user.Email); got != 1 {
		t.Errorf("emails after the second sweep = %d, want still 1", got)
	}

	// A raised floor is a new obligation, and a new window: one more email, once.
	srv.cfg.MinClientVersion = "v1.50.0"
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 11)
	srv.warnUsersEnteringMinVersionWindow()
	srv.warnUsersEnteringMinVersionWindow()
	if got := warningsTo(mail, user.Email); got != 2 {
		t.Errorf("emails after raising the floor = %d, want 2", got)
	}
}

func TestTheVersionWarningIsNotEmailedTooEarlyOrToTheWrongPeople(t *testing.T) {
	srv, mail, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()

	inGrace := userOnClient(t, srv, "grace@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, inGrace.ID, 2)
	// Seen below the floor, and has upgraded since.
	upgraded := userOnClient(t, srv, "upgraded@example.com", "v1.45.0")
	minVersionFirstSeenDaysAgo(t, srv, upgraded.ID, 11)
	dev := userOnClient(t, srv, "dev@example.com", "dev")
	minVersionFirstSeenDaysAgo(t, srv, dev.ID, 11)
	unknown := userOnClient(t, srv, "unknown@example.com", "")
	minVersionFirstSeenDaysAgo(t, srv, unknown.ID, 11)
	// Control, in the same sweep: due a warning, so the sweep is shown to send.
	due := userOnClient(t, srv, "due@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, due.ID, 11)

	srv.warnUsersEnteringMinVersionWindow()
	if warningsTo(mail, due.Email) != 1 {
		t.Fatal("control: the user due a warning was not emailed, so the absences below prove nothing")
	}
	for _, u := range []*db.User{inGrace, upgraded, dev, unknown} {
		if got := warningsTo(mail, u.Email); got != 0 {
			t.Errorf("%s was emailed %d time(s), want none", u.Email, got)
		}
	}
}

// Expired with no warning ever sent -- a gateway down when the window opened -- still tells them.
func TestAnExpiredClientNeverWarnedIsToldWhatHappened(t *testing.T) {
	srv, mail, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	user := userOnClient(t, srv, "late@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 20)

	srv.warnUsersEnteringMinVersionWindow()
	emails := mail.getSentEmails()
	if len(emails) != 1 {
		t.Fatalf("want one email, got %d", len(emails))
	}
	if body := emails[0].HtmlBody + emails[0].TextBody; !strings.Contains(body, "new tunnels are refused") && !strings.Contains(body, "start new tunnels again") {
		t.Errorf("an expired client was not told new tunnels are refused:\n%s", body)
	}
}

func TestAFailedVersionWarningIsRetried(t *testing.T) {
	srv, mail, cleanup := setupMinVersionNoticeServer(t)
	defer cleanup()
	user := userOnClient(t, srv, "retry@example.com", "v1.40.0")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 11)

	mail.failSends(errors.New("relay down"))
	srv.warnUsersEnteringMinVersionWindow()
	if mail.sendAttempts() == 0 {
		t.Fatal("fixture: the sweep never tried to send")
	}
	mail.failSends(nil)
	srv.warnUsersEnteringMinVersionWindow()
	if got := warningsTo(mail, user.Email); got != 1 {
		t.Errorf("after a failed send and a recovered relay, emails = %d, want 1 -- the claim was not released", got)
	}
}
