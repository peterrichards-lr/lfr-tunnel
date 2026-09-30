package server

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"lfr-tunnel/pkg/db"
)

// The `approval` half of never_expires (#2267).
//
// Subdomains and custom domains already had this shape: the holder sets a flag on the row, an
// admin works a queue and decides. Personal Access Tokens had nothing -- permanence was a thing
// an admin could do TO a token, with no way for its holder to ask and no record that they had.
//
// So the flow here is deliberately the same one, not a second invention: ask, wait, be told.
// What it adds over the reservation flow is a real DENY. A request that can only ever be granted
// or ignored leaves the holder unable to tell "nobody has looked" from "no", and leaves the admin
// unable to tell a fresh request from the one they already refused.

// ExtensionRequestView is one row of the admin extension queue.
//
// The embedded pointer flattens in JSON, so every field both portal arms already read is
// unchanged and these two are additive.
//
// ResourceKind exists because a custom domain IS a subdomain reservation with an empty Subdomain
// (#1004), and the queue presented every entry as a subdomain extension. An admin approving
// "an extension for .customer.com" was being shown the wrong noun for the thing they were
// deciding about. Both arms could derive it from `subdomain === ”`, which is exactly how two
// arms come to disagree; the server says it once.
type ExtensionRequestView struct {
	*db.SubdomainReservation
	// ResourceKind is "subdomain" or "custom_domain".
	ResourceKind string `json:"resource_kind"`
	// PermanenceAllowed reports whether this gateway will accept a permanent grant for this
	// kind of resource at all. The admin UI offers the "Permanent" choice only when it is
	// true -- under never_expires `disabled`, AdminApproveExtension refuses a permanent
	// approval with 403, and a button that always errors is worse than no button.
	PermanenceAllowed bool `json:"permanence_allowed"`
}

const (
	resourceKindSubdomain    = "subdomain"
	resourceKindCustomDomain = "custom_domain"
	// auditTargetPAT is the TargetType every Personal Access Token audit entry carries. Named
	// because three call sites spell it, and three copies of a string that has to match is the
	// shape of an audit trail that silently splits in two after a typo.
	auditTargetPAT = "pat"
	// unknownUserEmail is what an admin queue shows for a user id that resolves to nobody.
	//
	// Named because two queues need it and goconst counts the literal package-wide, attributing
	// every occurrence to the NEWEST file (#1655) -- so adding the second one turned it red here
	// rather than where the duplication started. Deliberately NOT shared with the "Unknown" in
	// edge_control_ws.go or server_edge.go: those mean an unknown edge VERSION, a different fact
	// that happens to be spelled the same, and one constant for both would make a rename of
	// either silently change the other.
	unknownUserEmail = "Unknown"
)

// reservationResourceKind names what a reservation row actually is.
func reservationResourceKind(res *db.SubdomainReservation) string {
	if res != nil && res.Subdomain == "" {
		return resourceKindCustomDomain
	}
	return resourceKindSubdomain
}

// RequestTokenPermanence records that a token's holder has asked for it never to expire.
//
// Separate from creation because a holder's needs change: a token made for a fortnight's work
// that turns into a standing integration should not have to be replaced -- and replacing it
// means a new secret in somebody's CI, which is the change most likely to be done badly.
func (s *portalService) RequestTokenPermanence(user *db.User, tokenID, ip string) (*db.PersonalAccessToken, error) {
	if !s.cfg.NeverExpiresTokens().RequiresApproval() {
		// Under `allowed` there is nothing to request -- the holder can create a permanent
		// token outright -- and under `disabled` there is nothing to grant. Refusing both
		// keeps a pending row from existing in a state where no admin action could ever
		// clear it.
		return nil, ErrPermanenceNotAllowed
	}

	pat, err := s.ownedToken(user, tokenID)
	if err != nil {
		return nil, err
	}
	if pat.ExpiresAt == nil {
		// Already permanent. Not an error worth surfacing as a failure, but not a request
		// either: there is nothing for an admin to decide.
		return pat, nil
	}
	if pat.ExpiresAt.Before(time.Now().UTC()) {
		// ALREADY LAPSED. Granting this would not extend a credential, it would revive one --
		// a grant writes expires_at = NULL, so a token that stopped authenticating last month
		// works again (#2280).
		//
		// Refused HERE rather than only in the portal, because the portal is not the boundary:
		// V1's action menu gates on `!isRevoked` and knows nothing about expiry, so it offered
		// this on a lapsed token while V2 did not, and the API accepted it from either. A
		// condition that only one arm applies is not a rule.
		return nil, ErrTokenAlreadyExpired
	}
	if pat.PermanenceState == db.PATPermanencePending {
		// Idempotent. A holder who clicks twice has one request, not two queue entries.
		return pat, nil
	}

	if err := s.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); err != nil {
		return nil, ErrInternalError
	}
	pat.PermanenceState = db.PATPermanencePending

	s.auditPermanence(user.Email, "token.permanence_requested", strconv.FormatInt(pat.ID, 10),
		fmt.Sprintf("Holder asked for token %q (%s) never to expire", pat.Name, pat.TokenPrefix), ip)

	return pat, nil
}

// ResolvePermanenceAfterAdminGrant records that an admin granted permanence by the OTHER route.
//
// "Extend Permanent" on the admin token list clears the expiry and, until #2316, touched nothing
// else. So a token whose holder had asked was made permanent and LEFT IN THE QUEUE, showing an
// admin a request to decide about a token that already never expires -- with its Expires column
// reading "Never" in both arms. Clicking Deny on that row returned 200 and wrote
//
//	Denied; token "ci" (pfx-ci) keeps its expiry
//
// against a token with no expiry, which is an audit entry recording an effect it did not have.
//
// The owner's call is that this resolves as GRANTED rather than leaving the two routes
// independent: the request WAS granted, just through the other door, and the holder's badge
// should say so. That also closes the queue row, so the false denial above becomes unreachable
// -- AdminDecideTokenPermanence refuses anything that is not pending.
//
// DENIED is resolved too, and for the same reason rather than as an extra. A holder refused last
// week whose token an admin then makes permanent would otherwise carry a "declined" badge on a
// credential that never expires, which is the same contradiction pointing the other way.
//
// Best effort by design. The expiry is already written when this runs, and failing to update a
// badge must not turn a successful extension into an error the admin has to retry.
func (s *portalService) ResolvePermanenceAfterAdminGrant(patID int64, actor, ip string) {
	pat, err := s.db.GetPATByID(patID)
	if err != nil {
		return
	}
	if pat.ExpiresAt != nil {
		// Not a permanent grant -- an ordinary extension leaves a pending request pending,
		// because thirty more days is not an answer to "may this never expire".
		return
	}

	switch pat.PermanenceState {
	case db.PATPermanencePending:
		// Conditional, so two admins acting at once cannot both claim it. The queue decision
		// path uses the same transition for the same reason.
		if err := s.db.TransitionPATPermanenceState(pat.ID, db.PATPermanencePending, db.PATPermanenceGranted); err != nil {
			return
		}
	case db.PATPermanenceDenied:
		if err := s.db.SetPATPermanenceState(pat.ID, db.PATPermanenceGranted); err != nil {
			return
		}
	default:
		// No request to resolve, or already granted.
		return
	}

	s.auditPermanence(actor, "token.permanence_granted", strconv.FormatInt(pat.ID, 10),
		fmt.Sprintf("Granted via Extend Permanent; token %q (%s) no longer expires", pat.Name, pat.TokenPrefix), ip)
}

// ownedToken resolves a token id against the caller, refusing one that belongs to somebody else.
//
// ErrNotFound rather than ErrForbidden for another user's token, deliberately: a 403 confirms
// the id exists, which turns an id space into an enumeration oracle. The holder of a token they
// do own can tell the difference from their own list.
func (s *portalService) ownedToken(user *db.User, tokenID string) (*db.PersonalAccessToken, error) {
	id, err := strconv.ParseInt(tokenID, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	pat, err := s.db.GetPATByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}
	if pat.UserID != user.ID {
		return nil, ErrNotFound
	}
	if pat.RevokedAt != nil {
		return nil, ErrNotFound
	}
	return pat, nil
}

// PermanenceRequestView is one row of the admin token permanence queue.
//
// The embedded pointer flattens in JSON, so every field the holder's own token list already
// reads is unchanged and UserEmail is additive -- the same shape as ExtensionRequestView above,
// deliberately, because these are two halves of one queue idea.
//
// UserEmail exists because PersonalAccessToken carries a UserID and nothing else. An admin
// deciding "should this token never expire" was being shown an opaque id instead of who was
// asking. The reservation queue this mirrors has the same hole and renders it differently in
// each arm -- blank in V2, "User <id>" in V1 (#2314) -- which is exactly what deriving a missing
// field separately in two places looks like. The server says it once.
type PermanenceRequestView struct {
	*db.PersonalAccessToken
	// UserEmail is the holder's address, or "Unknown" for an id with no user. Never empty:
	// a blank cell reads as a column that has not loaded yet, and this one never would.
	UserEmail string `json:"user_email"`
}

// AdminListTokenPermanenceRequests returns every token waiting on a decision.
func (s *portalService) AdminListTokenPermanenceRequests() ([]*PermanenceRequestView, error) {
	list, err := s.db.ListPATPermanenceRequests()
	if err != nil {
		return nil, ErrInternalError
	}

	// One ListUsers rather than a lookup per row: the queue is small but the pattern is
	// handleAdminListSubdomains's, and a per-row GetUser turns a five-row queue into five
	// queries against a database with SetMaxOpenConns(1).
	users, err := s.db.ListUsers()
	if err != nil {
		return nil, ErrInternalError
	}
	emails := make(map[string]string, len(users))
	for _, u := range users {
		emails[u.ID] = u.Email
	}

	views := make([]*PermanenceRequestView, 0, len(list))
	for _, pat := range list {
		email := emails[pat.UserID]
		if email == "" {
			// A user deleted while their request sat in the queue. Named rather than blank,
			// so the admin can tell "nobody by that id" from "this cell is still loading".
			email = unknownUserEmail
		}
		views = append(views, &PermanenceRequestView{PersonalAccessToken: pat, UserEmail: email})
	}
	return views, nil
}

// AdminDecideTokenPermanence grants or denies one request.
//
// One method for both outcomes rather than an approve and a deny that drift apart: the checks
// are identical, only the state written and the expiry touched differ, and the pair that gets
// written separately is the pair where one of them forgets the policy check.
func (s *portalService) AdminDecideTokenPermanence(actor, idStr string, grant bool, ip string) (*db.PersonalAccessToken, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	// A grant is subject to the policy exactly as every other route to permanence is. Checked
	// even though only `approval` can produce a pending request: an operator can tighten the
	// policy to `disabled` while requests are already in the queue, and the queue must not
	// then be a way to grant what the gateway no longer allows.
	if grant && !permanenceGrantAllowed(s.cfg.NeverExpiresTokens()) {
		return nil, ErrPermanenceNotAllowed
	}

	pat, err := s.db.GetPATByID(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}
	if pat.PermanenceState != db.PATPermanencePending {
		// Deciding a request that is not pending would let a second admin silently reverse
		// the first, with the queue showing nothing either time. Read first so the caller
		// gets this answer rather than a bare conflict from the write below.
		return nil, ErrConflict
	}
	if pat.RevokedAt != nil {
		// The queue excludes revoked tokens, but an admin can be looking at a page that was
		// rendered a second before the revoke. Granting here would put expires_at = NULL and
		// `granted` on a dead credential -- harmless, because revocation still wins at auth,
		// and still a row that contradicts what the queue promised (#2267 review).
		return nil, ErrConflict
	}
	if grant && pat.ExpiresAt != nil && pat.ExpiresAt.Before(time.Now().UTC()) {
		// Requested while live, LAPSED while queued. RequestTokenPermanence refuses an already
		// expired token, so the only way to reach this is time passing with the row in the
		// queue -- and an admin working a backlog sees a date in the Expires column with
		// nothing to say it is in the past. A grant here revives the credential (#2280).
		//
		// Only a GRANT. Denying stays available, because an admin must be able to clear the
		// row; a request that can be neither granted nor denied sits there for ever.
		return nil, ErrTokenAlreadyExpired
	}

	state := db.PATPermanenceDenied
	action := "token.permanence_denied"
	detail := fmt.Sprintf("Denied; token %q (%s) keeps its expiry", pat.Name, pat.TokenPrefix)
	if grant {
		state = db.PATPermanenceGranted
		action = "token.permanence_granted"
		detail = fmt.Sprintf("Granted; token %q (%s) no longer expires", pat.Name, pat.TokenPrefix)
	}

	// CLAIM THE REQUEST FIRST, conditionally, and only then touch the expiry.
	//
	// The order matters and the condition matters. Read-check-write let two admins both pass
	// the check above -- SetMaxOpenConns(1) serialises statements, not sequences -- and land a
	// grant's UpdatePATExpiry alongside a denial's state write, leaving a row recorded `denied`
	// with no expiry. Claiming the transition first means exactly one of them proceeds.
	if err := s.db.TransitionPATPermanenceState(pat.ID, db.PATPermanencePending, state); err != nil {
		if errors.Is(err, db.ErrStateChanged) {
			return nil, ErrConflict
		}
		return nil, ErrInternalError
	}

	if grant {
		if err := s.db.UpdatePATExpiry(pat.ID, nil); err != nil {
			// The claim succeeded and the grant did not, so the row would read `granted`
			// over a token that still expires. Put it back, and report the failure rather
			// than leaving a decision recorded that did not happen.
			if rerr := s.db.SetPATPermanenceState(pat.ID, db.PATPermanencePending); rerr != nil {
				auditWriteFailed("token.permanence_rollback", strconv.FormatInt(pat.ID, 10), rerr)
			}
			return nil, ErrInternalError
		}
		pat.ExpiresAt = nil
	}
	pat.PermanenceState = state

	s.auditPermanence(actor, action, strconv.FormatInt(pat.ID, 10), detail, ip)

	return pat, nil
}

// auditPermanence writes one audit entry, logging rather than swallowing a failure.
//
// Not `_ =` with a nolint, which is what the surrounding methods do and what the ratchet is at
// its ceiling for: a decision about a credential's lifetime with no audit entry is the one gap an
// audit log must not have, and a caller cannot be told the decision failed when it did not.
func (s *portalService) auditPermanence(actor, action, targetID, details, ip string) {
	if aerr := s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     action,
		TargetType: auditTargetPAT,
		TargetID:   targetID,
		Details:    details,
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	}); aerr != nil {
		auditWriteFailed(action, targetID, aerr)
	}
}

// auditWriteFailed reports an audit entry that could not be written.
//
// One function so every such failure reads the same way in a log an operator greps, and so the
// next one added cannot quietly become a bare `_ =`.
func auditWriteFailed(action, targetID string, err error) {
	slog.Warn(fmt.Sprintf("[Portal] Recorded %s for %s but could not write the audit entry: %v", action, targetID, err))
}
