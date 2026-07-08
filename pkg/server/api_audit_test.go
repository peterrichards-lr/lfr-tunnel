package server

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

func TestHandleAdminAuditLog(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	originalDB := srv.db
	defer func() { srv.db = originalDB }()

	// Insert some dummy audit entries directly into the DB.
	err := srv.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    "admin1",
		Action:     "login",
		TargetType: "user",
		TargetID:   "admin1",
		Details:    "logged in",
		IPAddress:  "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Failed to add audit entry: %v", err)
	}
	err = srv.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    "admin2",
		Action:     "kick",
		TargetType: "lease",
		TargetID:   "lease123",
		Details:    "kicked",
		IPAddress:  "192.168.1.1",
	})
	if err != nil {
		t.Fatalf("Failed to add audit entry: %v", err)
	}

	tests := []struct {
		name           string
		setup          func()
		teardown       func()
		queryParams    string
		expectedStatus int
		verifyBody     func(t *testing.T, body string)
	}{
		{
			name:           "Success_ListAll",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			verifyBody: func(t *testing.T, body string) {
				var entries []db.AuditEntry
				if err := json.Unmarshal([]byte(body), &entries); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if len(entries) < 2 {
					t.Errorf("Expected at least 2 entries, got %d", len(entries))
				}
			},
		},
		{
			name:           "Success_FilterByActor",
			queryParams:    "?actor=admin2",
			expectedStatus: http.StatusOK,
			verifyBody: func(t *testing.T, body string) {
				var entries []db.AuditEntry
				if err := json.Unmarshal([]byte(body), &entries); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if len(entries) == 0 {
					t.Fatalf("Expected entries for admin2, got 0")
				}
				for _, e := range entries {
					if e.ActorID != "admin2" {
						t.Errorf("Expected actor admin2, got %s", e.ActorID)
					}
				}
			},
		},
		{
			name:           "Success_LimitAndOffset",
			queryParams:    "?limit=1&offset=0",
			expectedStatus: http.StatusOK,
			verifyBody: func(t *testing.T, body string) {
				var entries []db.AuditEntry
				if err := json.Unmarshal([]byte(body), &entries); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if len(entries) != 1 {
					t.Errorf("Expected exactly 1 entry due to limit=1, got %d", len(entries))
				}
			},
		},
		{
			name: "DB_NotConfigured",
			setup: func() {
				srv.db = nil
			},
			teardown: func() {
				srv.db = originalDB
			},
			queryParams:    "",
			expectedStatus: http.StatusNotImplemented,
			verifyBody: func(t *testing.T, body string) {
				if !strings.Contains(body, "Database not configured") {
					t.Errorf("Expected 'Database not configured' error, got %s", body)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			if tt.teardown != nil {
				defer tt.teardown()
			}

			req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/audit"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			srv.handleAdminAuditLog(w, req, "admin")

			if status := w.Result().StatusCode; status != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, status)
			}
			if tt.verifyBody != nil {
				tt.verifyBody(t, w.Body.String())
			}
		})
	}
}

func TestHandleAdminAuditExport(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	originalDB := srv.db
	defer func() { srv.db = originalDB }()

	// Insert some dummy audit entries directly into the DB.
	err := srv.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    "export_admin",
		Action:     "export_test",
		TargetType: "export_target",
		TargetID:   "t1",
		Details:    "exporting",
		IPAddress:  "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("Failed to add audit entry: %v", err)
	}

	t.Run("Success_CSVExport", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/audit/export", nil)
		w := httptest.NewRecorder()

		srv.handleAdminAuditExport(w, req, "admin")

		if status := w.Result().StatusCode; status != http.StatusOK {
			t.Errorf("expected status %v, got %v", http.StatusOK, status)
		}

		if cType := w.Header().Get("Content-Type"); cType != "text/csv" {
			t.Errorf("expected Content-Type text/csv, got %s", cType)
		}

		if cDisp := w.Header().Get("Content-Disposition"); !strings.Contains(cDisp, "attachment;filename=audit_log.csv") {
			t.Errorf("expected Content-Disposition attachment, got %s", cDisp)
		}

		reader := csv.NewReader(w.Body)
		records, err := reader.ReadAll()
		if err != nil {
			t.Fatalf("Failed to parse CSV response: %v", err)
		}

		if len(records) < 2 {
			t.Fatalf("Expected at least header and one row, got %d rows", len(records))
		}

		// Verify headers
		expectedHeaders := []string{"ID", "Actor", "Action", "TargetType", "TargetID", "Details", "IP", "Timestamp"}
		for i, h := range expectedHeaders {
			if records[0][i] != h {
				t.Errorf("Expected header %s at pos %d, got %s", h, i, records[0][i])
			}
		}

		// Verify data row exists
		found := false
		for _, row := range records[1:] {
			if row[1] == "export_admin" && row[2] == "export_test" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Failed to find inserted test entry in CSV output")
		}
	})

	t.Run("DB_NotConfigured", func(t *testing.T) {
		srv.db = nil
		defer func() { srv.db = originalDB }()

		req := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/audit/export", nil)
		w := httptest.NewRecorder()

		srv.handleAdminAuditExport(w, req, "admin")

		if status := w.Result().StatusCode; status != http.StatusNotImplemented {
			t.Errorf("expected status %v, got %v", http.StatusNotImplemented, status)
		}
	})
}
