package server

import (
	"log"

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

// SendAdminAlert checks admin preferences in the database and dispatches the alert via email.
func (n *NotificationService) SendAdminAlert(settingKey, subject, htmlBody string) {
	if n.db == nil || n.sender == nil || n.cfg.AdminNotificationEmail == "" {
		return
	}

	val, err := n.db.GetAdminSetting(settingKey)
	if err != nil {
		log.Printf("[Warning] Failed to fetch admin setting %s: %v", settingKey, err)
		return
	}

	// Default true for "alert_notify_registration" and "alert_notify_blacklist"
	if val == "false" {
		return
	}
	if val == "" && settingKey == "alert_notify_tunnel_offline" {
		return // default false
	}

	// Check the admin user's personal notification preferences
	if adminUser, err := n.db.GetUserByEmail(n.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
		if adminUser.NotificationPrefs == "disabled" {
			return
		}
	}

	go func() {
		if err := n.sender.Send(n.cfg.AdminNotificationEmail, subject, htmlBody, "An alert has been triggered."); err != nil {
			log.Printf("[Mail] Failed to send admin alert %s: %v", settingKey, err)
		}
	}()
}
