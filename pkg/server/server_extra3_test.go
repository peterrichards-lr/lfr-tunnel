package server

import (
	"bytes"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/nginx"
	"net/http/httptest"
	"testing"
)

func TestHandleAdminMaintenance_Failures(t *testing.T) {
	cfg := &config.ServerConfig{}
	s := &Server{
		cfg:          cfg,
		nginxManager: nginx.NewMaintenanceManager(cfg.MaintenanceTriggerPath),
	}
	// Missing id
	req := httptest.NewRequest("POST", "/api/admin/maintenance", nil)
	w := httptest.NewRecorder()
	s.handleAdminMaintenance(w, req, "")

	// Valid but empty body
	req = httptest.NewRequest("POST", "/api/admin/maintenance", bytes.NewBufferString(`{}`))
	w = httptest.NewRecorder()
	s.handleAdminMaintenance(w, req, "")
}

func TestHandleAdminGetUser_Failures(t *testing.T) {
	s := &Server{
		cfg: &config.ServerConfig{},
	}
	req := httptest.NewRequest("GET", "/api/admin/users/1", nil)
	w := httptest.NewRecorder()
	s.handleAdminGetUser(w, req, "1")
}

func TestHandleAdminOverrideLimit_Failures(t *testing.T) {
	s := &Server{
		cfg: &config.ServerConfig{},
	}
	req := httptest.NewRequest("PATCH", "/api/admin/users/1/limit", nil)
	w := httptest.NewRecorder()
	s.handleAdminOverrideLimit(w, req, "1")
}

func TestHandleAdminOverrideTunnelsLimit_Failures(t *testing.T) {
	s := &Server{
		cfg: &config.ServerConfig{},
	}
	req := httptest.NewRequest("PATCH", "/api/admin/users/1/tunnels-limit", nil)
	w := httptest.NewRecorder()
	s.handleAdminOverrideTunnelsLimit(w, req, "1")
}

func TestHandleAdminListBackups_Failures(t *testing.T) {
	s := &Server{
		cfg: &config.ServerConfig{},
	}
	req := httptest.NewRequest("GET", "/api/admin/backups", nil)
	w := httptest.NewRecorder()
	s.handleAdminListBackups(w, req, "")
}
