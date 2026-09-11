package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// Accepting, keeping and erasing collected bundles (#1894, part 3b of #1763).
//
// #1696 created no store deliberately, so that the first time this gateway kept user data on the
// owner's behalf would be a decision. These are the consequences of that decision, in one place:
// the upload endpoint, the retention sweep, and the two erasure paths.

const (
	// maxBundleUploadBytes bounds one upload. #1885 caps the client at 4 MiB across all three
	// logs; this is the same budget with headroom for the JSON envelope, and it is enforced
	// here as well because a bound the sender promises is not a bound.
	maxBundleUploadBytes = 6 << 20

	// diagnosticsAuditUploaded records that logs actually arrived.
	diagnosticsAuditUploaded = "diagnostics.collect_uploaded"
	// diagnosticsAuditRead records an admin reading a stored bundle. #1763 asks for the
	// delivery AND any later read to be audited: collection and access are different events,
	// and only this one shows somebody looked.
	diagnosticsAuditRead = "diagnostics.bundle_read"
	// diagnosticsAuditPurged records bundles destroyed because consent was withdrawn.
	diagnosticsAuditPurged = "diagnostics.bundles_purged"
)

// bundleUpload is what the client POSTs.
type bundleUpload struct {
	RequestID string `json:"request_id"`
	Logs      []struct {
		Kind         string `json:"kind"`
		Content      string `json:"content"`
		Truncated    bool   `json:"truncated"`
		DroppedLines int    `json:"dropped_lines"`
	} `json:"logs"`
}

// handleDiagnosticsUpload accepts a bundle from a client that was asked for one.
//
// Authorised by the COMMAND, not by the caller's say-so. A client may only upload against a
// request id this gateway issued and has not yet seen satisfied, which is what stops the endpoint
// becoming a way to push arbitrary content at the gateway: there is no path here that accepts an
// upload nobody asked for.
//
// Consent is re-checked once more. It was checked when the request was authorised (#1696) and
// again at delivery (#1884); a withdrawal between delivery and upload is a narrow window, but
// the whole design of this feature is that withdrawal takes effect immediately, and "immediately"
// should not have an exception measured in seconds.
func (s *Server) handleDiagnosticsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	sessionToken := strings.TrimSpace(r.Header.Get("X-Session-Token"))
	if sessionToken == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	leases := s.registry.GetSessionLeases(sessionToken)
	userID := ""
	for _, l := range leases {
		if l != nil && l.UserID != "" {
			userID = l.UserID
			break
		}
	}
	if userID == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var payload bundleUpload
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBundleUploadBytes)).Decode(&payload); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	// The command is the authorisation. Claiming it here also makes the upload idempotent: a
	// client that retries after a timeout finds the request already satisfied rather than
	// storing a second copy.
	cmd := s.ackDiagnosticsCommand(userID, strings.TrimSpace(payload.RequestID))
	if cmd == nil {
		http.Error(w, `{"error":"No collection was requested, or it has already been satisfied"}`, http.StatusForbidden)
		return
	}

	target, err := s.db.GetUser(userID)
	if err != nil || target == nil || !diagnosticsCollectionAllowed(target) {
		s.auditDiagnostics(cmd.requestedBy, diagnosticsAuditRefused, "user", userID,
			"Upload refused: consent is no longer in place", r)
		http.Error(w, `{"error":"Diagnostic log sharing is not enabled for this account"}`, http.StatusForbidden)
		return
	}

	stored, total := 0, 0
	for _, l := range payload.Logs {
		kind := strings.TrimSpace(l.Kind)
		if kind == "" || l.Content == "" {
			continue
		}
		b := &db.DiagnosticsBundle{
			ID:          newDiagnosticsCommandID() + "-" + kind,
			UserID:      userID,
			RequestedBy: cmd.requestedBy,
			Kind:        kind,
			Content:     []byte(l.Content),
			Bytes:       len(l.Content),
			Truncated:   l.Truncated,
			Dropped:     l.DroppedLines,
		}
		if err := s.db.StoreDiagnosticsBundle(b); err != nil {
			slog.Error(fmt.Sprintf("[Diagnostics] Could not store a %s bundle for %s: %v", kind, userID, err))
			continue
		}
		stored++
		total += b.Bytes
	}

	if stored == 0 {
		http.Error(w, `{"error":"No usable logs in the upload"}`, http.StatusBadRequest)
		return
	}

	s.auditDiagnostics(cmd.requestedBy, diagnosticsAuditUploaded, "user", userID,
		fmt.Sprintf("Received %d diagnostic log(s), %d bytes, for request %s (kept %d days)",
			stored, total, cmd.ID, db.DiagnosticsRetentionDays), r)

	respondJSON(w, http.StatusOK, map[string]any{
		"status":         "stored",
		"logs":           stored,
		"bytes":          total,
		"retention_days": db.DiagnosticsRetentionDays,
	})
}

// purgeDiagnosticsBundlesOnWithdrawal destroys what was already collected from a user who has
// just withdrawn consent.
//
// The schema's ON DELETE CASCADE does not cover this: it fires when the USER row goes, and a
// withdrawal leaves the user in place. Relying on the cascade alone would mean withdrawal
// silently retained everything, which is the outcome #1763 named as insufficient.
func (s *Server) purgeDiagnosticsBundlesOnWithdrawal(userID, actor string, r *http.Request) {
	if s.db == nil {
		return
	}
	n, err := s.db.DeleteDiagnosticsBundlesForUser(userID)
	if err != nil {
		// Loud, and not fatal to the withdrawal: refusing to record that consent was
		// withdrawn because the erasure failed would be worse than recording both.
		slog.Error(fmt.Sprintf("[Diagnostics] Could not purge bundles for %s after withdrawal: %v", userID, err))
		return
	}
	if n == 0 {
		return
	}
	s.auditDiagnostics(actor, diagnosticsAuditPurged, "user", userID,
		fmt.Sprintf("Consent withdrawn: deleted %d previously collected diagnostic log(s)", n), r)
}

// pruneDiagnosticsBundles enforces the retention period PRIVACY.md states.
//
// On the existing hourly pass rather than a timer of its own. A published retention period that
// nothing enforces is the failure this is here to prevent -- the number in the policy is only
// true because this runs.
func (s *Server) pruneDiagnosticsBundles() {
	if s.db == nil {
		return
	}
	n, err := s.db.PruneDiagnosticsBundles()
	if err != nil {
		slog.Error(fmt.Sprintf("[Diagnostics] Retention sweep failed: %v", err))
		return
	}
	if n > 0 {
		slog.Info(fmt.Sprintf("[Diagnostics] Retention sweep deleted %d bundle(s) older than %d days",
			n, db.DiagnosticsRetentionDays))
	}
}

// handleAdminDiagnosticsBundles lists or downloads stored bundles for an admin.
func (s *Server) handleAdminDiagnosticsBundles(w http.ResponseWriter, r *http.Request) {
	actor, err := s.getCurrentUserRaw(r)
	if err != nil || actor == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if actor.Role != roleAdmin && actor.Role != roleOwner && !s.isOwner(actor.Email) {
		http.Error(w, `{"error":"Forbidden: administrator access required"}`, http.StatusForbidden)
		return
	}
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		bundle, err := s.db.GetDiagnosticsBundle(id)
		if err != nil || bundle == nil {
			http.Error(w, `{"error":"No such bundle"}`, http.StatusNotFound)
			return
		}
		// Audited on READ, synchronously, before a byte is served. Collection and access are
		// different events, and only this one records that somebody actually looked at
		// another person's logs (#1763).
		s.auditDiagnostics(actor.Email, diagnosticsAuditRead, "user", bundle.UserID,
			fmt.Sprintf("Read the %s log collected %s", bundle.Kind,
				bundle.CollectedAt.UTC().Format(time.RFC3339)), r)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", bundle.Kind+"-"+bundle.ID+".log"))
		if _, werr := w.Write(bundle.Content); werr != nil {
			// The audit entry above already records that this bundle was read, which is
			// the part that matters; a broken pipe partway through the download does not
			// unmake that.
			slog.Error(fmt.Sprintf("[Diagnostics] Failed writing bundle %s: %v", bundle.ID, werr))
		}
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	if email == "" {
		http.Error(w, `{"error":"An email address is required"}`, http.StatusBadRequest)
		return
	}
	target, err := s.db.GetUserByEmail(email)
	if err != nil || target == nil {
		http.Error(w, `{"error":"No such user"}`, http.StatusNotFound)
		return
	}
	bundles, err := s.db.ListDiagnosticsBundles(target.ID)
	if err != nil {
		http.Error(w, `{"error":"Failed to list bundles"}`, http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"bundles":        bundles,
		"retention_days": db.DiagnosticsRetentionDays,
	})
}
