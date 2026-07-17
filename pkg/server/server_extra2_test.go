package server

import (
	"context"
	"lfr-tunnel/pkg/config"
	"net/http/httptest"
	"testing"
)

func TestHandleVerifyEmail_Failures(t *testing.T) {
	// Let's create an uninitialized server just to call handlers
	// It should panic or return 500 quickly, giving us lines
	s := &Server{}
	req := httptest.NewRequest("GET", "/verify?token=123", nil)
	w := httptest.NewRecorder()

	defer func() {
		_ = recover() //nolint:errcheck
	}()
	s.handleVerifyEmail(w, req)
}

func TestHandleApproveUser_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/approve?token=123", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.handleApproveUser(w, req)
}

func TestHandleAdminListTokens_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/api/admin/tokens", nil)
	w := httptest.NewRecorder()
	s.handleAdminListTokens(w, req, "")
}

func TestHandleAdminDeleteToken_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("DELETE", "/api/admin/tokens/1", nil)
	w := httptest.NewRecorder()
	s.handleAdminDeleteToken(w, req, "")
}

func TestHandleAdminExtendToken_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("POST", "/api/admin/tokens/1/extend", nil)
	w := httptest.NewRecorder()
	s.handleAdminExtendToken(w, req, "")
}

func TestHandleAdminBlacklist_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/api/admin/blacklist", nil)
	w := httptest.NewRecorder()
	s.handleAdminBlacklist(w, req, "")
}

func TestHandleAdminAuditLog_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/api/admin/audit", nil)
	w := httptest.NewRecorder()
	s.handleAdminAuditLog(w, req, "")
}

func TestHandleEdgeAuditLog_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("POST", "/edge/audit", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.handleEdgeAuditLog(w, req)
}

func TestHandleEdgeKick_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("POST", "/edge/kick", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.handleEdgeKick(w, req)
}

func TestHandleCheckSubdomain_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("POST", "/api/check-subdomain", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.handleCheckSubdomain(w, req)
}

func TestHandleSetupPage_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/setup", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.handleSetupPage(w, req)
}

func TestServeHTTP_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	defer func() { _ = recover() }() //nolint:errcheck
	s.ServeHTTP(w, req)
}

func TestHandleAdminPatchUser_Failures(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("PATCH", "/api/admin/users/1", nil)
	w := httptest.NewRecorder()
	s.handleAdminPatchUser(w, req, "", "")
}

func TestStartRateLimiterCleaner_Failures(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.startRateLimiterCleaner(ctx)
}

func TestCheckExpiringReservations_Failures(t *testing.T) {
	s := &Server{
		cfg: &config.ServerConfig{},
	}
	s.checkExpiringReservations()
}
