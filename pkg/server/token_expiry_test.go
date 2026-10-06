package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// #2344: a Personal Access Token used to expire with no notice, before or after. Every case here
// goes red against the code before it, which had no sweep, no field and no decision email.

// templateMarker appears in every rendered email template and in none of the inline fallbacks,
// so a body containing it was rendered from the template -- the owner's requirement that these
// emails "use a template like the others", asserted rather than assumed.
const templateMarker = `style="margin: 24px 0;"`

func daysFromNow(days float64) *time.Time {
	t := time.Now().Add(time.Duration(days * float64(24*time.Hour)))
	return &t
}

func expiryUser(t *testing.T, srv *Server, email string) *db.User {
	t.Helper()
	u := &db.User{ID: email, Email: email, FirstName: "Dev", Role: "user", Status: "approved", LanguagePreference: "en"}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	return u
}

func emailsTo(mail *mockMailSender, to string) []mockEmail {
	var out []mockEmail
	for _, e := range mail.getSentEmails() {
		if e.To == to {
			out = append(out, e)
		}
	}
	return out
}

func TestTokenExpirySweepWarnsOnceInsideTheWindow(t *testing.T) {
	srv, mail, cleanup := setupTestServer(t)
	defer cleanup()
	dev := expiryUser(t, srv, "dev@example.com")

	due := tokenFor(t, srv, dev, "due", daysFromNow(3))
	tokenFor(t, srv, dev, "later", daysFromNow(20))
	tokenFor(t, srv, dev, "lapsed", daysFromNow(-1))
	tokenFor(t, srv, dev, "permanent", nil)
	revoked := tokenFor(t, srv, dev, "revoked", daysFromNow(2))
	if err := srv.db.RevokePAT(revoked.ID); err != nil {
		t.Fatal(err)
	}

	srv.checkExpiringTokens()

	sent := emailsTo(mail, dev.Email)
	if len(sent) != 1 {
		t.Fatalf("expected exactly one warning, for the live token inside the window; got %d: %+v", len(sent), sent)
	}
	if !strings.Contains(sent[0].Subject, "due") {
		t.Errorf("the warning names the wrong token: %q", sent[0].Subject)
	}
	if !strings.Contains(sent[0].TextBody, templateMarker) {
		t.Errorf("the warning was not rendered from token_expiring.html:\n%s", sent[0].TextBody)
	}
	if !strings.Contains(sent[0].TextBody, loginCommand) {
		t.Errorf("the warning does not say how to replace the token:\n%s", sent[0].TextBody)
	}

	// Once: the hourly sweep must not send one an hour.
	mail.reset()
	srv.checkExpiringTokens()
	if again := emailsTo(mail, dev.Email); len(again) != 0 {
		t.Errorf("the token was warned again on the next sweep: %+v", again)
	}

	// An admin extending the token re-arms the warning for the new date.
	if err := srv.db.UpdatePATExpiry(due.ID, daysFromNow(5)); err != nil {
		t.Fatal(err)
	}
	srv.checkExpiringTokens()
	if rearmed := emailsTo(mail, dev.Email); len(rearmed) != 1 {
		t.Errorf("a token whose expiry was changed must be warned before its NEW date; got %d emails", len(rearmed))
	}
}

// The window is the operator's (owner requirement), and unset means the default.
func TestTokenExpiryWindowIsConfigurable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int
		expiresIn  float64
		warned     bool
	}{
		{"default 7: 3 days out is inside", 0, 3, true},
		{"default 7: 10 days out is outside", 0, 10, false},
		{"configured 14: 10 days out is inside", 14, 10, true},
		{"configured 2: 3 days out is outside", 2, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, mail, cleanup := setupTestServer(t)
			defer cleanup()
			srv.cfg.TokenExpiryWarningDays = tc.configured
			dev := expiryUser(t, srv, "dev@example.com")
			tokenFor(t, srv, dev, "tok", daysFromNow(tc.expiresIn))

			srv.checkExpiringTokens()

			if got := len(emailsTo(mail, dev.Email)) == 1; got != tc.warned {
				t.Errorf("token_expiry_warning_days=%d, expiring in %.0f days: warned=%v, want %v",
					tc.configured, tc.expiresIn, got, tc.warned)
			}
		})
	}
}

// A warning that failed to send is not a warning: the stage must not advance, so the next sweep
// tries again (#1724's rule, applied here).
func TestTokenExpiryWarningRetriesAFailedSend(t *testing.T) {
	srv, mail, cleanup := setupTestServer(t)
	defer cleanup()
	dev := expiryUser(t, srv, "dev@example.com")
	tokenFor(t, srv, dev, "tok", daysFromNow(3))

	mail.failSends(errors.New("relay down"))
	srv.checkExpiringTokens()
	if mail.sendAttempts() == 0 {
		t.Fatal("the sweep never tried to send, so this test is not exercising a failed send")
	}

	mail.failSends(nil)
	srv.checkExpiringTokens()
	if sent := emailsTo(mail, dev.Email); len(sent) != 1 {
		t.Errorf("a warning whose send failed was recorded as delivered and never retried; got %d emails", len(sent))
	}
}

// registerWithToken registers directly against srv as the holder of a token.
func registerWithToken(t *testing.T, srv *Server, token, subdomain string) RegisterResponse {
	t.Helper()
	_, resp := registerTunnelAsVersion(t, srv, token, subdomain, "v9.9.9")
	if resp.Status != "success" {
		t.Fatalf("registration failed: %+v", resp)
	}
	return resp
}

func seedRealToken(t *testing.T, srv *Server, userID, raw string, expires *time.Time) {
	t.Helper()
	sum := sha256.Sum256([]byte(raw))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID: userID, TokenHash: hex.EncodeToString(sum[:]), TokenPrefix: raw[:6], Name: raw, ExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectRegistrationCarriesTheTokenExpiry(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	srv.cfg.AllowClientAutoReservation = true
	srv.cfg.TokenExpiryWarningDays = 9
	dev := expiryUser(t, srv, "dev@example.com")
	expires := daysFromNow(3)
	seedRealToken(t, srv, dev.ID, "lft_pat_expiring", expires)
	seedRealToken(t, srv, dev.ID, "lft_pat_forever", nil)

	resp := registerWithToken(t, srv, "lft_pat_expiring", "expiring-sub")
	if want := expires.UTC().Format(time.RFC3339); resp.TokenExpiresAt != want {
		t.Errorf("token_expires_at = %q, want %q", resp.TokenExpiresAt, want)
	}
	if resp.TokenExpiryWarningDays != 9 {
		t.Errorf("token_expiry_warning_days = %d, want the configured 9", resp.TokenExpiryWarningDays)
	}

	if forever := registerWithToken(t, srv, "lft_pat_forever", "forever-sub"); forever.TokenExpiresAt != "" {
		t.Errorf("a token that never expires reported an expiry: %q", forever.TokenExpiresAt)
	}
}

// Most clients register through an edge, which rebuilds the response from a narrow struct and has
// dropped central's fields before (#2130). Exercised end to end -- central, a real edge pointed at
// it, and the client-facing response -- rather than by reading the source, because the defect it
// guards is a field that compiles, is set on one side, and silently never arrives.
func TestEdgeRegistrationRelaysTheTokenExpiry(t *testing.T) {
	tmp := t.TempDir()
	edgeSecret := "usedge-expiry-secret"
	sum := sha256.Sum256([]byte(edgeSecret))

	controlCfg := config.DefaultServerConfig()
	controlCfg.DBPath = filepath.Join(tmp, "control.db")
	controlCfg.Domains = []string{"example.se"}
	controlCfg.DisableBackupScheduler = true
	controlCfg.AllowClientAutoReservation = true
	controlCfg.TokenExpiryWarningDays = 11
	controlCfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "usedge", TokenHash: hex.EncodeToString(sum[:])}}
	control, err := NewServer(controlCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { time.Sleep(50 * time.Millisecond); control.Stop() }()
	ts := httptest.NewServer(control)
	defer ts.Close()

	dev := expiryUser(t, control, "dev@example.com")
	expires := daysFromNow(3)
	seedRealToken(t, control, dev.ID, "lft_pat_edgeexp", expires)

	edgeCfg := config.DefaultServerConfig()
	edgeCfg.DBPath = ""
	edgeCfg.Domains = []string{"usedge.example.se"}
	edgeCfg.ControlPlaneURL = ts.URL
	edgeCfg.EdgeToken = edgeSecret
	edgeCfg.DisableBackupScheduler = true
	edge, err := NewServer(edgeCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { time.Sleep(50 * time.Millisecond); edge.Stop() }()

	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: "edge-expiry", AuthToken: "lft_pat_edgeexp",
		Ports: []PortMapping{{LocalPort: 8080}}, ClientVersion: "v9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://usedge.example.se/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	edge.handleRegister(rec, req)

	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Status != "success" {
		t.Fatalf("edge registration failed (%d): %s", rec.Code, rec.Body.String())
	}
	if want := expires.UTC().Format(time.RFC3339); resp.TokenExpiresAt != want {
		t.Errorf("the edge did not relay token_expires_at: got %q, want %q", resp.TokenExpiresAt, want)
	}
	if resp.TokenExpiryWarningDays != 11 {
		t.Errorf("the edge did not relay central's token_expiry_warning_days: got %d, want 11", resp.TokenExpiryWarningDays)
	}
}

func decideOverHTTP(t *testing.T, srv *Server, patID int64, grant bool) {
	t.Helper()
	body := fmt.Sprintf(`{"grant": %v}`, grant)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/tokens/%d/permanence", patID), strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleAdminDecideTokenPermanence(rec, req, "admin@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("deciding grant=%v: %d %s", grant, rec.Code, rec.Body.String())
	}
}

// waitForEmail polls for an asynchronous send. The decision email goes out on its own goroutine
// so an SMTP timeout cannot hold up the admin's request.
func waitForEmail(t *testing.T, mail *mockMailSender, to string) []mockEmail {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sent := emailsTo(mail, to); len(sent) > 0 {
			return sent
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestPermanenceDecisionIsEmailedForBothOutcomes(t *testing.T) {
	for _, grant := range []bool{true, false} {
		t.Run(fmt.Sprintf("grant=%v", grant), func(t *testing.T) {
			srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
			mail := &mockMailSender{}
			srv.notifications = NewNotificationService(mail, srv.db, srv.cfg)
			dev := expiryUser(t, srv, "dev@example.com")
			pat := tokenFor(t, srv, dev, "asked", in30Days())
			if err := srv.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); err != nil {
				t.Fatal(err)
			}

			decideOverHTTP(t, srv, pat.ID, grant)

			sent := waitForEmail(t, mail, dev.Email)
			if len(sent) != 1 {
				t.Fatalf("expected one decision email to the holder, got %d", len(sent))
			}
			if !strings.Contains(sent[0].TextBody, templateMarker) {
				t.Errorf("the decision was not rendered from token_permanence_decided.html:\n%s", sent[0].TextBody)
			}
			if !grant && !strings.Contains(sent[0].TextBody, "still expires") {
				t.Errorf("a denial must say the token still expires, which is the one thing the holder would otherwise assume wrongly:\n%s", sent[0].TextBody)
			}
			if grant && !strings.Contains(sent[0].TextBody, "no longer expires") {
				t.Errorf("a grant must say the token no longer expires:\n%s", sent[0].TextBody)
			}
		})
	}
}

// A suspended user's tokens are refused by validatePAT anyway, so "replace it before it expires"
// would be advice about a credential that does not work. Skipped -- and left unwarned, so the
// warning still reaches them if they are reinstated (#2344 review).
func TestTokenExpirySweepSkipsUnusableAccountsUntilReinstated(t *testing.T) {
	srv, mail, cleanup := setupTestServer(t)
	defer cleanup()
	dev := expiryUser(t, srv, "dev@example.com")
	tokenFor(t, srv, dev, "tok", daysFromNow(3))

	dev.Status = db.UserStatusRevoked
	if err := srv.db.UpdateUser(dev); err != nil {
		t.Fatal(err)
	}
	srv.checkExpiringTokens()
	if sent := emailsTo(mail, dev.Email); len(sent) != 0 {
		t.Fatalf("a suspended user was told to replace a token that is already refused: %+v", sent)
	}

	dev.Status = db.UserStatusApproved
	if err := srv.db.UpdateUser(dev); err != nil {
		t.Fatal(err)
	}
	srv.checkExpiringTokens()
	if sent := emailsTo(mail, dev.Email); len(sent) != 1 {
		t.Errorf("a reinstated user must still be warned; got %d emails", len(sent))
	}
}

// A DENY is allowed on a token that lapsed in the queue (#2280). Promising a reminder for a date
// already past would be false.
func TestDenyingALapsedRequestSaysTheTokenHasExpired(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	mail := &mockMailSender{}
	srv.notifications = NewNotificationService(mail, srv.db, srv.cfg)
	dev := expiryUser(t, srv, "dev@example.com")
	pat := tokenFor(t, srv, dev, "lapsed", daysFromNow(-2))
	if err := srv.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); err != nil {
		t.Fatal(err)
	}

	decideOverHTTP(t, srv, pat.ID, false)

	sent := waitForEmail(t, mail, dev.Email)
	if len(sent) != 1 {
		t.Fatalf("expected one decision email, got %d", len(sent))
	}
	if !strings.Contains(sent[0].TextBody, "has already expired") || strings.Contains(sent[0].TextBody, "reminded") {
		t.Errorf("a denial on a lapsed token must say it has expired, not promise a reminder:\n%s", sent[0].TextBody)
	}
}

// "Extend Permanent" answers a pending request as GRANTED (#2316). That is a decision too, and
// the holder is told through whichever admin door it came.
func TestExtendPermanentTellsTheHolderTheirRequestWasGranted(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	mail := &mockMailSender{}
	srv.notifications = NewNotificationService(mail, srv.db, srv.cfg)
	dev := expiryUser(t, srv, "dev@example.com")
	pat := tokenFor(t, srv, dev, "asked", in30Days())
	if err := srv.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/tokens/%d/extend", pat.ID), strings.NewReader(`{"days": 0}`))
	rec := httptest.NewRecorder()
	srv.handleAdminExtendToken(rec, req, "admin@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("extend permanent: %d %s", rec.Code, rec.Body.String())
	}

	sent := waitForEmail(t, mail, dev.Email)
	if len(sent) != 1 || !strings.Contains(sent[0].TextBody, "no longer expires") {
		t.Errorf("the holder was not told their request was granted via Extend Permanent: %+v", sent)
	}
}

// A decision that did not happen must not be announced. A second decision on an already-decided
// request is refused with 409; the holder hears about the first only.
func TestARefusedDecisionSendsNoEmail(t *testing.T) {
	srv := serverWithPolicy(t, config.NeverExpiresApproval, config.NeverExpiresDisabled, config.NeverExpiresDisabled)
	mail := &mockMailSender{}
	srv.notifications = NewNotificationService(mail, srv.db, srv.cfg)
	dev := expiryUser(t, srv, "dev@example.com")
	pat := tokenFor(t, srv, dev, "never-asked", in30Days())

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/tokens/%d/permanence", pat.ID), strings.NewReader(`{"grant": true}`))
	rec := httptest.NewRecorder()
	srv.handleAdminDecideTokenPermanence(rec, req, "admin@example.com")
	if rec.Code == http.StatusOK {
		t.Fatalf("deciding a request nobody made should be refused, got 200")
	}

	time.Sleep(200 * time.Millisecond) // the send is asynchronous; give a wrong one time to land
	if sent := emailsTo(mail, dev.Email); len(sent) != 0 {
		t.Errorf("a refused decision was emailed to the holder: %+v", sent)
	}
}
