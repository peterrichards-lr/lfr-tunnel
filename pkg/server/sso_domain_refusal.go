package server

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"lfr-tunnel/pkg/config"
)

// domainRefusalShort is the plain refusal, used when the page is switched off.
const domainRefusalShort = "Your email domain is not authorized to access this gateway."

// defaultDomainRefusalMessage is shown when domain_refusal.message is empty. It says why, without
// naming the organisation -- a self-hosted gateway restricts to its own domains, not Liferay's --
// and says what the person can do instead.
const defaultDomainRefusalMessage = "This gateway is provided for the people of the organisation that runs it, " +
	"and your account is not one of theirs, so it cannot be used to sign in here.\n\n" +
	"Liferay Tunnel itself is open source under the MIT licence: you are welcome to run your own " +
	"instance on your own resources."

// respondDomainRefused answers an SSO sign-in whose verified address is outside
// allowed_email_domains (#2471). The status is 403 either way; the page says why and what to do,
// which a bare 403 did not.
func (s *Server) respondDomainRefused(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DomainRefusal.Shown() {
		http.Error(w, domainRefusalShort, http.StatusForbidden)
		return
	}
	page, err := staticFS.ReadFile("static/domain_refused.html")
	if err != nil {
		http.Error(w, domainRefusalShort, http.StatusForbidden)
		return
	}
	message := strings.TrimSpace(s.cfg.DomainRefusal.Message)
	if message == "" {
		message = defaultDomainRefusalMessage
	}
	// Plain text from the config: escaped, so it cannot inject markup, with its line breaks kept.
	rendered := strings.ReplaceAll(html.EscapeString(message), "\n", "<br>")

	out := strings.ReplaceAll(string(page), "{{.Message}}", rendered)
	out = strings.ReplaceAll(out, "{{.RepositoryURL}}", html.EscapeString(config.DefaultRepositoryURL))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	if _, err := w.Write(s.injectBaseTag([]byte(out), r)); err != nil {
		slog.Debug(fmt.Sprintf("[SSO] Writing the domain refusal page: %v", err))
	}
}
