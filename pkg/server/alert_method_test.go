package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/webhook"
)

// Every admin alert goes to the admin inbox; it can ALSO reach Slack by exactly one of two
// integrations -- the channel's email address or the incoming webhook -- chosen by
// admin_alerts.slack.method or the owner's override (#2435).

const (
	testWebhookURL   = "http://127.0.0.1:1/slack" // never reached: the queue is read directly
	testChannelEmail = "chan-x@example.slack.com"
	testInbox        = "alerts@example.com"
)

// alertRouteServer has the inbox and both Slack integrations configured, so a test can see
// exactly where an alert went. The webhook queue is the database's, as in production.
func alertRouteServer(t *testing.T) (*Server, *mockMailSender) {
	t.Helper()
	srv := setupTestServerForAPI(t)
	srv.cfg.AdminNotificationEmail = testInbox
	srv.cfg.Owner.UserID = "owner@example.com"
	srv.cfg.Webhooks.SlackURL = testWebhookURL
	srv.cfg.AdminAlerts.Slack.ChannelEmail = testChannelEmail
	srv.cfg.AdminAlerts.Slack.Method = config.SlackAlertMethodOff
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

// waitForRecipients waits until mail has gone to exactly want (sends are asynchronous), then
// gives any stray extra send a moment to show itself.
func waitForRecipients(t *testing.T, m *mockMailSender, want ...string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(m.getSentEmails()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	var got []string
	for _, e := range m.getSentEmails() {
		got = append(got, e.To)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mail went to %v, want %v", got, want)
	}
	return got
}

func setSlackMethod(t *testing.T, srv *Server, m string) {
	t.Helper()
	if err := srv.db.SetAdminSetting(slackAlertMethodSettingKey, m); err != nil {
		t.Fatal(err)
	}
}

func TestSlackOffSendsOnlyToTheInbox(t *testing.T) {
	srv, mail := alertRouteServer(t)
	srv.sendAdminAlert("alert_notify_registration", "Inbox only", "<p>body</p>")
	waitForRecipients(t, mail, testInbox)
	if got := queuedWebhookTitles(t, srv); len(got) != 0 {
		t.Fatalf("Slack is off but the webhook got %v", got)
	}
}

func TestSlackByChannelEmailAddsTheChannelAndNotTheWebhook(t *testing.T) {
	srv, mail := alertRouteServer(t)
	setSlackMethod(t, srv, "email")
	srv.sendAdminAlert("alert_notify_registration", "Channel email", "<p>body</p>")
	waitForRecipients(t, mail, testInbox, testChannelEmail)
	if got := queuedWebhookTitles(t, srv); len(got) != 0 {
		t.Fatalf("channel email chosen but the webhook also got %v -- the channel would see it twice", got)
	}
}

func TestSlackByWebhookAddsTheWebhookAndNotTheChannelEmail(t *testing.T) {
	srv, mail := alertRouteServer(t)
	setSlackMethod(t, srv, "webhook")
	srv.sendAdminAlert("alert_notify_registration", "Webhook", "<p>body</p>")
	waitForRecipients(t, mail, testInbox)
	if got := queuedWebhookTitles(t, srv); len(got) != 1 || got[0] != "Webhook" {
		t.Fatalf("want one webhook alert, got %v", got)
	}
}

// TestEveryAlertReachesTheWebhook: the webhook used to carry five of the alerts, so it could not
// stand in for the channel email. Every declared key must now arrive.
func TestEveryAlertReachesTheWebhook(t *testing.T) {
	srv, _ := alertRouteServer(t)
	setSlackMethod(t, srv, "webhook")
	for _, a := range AlertSettings {
		// tunnel_offline defaults off; switch everything on so default-off is not mistaken
		// for missing coverage.
		if err := srv.db.SetAdminSetting(a.Key, alertSettingOn); err != nil {
			t.Fatal(err)
		}
		srv.sendAdminAlert(a.Key, a.Key, "<p>x</p>")
	}
	if got := queuedWebhookTitles(t, srv); len(got) != len(AlertSettings) {
		t.Fatalf("want all %d alerts on the webhook, got %d: %v", len(AlertSettings), len(got), got)
	}
}

// TestAlertTogglesGateSlackToo: turning Slack on must not change WHICH alerts arrive.
func TestAlertTogglesGateSlackToo(t *testing.T) {
	srv, _ := alertRouteServer(t)
	setSlackMethod(t, srv, "webhook")
	if err := srv.db.SetAdminSetting("alert_notify_registration", alertSettingOff); err != nil {
		t.Fatal(err)
	}
	srv.sendAdminAlert("alert_notify_registration", "Muted", "<p>x</p>")
	// Control: an alert that is on still goes, so the absence above means "gated", not "the
	// webhook path is broken".
	srv.sendAdminAlert("alert_notify_blacklist", "Not muted", "<p>x</p>")
	if got := queuedWebhookTitles(t, srv); len(got) != 1 || got[0] != "Not muted" {
		t.Fatalf("want only the enabled alert queued, got %v", got)
	}
}

// TestUnavailableOverrideFallsBackToTheYAML: the owner chose the webhook, then its URL was removed
// from the YAML. The YAML's own method applies rather than alerts queueing for nowhere.
func TestUnavailableOverrideFallsBackToTheYAML(t *testing.T) {
	srv, mail := alertRouteServer(t)
	srv.cfg.AdminAlerts.Slack.Method = config.SlackAlertMethodEmail
	setSlackMethod(t, srv, "webhook")
	srv.cfg.Webhooks.SlackURL = ""

	if m, src := srv.effectiveSlackMethod(); m != config.SlackAlertMethodEmail || src != alertMethodSourceServerConfig {
		t.Fatalf("want the YAML's email method in force, got %q from %q", m, src)
	}
	srv.sendAdminAlert("alert_notify_registration", "Fallback", "<p>x</p>")
	waitForRecipients(t, mail, testInbox, testChannelEmail)
}

func postSlackMethod(t *testing.T, srv *Server, actor, value string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{slackAlertMethodSettingKey: value})
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

func TestSlackMethodOverrideIsOwnerOnlyAndReportsItsSource(t *testing.T) {
	srv, _ := alertRouteServer(t)

	out := getSettings(t, srv, "owner@example.com")
	if out["admin_alert_slack_method"] != "off" || out["admin_alert_slack_method_source"] != "server_config" {
		t.Fatalf("unset override: want off from server_config, got %v from %v", out["admin_alert_slack_method"], out["admin_alert_slack_method_source"])
	}
	if out["can_change_alert_method"] != true || out["slack_webhook_configured"] != true || out["slack_channel_email"] != testChannelEmail {
		t.Fatalf("owner view: %v", out)
	}
	for k, v := range out {
		if s, ok := v.(string); ok && strings.Contains(s, testWebhookURL) {
			t.Fatalf("settings response leaks the webhook URL under %q", k)
		}
	}

	if rec := postSlackMethod(t, srv, "admin@example.com", "webhook"); rec.Code != http.StatusForbidden {
		t.Fatalf("a non-owner changed the Slack method: %d %s", rec.Code, rec.Body.String())
	}
	if got := getSettings(t, srv, "admin@example.com")["can_change_alert_method"]; got != false {
		t.Fatalf("non-owner told they can change the method: %v", got)
	}
	if rec := postSlackMethod(t, srv, "owner@example.com", "pigeon"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown method accepted: %d", rec.Code)
	}

	if rec := postSlackMethod(t, srv, "owner@example.com", "webhook"); rec.Code != http.StatusOK {
		t.Fatalf("owner could not choose the webhook: %d %s", rec.Code, rec.Body.String())
	}
	out = getSettings(t, srv, "owner@example.com")
	if out["admin_alert_slack_method_override"] != "webhook" || out["admin_alert_slack_method"] != "webhook" || out["admin_alert_slack_method_source"] != "portal" {
		t.Fatalf("after override: %v / %v / %v", out["admin_alert_slack_method_override"], out["admin_alert_slack_method"], out["admin_alert_slack_method_source"])
	}

	// Clearing returns to the YAML.
	if rec := postSlackMethod(t, srv, "owner@example.com", ""); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	if out = getSettings(t, srv, "owner@example.com"); out["admin_alert_slack_method"] != "off" || out["admin_alert_slack_method_source"] != "server_config" {
		t.Fatalf("after clearing: want off from server_config, got %v from %v", out["admin_alert_slack_method"], out["admin_alert_slack_method_source"])
	}

	// A method with no destination cannot be chosen.
	srv.cfg.Webhooks.SlackURL = ""
	srv.cfg.AdminAlerts.Slack.ChannelEmail = ""
	for _, m := range []string{"webhook", "email"} {
		if rec := postSlackMethod(t, srv, "owner@example.com", m); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted with no destination: %d", m, rec.Code)
		}
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
