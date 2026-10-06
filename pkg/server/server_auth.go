package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"lfr-tunnel/pkg/db"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Credential validation lives in one place per credential type (#1308).
//
// It used to be copied: isValidToken and requireAdmin's PAT branch each hashed the token, looked
// it up, checked revocation, checked expiry, checked the user was approved and fired the async
// last-used update. Both were correct, which is exactly why this is worth doing now -- #1304 is
// the evidence of what happens next. There, two paths resolved a portal session and only one
// checked expiry, so an expired session still resolved a user on every /api path routed through
// the other. Nobody wrote that deliberately; the two simply drifted, because there were two.
//
// The role requirement deliberately stays with requireAdmin. "Is this credential currently good?"
// and "may this user do admin things?" are different questions, and folding the second in here
// would mean every caller had to opt out of it.

// tokenVerdict is what checking a personal access token concluded (#2347).
type tokenVerdict int

const (
	// tokenValid: the token resolves to an approved user and may be used.
	tokenValid tokenVerdict = iota
	// tokenRefused: the token is missing, unknown, revoked or expired, or its user is not
	// approved. Deliberately one verdict for all of them -- the gateway's 401 must not say which
	// (#2342).
	tokenRefused
	// tokenStoreUnavailable: the lookup itself failed, so nothing is known about the token. Kept
	// apart from tokenRefused because the registration handlers answer the two differently, and
	// the client treats a refusal as final.
	tokenStoreUnavailable
)

// checkPAT resolves a personal access token to its user, or says why it cannot. A nil error from
// the lookup is not enough: the token must also be unrevoked, unexpired, and belong to an approved
// user.
//
// Only a failure of the lookup QUERY is reported as tokenStoreUnavailable. At that point nothing is
// known about the token, so saying "the store is unavailable" says nothing about it -- the same
// answer would come back for a token that does not exist. Anything that happens only because the
// row was found -- the row itself unreadable, or reading its user failing -- is reported as
// tokenRefused, because answering it differently would be an answer only ever given for a token
// that exists.
func (s *Server) checkPAT(token string) (*db.User, *db.PersonalAccessToken, tokenVerdict) {
	if token == "" || s.db == nil {
		return nil, nil, tokenRefused
	}

	hashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hashBytes[:])

	pat, err := s.db.GetPATByHash(tokenHash)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, tokenRefused
	}
	if errors.Is(err, db.ErrRowUnreadable) {
		// FOUND, but damaged. A refusal, not "unavailable": answering this 503 would happen only
		// for a token that exists. Logged without the token, so the damage is visible.
		slog.Error(fmt.Sprintf("[Server] A token row exists but could not be read; refusing it: %v", err))
		return nil, nil, tokenRefused
	}
	if err != nil {
		slog.Error(fmt.Sprintf("[Server] Token lookup failed, so the token could not be checked: %v", err))
		return nil, nil, tokenStoreUnavailable
	}
	if pat.RevokedAt != nil {
		return nil, nil, tokenRefused
	}
	if pat.ExpiresAt != nil && !pat.ExpiresAt.After(time.Now().UTC()) {
		return nil, nil, tokenRefused
	}

	user, err := s.db.GetUser(pat.UserID)
	if err != nil || user == nil || user.Status != db.UserStatusApproved {
		return nil, nil, tokenRefused
	}
	return user, pat, tokenValid
}

// validatePAT is checkPAT for callers that only need "usable or not". Every caller of this treats
// an unavailable store as a refusal, which is what they all did before #2347 -- only the
// registration handlers, where a refusal is final, needed the difference.
func (s *Server) validatePAT(token string) (*db.User, *db.PersonalAccessToken, bool) {
	user, pat, verdict := s.checkPAT(token)
	return user, pat, verdict == tokenValid
}

// touchPAT records the token as used, without making the caller wait for a write it does not
// depend on. Failure is logged rather than surfaced: a last-used timestamp that did not update is
// not a reason to reject a credential that is otherwise good.
func (s *Server) touchPAT(patID int64) {
	dbConn := s.db
	s.goTracked(func() {
		if err := dbConn.UpdatePATUsed(patID); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to update PAT last used time: %v", err))
		}
	})
}

// isValidToken checks if a token is valid, checking personal access tokens (PATs)
// in the database.
func (s *Server) isValidToken(token string) (*db.User, bool) {
	user, _, verdict := s.authenticateToken(token)
	return user, verdict == tokenValid
}

// authenticateToken is isValidToken for the registration handlers, which need the token itself --
// to tell the client when it expires (#2344) -- and the verdict, to answer an unavailable token
// store with 503 rather than the final 401 (#2347). The same check and the same last-used update;
// only the return differs, so the two cannot drift apart.
func (s *Server) authenticateToken(token string) (*db.User, *db.PersonalAccessToken, tokenVerdict) {
	user, pat, verdict := s.checkPAT(token)
	if verdict != tokenValid {
		return nil, nil, verdict
	}
	s.touchPAT(pat.ID)
	return user, pat, tokenValid
}

// tokenStoreUnavailableRetrySeconds is the Retry-After on a 503 for an unavailable token store.
//
// Set because it is correct HTTP for a 503, not because anything acts on it today: an edge rebuilds
// the response without copying headers, and the client does not read it. What a lookup can
// realistically fail with is an I/O error, corruption, SQLITE_BUSY from another process holding the
// file, or "database is closed" for a request that outlives shutdown -- not in-process contention,
// which SetMaxOpenConns(1) turns into a wait rather than an error.
const tokenStoreUnavailableRetrySeconds = 5

// tokenStoreUnavailableMessage names no token state, on purpose: the same answer comes back
// whatever token was sent.
const tokenStoreUnavailableMessage = "the gateway could not check your token just now; retry in a moment"

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var actorEmail string
	var actorRole string
	var authenticated bool

	// 1. Check HTTP-only cookie
	cookie, err := r.Cookie("lfr_session")
	if err == nil && cookie.Value != "" {
		if sessionData, ok := s.sessionStore().loadPortalSession(cookie.Value); ok {
			{
				actorEmail = sessionData.Email

				// The role comes from the database, and a lookup that succeeds is believed
				// (#1760). This previously defaulted to "admin" and then folded `actorRole ==
				// "user"` in with `actorRole == ""` before promoting -- so "the database says
				// this person is an ordinary user" was treated identically to "we could not
				// find out", and every authenticated portal session became admin on all ~47
				// /api/admin/* routes, including database backup download and audit export.
				//
				// The PAT branch below has always required admin or owner explicitly. This is
				// the same rule, applied on the path that a browser actually uses.
				actorRole = ""
				if s.db != nil {
					if u, err := s.db.GetUserByEmail(actorEmail); err == nil && u != nil {
						actorRole = u.Role
					}
				}

				// The configured owner is the owner even if the row says otherwise: the
				// account can be created by a login path that stamps "user" (SSO does), and
				// the deployment's own configuration outranks that.
				if s.cfg.Owner.UserID != "" && strings.EqualFold(actorEmail, s.cfg.Owner.UserID) {
					actorRole = "owner"
				}

				// Anything that is not explicitly admin or owner is refused, INCLUDING an
				// unreadable role. Failing closed matters more here than staying reachable:
				// a gateway whose database is unavailable should stop serving admin routes,
				// not open them.
				if actorRole == roleAdmin || actorRole == roleOwner {
					authenticated = true
				}

				// The sliding expiry used to live here, and only here (#1655) -- so an
				// ordinary portal user's session was never extended, and even an admin's
				// cookie was never re-issued, which is what made the whole mechanism
				// ineffective. ServeHTTP now slides every live session in one place and
				// re-issues the cookie with it.
			}
		}
	}

	// 2. Fallback to API Token (PAT)
	if !authenticated {
		token := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = token[7:]
		} else {
			token = r.URL.Query().Get("token")
		}

		if token != "" && strings.HasPrefix(token, "lfr_pat_") {
			// Same validation as every other PAT caller, with the ROLE requirement kept here --
			// that is the one legitimate difference between this path and isValidToken (#1308).
			if user, pat, ok := s.validatePAT(token); ok && (user.Role == "admin" || user.Role == "owner") {
				s.touchPAT(pat.ID)
				actorEmail = user.Email
				actorRole = user.Role
				authenticated = true
			}
		}
	}

	if !authenticated {
		http.Error(w, `{"error":"Unauthorized: admin access required"}`, http.StatusUnauthorized)
		return "", "", false
	}
	return actorEmail, actorRole, true
}

func (s *Server) isOwner(actor string) bool {
	if s.cfg.Owner.UserID != "" && strings.EqualFold(actor, s.cfg.Owner.UserID) {
		return true
	}
	if s.db != nil {
		if u, err := s.db.GetUserByEmail(actor); err == nil && u != nil {
			return u.Role == "owner"
		}
	}
	return false
}
