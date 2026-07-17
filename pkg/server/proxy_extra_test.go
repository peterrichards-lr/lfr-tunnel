package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxy_serveUnauthorizedIPPage(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()

	p := &ProxyHandler{}
	p.serveUnauthorizedIPPage(w, req, "1.2.3.4", "domain.com")

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", w.Code)
	}
}

func TestProxy_verifySessionCookie(t *testing.T) {
	p := &ProxyHandler{}

	valid := p.verifySessionCookie("passcode", "subdomain")
	if valid {
		t.Errorf("Expected invalid")
	}
}

func TestEdgeControlWS_sendEdgeWSHeaders(t *testing.T) {
	s := &Server{}
	s.sendEdgeWSHeaders("nodeID", "fullHost", nil)
}

func TestEdgeControlWS_SendEdgeMaintenance(t *testing.T) {
	s := &Server{}
	_ = s.SendEdgeMaintenance("nodeID", "action", 60, "reason") //nolint:errcheck
}
