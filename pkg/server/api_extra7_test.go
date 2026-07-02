package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestServer_AdminExtendTokenAndEdge(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	testCases := []struct {
		name   string
		method string
		url    string
		body   interface{}
	}{
		{
			"handleAdminExtendToken",
			http.MethodPost,
			"http://example.com/api/admin/users/admin@example.com/tokens/1/extend",
			map[string]interface{}{"days": 30},
		},
		{
			"handleEdgeAuditLog",
			http.MethodGet,
			"http://example.com/api/admin/edge/audit?node_id=edge-1",
			nil,
		},
		{
			"handleEdgeKick",
			http.MethodPost,
			"http://example.com/api/admin/edge/kick",
			map[string]interface{}{"node_id": "edge-1", "subdomain": "test-sub"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != nil {
				bodyBytes, _ := json.Marshal(tc.body)
				req, _ = http.NewRequest(tc.method, tc.url, bytes.NewBuffer(bodyBytes))
			} else {
				req, _ = http.NewRequest(tc.method, tc.url, nil)
			}
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
			w := httptest.NewRecorder()

			switch tc.name {
			case "handleAdminExtendToken":
				srv.handleAdminExtendToken(w, req, admin.Email)
			case "handleEdgeAuditLog":
				srv.handleEdgeAuditLog(w, req)
			case "handleEdgeKick":
				srv.handleEdgeKick(w, req)
			}

			if w.Code == http.StatusInternalServerError {
				t.Errorf("%s: expected not 500, got %d", tc.name, w.Code)
			}
		})
	}
}
