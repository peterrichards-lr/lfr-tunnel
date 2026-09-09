package server

import (
	"fmt"
	"log/slog"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/mail"
)

// NotificationService wraps the generic SMTP client and provides higher-level alerting logic.
type NotificationService struct {
	sender mail.Sender
	db     *db.DB
	cfg    *config.ServerConfig
}

// NewNotificationService initializes a new NotificationService.
func NewNotificationService(sender mail.Sender, database *db.DB, cfg *config.ServerConfig) *NotificationService {
	return &NotificationService{
		sender: sender,
		db:     database,
		cfg:    cfg,
	}
}

// Sender returns the underlying SMTP mail.Sender for direct HTML dispatches.
func (n *NotificationService) Sender() mail.Sender {
	return n.sender
}

// adminAlertRecipient decides whether an alert for settingKey should go out at all, and to
// whom. It returns "" when the alert is suppressed -- by configuration, by the per-alert admin
// setting, or by the admin's own notification preference.
//
// This used to be SendAdminAlert, which also did the sending. The decision stayed here and the
// send moved to Server.sendAdminAlert (notify.go), because the send was the one remaining place
// in the package that dispatched mail and logged its failure at INFO with no audit row -- and of
// all the sites for that to be true of, the owner's own alert channel is the worst: a missing
// alert is indistinguishable from nothing having happened (#1732).
func (n *NotificationService) adminAlertRecipient(settingKey string) string {
	if n.db == nil || n.sender == nil || n.cfg.AdminNotificationEmail == "" {
		return ""
	}

	val, err := n.db.GetAdminSetting(settingKey)
	if err != nil {
		slog.Info(fmt.Sprintf("[Warning] Failed to fetch admin setting %s: %v", settingKey, err))
		return ""
	}

	// Default true for "alert_notify_registration" and "alert_notify_blacklist"
	if val == "false" {
		return ""
	}
	if val == "" && settingKey == "alert_notify_tunnel_offline" {
		return "" // default false
	}

	// Notification: routine operational news -- a registration happened, an IP was
	// blacklisted, a tunnel went offline. All of it is visible in the admin dashboard,
	// so an admin who mutes it loses nothing. The approve-link email that accompanies a
	// registration is classified transactional at its own send site and is unaffected.
	if adminUser, err := n.db.GetUserByEmail(n.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
		if !shouldSendTo(adminUser, emailNotification) {
			return ""
		}
	}

	return n.cfg.AdminNotificationEmail
}
