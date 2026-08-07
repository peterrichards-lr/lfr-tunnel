package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"lfr-tunnel/pkg/config"
)

func (s *Server) monitorEdgeHealth() {
	for {
		outboundOk := s.checkOutboundConnectivity()
		s.outboundMutex.Lock()
		s.outboundConnected = outboundOk
		s.outboundMutex.Unlock()

		for _, edge := range s.cfg.EdgeNodes {
			s.checkEdgeNodeHealth(edge, outboundOk)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

// checkEdgeNodeHealth performs a single health check for one edge node and
// records the result. Shared by the periodic monitorEdgeHealth loop and
// triggerEdgeHealthRecheck (an on-demand burst run after a portal power
// action, so status reflects sooner than the periodic loop's up-to-60s lag).
func (s *Server) checkEdgeNodeHealth(edge config.EdgeNodeConfig, outboundOk bool) {
	if edge.URL == "" {
		return
	}

	if !outboundOk {
		s.updateEdgeHealth(edge.ID, "Unknown", 0, "Gateway outbound connectivity check failed", "", false)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, edge.URL+"/api/version", nil)
	if err != nil {
		s.updateEdgeHealth(edge.ID, "Offline", 0, err.Error(), "", false)
		return
	}

	req.Header.Set("User-Agent", "lfr-tunnel-health-monitor")

	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		s.updateEdgeHealth(edge.ID, "Offline", latency, err.Error(), "", false)
		return
	}

	var version string
	var maintenanceActive bool
	if resp.StatusCode == http.StatusOK {
		var versionResp struct {
			ServerVersion   string `json:"server_version"`
			MaintenanceMode string `json:"maintenance_mode"`
		}
		if bodyBytes, readErr := io.ReadAll(resp.Body); readErr == nil {
			_ = json.Unmarshal(bodyBytes, &versionResp) //nolint:errcheck
			version = versionResp.ServerVersion
			// "pending" (a scheduled-but-not-yet-active countdown) only
			// applies to the local/deploy-triggered maintenance path, not
			// the edge_control_ws maintenance_trigger message this reads --
			// that one flips straight to "true", so only it counts as active.
			maintenanceActive = versionResp.MaintenanceMode == "true"
		}
	}
	_ = resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusOK {
		s.updateEdgeHealth(edge.ID, "Online", latency, "", version, maintenanceActive)
	} else {
		s.updateEdgeHealth(edge.ID, "Offline", latency, fmt.Sprintf("HTTP %d", resp.StatusCode), "", false)
	}
}

// triggerEdgeHealthRecheck re-checks a single node's health every 5s for up
// to 2 minutes in the background. Called after a portal power action
// (start/stop/restart) so the Network Health table reflects the outcome
// within seconds rather than waiting for monitorEdgeHealth's next scheduled
// pass, which could be up to 60s away regardless of client-side polling.
func (s *Server) triggerEdgeHealthRecheck(nodeID string) {
	var target config.EdgeNodeConfig
	found := false
	for _, edge := range s.cfg.EdgeNodes {
		if edge.ID == nodeID {
			target = edge
			found = true
			break
		}
	}
	if !found {
		return
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		deadline := time.After(2 * time.Minute)
		for {
			s.outboundMutex.RLock()
			outboundOk := s.outboundConnected
			s.outboundMutex.RUnlock()
			s.checkEdgeNodeHealth(target, outboundOk)

			select {
			case <-s.ctx.Done():
				return
			case <-deadline:
				return
			case <-ticker.C:
			}
		}
	}()
}

// scheduledStartGraceSeconds is how long past a schedule's start_time a node
// is still shown as "Disabled" rather than "Offline" -- covers real EC2 boot
// + systemd startup time. Once exceeded, a still-unreachable node is treated
// as a genuine incident: the schedule said it should be up by now and it
// isn't, which is exactly the kind of thing worth alerting on (#887).
const scheduledStartGraceSeconds = 300

func (s *Server) updateEdgeHealth(id, status string, latency int64, errMsg string, version string, maintenanceActive bool) {
	s.edgeHealthMu.Lock()
	prev := s.edgeHealth[id]
	s.edgeHealthMu.Unlock()

	var ipv4, ipv6 string
	for _, edge := range s.cfg.EdgeNodes {
		if edge.ID == id && edge.URL != "" {
			if u, err := url.Parse(edge.URL); err == nil {
				ipv4, ipv6 = resolveIPv4AndIPv6(u.Hostname())
			}
			break
		}
	}

	// Uptime tracks true reachability, independent of the display status
	// below -- a node in soft maintenance hasn't actually restarted, so its
	// process uptime keeps counting even though it displays as "Disabled".
	// The portal only ever shows uptime for status == "Online" anyway, so
	// this has no visible effect while maintenance is active.
	onlineSince := prev.OnlineSince
	if status == "Online" {
		if prev.Status != "Online" || prev.OnlineSince == 0 {
			onlineSince = time.Now().Unix()
		}
	} else {
		onlineSince = 0
	}

	// The schedule (timezone + stop/start times + enabled flag) practically
	// never changes once set, so fetch it once and cache it here rather than
	// hitting the provisioner sidecar (and the AWS API behind it) on every
	// 60s health check. Fetched regardless of online/offline status -- a
	// currently-unreachable node is exactly the case that needs this data,
	// to tell a real outage apart from a scheduled stop window, and the
	// portal shows a node's local time regardless of its status too.
	// handleAdminEdgeSetSchedule clears the cache on save so an edit is
	// picked up on the next check.
	timezone := prev.Timezone
	schedStop := prev.ScheduleStopTime
	schedStart := prev.ScheduleStartTime
	schedEnabled := prev.ScheduleEnabled
	if timezone == "" && s.provisionerClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		sched, err := s.provisionerClient.GetSchedule(ctx, id)
		cancel()
		if err == nil {
			timezone = sched.Timezone
			schedStop = sched.StopTime
			schedStart = sched.StartTime
			schedEnabled = sched.Enabled
		}
	}

	// Product decision (#887): only these two signals count as an
	// intentional, non-incident state -- a node stopped some other way (e.g.
	// directly via the AWS console/CLI, bypassing the portal) is a real
	// outage and must show "Offline", not "Disabled". Order matters: a
	// health check that actually succeeded takes precedence over a stale
	// AdminDisabled flag (e.g. someone started it back up outside the
	// portal), and the schedule-window check has its own grace period so a
	// scheduled start that didn't actually come back up still alerts.
	switch {
	case status == "Online" && maintenanceActive:
		status = "Disabled"
		errMsg = ""
	case status == "Offline" && prev.AdminDisabled:
		status = "Disabled"
		errMsg = ""
	case status == "Offline" && schedEnabled && isWithinScheduledDowntime(time.Now(), schedStop, schedStart, timezone):
		status = "Disabled"
		errMsg = ""
	}

	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()
	s.edgeHealth[id] = EdgeHealthStatus{
		Status:            status,
		LatencyMs:         latency,
		LastCheckAt:       time.Now().Unix(),
		ErrorMessage:      errMsg,
		ResolvedIP:        preferredIP(ipv4, ipv6),
		ResolvedIPv4:      ipv4,
		ResolvedIPv6:      ipv6,
		Version:           version,
		OnlineSince:       onlineSince,
		Timezone:          timezone,
		ScheduleStopTime:  schedStop,
		ScheduleStartTime: schedStart,
		ScheduleEnabled:   schedEnabled,
		AdminDisabled:     prev.AdminDisabled,
	}
}

// updateEdgeLatencyFromPing records a fresh RTT measurement for a WS-connected edge,
// independent of the HTTP-polling path above -- edges configured with no `url` (the
// current norm; see docs/server/edge_setup_guide.md's example config) never get a
// health-check entry via checkEdgeNodeHealth at all, which left LatencyMs permanently
// unset and rendered as "--" in the portal (#976). Measured over the existing WS
// keepalive Ping/Pong instead (see edge_control_ws.go's per-connection ping ticker).
// Preserves every other field already recorded for this node -- handleEdgeHealth
// overlays Status "Online" for any node with a live WS connection regardless of what's
// stored here, so this never needs to touch Status itself.
func (s *Server) updateEdgeLatencyFromPing(nodeID string, latencyMs int64) {
	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()
	h := s.edgeHealth[nodeID]
	h.LatencyMs = latencyMs
	h.LastCheckAt = time.Now().Unix()
	s.edgeHealth[nodeID] = h
}

// isWithinScheduledDowntime reports whether now (evaluated in tz) falls
// inside a node's scheduled stop window, extended by
// scheduledStartGraceSeconds past its start_time. Handles the overnight-wrap
// case (e.g. stop=22:00, start=06:00) via modular arithmetic on
// seconds-of-day: the window's length plus grace is compared against how far
// past stop_time "now" is, both taken mod 24h, so it doesn't matter whether
// the window crosses midnight. Returns false on any malformed input rather
// than erroring -- a node with no valid schedule just isn't in a stop window.
func isWithinScheduledDowntime(now time.Time, stopHHMM, startHHMM, tz string) bool {
	if stopHHMM == "" || startHHMM == "" || tz == "" {
		return false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return false
	}
	stopSec, err := hhmmToSeconds(stopHHMM)
	if err != nil {
		return false
	}
	startSec, err := hhmmToSeconds(startHHMM)
	if err != nil {
		return false
	}
	if stopSec == startSec {
		return false
	}

	const daySeconds = 24 * 60 * 60
	windowLen := ((startSec - stopSec) + daySeconds) % daySeconds

	nowLocal := now.In(loc)
	nowSec := nowLocal.Hour()*3600 + nowLocal.Minute()*60 + nowLocal.Second()
	deltaFromStop := ((nowSec - stopSec) + daySeconds) % daySeconds

	return deltaFromStop < windowLen+scheduledStartGraceSeconds
}

func hhmmToSeconds(hhmm string) (int, error) {
	var hour, minute int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &hour, &minute); err != nil {
		return 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid time %q", hhmm)
	}
	return hour*3600 + minute*60, nil
}

// invalidateEdgeScheduleCache clears a node's cached schedule (timezone,
// stop/start times, enabled flag) so the next health check re-fetches it
// from the provisioner sidecar. Called after a successful schedule save so
// an edit shows up promptly instead of waiting on the (never, once cached)
// natural cache expiry.
func (s *Server) invalidateEdgeScheduleCache(id string) {
	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()
	if h, ok := s.edgeHealth[id]; ok {
		h.Timezone = ""
		h.ScheduleStopTime = ""
		h.ScheduleStartTime = ""
		h.ScheduleEnabled = false
		s.edgeHealth[id] = h
	}
}

// setEdgeAdminDisabled records whether a node was last stopped intentionally
// via a portal power action, so a subsequent failed health check reports
// "Disabled" instead of "Offline" (#887). Cleared by a successful start or
// restart; a health check finding the node Online again overrides this
// naturally regardless (Status wins over the cached flag either way).
func (s *Server) setEdgeAdminDisabled(id string, disabled bool) {
	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()
	h := s.edgeHealth[id]
	h.AdminDisabled = disabled
	s.edgeHealth[id] = h
}

// resolveIPv4AndIPv6 resolves host's A and AAAA records independently, so a
// dual-stack node's health status can report both addresses rather than
// whichever family net.LookupHost happened to return first (see #886).
// Either return value is empty if that record type doesn't exist for host.
func resolveIPv4AndIPv6(host string) (ipv4, ipv6 string) {
	if ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip4", host); err == nil && len(ips) > 0 {
		ipv4 = ips[0].String()
	}
	if ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip6", host); err == nil && len(ips) > 0 {
		ipv6 = ips[0].String()
	}
	return ipv4, ipv6
}

// preferredIP picks a single address for EdgeHealthStatus.ResolvedIP, the
// legacy field the V1 dashboard's single IP-address column still reads --
// prefers IPv4, falling back to IPv6 for an IPv6-only node.
func preferredIP(ipv4, ipv6 string) string {
	if ipv4 != "" {
		return ipv4
	}
	return ipv6
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
