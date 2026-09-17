package server

import (
	"fmt"
	"log/slog"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// MinVersionDocumentID is the key this floor's first-sight records are stored under in
// user_acknowledgement_notices (#1988).
//
// That table records what a user has been SHOWN rather than what they agreed to, and it is
// keyed on (user_id, document_id, version) precisely so a second notice can register itself
// without another table. Using it here reuses the storage and NOT the obligation: min_version
// rows sit under their own document id, so a version deadline and a consent deadline are
// separate rows with separate windows. A user can be inside one and outside the other, which
// is the point -- they are different obligations with different remedies, and the issue is
// explicit that they must not be coupled.
//
// Keyed on the min version string as the "version", so raising min_version starts a fresh
// window for everybody rather than inheriting the previous rollout's expired one.
const MinVersionDocumentID = "min_client_version"

// UpgradeCommand is the remedy for a version floor, named in every message this file
// produces. Consent is resolved in the portal; a version is resolved here, and telling
// someone their client is too old without telling them this is how the support burden
// lands on the one person #1948 exists to protect.
const UpgradeCommand = "lfr-tunnel -upgrade"

// MinVersionState is one client's standing against this gateway's minimum version. It rides
// the registration response, which is the only authenticated exchange a client makes before
// a tunnel exists -- /api/version advertises the floor but is unauthenticated, and the
// deadline is per-user.
//
// Deliberately a separate block from ConsentState rather than a field on it: the two travel
// together, expire independently, and say different things about what to do next.
type MinVersionState struct {
	Required bool `json:"required"`
	// MinVersion is the floor this gateway enforces, and ClientVersion is what the client
	// said it was. Both are carried so the client can render "you are on X, the minimum is
	// Y" without re-deriving either -- the old message named neither.
	MinVersion    string `json:"min_version,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	Phase         string `json:"phase,omitempty"`
	// Deadline is when this client stops being accepted, RFC3339. Empty when the window has
	// not started.
	Deadline string `json:"deadline,omitempty"`
	// SecondsRemaining is how long until Deadline, floored at zero. The client renders a
	// countdown from this rather than parsing Deadline, matching ConsentState.
	SecondsRemaining int64 `json:"seconds_remaining,omitempty"`
	// UpgradeCommand is the remedy, carried rather than compiled into the client so a
	// client showing this message cannot be one that predates the wording.
	UpgradeCommand string `json:"upgrade_command,omitempty"`
}

// Blocking reports whether this state should refuse a new tunnel. Only the expired phase
// blocks; grace and warning are notice, not enforcement -- the same rule ConsentState uses.
func (m MinVersionState) Blocking() bool {
	return m.Required && m.Phase == GracePhaseExpired
}

// minVersionGraceDays is how long a client below the floor keeps working after it is first
// seen below it. Configured rather than constant, and per rollout: the FIRST bump wants a
// generous window because the fleet contains clients too old to have been warned at all,
// while a later one tightening an already-current fleet does not (#1948).
func (s *Server) minVersionGraceDays() int {
	if s.cfg == nil || s.cfg.MinClientVersionGraceDays <= 0 {
		return 14
	}
	return s.cfg.MinClientVersionGraceDays
}

// minVersionWarningDays is how long before the deadline the client starts saying so.
func (s *Server) minVersionWarningDays() int {
	grace := s.minVersionGraceDays()
	configured := 0
	if s.cfg != nil {
		configured = s.cfg.MinClientVersionWarningDays
	}
	return clampWarningDays(grace, configured, 5)
}

// minVersionState resolves one client's standing against the floor.
//
// When record is true this client's first sight of the current floor is stamped if it has not
// been already, which is what starts its window. Pass true from tunnel registration -- the
// moment a too-old client actually presents itself -- and false from anywhere merely
// inspecting.
//
// Every failure path yields "nothing outstanding". Deliberate, and the same choice consent
// made: this gate can stop a demo, and a storage error is not evidence that anybody is
// running an old client. The failure is logged rather than swallowed.
func (s *Server) minVersionState(user *db.User, clientVersion string, record bool) MinVersionState {
	state := MinVersionState{}
	if user == nil || s.cfg == nil || s.db == nil {
		return state
	}
	floor := s.cfg.MinClientVersion
	if floor == "" {
		return state
	}
	// A client that reports no version cannot be judged against a floor, and a development
	// build is exempt for the same reason the client exempts itself: "dev" orders below
	// every release, so enforcing it would lock out everyone building from source.
	if clientVersion == "" || clientVersion == "dev" {
		return state
	}
	if config.CompareVersions(clientVersion, floor) >= 0 {
		return state
	}

	now := time.Now().UTC()
	var firstSeen time.Time
	var err error
	if record {
		firstSeen, err = s.db.RecordFirstSeen(user.ID, MinVersionDocumentID, floor, now)
	} else {
		firstSeen, err = s.db.GetFirstSeen(user.ID, MinVersionDocumentID, floor)
	}
	if err != nil {
		slog.Error("[MinVersion] Failed to read or record first sight; treating the client as acceptable", "user", user.ID, "error", err)
		return MinVersionState{}
	}

	state.Required = true
	state.MinVersion = floor
	state.ClientVersion = clientVersion
	state.UpgradeCommand = UpgradeCommand
	phase, deadline := gracePhase(firstSeen, now, s.minVersionGraceDays(), s.minVersionWarningDays())
	state.Phase = phase
	if !deadline.IsZero() {
		state.Deadline = deadline.Format(time.RFC3339)
		if remaining := deadline.Sub(now); remaining > 0 {
			state.SecondsRemaining = int64(remaining.Seconds())
		}
	}
	return state
}

// minVersionRefusalMessage is what a refused client prints.
//
// It has to name three things the old message named none of: which version the client is on,
// which version is required, and the command that fixes it. The audience is somebody on the
// CLI whose tunnel has just stopped working, and "too old" without a remedy turns every one
// of them into a support request.
func minVersionRefusalMessage(m MinVersionState) string {
	return fmt.Sprintf(
		"Your Liferay Tunnel client (%s) is older than the minimum this gateway accepts (%s), and the upgrade period has ended, so new tunnels are refused. Run `%s` to update. Tunnels already running are not affected.",
		m.ClientVersion, m.MinVersion, UpgradeCommand,
	)
}

// MinVersionNoticeText renders the startup warning a client prints during its warning window.
// Returns "" when there is nothing to say, matching PolicyConsentNoticeText's shape so the
// caller stays a two-line if.
//
// Silent during grace, for the same reason consent is: a message on every tunnel start for two
// weeks is noise, and noise is what stops the one message that matters from being read. A
// client below the floor is also below the latest version, so it is already being nudged to
// upgrade by the ordinary "a new version is available" line.
func MinVersionNoticeText(m *MinVersionState) string {
	if m == nil || !m.Required {
		return ""
	}
	switch m.Phase {
	case GracePhaseWarning:
		return fmt.Sprintf(
			"Your Liferay Tunnel client (%s) is older than the minimum this gateway accepts (%s). Run `%s` within %s, or new tunnels will stop being accepted.",
			m.ClientVersion, m.MinVersion, UpgradeCommand, formatGraceRemaining(m.SecondsRemaining),
		)
	case GracePhaseExpired:
		return minVersionRefusalMessage(*m)
	default:
		return ""
	}
}
