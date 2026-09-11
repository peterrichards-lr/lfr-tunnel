package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"lfr-tunnel/pkg/db"
	"strconv"
	"strings"
	"testing"
)

// patchStatus PATCHes a status onto a user and returns the recorder, so each case can assert on
// the code AND on what the database holds afterwards.
func patchStatus(t *testing.T, srv *Server, email, status string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		t.Fatalf("marshalling body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch,
		"/api/admin/users/"+url.PathEscape(email), strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	srv.handleAdminPatchUser(w, req, "admin@example.com", "admin")
	return w
}

// A near-miss spelling is the whole point of #1872.
//
// approveOnSSOSignIn refuses exactly one status -- `user.Status == statusRejected` -- and
// auto-approves everything else. So before this validation, "Rejected" produced a row that read as
// rejected in the portal and in the audit log, and was then silently APPROVED on the user's next
// SSO sign-in. The endpoint took a free-form string and nothing said otherwise, so a capitalised
// value is what a human or a script writes, not a contrived attack.
func TestPatchUserRejectsAStatusOutsideTheVocabulary(t *testing.T) {
	srv := setupTestServerForAPI(t)

	for i, status := range []string{
		"Rejected",  // the realistic one: capitalisation
		"REJECTED",  //
		"rejcted",   // a typo
		"denied",    // a plausible synonym that means nothing here
		"",          // empty
		"approved ", // trailing space
		" approved", // leading space
	} {
		t.Run("status="+strconv.Quote(status), func(t *testing.T) {
			// A distinct row per case: the server is shared across subtests, and reusing one
			// address made every case after the first fail on the UNIQUE constraint rather than
			// on the thing under test.
			email := fmt.Sprintf("near-miss-%d@example.com", i)
			seedPendingRegistration(t, srv, email, fmt.Sprintf("tok-%d", i))
			before := mustGetUser(t, srv, email).Status

			w := patchStatus(t, srv, email, status)

			if w.Code != http.StatusBadRequest {
				t.Errorf("PATCH status=%q returned %d, want 400. approveOnSSOSignIn matches only "+
					"the exact string %q, so any other spelling is a rejection that SSO will not "+
					"honour", status, w.Code, statusRejected)
			}
			if got := mustGetUser(t, srv, email).Status; got != before {
				t.Errorf("database holds %q after a refused PATCH, want it unchanged at %q -- "+
					"refusing the request but writing anyway is the worst of both", got, before)
			}
			if !strings.Contains(w.Body.String(), "Valid values are") {
				t.Errorf("error body %q does not list the accepted values; the caller cannot tell "+
					"what to send instead", w.Body.String())
			}
		})
	}
}

// The discriminating half. A validator that refused everything would satisfy the test above and
// break every legitimate status change, including the un-rejection #1830 relies on.
func TestPatchUserAcceptsEveryStatusInTheVocabulary(t *testing.T) {
	srv := setupTestServerForAPI(t)

	for _, status := range db.UserStatuses {
		t.Run("status="+status, func(t *testing.T) {
			email := status + "-ok@example.com"
			seedPendingRegistration(t, srv, email, "tok-ok-"+status)

			w := patchStatus(t, srv, email, status)

			if w.Code != http.StatusOK {
				t.Fatalf("PATCH status=%q returned %d (%s), want 200 -- this is a valid status and "+
					"refusing it would break un-rejecting a user from the admin portal",
					status, w.Code, strings.TrimSpace(w.Body.String()))
			}
			if got := mustGetUser(t, srv, email).Status; got != status {
				t.Errorf("database holds %q, want %q", got, status)
			}
		})
	}
}
