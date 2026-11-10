package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/webhook"
)

// Admin alerts take exactly one route, chosen by admin_alerts.method or the owner's override
// (#2435). Before this, email and the webhook ran side by side, and an operator whose email
// already forwarded into Slack got every alert twice.

const testWebhookURL = "http://127.0.0.1:1/slack" // never reached: the queue is read directly

// alertRouteServer is a server with both routes configured, so a test can see which one an
// alert actually took. The webhook queue is the database's, which is how production queues it.
func alertRouteServer(t *testing.T) (*Server, *mockMailSender) {
	t.Helper()
	srv := setupTestServerForAPI(t)
	srv.cfg.AdminNotificationEmail = "alerts@example.com"
	srv.cfg.Owner.UserID = "owner@example.com"
	srv.cfg.Webhooks.SlackURL = testWebhookURL
	m := &mockMailSender{}
	srv.notifications = NewNotificationService(m, srv.db, srv.cfg)
	srv.webhooks = webhook.NewWebhookService(srv.cfg.Webhooks, srv.db)
	return srv, m
}

func queuedWebhookTitles(t *testing.T, srv *Server) []string {
	t.Helper()
	msgs, err := srv.db.DequeueWebhookMessages(100)
	if err != nil {
		t.Fatalf("read webhook queue: %v", err)
	}
	var titles []string
	for _, m := range msgs {
		titles = append(titles, m.Title)
	}
	return titles
}

func TestAdminAlertTakesExactlyOneRoute(t *testing.T) {
	srv, mail := alertRouteServer(t)

	// Email: the default. Nothing may reach the webhook queue.
	srv.sendAdminAlert("alert_notify_registration", "By email", "<p>body</p>")
	waitForSentEmail(t, mail)
	if got := queuedWebhookTitles(t, srv); len(got) != 0 {
		t.Fatalf("email mode also queued a webhook alert: %v", got)
	}

	// Webhook, chosen by the owner. The webhook path is synchronous and the email path is
	// never entered, so the attempt count is final the moment the call returns.
	if err := srv.db.SetAdminSetting(adminAlertMethodSettingKey, "webhook"); err != nil {
		t.Fatal(err)
	}
	before := mail.sendAttempts()
	srv.sendAdminAlert("alert_notify_registration", "By webhook", "<p>body</p>")
	if got := queuedWebhookTitles(t, srv); len(got) != 1 || got[0] != "By webhook" {
		t.Fatalf("webhook mode: want one queued alert titled %q, got %v", "By webhook", got)
	}
	if after := mail.sendAttempts(); after != before {
		t.Fatalf("webhook mode also sent %d email(s)", after-before)
	}
}

// TestEveryAlertReachesTheWebhook: the webhook used to carry five of the alerts, so it could not
// stand in for email. Every declared key must now arrive, which is what makes it an alternative
// rather than a subset.
func TestEveryAlertReachesTheWebhook(t *testing.T) {
	srv, _ := alertRouteServer(t)
	if err := srv.db.SetAdminSetting(adminAlertMethodSettingKey, "webhook"); err != nil {
		t.Fatal(err)
	}
	for _, a := range AlertSettings {
		// tunnel_offline defaults off; switch everything on so default-off is not mistaken
		// for missing coverage.
		if err := srv.db.SetAdminSetting(a.Key, alertSettingOn); err != nil {
			t.Fatal(err)
		}
		srv.sendAdminAlert(a.Key, a.Key, "<p>x</p>")
	}
	got := queuedWebhookTitles(t, srv)
	if len(got) != len(AlertSettings) {
		t.Fatalf("want all %d alerts on the webhook, got %d: %v", len(AlertSettings), len(got), got)
	}
}

// TestAlertTogglesGateTheWebhookToo: switching method must not change WHICH alerts arrive.
func TestAlertTogglesGateTheWebhookToo(t *testing.T) {
	srv, _ := alertRouteServer(t)
	if err := srv.db.SetAdminSetting(adminAlertMethodSettingKey, "webhook"); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.SetAdminSetting("alert_notify_registration", alertSettingOff); err != nil {
		t.Fatal(err)
	}
	srv.sendAdminAlert("alert_notify_registration", "Muted", "<p>x</p>")
	// Control: an alert that is on still goes, so an empty queue above means "gated", not
	// "the webhook path is broken".
	srv.sendAdminAlert("alert_notify_blacklist", "Not muted", "<p>x</p>")

	got := queuedWebhookTitles(t, srv)
	if len(got) != 1 || got[0] != "Not muted" {
		t.Fatalf("want only the enabled alert queued, got %v", got)
	}
}

// TestWebhookChoiceWithoutURLFallsBackToEmail: an owner chose the webhook, then the URL was
// removed from the YAML. Alerts go by email rather than into a queue that can never deliver.
func TestWebhookChoiceWithoutURLFallsBackToEmail(t *testing.T) {
	srv, mail := alertRouteServer(t)
	if err := srv.db.SetAdminSetting(adminAlertMethodSettingKey, "webhook"); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Webhooks.SlackURL = ""

	if m, _ := srv.effectiveAlertMethod(); m != config.AdminAlertMethodEmail {
		t.Fatalf("with no URL the method in force must be email, got %q", m)
	}
	srv.sendAdminAlert("alert_notify_registration", "Fallback", "<p>x</p>")
	waitForSentEmail(t, mail)
}

func postAlertMethod(t *testing.T, srv *Server, actor, value string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{adminAlertMethodSettingKey: value})
	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", bytes.NewReader(body)), actor)
	return rec
}

func getSettings(t *testing.T, srv *Server, actor string) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAdminSettings(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), actor)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAlertMethodOverrideIsOwnerOnlyAndReportsItsSource(t *testing.T) {
	srv, _ := alertRouteServer(t)

	out := getSettings(t, srv, "owner@example.com")
	if out["admin_alert_method"] != "email" || out["admin_alert_method_source"] != "server_config" {
		t.Fatalf("unset override: want email from server_config, got %v from %v", out["admin_alert_method"], out["admin_alert_method_source"])
	}
	if out["can_change_alert_method"] != true || out["webhook_slack_configured"] != true {
		t.Fatalf("owner view: %v", out)
	}
	for k, v := range out {
		if s, ok := v.(string); ok && strings.Contains(s, testWebhookURL) {
			t.Fatalf("settings response leaks the webhook URL under %q", k)
		}
	}

	if rec := postAlertMethod(t, srv, "admin@example.com", "webhook"); rec.Code != http.StatusForbidden {
		t.Fatalf("a non-owner changed the alert method: %d %s", rec.Code, rec.Body.String())
	}
	if got := getSettings(t, srv, "admin@example.com")["can_change_alert_method"]; got != false {
		t.Fatalf("non-owner told they can change the method: %v", got)
	}
	if rec := postAlertMethod(t, srv, "owner@example.com", "pigeon"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown method accepted: %d", rec.Code)
	}

	if rec := postAlertMethod(t, srv, "owner@example.com", "webhook"); rec.Code != http.StatusOK {
		t.Fatalf("owner could not choose the webhook: %d %s", rec.Code, rec.Body.String())
	}
	out = getSettings(t, srv, "owner@example.com")
	if out["admin_alert_method_override"] != "webhook" {
		t.Fatalf("override not reported: %v", out["admin_alert_method_override"])
	}
	if out["admin_alert_method"] != "webhook" || out["admin_alert_method_source"] != "portal" {
		t.Fatalf("after override: want webhook from portal, got %v from %v", out["admin_alert_method"], out["admin_alert_method_source"])
	}

	// Clearing returns to the YAML.
	if rec := postAlertMethod(t, srv, "owner@example.com", ""); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	out = getSettings(t, srv, "owner@example.com")
	if out["admin_alert_method"] != "email" || out["admin_alert_method_source"] != "server_config" {
		t.Fatalf("after clearing: want email from server_config, got %v from %v", out["admin_alert_method"], out["admin_alert_method_source"])
	}

	// No URL: the owner cannot pick a webhook that has nowhere to post.
	srv.cfg.Webhooks.SlackURL = ""
	if rec := postAlertMethod(t, srv, "owner@example.com", "webhook"); rec.Code != http.StatusBadRequest {
		t.Fatalf("webhook accepted with no URL: %d", rec.Code)
	}
}

func TestHTMLToSlack(t *testing.T) {
	cases := []struct{ in, want string }{
		// Control: plain text, which most alert bodies are, passes through.
		{"[lfr-tunnel] New user registration: a@b.com requires approval.", "[lfr-tunnel] New user registration: a@b.com requires approval."},
		{"<p>Hook failed.</p><ul><li>Domain: x.com</li><li>Error: boom</li></ul>", "Hook failed.\n• Domain: x.com\n• Error: boom"},
		{`<p>See <a href="https://example.com/a?b=1&amp;c=2">the portal</a>.</p>`, "See <https://example.com/a?b=1&amp;c=2|the portal>."},
		{"<p><strong>Quota</strong> exceeded</p>", "*Quota* exceeded"},
		// Escaped in the email, and must be escaped again for Slack rather than read as markup.
		{"<p>Error: exit &lt;1&gt; &amp; more</p>", "Error: exit &lt;1&gt; &amp; more"},
	}
	for _, tc := range cases {
		if got := htmlToSlack(tc.in); got != tc.want {
			t.Errorf("htmlToSlack(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
