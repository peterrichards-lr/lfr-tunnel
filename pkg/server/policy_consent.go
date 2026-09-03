package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// PolicyDocumentID is the identifier of the one acknowledgement consumer registered in
// v1 of the versioned-acknowledgement mechanism (#1707): the combined Privacy Policy and
// Cookie Disclosure.
//
// The storage layer is keyed on (document_id, version) rather than assuming a single
// document, so #1696's diagnostics consent -- and any later terms change -- can register
// itself here without another table. What is deliberately absent is a registry with
// content, scheduling or audience targeting: those are easy to add against this table
// later and very hard to remove once they exist.
const PolicyDocumentID = "privacy_policy"

// The endpoints the consent gate lets through, named rather than repeated as literals.
//
// This is not the ForceMFA gate's bypass list with a different name: the two gates block
// for different reasons and are cleared by different endpoints, so they overlap without
// being the same set. Sharing one list would couple them into agreeing about things they
// have no reason to agree about.
const (
	pathAPIMe               = "/api/me"
	pathPolicyConsent       = "/api/me/policy-consent"
	pathPolicyConsentRemind = "/api/me/policy-consent/remind-later"
	pathAPILogout           = "/api/auth/logout"
	pathAPIVersion          = "/api/version"
	pathAPIi18n             = "/api/i18n"
	pathAPICompleteSetup    = "/api/complete-setup"
	pathAPILogin            = "/api/auth/login"
	pathAPIRegister         = "/api/register"
	pathAPIDeregister       = "/api/deregister"
	pathAPITunnelStatus     = "/api/tunnel-status"
)

// Consent phases. These strings cross the wire to both portals and to the client, so
// they are part of the API surface.
const (
	// ConsentPhaseNone means there is nothing outstanding: either re-consent is not
	// configured, or this user has already accepted the current version.
	ConsentPhaseNone = ""
	// ConsentPhaseGrace means outstanding, deadline not close. Portal usable behind a
	// dismissible gate; clients work normally.
	ConsentPhaseGrace = "grace"
	// ConsentPhaseWarning means outstanding and inside the warning window. Escalated
	// banner in the portal, and a startup warning from the client.
	ConsentPhaseWarning = "warning"
	// ConsentPhaseExpired means the grace window has run out. Portal blocked, and new
	// tunnels refused.
	ConsentPhaseExpired = "expired"
)

// ConsentState is one user's standing against the current policy version. It is
// serialised onto /api/me for both portals and onto the tunnel registration response for
// the client -- the client's copy has to come from an authenticated exchange, because
// this is per-user and /api/version is not authenticated.
type ConsentState struct {
	Required   bool   `json:"required"`
	DocumentID string `json:"document_id,omitempty"`
	Version    string `json:"version,omitempty"`
	Phase      string `json:"phase,omitempty"`
	// Deadline is when the grace window runs out for this user, RFC3339. Empty when
	// nothing is outstanding.
	Deadline string `json:"deadline,omitempty"`
	// SecondsRemaining is how long until Deadline, floored at zero. The client renders a
	// countdown from this rather than parsing Deadline, matching how it already handles
	// the gateway shutdown warning.
	SecondsRemaining int64  `json:"seconds_remaining,omitempty"`
	PolicyURL        string `json:"policy_url,omitempty"`
	CookieURL        string `json:"cookie_url,omitempty"`
	// PortalURL is where the acceptance is actually made. Carried explicitly because the
	// people this matters most to are CLI-only and have no other way to find it.
	PortalURL string `json:"portal_url,omitempty"`
	// AcceptedAt is when this user accepted the CURRENT version, RFC3339, present only
	// when Required is false because they have accepted. Lets the portal show "you
	// accepted version N on date D" rather than only ever showing an outstanding ask.
	AcceptedAt string `json:"accepted_at,omitempty"`
}

// policyGateRefusal is the 403 body the portal gate returns. A named type rather than a
// map so the wire keys live in struct tags -- the same shape the MFA gate's response has,
// which is what lets a single global handler in each portal recognise it.
type policyGateRefusal struct {
	Error                 string `json:"error"`
	PolicyConsentRequired bool   `json:"policy_consent_required"`
}

// policyConsentAcceptance is what POST /api/me/policy-consent answers with: the new state,
// so the caller need not re-fetch /api/me to find out it is now clear.
type policyConsentAcceptance struct {
	Status string `json:"status"`
	// A pointer with omitempty so "Remind me later", which shares this type, does not
	// report a zero-valued consent block that reads as "nothing outstanding".
	PolicyConsent *ConsentState `json:"policy_consent,omitempty"`
}

// Blocking reports whether this state should stop the user doing anything. Only the
// expired phase blocks; grace and warning are notice, not enforcement.
func (c ConsentState) Blocking() bool {
	return c.Required && c.Phase == ConsentPhaseExpired
}

// consentGraceDays returns the configured grace window in days, falling back to the
// default when unset or nonsensical. Zero is treated as unset rather than as "no grace":
// a deployment wanting no grace at all should not be running a grace period, and reading
// an unset key as an instant lockout is the kind of surprise this whole feature exists
// to avoid.
func (s *Server) consentGraceDays() int {
	if s.cfg == nil || s.cfg.PolicyConsentGraceDays <= 0 {
		return 14
	}
	return s.cfg.PolicyConsentGraceDays
}

// consentWarningDays returns the warning window, clamped to the grace window. A warning
// that starts before the window it warns about would be permanently on, which is the
// same as having no warning at all.
func (s *Server) consentWarningDays() int {
	grace := s.consentGraceDays()
	if s.cfg == nil || s.cfg.PolicyConsentWarningDays <= 0 {
		if grace < 5 {
			return grace
		}
		return 5
	}
	if s.cfg.PolicyConsentWarningDays > grace {
		return grace
	}
	return s.cfg.PolicyConsentWarningDays
}

// consentPhase is the whole enforcement decision, as a pure function of four values so
// it can be tested without a database or a clock.
//
// firstSeen is when this user first had the version put in front of them; a zero value
// means never, which is not expired -- it is "the window has not started". Somebody who
// has not logged in or run a client since the version was published has not been asked
// yet, and cutting them off for not answering a question nobody put to them is exactly
// the failure the first-sight model was chosen to avoid.
func consentPhase(firstSeen, now time.Time, graceDays, warningDays int) (string, time.Time) {
	if firstSeen.IsZero() {
		return ConsentPhaseGrace, time.Time{}
	}
	deadline := firstSeen.Add(time.Duration(graceDays) * 24 * time.Hour)
	if !now.Before(deadline) {
		return ConsentPhaseExpired, deadline
	}
	if !now.Before(deadline.Add(-time.Duration(warningDays) * 24 * time.Hour)) {
		return ConsentPhaseWarning, deadline
	}
	return ConsentPhaseGrace, deadline
}

// policyConsentState resolves one user's consent standing.
//
// When record is true the user's first sight of the current version is stamped if it has
// not been already, which is what starts their grace window. Pass true from the paths
// that genuinely put the policy in front of somebody -- the portal's /api/me and the
// client's tunnel registration -- and false from anywhere merely inspecting the state.
//
// Every database failure here yields "nothing outstanding". That is deliberate: this
// gate can stop a demo, and a transient storage error is not evidence that a user has
// failed to consent. The failure is logged rather than swallowed.
func (s *Server) policyConsentState(user *db.User, record bool) ConsentState {
	state := ConsentState{}
	if user == nil || s.cfg == nil || s.db == nil {
		return state
	}
	version := s.cfg.PolicyVersion
	if version == "" {
		// Re-consent is off. Pre-#1707 behaviour, and the default.
		return state
	}

	accepted, err := s.db.HasAcknowledged(user.ID, PolicyDocumentID, version)
	if err != nil {
		slog.Error("[Consent] Failed to read acknowledgement history; treating consent as satisfied", "user", user.ID, "error", err)
		return state
	}

	state.DocumentID = PolicyDocumentID
	state.Version = version
	state.PolicyURL = s.cfg.PrivacyPolicyURL
	state.CookieURL = s.cfg.CookiePolicyURL
	state.PortalURL = s.cfg.PortalURL

	if accepted {
		if history, err := s.db.ListAcknowledgements(user.ID); err == nil {
			for _, a := range history {
				if a.DocumentID == PolicyDocumentID && a.Version == version {
					state.AcceptedAt = a.AcceptedAt.UTC().Format(time.RFC3339)
					break
				}
			}
		}
		return state
	}

	now := time.Now().UTC()
	var firstSeen time.Time
	if record {
		firstSeen, err = s.db.RecordFirstSeen(user.ID, PolicyDocumentID, version, now)
	} else {
		firstSeen, err = s.db.GetFirstSeen(user.ID, PolicyDocumentID, version)
	}
	if err != nil {
		slog.Error("[Consent] Failed to read or record first sight; treating consent as satisfied", "user", user.ID, "error", err)
		return ConsentState{}
	}

	state.Required = true
	phase, deadline := consentPhase(firstSeen, now, s.consentGraceDays(), s.consentWarningDays())
	state.Phase = phase
	if !deadline.IsZero() {
		state.Deadline = deadline.Format(time.RFC3339)
		if remaining := deadline.Sub(now); remaining > 0 {
			state.SecondsRemaining = int64(remaining.Seconds())
		}
	}
	return state
}

// recordPolicyConsent appends one acceptance of the current version.
func (s *Server) recordPolicyConsent(user *db.User, ip, userAgent string) error {
	return s.db.RecordAcknowledgement(&db.Acknowledgement{
		UserID:     user.ID,
		DocumentID: PolicyDocumentID,
		Version:    s.cfg.PolicyVersion,
		AcceptedAt: time.Now().UTC(),
		IP:         ip,
		UserAgent:  userAgent,
	})
}

// policyConsentRefusalMessage is what a refused client prints. It has to be
// self-contained: the audience for this message is somebody who lives on the CLI, has
// never opened the portal, and has just had a tunnel refused. Telling them only that
// consent is outstanding, without saying where to give it, would leave them stuck.
func policyConsentRefusalMessage(c ConsentState) string {
	where := c.PortalURL
	if where == "" {
		where = "the Liferay Tunnel portal"
	}
	return fmt.Sprintf(
		"Your acceptance of the updated Privacy Policy and Cookie Disclosure is overdue, so new tunnels are refused. Accept it at %s to continue. Tunnels already running are not affected.",
		where,
	)
}

// PolicyConsentNoticeText renders the startup warning a client prints during the warning
// window. Returns "" when there is nothing to say, matching PinnedShutdownNotice's shape
// so the caller stays a two-line if.
//
// Only the warning and expired phases speak. The grace phase deliberately stays silent on
// the CLI: a message on every tunnel start for two weeks is noise, and noise is what stops
// the one message that matters from being read.
func PolicyConsentNoticeText(c *ConsentState) string {
	if c == nil || !c.Required {
		return ""
	}
	where := c.PortalURL
	if where == "" {
		where = "the Liferay Tunnel portal"
	}
	switch c.Phase {
	case ConsentPhaseWarning:
		return fmt.Sprintf(
			"The Privacy Policy and Cookie Disclosure have changed. Accept the update at %s within %s, or new tunnels will stop being accepted.",
			where, formatConsentRemaining(c.SecondsRemaining),
		)
	case ConsentPhaseExpired:
		return policyConsentRefusalMessage(*c)
	default:
		return ""
	}
}

// formatConsentRemaining renders a coarse duration -- days and hours, or hours and
// minutes inside the last day. Coarse on purpose: a deadline days away rendered to the
// second reads as machine output rather than as something to act on.
func formatConsentRemaining(seconds int64) string {
	if seconds <= 0 {
		return "no time"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// handlePolicyConsentAccept records this user's acceptance of the current version.
//
// POST rather than a field on PUT /api/me: consent is an event, not a profile attribute,
// and the thing being recorded is that it happened at a particular moment from a
// particular address.
func (s *Server) handlePolicyConsentAccept(w http.ResponseWriter, r *http.Request) {
	// getCurrentUserRaw, not getCurrentUser: an owner previewing another role must not be
	// able to record consent as somebody else, and getCurrentUser returns a copy carrying
	// the previewed role.
	user, err := s.getCurrentUserRaw(r)
	if err != nil || user == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if s.cfg == nil || s.cfg.PolicyVersion == "" || s.db == nil {
		respondJSON(w, http.StatusOK, policyConsentAcceptance{Status: "ok", PolicyConsent: &ConsentState{}})
		return
	}
	if err := s.recordPolicyConsent(user, s.clientIP(r), r.UserAgent()); err != nil {
		slog.Error("[Consent] Failed to record acceptance", "user", user.ID, "error", err)
		http.Error(w, `{"error":"Failed to record consent"}`, http.StatusInternalServerError)
		return
	}
	s.writeAudit(user.Email, "policy.consent", "policy", s.cfg.PolicyVersion,
		"Accepted "+PolicyDocumentID+" version "+s.cfg.PolicyVersion, r)

	state := s.policyConsentState(user, false)
	respondJSON(w, http.StatusOK, policyConsentAcceptance{
		Status:        "ok",
		PolicyConsent: &state,
	})
}

// handlePolicyConsentRemindLater suppresses the gate for THIS session only.
//
// Held on the session and not written to the database, so the next login sees the gate
// again. A dismissal that outlived the session would turn the banner into wallpaper and
// leave the grace window as the only pressure -- see PortalSessionData.
func (s *Server) handlePolicyConsentRemindLater(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	data, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	data.PolicyReminderDismissed = true
	s.sessionStore().storePortalSession(cookie.Value, data)
	respondJSON(w, http.StatusOK, policyConsentAcceptance{Status: "ok"})
}

// policyGateSuppressed reports whether this request's session has dismissed the gate.
func (s *Server) policyGateSuppressed(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	data, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok {
		return false
	}
	return data.PolicyReminderDismissed
}

// enforcePolicyConsentGate blocks the portal API once a user's grace window has run out,
// and reports whether it answered the request.
//
// Modelled on the ForceMFA gate it sits beside: a 403 carrying a flag the portal
// recognises, so a single global handler can put the acceptance screen up rather than
// every panel failing separately and the app looking broken.
//
// The bypass list is what a blocked user still needs in order to become unblocked --
// reading who they are, accepting, logging out, and the unauthenticated endpoints. Tunnel
// registration is bypassed here because it enforces the same thing itself, with a message
// written for a terminal rather than for a browser.
func (s *Server) enforcePolicyConsentGate(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg == nil || s.cfg.PolicyVersion == "" {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	switch r.URL.Path {
	case pathAPIMe,
		pathPolicyConsent,
		pathPolicyConsentRemind,
		pathAPILogout,
		pathAPIVersion,
		pathAPIi18n,
		pathAPICompleteSetup,
		pathAPIRegister,
		pathAPIDeregister,
		pathAPITunnelStatus:
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/auth/") && r.URL.Path != pathAPILogin {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/internal/") {
		return false
	}

	user, err := s.getCurrentUserRaw(r)
	if err != nil || user == nil {
		return false
	}
	// record=false: a request being refused is not the moment somebody was shown the
	// policy, and stamping first-sight from an arbitrary XHR would start the clock for a
	// user who never saw anything.
	if !s.policyConsentState(user, false).Blocking() {
		return false
	}
	respondJSON(w, http.StatusForbidden, policyGateRefusal{
		Error:                 "Policy acceptance required",
		PolicyConsentRequired: true,
	})
	return true
}
