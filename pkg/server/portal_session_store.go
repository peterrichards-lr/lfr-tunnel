package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"lfr-tunnel/pkg/db"
)

// Portal sessions, held in memory and backed by the database (#1304).
//
// They used to live only in s.portalMap, so every restart -- including a routine deploy --
// signed out every logged-in user. The sliding expiry a few lines into loadPortalSession made
// that worse rather than better: a session's lifetime was carefully extended on each request
// and then discarded wholesale the next time the process stopped.
//
// The map stays, as a cache. A portal request checks a session on essentially every call, and
// turning that into a query per request to fix a restart-only problem would be a poor trade.
// The database is consulted only when the map does not have the answer, which after a restart
// is once per returning user.
//
// Only "admin_session_" entries are persisted. The same map also holds magic-link tokens and
// MFA pre-auth state, both of which live for minutes and are re-requestable by design -- there
// is nothing to preserve across a restart, and persisting them would put a second class of
// credential in the database for no benefit.

const portalSessionKeyPrefix = "admin_session_"

// portalSessionStore owns the cache-plus-database pairing. A type rather than methods on
// Server because sessions are created from three places -- magic link, SSO and the MFA
// second factor -- and the last of those lives on portalService. Both hold the same store, so
// there is exactly one implementation of "what is a session and where does it live".
type portalSessionStore struct {
	portalMap *sync.Map
	db        *db.DB
}

func newPortalSessionStore(portalMap *sync.Map, database *db.DB) *portalSessionStore {
	return &portalSessionStore{portalMap: portalMap, db: database}
}

// sessionStore returns this server's session store, building it on first use.
//
// Lazily rather than only in NewServer because a Server is constructed directly in a good
// number of tests, and a store that exists only on the fully-built path is a nil dereference
// waiting for whichever caller was not built that way. The map is a field on Server, so this
// is safe to take the address of at any point.
func (s *Server) sessionStore() *portalSessionStore {
	s.sessionsOnce.Do(func() {
		s.sessions = newPortalSessionStore(&s.portalMap, s.db)
	})
	return s.sessions
}

// hashSessionToken is what goes in the database. Never the cookie value itself: a row is then
// useless to anyone who can read the database, the same reasoning personal_access_tokens
// already follows.
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// loadPortalSession returns the session for a cookie value, from the cache if it is there and
// from the database otherwise, repopulating the cache on the way through. Expired sessions are
// reported as absent wherever they are found.
func (s *portalSessionStore) loadPortalSession(token string) (PortalSessionData, bool) {
	if token == "" {
		return PortalSessionData{}, false
	}

	if val, ok := s.portalMap.Load(portalSessionKeyPrefix + token); ok {
		data, ok := val.(PortalSessionData)
		if ok && time.Now().Before(data.ExpiresAt) {
			return data, true
		}
		s.portalMap.Delete(portalSessionKeyPrefix + token)
		if ok {
			// Expired in the cache means expired everywhere; drop the durable copy too so it
			// cannot be resurrected by the next lookup.
			s.deletePortalSessionRow(token)
		}
		return PortalSessionData{}, false
	}

	if s.db == nil {
		return PortalSessionData{}, false
	}

	row, err := s.db.GetPortalSession(hashSessionToken(token))
	if err != nil {
		slog.Info(fmt.Sprintf("[Portal] Failed to read a persisted session: %v", err))
		return PortalSessionData{}, false
	}
	if row == nil {
		return PortalSessionData{}, false
	}

	data := PortalSessionData{
		Email:                 row.Email,
		ExpiresAt:             row.ExpiresAt,
		ClientIP:              row.ClientIP,
		PreviousLoginAt:       row.PreviousLoginAt,
		KilledPreviousSession: row.KilledPreviousSession,
		ViewAsRole:            row.ViewAsRole,
		SameSite:              row.SameSite,
		CreatedAt:             row.CreatedAt,
	}
	s.portalMap.Store(portalSessionKeyPrefix+token, data)
	return data, true
}

// storePortalSession records a session in both places. Called on login and on every sliding
// expiry refresh, so the write has to be an upsert rather than an insert.
func (s *portalSessionStore) storePortalSession(token string, data PortalSessionData) {
	if token == "" {
		return
	}
	s.portalMap.Store(portalSessionKeyPrefix+token, data)

	if s.db == nil {
		return
	}
	err := s.db.UpsertPortalSession(&db.PortalSession{
		TokenHash:             hashSessionToken(token),
		Email:                 data.Email,
		ClientIP:              data.ClientIP,
		ViewAsRole:            data.ViewAsRole,
		PreviousLoginAt:       data.PreviousLoginAt,
		KilledPreviousSession: data.KilledPreviousSession,
		ExpiresAt:             data.ExpiresAt,
		SameSite:              data.SameSite,
		CreatedAt:             data.CreatedAt,
	})
	if err != nil {
		// Deliberately not fatal. A session that is only in memory behaves exactly as every
		// session did before this existed -- the user stays logged in now and is signed out
		// by the next restart. Refusing the login instead would turn a durability problem
		// into an availability one.
		slog.Info(fmt.Sprintf("[Portal] Failed to persist a session; it will not survive a restart: %v", err))
	}
}

// deletePortalSession removes a session from both places, for logout.
func (s *portalSessionStore) deletePortalSession(token string) {
	if token == "" {
		return
	}
	s.portalMap.Delete(portalSessionKeyPrefix + token)
	s.deletePortalSessionRow(token)
}

func (s *portalSessionStore) deletePortalSessionRow(token string) {
	if s.db == nil {
		return
	}
	if err := s.db.DeletePortalSession(hashSessionToken(token)); err != nil {
		slog.Info(fmt.Sprintf("[Portal] Failed to delete a persisted session: %v", err))
	}
}

// killPortalSessionsFor invalidates every existing session for an account, which is what the
// strict-concurrency takeover does when someone logs in elsewhere. It has to reach the
// database as well: without that, a restart would resurrect the session that was deliberately
// killed, and the two devices would both be logged in again.
//
// Returns whether anything was actually killed, which the caller reports to the new session so
// the portal can tell the user their other session was ended.
func (s *portalSessionStore) killPortalSessionsFor(email string) bool {
	killed := false
	s.portalMap.Range(func(key, value interface{}) bool {
		k, ok := key.(string)
		if !ok || len(k) < len(portalSessionKeyPrefix) || k[:len(portalSessionKeyPrefix)] != portalSessionKeyPrefix {
			return true
		}
		data, ok := value.(PortalSessionData)
		if ok && data.Email == email {
			s.portalMap.Delete(k)
			killed = true
		}
		return true
	})

	if s.db != nil {
		removed, err := s.db.DeletePortalSessionsForEmail(email)
		if err != nil {
			slog.Info(fmt.Sprintf("[Portal] Failed to clear persisted sessions for %s: %v", email, err))
		} else if removed > 0 {
			// The cache is not the whole picture after a restart: the previous session can
			// exist only in the database, and the user should still be told it was ended.
			killed = true
		}
	}
	return killed
}
