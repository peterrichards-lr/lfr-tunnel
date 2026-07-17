package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"
)

func (s *Server) monitorEdgeHealth() {
	for {
		outboundOk := s.checkOutboundConnectivity()
		s.outboundMutex.Lock()
		s.outboundConnected = outboundOk
		s.outboundMutex.Unlock()

		for _, edge := range s.cfg.EdgeNodes {
			if edge.URL == "" {
				continue
			}

			if !outboundOk {
				s.updateEdgeHealth(edge.ID, "Unknown", 0, "Gateway outbound connectivity check failed", "")
				continue
			}

			client := &http.Client{Timeout: 5 * time.Second}
			start := time.Now()
			req, err := http.NewRequest(http.MethodGet, edge.URL+"/api/version", nil)
			if err != nil {
				s.updateEdgeHealth(edge.ID, "Offline", 0, err.Error(), "")
				continue
			}

			req.Header.Set("User-Agent", "lfr-tunnel-health-monitor")

			resp, err := client.Do(req)
			latency := time.Since(start).Milliseconds()

			if err != nil {
				s.updateEdgeHealth(edge.ID, "Offline", latency, err.Error(), "")
				continue
			}

			var version string
			if resp.StatusCode == http.StatusOK {
				var versionResp struct {
					ServerVersion string `json:"server_version"`
				}
				if bodyBytes, readErr := io.ReadAll(resp.Body); readErr == nil {
					_ = json.Unmarshal(bodyBytes, &versionResp) //nolint:errcheck
					version = versionResp.ServerVersion
				}
			}
			_ = resp.Body.Close() //nolint:errcheck

			if resp.StatusCode == http.StatusOK {
				s.updateEdgeHealth(edge.ID, "Online", latency, "", version)
			} else {
				s.updateEdgeHealth(edge.ID, "Offline", latency, fmt.Sprintf("HTTP %d", resp.StatusCode), "")
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

func (s *Server) updateEdgeHealth(id, status string, latency int64, errMsg string, version string) {
	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()

	var resolvedIP string
	for _, edge := range s.cfg.EdgeNodes {
		if edge.ID == id && edge.URL != "" {
			if u, err := url.Parse(edge.URL); err == nil {
				host := u.Hostname()
				if ips, err := net.LookupHost(host); err == nil && len(ips) > 0 {
					resolvedIP = ips[0]
				}
			}
			break
		}
	}

	s.edgeHealth[id] = EdgeHealthStatus{
		Status:       status,
		LatencyMs:    latency,
		LastCheckAt:  time.Now().Unix(),
		ErrorMessage: errMsg,
		ResolvedIP:   resolvedIP,
		Version:      version,
	}
}

// checkOutboundConnectivity checks outbound internet access by hitting highly available public endpoints.
func (s *Server) checkOutboundConnectivity() bool {
	targets := []string{"https://1.1.1.1", "https://www.google.com"}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, target := range targets {
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck
			return true
		}
	}
	return false
}

func (s *Server) notifyControlPlaneDeregister(userID, subdomain string) {
	client := &http.Client{Timeout: 5 * time.Second}
	payloadBytes, err := json.Marshal(map[string]string{
		"user_id":   userID,
		"subdomain": subdomain,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", s.cfg.ControlPlaneURL+"/api/internal/edge-deregister", bytes.NewReader(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server Edge] Failed to notify control plane deregister: %v", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		slog.Info(fmt.Sprintf("[Server Edge] Control plane deregister returned status: %d", resp.StatusCode))
	}
}

// checkExpiringReservations scans for expiring or expired subdomain reservations and triggers email notifications.
func (s *Server) checkExpiringReservations() {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}

	now := time.Now()
	// Warning window of 48 hours
	warningThreshold := now.Add(48 * time.Hour)

	expiring, err := s.db.GetExpiringSubdomainReservations(now, warningThreshold)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to fetch expiring subdomain reservations: %v", err))
		return
	}

	for _, res := range expiring {
		user, err := s.db.GetUser(res.UserID)
		if err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to retrieve user %s for expiring reservation: %v", res.UserID, err))
			continue
		}

		if user.NotificationPrefs == "disabled" {
			continue
		}

		lang := user.LanguagePreference
		baseURL := s.getPortalBaseURL(nil)
		portalLink := baseURL + "/portal"

		if res.ExpiresAt != nil && res.ExpiresAt.Before(now) {
			// Stage 2: Already expired and quarantined
			releasedAt := res.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
			releasedStr := releasedAt.Format("2006-01-02 15:04:05 MST")

			body, err := s.renderEmailTemplate(lang, "subdomain_expired.html", map[string]interface{}{
				"Name":       user.FirstName,
				"Subdomain":  res.Subdomain,
				"Domain":     res.Domain,
				"ReleasedAt": releasedStr,
				"PortalLink": portalLink,
			})
			if err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to render subdomain_expired email template: %v", err))
				body = fmt.Sprintf("<p>Hi %s,</p>"+
					"<p>Your subdomain reservation <strong>%s.%s</strong> has expired and entered a %d-day quarantine period.</p>"+
					"<p>If you take no action, it will be released to the public pool on <strong>%s</strong>.</p>"+
					"<p><a href=\"%s\">Go to Portal</a></p>",
					html.EscapeString(user.FirstName), html.EscapeString(res.Subdomain), html.EscapeString(res.Domain),
					s.cfg.SubdomainQuarantineDays, releasedStr, portalLink)
			}

			plainBody := fmt.Sprintf("Hi %s,\n\nYour subdomain reservation %s.%s has expired and entered quarantine. It will be released to the public pool on %s.\n\nGo to the portal to manage it:\n%s",
				user.FirstName, res.Subdomain, res.Domain, releasedStr, portalLink)

			subject := s.GetTranslation(lang, "subdomain_expired_subject")
			if subject == "" {
				subject = fmt.Sprintf("Subdomain Expired & Quarantined: %s.%s", res.Subdomain, res.Domain)
			}

			if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to send subdomain expired email to %s: %v", user.Email, err))
				continue
			}

			res.ExpiryWarningSent = 2
			if err := s.db.UpdateSubdomainReservation(res); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to update expiry warning state for reservation %d: %v", res.ID, err))
			}
		} else if res.ExpiresAt != nil {
			// Stage 1: Expiring soon (< 48 hours remaining)
			expiresStr := res.ExpiresAt.Format("2006-01-02 15:04:05 MST")

			body, err := s.renderEmailTemplate(lang, "subdomain_expiring.html", map[string]interface{}{
				"Name":       user.FirstName,
				"Subdomain":  res.Subdomain,
				"Domain":     res.Domain,
				"ExpiresAt":  expiresStr,
				"PortalLink": portalLink,
			})
			if err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to render subdomain_expiring email template: %v", err))
				body = fmt.Sprintf("<p>Hi %s,</p>"+
					"<p>Your subdomain reservation <strong>%s.%s</strong> is expiring soon on <strong>%s</strong>.</p>"+
					"<p>To avoid service disruption, please renew your reservation or request an extension in the Liferay Tunnel Portal.</p>"+
					"<p><a href=\"%s\">Go to Portal</a></p>",
					html.EscapeString(user.FirstName), html.EscapeString(res.Subdomain), html.EscapeString(res.Domain),
					expiresStr, portalLink)
			}

			plainBody := fmt.Sprintf("Hi %s,\n\nYour subdomain reservation %s.%s is expiring soon on %s.\n\nPlease renew or request an extension in the portal:\n%s",
				user.FirstName, res.Subdomain, res.Domain, expiresStr, portalLink)

			subject := s.GetTranslation(lang, "subdomain_expiring_subject")
			if subject == "" {
				subject = fmt.Sprintf("Subdomain Expiring Soon: %s.%s", res.Subdomain, res.Domain)
			}

			if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to send subdomain expiring email to %s: %v", user.Email, err))
				continue
			}

			res.ExpiryWarningSent = 1
			if err := s.db.UpdateSubdomainReservation(res); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to update expiry warning state for reservation %d: %v", res.ID, err))
			}
		}
	}
}
