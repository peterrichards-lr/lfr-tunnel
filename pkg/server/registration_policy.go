package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// Per-registration policy, resolved in one place rather than once per registration path (#2018).
//
// This is the same class #2005 named and #2017 fixed the first instance of: a decision about a
// single registration, computed independently by handleRegister (a client registering directly on
// this gateway) and handleEdgeRegister (central validating on an edge's behalf), thousands of
// lines apart in the same file. Edge-served registration has been the NORMAL case since #1947, so
// a change made to one block lands on whichever half of the fleet a client happens to be routed
// to, and nothing reports the divergence -- both paths still register successfully.
//
// The max-active-tunnels resolution is the sharper of the two, because it decides a REFUSAL: a
// divergence there is not a wrong number on a dashboard, it is a user who can open a tunnel from
// one region and not from another.
//
// #2020 closed the class out with the last two members: the random-subdomain generation loop --
// the only one that had already diverged, and the only one whose divergence had a live consequence
// -- and the client version/OS bookkeeping. See grantRandomSubdomain for why that divergence was a
// defect rather than a deliberate asymmetry, and for the two commits that turned it into one.
//
// What is NOT here, deliberately: the tunnel COUNTING around maxActiveTunnelsFor. The two paths
// count different things and are right to -- central has to add s.edgeLeases for tunnels it is
// not itself serving, and the direct path has no such leases to add. Only the resolution of the
// limit was duplicated. See registration_policy_test.go for the property that holds both halves
// together.

// Refusal messages. Named constants rather than inline literals because each is now written from
// exactly one place, and a caller that renders it differently (RegisterResponse on the direct
// path, a bare JSON map on the edge path) must still be rendering the same words.
const (
	activeTunnelLimitFormat      = "Active tunnels concurrency limit reached (%d). Stop another active tunnel or ask an administrator to increase your limit."
	subdomainQuarantinedMessage  = "Subdomain is currently in quarantine"
	subdomainReservedByOther     = "Subdomain is reserved by another user"
	subdomainMustBeReserved      = "Custom subdomains must be reserved in the portal prior to connecting"
	subdomainQuotaReachedMessage = "Subdomain reservation quota limit reached"
	randomSubdomainRefusal       = "failed to generate unique random subdomain"
)

// randomSubdomainAttempts is how many candidates either path tries before refusing. Kept at the
// 10 both handlers used: the point of this extraction is that the two agree, not that they try
// harder.
const randomSubdomainAttempts = 10

// defaultSubdomainStyle is the generator used when a registration has no usable preference to
// honour. It is the value db.User defaults the column to, so an account that has never touched
// the setting keeps exactly the names it had before #2031.
const defaultSubdomainStyle = subdomainStyleLiferay

// The generator styles, named once. goconst attributes a literal's package-wide occurrences to
// whichever file is newest (#1655), so introducing the set below as bare strings made this file
// answer for every "random" and "words" in the package -- and the names are worth having anyway,
// because server_domain.go's switch and subdomainStyles below MUST agree and now say so.
const (
	subdomainStyleLiferay = "liferay"
	subdomainStyleWords   = "words"
	subdomainStyleHeroku  = "heroku"
	subdomainStyleNgrok   = "ngrok"
	// subdomainStyleRandom is generateRandomSubdomainPrefix's default branch: eight
	// alphanumerics. A real style, not a fallback.
	subdomainStyleRandom = "random"
)

// subdomainStyles is every value generateRandomSubdomainPrefix actually distinguishes, and so
// every value a stored preference may name.
//
// "random" is in it deliberately: it is the portal's "Alphanumeric" option and reaches
// generateRandomSubdomainPrefix's default branch, which is a real style rather than a fallback.
// Note it is unrelated to the literal "random" a client sends as its SUBDOMAIN PREFIX to ask for
// a generated name -- different field, same word.
//
// TestEveryStyleThePortalOffersIsOneTheServerHonours holds this in step with the two account
// settings screens that write the column, so offering a style the server quietly ignores fails
// the build rather than shipping -- which is the defect #2031 itself was.
var subdomainStyles = map[string]bool{
	subdomainStyleLiferay: true,
	subdomainStyleWords:   true,
	subdomainStyleHeroku:  true,
	subdomainStyleNgrok:   true,
	subdomainStyleRandom:  true,
}

// subdomainStyleFor resolves which generator this registration should use.
//
// #2031: db.User.SubdomainStyle is writable from both portals and, until now, was read by nothing
// on the registration path -- both handlers hardcoded "liferay". The account setting's own help
// text says "The style used to generate a default subdomain when you connect your CLI without
// specifying one", which was the one thing it did not do.
//
// A nil userRec falls back rather than refusing: it means there is no database, or the read of the
// user's row failed, and an unavailable record is not evidence of a preference. An unrecognised
// value falls back too, rather than reaching generateRandomSubdomainPrefix's default branch --
// that branch is the "random" style, so letting a typo land there would silently grant a DIFFERENT
// real style instead of the intended one.
func subdomainStyleFor(userRec *db.User) string {
	if userRec == nil || !subdomainStyles[userRec.SubdomainStyle] {
		return defaultSubdomainStyle
	}
	return userRec.SubdomainStyle
}

// subdomainHeldInMemory reports whether a live tunnel already occupies this name, anywhere on the
// fleet this control plane knows about.
//
// BOTH in-memory views, because the control plane has both and a collision between them is a
// collision on one hostname:
//
//   - s.registry holds the leases this gateway serves itself;
//   - s.edgeLeases holds the leases every edge node serves, recorded by handleEdgeRegister and
//     read back by resolveRemoteRouteForHost to route visitor traffic.
//
// Matching on the full host rather than the bare prefix is what makes this a name collision test
// and not a prefix one -- resolveRemoteRouteForHost compares the same way. A lease recorded
// without a full host (no domains were supplied) falls back to the prefix, since that is all the
// name it has.
func (s *Server) subdomainHeldInMemory(subdomain string, domains []string) bool {
	if s.registry != nil {
		if available, _ := s.registry.CheckSubdomain(subdomain, domains); !available {
			return true
		}
	}

	s.edgeLeasesMu.RLock()
	defer s.edgeLeasesMu.RUnlock()
	for _, leases := range s.edgeLeases {
		for _, el := range leases {
			if el.FullHost == "" {
				if strings.EqualFold(el.Subdomain, subdomain) {
					return true
				}
				continue
			}
			for _, d := range domains {
				if strings.EqualFold(el.FullHost, subdomain+"."+d) {
					return true
				}
			}
		}
	}
	return false
}

// subdomainReserved reports whether any of domains already carries a reservation row for this
// name. Whose it is does not matter: a random name is being handed out to somebody who did not ask
// for this one, so "reserved at all" is the answer, unlike the explicit-subdomain path where the
// holder's identity decides the outcome.
func (s *Server) subdomainReserved(subdomain string, domains []string) bool {
	if s.db == nil {
		return false
	}
	for _, d := range domains {
		if existing, err := s.db.GetSubdomainReservationByName(subdomain, d); err == nil && existing != nil {
			return true
		}
	}
	return false
}

// grantRandomSubdomain picks a name for a client that asked for a random one, for both paths.
// Reports the name and whether one was found at all.
//
// The generator is the registering user's own, via subdomainStyleFor (#2031); userRec may be nil,
// which falls back to defaultSubdomainStyle.
//
// This was the last member of #2005's class, and the only one that had already diverged (#2020):
// the direct path checked the in-memory registry and the reservations table, the edge path checked
// only the reservations table. The asymmetry was harmless when it was written -- an edge issued
// tunnels under its own regional domain (#183, 2026-06-25), so a prefix shared with a central
// tunnel produced two different hostnames. It stopped being harmless on 2026-08-24, when
// tunnel_domains made every gateway issue on the shared apex (#1288) and the control plane began
// publishing a per-tunnel DNS record that beats the wildcard (#1295). From then on the two names
// were one name, and DNS decided which of two users a visitor reached.
//
// It is NOT a case of "the edge cannot know". Both handlers run on the control plane -- an edge's
// registration is validated here, not there -- and handleEdgeRegister already reads both
// s.registry and s.edgeLeases thirty lines further down to count the user's active tunnels. The
// information was present and used in the same function.
func (s *Server) grantRandomSubdomain(domains []string, userRec *db.User) (string, bool) {
	style := subdomainStyleFor(userRec)
	for attempt := 0; attempt < randomSubdomainAttempts; attempt++ {
		candidate := s.generateRandomSubdomainPrefix(style)
		if s.subdomainHeldInMemory(candidate, domains) {
			continue
		}
		if s.subdomainReserved(candidate, domains) {
			continue
		}
		return candidate, true
	}
	return "", false
}

// recordClientVersionAndOS stamps the client's reported version and OS onto the user row, writing
// it back only when something actually changed.
//
// The lowest-stakes member of the class -- a divergence here shows up as a stale value on an admin
// screen, not a refusal -- but it was five byte-identical lines in both handlers, and the write it
// performs is the one both paths were discarding the error of. Logged rather than discarded now:
// errcheck runs with check-blank, so `_ =` never satisfied it anyway, and a failed write here
// means the admin screen is about to show a stale client version with nothing saying why.
func (s *Server) recordClientVersionAndOS(userRec *db.User, clientVersion, clientOS string) {
	if userRec == nil || s.db == nil {
		return
	}
	changed := false
	if clientVersion != "" && userRec.LastClientVersion != clientVersion {
		userRec.LastClientVersion = clientVersion
		changed = true
	}
	if clientOS != "" && userRec.LastClientOS != clientOS {
		userRec.LastClientOS = clientOS
		changed = true
	}
	if !changed {
		return
	}
	if err := s.db.UpdateUser(userRec); err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to record client version/OS for %s: %v", userRec.ID, err))
	}
}

// maxActiveTunnelsFor resolves how many concurrent tunnels this registration's user is allowed,
// where 0 (or less) means unlimited.
//
// Two steps, and the order is the policy:
//
//  1. the role default -- admin and owner may each carry their own ceiling, and fall back to
//     default_max_active_tunnels when they do not;
//  2. the user's own override, when their record sets one, wins outright over the role default,
//     in both directions. An operator raising one user above their role's ceiling and an
//     operator capping one user below it are the same mechanism, so this is a replacement and
//     not a clamp.
//
// user is the authenticated identity and carries the role; userRec is the same user re-read from
// the database and may be nil when that read failed or there is no database. A nil userRec
// therefore falls back to the role default rather than refusing -- the override is the user's,
// and an unavailable record is not evidence that one exists.
func (s *Server) maxActiveTunnelsFor(user *db.User, userRec *db.User) int {
	maxTunnels := s.cfg.DefaultMaxActiveTunnels
	if user != nil {
		// roleAdmin/roleOwner rather than the literals the two call sites used: goconst
		// attributes a literal's package-wide occurrences to whichever file is newest, so
		// moving "admin" here made this file answer for all 33 of them.
		if user.Role == roleAdmin && s.cfg.AdminMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.AdminMaxActiveTunnels
		} else if user.Role == roleOwner && s.cfg.OwnerMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.OwnerMaxActiveTunnels
		}
	}
	if userRec != nil && userRec.MaxTunnels != nil {
		maxTunnels = *userRec.MaxTunnels
	}
	return maxTunnels
}

// activeTunnelLimitRefusal is the message both paths refuse with once the limit is reached. The
// number is in the text on purpose: "you have too many" is not actionable without it.
func activeTunnelLimitRefusal(maxTunnels int) string {
	return fmt.Sprintf(activeTunnelLimitFormat, maxTunnels)
}

// reservationStanding is where an existing reservation sits on the
// live -> expired-but-quarantined -> lapsed timeline.
//
// The quarantine window is what stops a subdomain that expired an hour ago from being claimed by
// a stranger while its former holder is still reconnecting to it. Shared by all THREE reservation
// sites, including the custom-domain one that otherwise has its own quota, its own expiry rule
// and its own wording.
type reservationStanding int

const (
	// reservationLive: unexpired, or permanent. Only its holder may register on it.
	reservationLive reservationStanding = iota
	// reservationQuarantined: expired, still inside subdomain_quarantine_days. Its former
	// holder may take it back; nobody else may take it at all.
	reservationQuarantined
	// reservationLapsed: expired and past quarantine. Reclaimable by anyone, and the stale row
	// is deleted on the way past.
	reservationLapsed
)

// standingOf classifies an existing reservation. Written against the row rather than against a
// bare *time.Time so the three-way answer is impossible to collapse back into a bool at a call
// site -- "not live" and "free to take" are different questions, and conflating them is what a
// quarantine window exists to prevent.
func (s *Server) standingOf(existing *db.SubdomainReservation) reservationStanding {
	if existing == nil || existing.ExpiresAt == nil || !existing.ExpiresAt.Before(time.Now()) {
		return reservationLive
	}
	if time.Now().Before(existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)) {
		return reservationQuarantined
	}
	return reservationLapsed
}

// reservationRefusal is a decision to refuse, returned rather than written.
//
// The two registration paths do not write errors the same way: the direct path answers with a
// RegisterResponse the client parses, the edge path answers a bare JSON map that the edge relays
// verbatim. Returning the decision keeps the POLICY shared while leaving each caller its own
// wire format -- which is the part that legitimately differs.
type reservationRefusal struct {
	Status  int
	Message string
}

// resolveSubdomainReservations decides which of domains must be auto-reserved for subdomain
// before this registration can proceed, or refuses.
//
// Returns the domains needing a reservation and a nil refusal when the registration may go ahead;
// an empty list and a non-nil refusal otherwise. Only ever called with s.db non-nil.
func (s *Server) resolveSubdomainReservations(subdomain string, domains []string, user *db.User, userRec *db.User) ([]string, *reservationRefusal) {
	var domainsToReserve []string
	for _, d := range domains {
		existing, err := s.db.GetSubdomainReservationByName(subdomain, d)
		if err != nil || existing == nil {
			// No reservation exists. Whether the client may create one on the spot is the
			// operator's call, per role.
			if !s.canUserAutoReserve(userRec) {
				return nil, &reservationRefusal{Status: http.StatusForbidden, Message: subdomainMustBeReserved}
			}
			domainsToReserve = append(domainsToReserve, d)
			continue
		}

		switch s.standingOf(existing) {
		case reservationQuarantined:
			if existing.UserID != user.ID {
				return nil, &reservationRefusal{Status: http.StatusConflict, Message: subdomainQuarantinedMessage}
			}
			// Quarantined but this user's own: extend/re-reserve it.
			domainsToReserve = append(domainsToReserve, d)
		case reservationLapsed:
			// Past quarantine: drop the stale row and re-reserve, whoever held it.
			if err := s.db.DeleteSubdomainReservation(existing.ID); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to delete lapsed reservation for %s on %s: %v", subdomain, d, err))
			}
			domainsToReserve = append(domainsToReserve, d)
		case reservationLive:
			if existing.UserID != user.ID {
				return nil, &reservationRefusal{Status: http.StatusConflict, Message: subdomainReservedByOther}
			}
		}
	}
	return domainsToReserve, nil
}

// createSubdomainReservations checks the user's reservation quota and then creates the
// reservations resolveSubdomainReservations asked for, or refuses.
//
// Only ever called with s.db non-nil. A no-op, and never a refusal, when there is nothing to
// reserve -- the quota bounds what a user may hold, not what they may register on.
func (s *Server) createSubdomainReservations(subdomain string, domainsToReserve []string, user *db.User, userRec *db.User) *reservationRefusal {
	if len(domainsToReserve) == 0 {
		return nil
	}

	limit := s.cfg.DefaultMaxReservations
	if userRec != nil {
		limit = s.getUserMaxReservations(userRec)
	}

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	activeCount := 0
	if err == nil {
		for _, res := range list {
			// Custom domain reservations (Subdomain == "") are tracked against their own,
			// separate and smaller quota (#1004) and must not count here.
			if res.Subdomain == "" {
				continue
			}
			if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
				activeCount++
			}
		}
	}

	if limit >= 0 && activeCount+len(domainsToReserve) > limit {
		return &reservationRefusal{Status: http.StatusForbidden, Message: subdomainQuotaReachedMessage}
	}

	for _, d := range domainsToReserve {
		// Delete any existing quarantined or expired reservation for this user first.
		if existing, err := s.db.GetSubdomainReservationByName(subdomain, d); err == nil && existing != nil {
			if err := s.db.DeleteSubdomainReservation(existing.ID); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to clear reservation before re-creating %s on %s: %v", subdomain, d, err))
			}
		}
		res := &db.SubdomainReservation{
			UserID:    user.ID,
			Subdomain: subdomain,
			Domain:    d,
			ExpiresAt: s.getUserSubdomainExpiry(user),
		}
		if err := s.db.CreateSubdomainReservation(res); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to auto-create reservation for %s on %s: %v", subdomain, d, err))
		}
	}
	return nil
}

// applySubdomainReservationPolicy is the whole per-registration reservation decision: resolve,
// then create. Both registration paths call exactly this, and differ only in how they render the
// refusal it returns.
func (s *Server) applySubdomainReservationPolicy(subdomain string, domains []string, user *db.User, userRec *db.User) *reservationRefusal {
	domainsToReserve, refusal := s.resolveSubdomainReservations(subdomain, domains, user, userRec)
	if refusal != nil {
		return refusal
	}
	return s.createSubdomainReservations(subdomain, domainsToReserve, user, userRec)
}
