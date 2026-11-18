package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// ActionSSODeniedRejected is written when SSO sign-in is refused because an administrator had
// already rejected this registration.
//
// Its own action rather than a variant of the rejection row: this is somebody attempting to use
// the account after the decision, which is the row an owner wants to see if a rejection turns out
// to have been contentious.
const ActionSSODeniedRejected = "user.sso_denied.rejected"

// ActionSSODeniedUnverified is written when SSO sign-in is refused because the identity provider
// has not verified the email address it asserted (#2451).
const ActionSSODeniedUnverified = "user.sso_denied.unverified_email"

// errSSORegistrationRejected distinguishes "this gateway has decided against this person" from
// "the approval write failed", which the callback answers with 403 and 500 respectively. A bare
// bool made both of them read as a refusal to the person signing in.
var errSSORegistrationRejected = errors.New("registration was rejected by an administrator")

// ssoPortalCookie remembers which portal started an SSO sign-in, so the callback can return the
// person to it (#2447). The callback used to redirect to "/" unconditionally, so a sign-in started
// from Portal V2 landed in V1 -- one arm of a live A/B test handing its users to the other.
//
// It holds a fixed value, never a URL: a return URL carried through the identity provider is an
// open redirect waiting for someone to put an address in it. ssoReturnPath maps the value to a
// path, and anything it does not recognise returns to V1, the old behaviour.
const ssoPortalCookie = "oidc_portal"

// ssoPortalV2 is the only value ssoPortalCookie can usefully hold.
const ssoPortalV2 = "v2"

// ssoCLICookie remembers that `lfr-tunnel login` started this sign-in (#2459), so the portal the
// callback returns to can ask the user to create a token for the waiting CLI. Like
// ssoPortalCookie it holds a fixed value, never anything that becomes a URL by itself.
const ssoCLICookie = "oidc_cli"

// ssoReturnPath is where the callback sends someone who has just signed in.
func ssoReturnPath(r *http.Request) string {
	return ssoReturnFromCookies(r).path()
}

// ssoReturn is where a finished sign-in lands. It is carried in the flow cookies, and in a pending
// sign-in when the gateway confirms the address itself (#2472) -- the link may be opened in a
// browser that never saw those cookies.
type ssoReturn struct {
	v2  bool
	cli bool
}

func ssoReturnFromCookies(r *http.Request) ssoReturn {
	var rt ssoReturn
	if c, err := r.Cookie(ssoPortalCookie); err == nil && c.Value == ssoPortalV2 {
		rt.v2 = true
	}
	if c, err := r.Cookie(ssoCLICookie); err == nil && c.Value == "1" {
		rt.cli = true
	}
	return rt
}

func (rt ssoReturn) path() string {
	path := "/"
	if rt.v2 {
		path = "/portalv2/dashboard"
	}
	if rt.cli {
		path += "?cli_login=1"
	}
	return path
}

// The PKCE verifier and the nonce live beside oidc_state, in the browser that started the sign-in
// (#2451). PKCE means an intercepted authorization code cannot be redeemed without the verifier;
// the nonce means an ID token minted for some other sign-in is not accepted for this one.
const (
	ssoVerifierCookie = "oidc_verifier"
	ssoNonceCookie    = "oidc_nonce"
)

// ssoSignInFailed is all a person is told when the exchange with the identity provider fails.
// The underlying error names provider endpoints and library internals, so it goes to the log
// only (#2451).
const ssoSignInFailed = "Sign-in with your identity provider failed. Please try again, or contact the gateway administrator if it keeps happening."

// setSSOFlowCookie writes one of the short-lived cookies that carry a sign-in from
// handleSSOLogin to handleSSOCallback.
func setSSOFlowCookie(w http.ResponseWriter, name, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// cookieMatches reports whether the request carries cookie name with exactly value, comparing in
// constant time. An empty value never matches.
func cookieMatches(r *http.Request, name, value string) bool {
	c, err := r.Cookie(name)
	return err == nil && value != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(value)) == 1
}

// generateRandomState generates a secure random state for OAuth2 CSRF protection
func generateRandomState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) //nolint:errcheck
	return base64.RawURLEncoding.EncodeToString(b)
}

func getOIDCConfig(cfg *config.ServerConfig, providerID string, r *http.Request) (*config.SSOProviderConfig, *oauth2.Config, *oidc.Provider, error) {
	var p *config.SSOProviderConfig
	for _, prov := range cfg.SSOProviders {
		if prov.ID == providerID {
			p = &prov
			break
		}
	}
	if p == nil {
		return nil, nil, nil, http.ErrNotSupported
	}

	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, p.IssuerURL)
	if err != nil {
		return nil, nil, nil, err
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	redirectURL := scheme + "://" + r.Host + "/api/auth/callback?provider=" + p.ID

	oauth2Config := &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return p, oauth2Config, provider, nil
}

func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	if providerID == "" {
		http.Error(w, `{"error":"provider required"}`, http.StatusBadRequest)
		return
	}

	// One host for the whole flow (#2470). The redirect URI is built from the request's host and
	// the provider registers exactly one, while the portal answers on tunnel., portal. and the
	// apex of every configured domain -- and `lfr-tunnel login` opens portal. The flow cookies and
	// the session are host-only, so the sign-in moves to the canonical host before anything is
	// started, not halfway through.
	if canonical := s.ssoCanonicalBase(r); canonical != nil && !sameHost(canonical.Host, r.Host) {
		http.Redirect(w, r, canonical.Scheme+"://"+canonical.Host+r.URL.RequestURI(), http.StatusFound)
		return
	}

	_, oauth2Config, _, err := getOIDCConfig(s.cfg, providerID, r)
	if err != nil {
		slog.Info(fmt.Sprintf("[SSO] Config error: %v", err))
		http.Error(w, `{"error":"Invalid provider configuration"}`, http.StatusInternalServerError)
		return
	}

	state := generateRandomState()
	verifier := oauth2.GenerateVerifier()
	nonce := generateRandomState()
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	setSSOFlowCookie(w, "oidc_state", state, secure)
	setSSOFlowCookie(w, ssoVerifierCookie, verifier, secure)
	setSSOFlowCookie(w, ssoNonceCookie, nonce, secure)
	// Always written, so a V1 sign-in clears a V2 one abandoned in the same browser rather than
	// inheriting its destination.
	portal := ""
	if r.URL.Query().Get("portal") == ssoPortalV2 {
		portal = ssoPortalV2
	}
	setSSOFlowCookie(w, ssoPortalCookie, portal, secure)
	// Always written too, so an abandoned CLI sign-in does not mark a later ordinary one.
	cli := ""
	if r.URL.Query().Get("cli_login") == "1" {
		cli = "1"
	}
	setSSOFlowCookie(w, ssoCLICookie, cli, secure)

	url := oauth2Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	if providerID == "" {
		http.Error(w, "Provider required", http.StatusBadRequest)
		return
	}

	state, err := r.Cookie("oidc_state")
	if err != nil || r.URL.Query().Get("state") != state.Value {
		http.Error(w, "State invalid", http.StatusBadRequest)
		return
	}

	p, oauth2Config, provider, err := getOIDCConfig(s.cfg, providerID, r)
	if err != nil {
		http.Error(w, "Invalid provider configuration", http.StatusInternalServerError)
		return
	}

	verifier, err := r.Cookie(ssoVerifierCookie)
	if err != nil || verifier.Value == "" {
		http.Error(w, "Sign-in session expired. Please start again.", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier.Value))
	if err != nil {
		slog.Warn(fmt.Sprintf("[SSO] %s: token exchange failed: %v", providerID, err))
		http.Error(w, ssoSignInFailed, http.StatusBadGateway)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		slog.Warn(fmt.Sprintf("[SSO] %s: token response carried no id_token", providerID))
		http.Error(w, ssoSignInFailed, http.StatusBadGateway)
		return
	}

	oidcConfig := &oidc.Config{
		ClientID:        oauth2Config.ClientID,
		SkipIssuerCheck: p.SkipIssuerCheck,
	}
	idToken, err := provider.Verifier(oidcConfig).Verify(ctx, rawIDToken)
	if err != nil {
		slog.Warn(fmt.Sprintf("[SSO] %s: ID token rejected: %v", providerID, err))
		http.Error(w, ssoSignInFailed, http.StatusUnauthorized)
		return
	}
	// The token must be the one minted for the sign-in this browser started.
	if !cookieMatches(r, ssoNonceCookie, idToken.Nonce) {
		slog.Warn(fmt.Sprintf("[SSO] %s: ID token nonce does not match this sign-in", providerID))
		http.Error(w, ssoSignInFailed, http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		// AMR is how the provider authenticated the user (RFC 8176), e.g. ["pwd","mfa"]. Logged,
		// not enforced: it is the evidence for a provider configured with satisfies_mfa (#2482).
		AMR        []string `json:"amr"`
		Name       string   `json:"name"`
		GivenName  string   `json:"given_name"`
		FamilyName string   `json:"family_name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		slog.Warn(fmt.Sprintf("[SSO] %s: unreadable ID token claims: %v", providerID, err))
		http.Error(w, ssoSignInFailed, http.StatusBadGateway)
		return
	}

	email := config.NormalizeEmail(claims.Email)
	if email == "" {
		http.Error(w, "Email required from provider", http.StatusBadRequest)
		return
	}

	// The email is what an account is found by, so an address the provider has not verified
	// could sign in as whoever owns it here (#2451). email_verification: gateway checks it itself
	// below, once the domain is known to be allowed (#2472).
	mode := p.EmailVerificationMode()
	if !claims.EmailVerified && mode == config.EmailVerificationProvider {
		s.writeAudit(email, ActionSSODeniedUnverified, "user", email,
			fmt.Sprintf("Sign-in through %s refused: the identity provider has not verified this email address", providerID), r)
		http.Error(w, "Your identity provider has not verified your email address. Verify it there, then sign in again.", http.StatusForbidden)
		return
	}

	// ENFORCE EMAIL DOMAIN WHITELIST
	allowed := false
	for _, d := range s.cfg.AllowedEmailDomains {
		if strings.HasSuffix(email, "@"+d) {
			allowed = true
			break
		}
	}
	if !allowed && len(s.cfg.AllowedEmailDomains) > 0 {
		s.respondDomainRefused(w, r)
		return
	}

	id := ssoIdentity{Email: email, GivenName: claims.GivenName, FamilyName: claims.FamilyName}
	if !claims.EmailVerified && mode == config.EmailVerificationGateway {
		user, err := s.db.GetUserByEmail(email)
		if err != nil && err != db.ErrNotFound {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		if !s.mailboxProven(user) {
			s.startSSOEmailConfirmation(w, r, p, id, ssoReturnFromCookies(r), user == nil)
			return
		}
	}

	// Logged once the address has passed the verified and domain checks, so it records sign-ins,
	// not refusals (#2482). amr is the evidence that the IdP really performed MFA.
	slog.Info(fmt.Sprintf("[SSO] %s: accepted sign-in for %s, amr=%v, satisfies_mfa=%v", providerID, email, claims.AMR, p.SatisfiesMFA))

	s.finishSSOSignIn(w, r, p, id, ssoReturnFromCookies(r), false)
}

// ssoIdentity is what a sign-in carries from the ID token to the account.
type ssoIdentity struct {
	Email      string
	GivenName  string
	FamilyName string
}

// finishSSOSignIn creates or loads the account for an accepted SSO identity and issues the
// session. It runs from the callback, or from the confirmation link when the gateway confirmed
// the address itself (#2472); confirmed records that, so later sign-ins do not ask again.
func (s *Server) finishSSOSignIn(w http.ResponseWriter, r *http.Request, p *config.SSOProviderConfig, id ssoIdentity, rt ssoReturn, confirmed bool) {
	providerID := p.ID
	email := id.Email

	// Create or load the user
	user, err := s.db.GetUserByEmail(email)
	if err != nil && err != db.ErrNotFound {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	// The sign-in before this one, read now: approveOnSSOSignIn overwrites it below (#2490).
	var previousLoginAt *time.Time
	if user != nil && user.LastLoginAt != nil {
		prev := *user.LastLoginAt
		previousLoginAt = &prev
	}
	if err == db.ErrNotFound {
		// #910: umbrella flag gating NEW account creation regardless of method. Without
		// this, SSO first-login always auto-provisioned a new account with only the
		// AllowedEmailDomains whitelist as a gate -- no way to close off new signups
		// independent of disabling SSO/email login entirely. The owner is exempt: they're
		// a pre-configured account, not a new registration, and locking them out would
		// leave nobody able to administer the server.
		if s.cfg.DisableNewRegistrations && !strings.EqualFold(email, s.cfg.Owner.UserID) {
			http.Error(w, "New registrations are currently closed.", http.StatusForbidden)
			return
		}
		user = &db.User{
			ID:        email,
			Email:     email,
			FirstName: id.GivenName,
			LastName:  id.FamilyName,
			// As /setup does for email signup: the preferred name starts as the first name, and
			// the user can change it in Account Settings (#2481).
			PreferredName: id.GivenName,
			Role:          "user",
			Status:        db.UserStatusApproved,
			AuthMethod:    "sso - " + providerID,
			CreatedAt:     time.Now().UTC(), // UTC like every other path: "oldest account wins" compares these as text (#2466)
			UpdatedAt:     time.Now().UTC(), // UTC like every other path: "oldest account wins" compares these as text (#2466)
		}
		// If they match the owner config, grant owner
		if strings.EqualFold(user.Email, s.cfg.Owner.UserID) {
			user.Role = "owner"
		}
		if err := s.db.CreateUser(user); err != nil {
			slog.Info(fmt.Sprintf("[SSO] Failed to create user %s: %v", email, err))
			http.Error(w, "Failed to create user", http.StatusInternalServerError)
			return
		}
		s.alertSSOAccountCreated(user, p)
	} else if user.Status != db.UserStatusApproved {
		// A rejected registration is the one status SSO must not promote, so the outcome is
		// checked rather than assumed: approveOnSSOSignIn refuses it, and refusing to approve
		// while still issuing a session would hand a rejected person a portal session anyway
		// (#1830).
		if err := s.approveOnSSOSignIn(user, providerID, r); err != nil {
			if errors.Is(err, errSSORegistrationRejected) {
				http.Error(w, "Your access request has been declined. Please contact the gateway "+
					"administrator if you think this is a mistake.", http.StatusForbidden)
				return
			}
			// The approval did not persist, so there is no approved account to issue a session
			// for. Saying so beats signing them in against a status the database does not hold.
			http.Error(w, "Failed to complete sign-in", http.StatusInternalServerError)
			return
		}
	}

	// Record the sign-in (#2490). Only approveOnSSOSignIn set last_login_at, and it runs only for an
	// account that was not yet approved -- so a new SSO account (created approved) and every
	// returning SSO user showed "Last Seen: Never". The session carries the previous sign-in for
	// the portal's "last login" notice, as the magic-link session does.
	s.recordSSOLogin(user, r)
	if confirmed {
		if err := s.db.RecordEmailConfirmed(user.ID, emailConfirmedBySSOLink); err != nil {
			// The person proved the mailbox; failing to remember it only means the next sign-in
			// asks again.
			slog.Error(fmt.Sprintf("[SSO] %s: could not record the confirmed address for %s: %v", providerID, email, err))
		}
	}

	// Issue the admin session cookie
	sessionID, err := generateSecureToken()
	if err != nil {
		http.Error(w, "Failed to generate session ID", http.StatusInternalServerError)
		return
	}
	killedPreviousSession := s.sessionStore().killPortalSessionsFor(user.Email)

	s.sessionStore().storePortalSession(sessionID, PortalSessionData{
		Email:                 user.Email,
		AuthProvider:          providerID, // force_mfa is met per session by a satisfies_mfa provider (#2482)
		PreviousLoginAt:       previousLoginAt,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              s.clientIP(r),
		KilledPreviousSession: killedPreviousSession,
		// Recorded so a slide can re-issue the cookie with the mode it was created with
		// rather than a default (#1655).
		SameSite:  sameSiteToStored(http.SameSiteLaxMode),
		CreatedAt: time.Now().UTC(),
	})

	http.SetCookie(w, s.newSessionCookie(r, sessionID, http.SameSiteLaxMode,
		s.sessionDeadline(PortalSessionData{
			ExpiresAt: time.Now().Add(s.cfg.PortalSessionDuration),
			CreatedAt: time.Now().UTC(),
		})))

	// Inject an audit log. Details name the provider (#2474): with several configured, "signed in
	// through SSO" without saying which is half the answer.
	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "user.login.sso",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    fmt.Sprintf("Signed in via %s (provider %s)", p.Name, providerID),
		IPAddress:  s.clientIP(r),
	})

	// Back to the portal the sign-in started from (#2447).
	http.Redirect(w, r, rt.path(), http.StatusFound)
}

// recordSSOLogin stamps last_login_at and last_login_ip for an SSO sign-in, as the magic-link
// path does (#2490). A failed write is logged, not fatal: the user has authenticated, and a stale
// "last seen" is no reason to refuse them a session.
func (s *Server) recordSSOLogin(user *db.User, r *http.Request) {
	now := time.Now().UTC()
	user.LastLoginAt = &now
	user.LastLoginIP = s.clientIP(r)
	if err := s.db.UpdateUser(user); err != nil {
		slog.Warn(fmt.Sprintf("[SSO] Could not record the sign-in time of %s: %v", user.Email, err))
		return
	}
	s.invalidateUserCache(user.Email)
}

// approveOnSSOSignIn completes the approval of an existing user who has just authenticated
// through SSO.
//
// Auto-approving here is deliberate: once Liferay SSO is configured, the Liferay server is what
// controls who may authenticate, so reaching this point IS the authorisation. A second, manual
// approval would only duplicate a decision already made upstream.
//
// What was wrong (#1824) is that this approved PARTIALLY -- it set the status and stopped,
// leaving three loose ends:
//
//   - approval_token stayed live, so the admin's emailed approve link still worked on an
//     already-approved user. Following it later would mint a claim token and send a
//     "Registration Approved!" mail for something that happened days earlier.
//   - no audit row, so the single most security-relevant transition a user can undergo --
//     pending to approved -- left no trace naming what did it.
//   - no last_login_at, so these users read as "never logged in" (only server.go's magic-link
//     path and api_service_mfa.go set it, and neither is on this route). That is what made the
//     production data look as though nobody had ever signed in, when they had.
//
// Deliberately does NOT send a "you are approved" email: the user is completing a sign-in as this
// runs, so they are about to be looking at the dashboard. The email in the admin-approval path
// exists to reach somebody who is NOT present. Here, they are.
//
// It refuses exactly one status, and returning false is how it says so: a registration an admin
// explicitly REJECTED (#1830). Everything above is about a decision the identity provider has
// already made and this gateway has not; a rejection is a decision this gateway made about a
// person who can authenticate perfectly well upstream. Without this, rejection lasted until the
// rejected person clicked "Sign in with Liferay" -- status was not "approved", so this function
// made it "approved", and they had approved themselves. An admin who wants to reverse a rejection
// does it deliberately, from the admin portal.
//
// Extracted from handleSSOCallback so the behaviour can be asserted directly: reaching the
// callback requires a full OIDC token exchange, and a test that faked one would be testing the
// fake. This is the real function the callback calls.
func (s *Server) approveOnSSOSignIn(user *db.User, providerID string, r *http.Request) error {
	if user.Status == statusRejected {
		slog.Warn(fmt.Sprintf("[SSO] Refused to auto-approve %s on SSO sign-in: the registration "+
			"was rejected by an administrator", user.Email))
		s.writeAudit(user.Email, ActionSSODeniedRejected, "user", user.Email,
			fmt.Sprintf("Sign-in through %s refused: this registration was rejected by an "+
				"administrator, and SSO auto-approval does not override that", providerID), r)
		return errSSORegistrationRejected
	}

	previousStatus := user.Status
	user.Status = db.UserStatusApproved
	user.ApprovalToken = "" // consumed by this approval; a stale link must not re-approve
	now := time.Now().UTC()
	user.LastLoginAt = &now

	if err := s.db.UpdateUser(user); err != nil {
		slog.Error(fmt.Sprintf("[SSO] Failed to approve %s on SSO sign-in: %v", user.Email, err))
		// The write failed, so this user is not approved. Reporting success would issue a portal
		// session on the strength of an approval that is not in the database.
		return fmt.Errorf("persisting the SSO approval of %s: %w", user.Email, err)
	}
	s.invalidateUserCache(user.Email)
	s.writeAudit(user.Email, "user.approved.sso", "user", user.Email,
		fmt.Sprintf("Auto-approved on SSO sign-in via %s (was %q); the identity provider is the access decision",
			providerID, previousStatus), r)
	return nil
}

// ssoCanonicalBase is the one host every SSO sign-in runs on: control_plane_url when set (an
// edge), else tunnel. of the FIRST configured domain -- not of whichever domain the request came
// in on, since the provider registers one redirect URI and a gateway may answer on several
// domains (review of #2473). The scheme is the request's, as nginx reports it. Nil when there is
// nothing to canonicalise to.
func (s *Server) ssoCanonicalBase(r *http.Request) *url.URL {
	if cp := strings.TrimSpace(s.cfg.ControlPlaneURL); cp != "" {
		if u, err := url.Parse(cp); err == nil && u.Host != "" {
			return u
		}
		return nil
	}
	if len(s.cfg.Domains) == 0 || strings.TrimSpace(s.cfg.Domains[0]) == "" {
		return nil
	}
	// Only the gateway's own names are moved: a host outside every configured domain --
	// localhost, an IP, as in the Keycloak e2e stack -- signs in where it is, as it always did.
	// Sending it to a tunnel. host that does not exist there broke that suite.
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	own := false
	for _, d := range s.cfg.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			own = true
			break
		}
	}
	if !own {
		return nil
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return &url.URL{Scheme: scheme, Host: "tunnel." + strings.ToLower(strings.TrimSpace(s.cfg.Domains[0]))}
}

// sameHost compares two Host values ignoring case and a default port, so a configured
// "https://tunnel.example.com:443" cannot redirect a browser that sends "tunnel.example.com"
// to itself forever.
func sameHost(a, b string) bool {
	strip := func(h string) string {
		h = strings.ToLower(h)
		h = strings.TrimSuffix(h, ":443")
		return strings.TrimSuffix(h, ":80")
	}
	return strip(a) == strip(b)
}

// alertSSOAccountCreated tells admins an SSO sign-in created an account (#2481). Email signup
// alerts them because they must approve it; SSO accounts are approved automatically, so this is
// notice rather than a request -- and only on creation, never on later sign-ins, which would be
// noise. It shares the registration alert's switch: both mean "someone new joined".
func (s *Server) alertSSOAccountCreated(user *db.User, p *config.SSOProviderConfig) {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = "(no name from the provider)"
	}
	body := fmt.Sprintf("<p>A new account was created by a first sign-in through <strong>%s</strong> (provider <code>%s</code>).</p>"+
		"<p><strong>%s</strong> &lt;%s&gt;</p>"+
		"<p>SSO accounts are approved automatically; the identity provider is the access decision. No action is needed.</p>",
		html.EscapeString(p.Name), html.EscapeString(p.ID), html.EscapeString(name), html.EscapeString(user.Email))
	s.sendAdminAlert("alert_notify_registration", "LFR Tunnel Alert: New User Registration (SSO)", body)
}

// sessionSatisfiesMFA reports whether this request's portal session was created by an SSO
// provider configured with satisfies_mfa, which then meets force_mfa for it (#2482). Decided by
// the session, not the account: the same user signing in by magic link is still held to the
// gateway's own TOTP. A provider since removed from the config, or one without the setting,
// satisfies nothing.
func (s *Server) sessionSatisfiesMFA(r *http.Request) bool {
	cookie, err := r.Cookie("lfr_session")
	if err != nil {
		return false
	}
	data, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok || data.AuthProvider == "" {
		return false
	}
	for _, p := range s.cfg.SSOProviders {
		if p.ID == data.AuthProvider {
			return p.SatisfiesMFA
		}
	}
	return false
}
