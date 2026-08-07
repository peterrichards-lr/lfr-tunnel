package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackInstallation is the result of a successful "Add to Slack" OAuth flow,
// stored as a single JSON blob under admin_settings (see #909). This project
// installs into exactly one workspace (the Liferay SE team's), so a dedicated
// multi-row table would be premature -- this mirrors the existing pattern
// already used for other admin-level settings (e.g. alert_notify_registration).
type SlackInstallation struct {
	TeamID      string    `json:"team_id"`
	TeamName    string    `json:"team_name"`
	BotUserID   string    `json:"bot_user_id"`
	AccessToken string    `json:"access_token"`
	InstalledAt time.Time `json:"installed_at"`
}

const slackInstallationSettingKey = "slack_installation"

// slackOAuthAccessResponse is the subset of Slack's oauth.v2.access response
// this handler needs. See https://api.slack.com/methods/oauth.v2.access.
type slackOAuthAccessResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	AccessToken string `json:"access_token"`
	BotUserID   string `json:"bot_user_id"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

// slackOAuthCallbackURL returns the exact redirect_uri this handler expects --
// must match byte-for-byte what's registered in the Slack app's dashboard.
func (s *Server) slackOAuthCallbackURL(r *http.Request) string {
	return s.getPortalBaseURL(r) + "/api/integrations/slack/callback"
}

// handleSlackOAuthCallback completes the "Add to Slack" OAuth handshake: Slack
// redirects the admin's browser here with a one-time ?code=... after they
// approve installation, which is exchanged server-side for a long-lived
// workspace access token (never exposed to the browser).
func (s *Server) handleSlackOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		s.writeSlackResult(w, false, fmt.Sprintf("Slack reported: %s", errParam))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.writeSlackResult(w, false, "Missing authorization code.")
		return
	}
	if s.cfg.SlackApp.ClientID == "" || s.cfg.SlackApp.ClientSecret == "" {
		slog.Info("[Slack] OAuth callback hit but slack_app.client_id/client_secret are not configured")
		s.writeSlackResult(w, false, "Slack app is not configured on this server.")
		return
	}

	form := url.Values{
		"client_id":     {s.cfg.SlackApp.ClientID},
		"client_secret": {s.cfg.SlackApp.ClientSecret},
		"code":          {code},
		"redirect_uri":  {s.slackOAuthCallbackURL(r)},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://slack.com/api/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		slog.Info(fmt.Sprintf("[Slack] Failed to build token exchange request: %v", err))
		s.writeSlackResult(w, false, "Internal error building the Slack request.")
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Slack] Token exchange request failed: %v", err))
		s.writeSlackResult(w, false, "Could not reach Slack. Please try again.")
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	var result slackOAuthAccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Info(fmt.Sprintf("[Slack] Failed to parse token exchange response: %v", err))
		s.writeSlackResult(w, false, "Received an unexpected response from Slack.")
		return
	}
	if !result.OK {
		slog.Info(fmt.Sprintf("[Slack] Token exchange rejected: %s", result.Error))
		s.writeSlackResult(w, false, fmt.Sprintf("Slack rejected the installation: %s", result.Error))
		return
	}

	installation := SlackInstallation{
		TeamID:      result.Team.ID,
		TeamName:    result.Team.Name,
		BotUserID:   result.BotUserID,
		AccessToken: result.AccessToken,
		InstalledAt: time.Now(),
	}
	blob, err := json.Marshal(installation)
	if err != nil {
		slog.Info(fmt.Sprintf("[Slack] Failed to marshal installation record: %v", err))
		s.writeSlackResult(w, false, "Internal error saving the installation.")
		return
	}
	if s.db == nil {
		slog.Info("[Slack] Installation succeeded but database storage is not enabled -- token discarded")
		s.writeSlackResult(w, false, "Database storage is not enabled on this server.")
		return
	}
	if err := s.db.SetAdminSetting(slackInstallationSettingKey, string(blob)); err != nil {
		slog.Info(fmt.Sprintf("[Slack] Failed to persist installation record: %v", err))
		s.writeSlackResult(w, false, "Internal error saving the installation.")
		return
	}

	slog.Info(fmt.Sprintf("[Slack] Installed into workspace %q (%s)", installation.TeamName, installation.TeamID))
	s.writeSlackResult(w, true, fmt.Sprintf("Connected to the %s Slack workspace.", installation.TeamName))
}

func (s *Server) writeSlackResult(w http.ResponseWriter, success bool, message string) {
	title, heading, color := "Slack Connection Failed", "Connection Failed ❌", "#dc2626"
	if success {
		title, heading, color = "Slack Connected", "Connected! ✅", "#10b981"
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}
	html := fmt.Sprintf(`<html><head><title>%s</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:%s;}</style></head><body><h1>%s</h1><p>%s</p></body></html>`,
		title, color, heading, message)
	if _, err := w.Write([]byte(html)); err != nil {
		slog.Info(fmt.Sprintf("[Slack] Failed to write response: %v", err))
	}
}
