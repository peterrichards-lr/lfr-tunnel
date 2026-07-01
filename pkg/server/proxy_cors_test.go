package server

import (
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyHandler_CORS(t *testing.T) {
	reg := NewRegistry(nil)
	reg.leases["protected-se.liferay.com"] = &TunnelLease{
		SubdomainPrefix: "protected-se",
		FullHost:        "protected-se.liferay.com",
	}
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"liferay.com"}
	handler := NewProxyHandler(reg, cfg)

	t.Run("Preflight Authorized Domain", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "http://protected-se.liferay.com/home", nil)
		req.Host = "protected-se.liferay.com"
		req.Header.Set("Origin", "https://portal.liferay.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected 204 No Content, got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "https://portal.liferay.com" {
			t.Errorf("expected ACAO to be set to Origin, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("Preflight Unauthorized Domain", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "http://protected-se.liferay.com/home", nil)
		req.Host = "protected-se.liferay.com"
		req.Header.Set("Origin", "https://evil.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Should not be intercepted by CORS preflight, so it will hit the 502 offline logic
		if rec.Code != http.StatusBadGateway {
			t.Errorf("expected 502 Bad Gateway (not intercepted by CORS), got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("expected ACAO to be empty")
		}
	})
}
