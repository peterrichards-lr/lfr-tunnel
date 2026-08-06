package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (s *Server) generateRandomSubdomainPrefix(style string) string {
	randInt := func(max int) int {
		b := make([]byte, 4)
		_, _ = rand.Read(b) //nolint:errcheck
		val := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
		if val < 0 {
			val = -val
		}
		return val % max
	}

	switch style {
	case "words":
		return fmt.Sprintf("%s-%s-%s", generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))])
	case "heroku":
		return fmt.Sprintf("%s-%s-%d", generatorAdjectives[randInt(len(generatorAdjectives))], generatorNouns[randInt(len(generatorNouns))], randInt(9000)+1000)
	case "liferay":
		return fmt.Sprintf("%s-%s-%d", generatorTechAdjectives[randInt(len(generatorTechAdjectives))], generatorLiferayNouns[randInt(len(generatorLiferayNouns))], randInt(900)+100)
	case "ngrok":
		const hexChars = "0123456789abcdef"
		b := make([]byte, 4)
		_, _ = rand.Read(b) //nolint:errcheck
		for i := range b {
			b[i] = hexChars[int(b[i])%len(hexChars)]
		}
		return fmt.Sprintf("%s-tunnel", string(b))
	default: // Completely Random (Alphanumeric) [a-z0-9]{8}
		const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
		b := make([]byte, 8)
		_, _ = rand.Read(b) //nolint:errcheck
		for i := range b {
			b[i] = chars[int(b[i])%len(chars)]
		}
		return string(b)
	}
}

// isCustomDomain checks if a host does not belong to configured root domains.
func (s *Server) isCustomDomain(host string) bool {
	for _, d := range s.cfg.Domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return false
		}
	}
	return true
}

// getVanityDomainHookConfig resolves the path and enabled state of the vanity domain hook.
// It prioritizes dynamic settings in the database, falling back to static config settings.
func (s *Server) getVanityDomainHookConfig() (string, bool) {
	path := s.cfg.VanityDomainHook
	enabled := s.cfg.VanityDomainHook != ""

	if s.db != nil {
		if dbPath, exists, err := s.db.GetAdminSettingOptional("vanity_domain_hook_path"); err == nil && exists {
			path = dbPath
		}
		if dbEnabled, exists, err := s.db.GetAdminSettingOptional("enable_vanity_domain_hook"); err == nil && exists && dbEnabled != "" {
			enabled = dbEnabled == "true"
		}
	}
	return path, enabled
}

// runVanityDomainHook runs the external script with action ("add"/"remove") and domain,
// on behalf of the given requestingUserID (the lease owner) -- used only to route a
// failure notice to them directly, never passed to the script itself. On failure, alerts
// both the admin (existing webhook mechanism) and the requesting user directly by email,
// bypassing their notification preference: this means the user's live custom domain may
// currently have no valid SSL certificate or be unreachable, which is closer to
// "Account Suspended"-style critical news than a routine lifecycle event they might have
// muted (see #913's precedent for that distinction).
func (s *Server) runVanityDomainHook(action, domain, requestingUserID string) {
	path, enabled := s.getVanityDomainHookConfig()
	if !enabled || path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	slog.Info(fmt.Sprintf("[Server] Executing vanity domain hook: %s %s %s", path, action, domain))
	cmd := exec.CommandContext(ctx, path, action, domain)
	cmd.Env = s.vanityHookEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Vanity domain hook error running %s for %s: %v. Output: %s", action, domain, err, string(output)))
		s.alertVanityDomainHookFailure(action, domain, requestingUserID, err)
		return
	}
	slog.Info(fmt.Sprintf("[Server] Vanity domain hook ran successfully for %s %s", action, domain))
}

// vanityHookEnv builds the environment for the vanity domain hook subprocess: everything
// inherited from this process, plus ACME_EMAIL forced to the configured Owner's contact.
// This is deliberately the server Owner's email, not the requesting user's -- the Owner
// is the operator responsible for the shared Nginx/Certbot install this hook manages, so
// they're the right contact for Let's Encrypt's own renewal/account notices, and this is
// already-required config (every deployment has an Owner), so nothing new to set up.
// Overrides anything an operator may have separately exported for this same key, so
// there's exactly one place this is ever configured.
func (s *Server) vanityHookEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ACME_EMAIL=") {
			env = append(env, kv)
		}
	}
	if s.cfg.Owner.UserID != "" {
		env = append(env, "ACME_EMAIL="+s.cfg.Owner.UserID)
	}
	return env
}

// alertVanityDomainHookFailure notifies the admin (email via SendAdminAlert + webhook,
// matching the existing pattern for every other admin alert in this codebase -- e.g. IP
// blacklist) and, separately, the requesting user directly by email, bypassing their
// notification preference (see runVanityDomainHook's comment for why). Skips the
// requesting-user email if they ARE the admin/owner -- they've already gotten the news
// above, and sending it again as "your domain" would just duplicate it in the same inbox.
func (s *Server) alertVanityDomainHookFailure(action, domain, requestingUserID string, hookErr error) {
	if s.notifications != nil {
		body := fmt.Sprintf(
			"<p>The vanity domain hook failed.</p><ul><li>Action: %s</li><li>Domain: %s</li><li>Requested by: %s</li><li>Error: %s</li></ul>",
			action, domain, requestingUserID, hookErr.Error(),
		)
		s.notifications.SendAdminAlert("alert_notify_vanity_hook_failure", "LFR Tunnel Alert: Vanity Domain Hook Failed", body)
	}
	if s.webhooks != nil {
		s.webhooks.SendVanityDomainHookFailureAlert(action, domain, requestingUserID, hookErr.Error())
	}

	if strings.EqualFold(requestingUserID, s.cfg.AdminNotificationEmail) || strings.EqualFold(requestingUserID, s.cfg.Owner.UserID) {
		return
	}

	if s.db == nil || s.notifications == nil || s.notifications.Sender() == nil || requestingUserID == "" {
		return
	}
	user, err := s.db.GetUserByEmail(requestingUserID)
	if err != nil || user == nil {
		return
	}

	body, err := s.renderEmailTemplate(user.LanguagePreference, "vanity_domain_hook_failed.html", map[string]interface{}{
		"Name":       user.FirstName,
		"Domain":     domain,
		"PortalLink": s.getPortalBaseURL(nil) + "/portal",
	})
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to render vanity_domain_hook_failed email: %v", err))
		return
	}
	subject := fmt.Sprintf("Action Needed: Custom Domain Setup Failed for %s", domain)
	plain := fmt.Sprintf("Hi %s,\n\nSomething went wrong setting up automated DNS/TLS provisioning for your custom domain %s. The administrator has been alerted, but %s may currently have no valid SSL certificate or be unreachable.", user.FirstName, domain, domain)

	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }() //nolint:errcheck
}

func (s *Server) checkQuarantineStatus(host string) (bool, string, string) {
	if s.db == nil {
		return false, "", ""
	}
	for _, domain := range s.cfg.Domains {
		if strings.HasSuffix(host, "."+domain) {
			subdomain := strings.TrimSuffix(host, "."+domain)
			existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
			if err == nil && existing != nil {
				if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
					quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
					if time.Now().Before(quarantineCutoff) {
						return true, host, quarantineCutoff.Format("2006-01-02 15:04:05 MST")
					}
				}
			}
		}
	}
	return false, "", ""
}
