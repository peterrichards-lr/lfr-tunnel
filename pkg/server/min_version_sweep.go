package server

import (
	"fmt"
	"html"
	"log/slog"
	"time"

	"lfr-tunnel/pkg/db"
)

// warnUsersEnteringMinVersionWindow emails each user once, when their client's warning window
// for the current min_client_version opens (#2522).
//
// The CLI already says so at startup, but only to someone who reads it: a client launched from a
// script, an IDE task or LDM can run a whole window unseen, and its user learns of the floor when
// a tunnel is refused -- usually at the start of a demo. The portal banner needs the user to show
// up too. This is the one notice that reaches them anyway.
//
// The same claim, send, release-on-failure sequence as the policy reminder
// (warnUsersEnteringConsentWindow), and for the same reasons; the records sit under
// MinVersionDocumentID with the floor as their version, so raising the floor opens a new window
// and a new email for everybody below it, once.
//
// Only users whose client was seen below the floor have a first-sight row, so a user with no
// recorded client, or one at or above the floor, is never a candidate.
//
// The row, not the version the user's client last reported, decides who is told. A user may run
// more than one client -- a current one on their laptop and an old one in a script, an IDE task or
// LDM, which is exactly the client nobody reads the output of -- and the last to register is
// whichever ran last. Judged by that alone, the old client's window passes unannounced whenever the
// laptop registered more recently, and once it expires the old client is refused before its
// version is recorded, so it never would be. The cost of trusting the row instead is one email to a
// user who has already upgraded every client; the email says so.
func (s *Server) warnUsersEnteringMinVersionWindow() {
	if s.cfg == nil || s.db == nil || s.cfg.MinClientVersion == "" {
		return
	}
	// Claiming with nothing able to send would record warnings that never left the building,
	// and leave them for nobody to retry.
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	floor := s.cfg.MinClientVersion
	cutoff := time.Now().UTC().Add(-time.Duration(s.minVersionGraceDays()-s.minVersionWarningDays()) * 24 * time.Hour)
	candidates, err := s.db.ListPendingWarnings(MinVersionDocumentID, floor, cutoff)
	if err != nil {
		slog.Error("[MinVersion] Failed to list users due a client version warning", "error", err)
		return
	}

	for _, userID := range candidates {
		user, err := s.db.GetUser(userID)
		if err != nil || user == nil {
			continue
		}
		state := s.minVersionSighting(user)
		// Expired as well as warning: a user whose window passed while no sweep ran (a gateway
		// down for the day) still has not been told, and is told what has happened instead.
		if !state.Required || state.Phase == GracePhaseGrace {
			continue
		}
		claimed, err := s.db.MarkWarningNotified(userID, MinVersionDocumentID, floor)
		if err != nil {
			slog.Error("[MinVersion] Failed to claim the client version warning email", "user", userID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendMinVersionWarningEmail(user, state); err != nil {
			slog.Error("[MinVersion] Failed to send the client version warning", "user", userID, "error", err)
			if relErr := s.db.ClearWarningNotified(userID, MinVersionDocumentID, floor); relErr != nil {
				slog.Error("[MinVersion] Could not release the client version warning claim: this user is recorded as warned but was not, and no later sweep will retry",
					"user", userID, "error", relErr)
			}
		}
	}
}

// minVersionSighting is a user's standing against the floor for the email: minVersionState for
// the client they last registered with when that one is below the floor, and otherwise the window
// a client seen below it earlier started -- with no version named, because which one it was is not
// recorded. Read-only: windows are started by registrations, never by the sweep.
func (s *Server) minVersionSighting(user *db.User) MinVersionState {
	if state := s.minVersionState(user, user.LastClientVersion, false); state.Required {
		return state
	}
	floor := s.cfg.MinClientVersion
	firstSeen, err := s.db.GetFirstSeen(user.ID, MinVersionDocumentID, floor)
	if err != nil || firstSeen.IsZero() {
		return MinVersionState{}
	}
	now := time.Now().UTC()
	phase, deadline := gracePhase(firstSeen, now, s.minVersionGraceDays(), s.minVersionWarningDays())
	state := MinVersionState{
		Required:       true,
		MinVersion:     floor,
		UpgradeCommand: UpgradeCommand,
		Phase:          phase,
		Deadline:       deadline.Format(time.RFC3339),
	}
	if remaining := deadline.Sub(now); remaining > 0 {
		state.SecondsRemaining = int64(remaining.Seconds())
	}
	return state
}

// minVersionWarningEmail is the template context for min_version_warning.html.
type minVersionWarningEmail struct {
	Name           string
	Expired        bool
	ClientVersion  string
	MinVersion     string
	Deadline       string
	Remaining      string
	UpgradeCommand string
	PortalLink     string
}

// minVersionClientPhrase names the client the email is about: by version when it is known.
func minVersionClientPhrase(version string) string {
	if version == "" {
		return "A Liferay Tunnel client on your account"
	}
	return fmt.Sprintf("Your Liferay Tunnel client (%s)", version)
}

// sendMinVersionWarningEmail sends one min_version_warning email, synchronously, so the sweep can
// release its claim when it fails.
//
// Transactional, like the policy reminder: notification_prefs = "disabled" does not suppress it.
// By email_policy.go's own criterion it is the last notice before new tunnels are refused, and a
// mute that silences it makes that loss arrive with no warning through any channel the user saw.
func (s *Server) sendMinVersionWarningEmail(user *db.User, state MinVersionState) error {
	if !shouldSendTo(user, emailTransactional) {
		return nil
	}
	deadline := state.Deadline
	if t, err := time.Parse(time.RFC3339, state.Deadline); err == nil {
		deadline = t.UTC().Format(expiryEmailTimeFormat)
	}
	data := minVersionWarningEmail{
		Name:           user.FirstName,
		Expired:        state.Blocking(),
		ClientVersion:  state.ClientVersion,
		MinVersion:     state.MinVersion,
		Deadline:       deadline,
		Remaining:      formatGraceRemaining(state.SecondsRemaining),
		UpgradeCommand: state.UpgradeCommand,
		PortalLink:     s.getPortalBaseURL(nil) + PortalV2Base,
	}
	body, err := s.renderEmailTemplate(user.LanguagePreference, "min_version_warning.html", data)
	if err != nil {
		slog.Warn("[MinVersion] Failed to render the min_version_warning template; sending the plain version", "error", err)
		body = fmt.Sprintf("<p>Hi %s,</p><p>%s is older than the minimum this gateway accepts (%s). Run <code>%s</code>.</p>",
			html.EscapeString(data.Name), html.EscapeString(minVersionClientPhrase(data.ClientVersion)), html.EscapeString(data.MinVersion), data.UpgradeCommand)
	}
	when := fmt.Sprintf("Upgrade it within %s (by %s), or new tunnels will be refused.", data.Remaining, data.Deadline)
	if data.Expired {
		when = fmt.Sprintf("The upgrade period ended on %s, so new tunnels are refused until it is upgraded.", data.Deadline)
	}
	ignore := ""
	if data.ClientVersion == "" {
		ignore = "If you have already upgraded every client you use, you can ignore this.\n\n"
	}
	plain := fmt.Sprintf("Hi %s,\n\n%s is older than the minimum this gateway accepts (%s). %s Tunnels already running are not interrupted.\n\nTo upgrade, run:\n  %s\n\n%sPortal: %s\n\nThis is sent regardless of your notification preferences, because new tunnels are refused after the deadline.\n",
		data.Name, minVersionClientPhrase(data.ClientVersion), data.MinVersion, when, data.UpgradeCommand, ignore, data.PortalLink)
	subject := fmt.Sprintf("Action required: upgrade your Liferay Tunnel client to %s", data.MinVersion)
	return s.sendNotification(notifyMinVersionWarning, user.Email, subject, body, plain)
}
