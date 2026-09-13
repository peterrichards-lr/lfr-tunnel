package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The settings endpoint used to name three of the six keys by hand, in BOTH directions. The GET
// half meant three alerts had no toggle to render; the POST half meant that even once a portal
// rendered them, three of the six were dropped on save while the response said "Settings
// updated" (#1882). These assert the endpoint now speaks the whole declared vocabulary.

func TestEveryDeclaredAlertIsReturnedByTheSettingsEndpoint(t *testing.T) {
	srv := setupTestServerForAPI(t)

	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), "owner@example.com")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The vocabulary itself, so a portal renders what this gateway declares rather than a
	// copy it maintains. Its absence is what forced both arms to hardcode keys.
	declared, ok := out["alert_settings"].([]interface{})
	if !ok || len(declared) != len(AlertSettings) {
		t.Fatalf("alert_settings: want %d entries, got %v", len(AlertSettings), out["alert_settings"])
	}

	for _, a := range AlertSettings {
		v, present := out[a.Key]
		if !present {
			t.Errorf("%s is declared but the settings endpoint does not return it -- no toggle can be rendered for it", a.Key)
			continue
		}
		// Unset rows must resolve to the declared default, not to a blank the portal
		// would render as off.
		want := "false"
		if a.DefaultOn {
			want = "true"
		}
		if v != want {
			t.Errorf("%s with no stored row: want %q (its declared default), got %v", a.Key, want, v)
		}
	}
}

func TestEveryDeclaredAlertCanActuallyBeSaved(t *testing.T) {
	srv := setupTestServerForAPI(t)

	// Flip every key away from its default. A key the POST silently ignores reads back as
	// its default, which is exactly how three of these were lost.
	payload := map[string]string{}
	for _, a := range AlertSettings {
		if a.DefaultOn {
			payload[a.Key] = "false"
		} else {
			payload[a.Key] = "true"
		}
	}
	body, _ := json.Marshal(payload)

	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", bytes.NewReader(body)), "owner@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST settings: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	for _, a := range AlertSettings {
		stored, err := srv.db.GetAdminSetting(a.Key)
		if err != nil {
			t.Fatalf("read back %s: %v", a.Key, err)
		}
		if stored != payload[a.Key] {
			t.Errorf("%s: posted %q but the row holds %q -- the save was accepted and discarded", a.Key, payload[a.Key], stored)
		}
	}
}

func TestAnUnknownSettingIsRefusedRatherThanIgnored(t *testing.T) {
	srv := setupTestServerForAPI(t)

	// A typo'd key used to be dropped silently under a "Settings updated" response, so the
	// setting looked like one that would not stick.
	body := []byte(`{"alert_notify_registratoin":"false"}`)
	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", bytes.NewReader(body)), "owner@example.com")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAnUndeclaredAlertStillSends(t *testing.T) {
	// alert_notify_test has no toggle and never will -- the admin clicked a button to send
	// it. Defaulting unknown keys OFF would have made this vanish with no trace, which is
	// the failure mode this whole area exists to prevent.
	if !alertSettingEnabled("alert_notify_test", "") {
		t.Error("an undeclared alert key must default ON; defaulting off silently drops the alert")
	}
	// An explicit stored value still wins over the fallback.
	if alertSettingEnabled("alert_notify_test", "false") {
		t.Error("an explicit \"false\" must switch an alert off whatever its default")
	}
}

func TestStoredValuesOverrideDeclaredDefaults(t *testing.T) {
	for _, a := range AlertSettings {
		if !alertSettingEnabled(a.Key, "true") {
			t.Errorf("%s: stored \"true\" must be on", a.Key)
		}
		if alertSettingEnabled(a.Key, "false") {
			t.Errorf("%s: stored \"false\" must be off", a.Key)
		}
		// An unparseable row falls back to the declaration rather than to off, so a
		// corrupted value cannot silently disable an alert.
		if got := alertSettingEnabled(a.Key, "yes"); got != a.DefaultOn {
			t.Errorf("%s: unparseable row should fall back to its default %v, got %v", a.Key, a.DefaultOn, got)
		}
	}
}
