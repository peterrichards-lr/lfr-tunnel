package nginx

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaintenanceManager handles Nginx file-based maintenance mode activation.
type MaintenanceManager struct {
	triggerPath string
}

// NewMaintenanceManager creates a new Nginx MaintenanceManager.
func NewMaintenanceManager(triggerPath string) *MaintenanceManager {
	return &MaintenanceManager{
		triggerPath: triggerPath,
	}
}

func (m *MaintenanceManager) getResolvedTriggerPath() string {
	path := m.triggerPath
	if path == "" {
		if fi, err := os.Stat("/var/lib/lfr-tunneld"); err == nil && fi.IsDir() {
			path = "/var/lib/lfr-tunneld/maintenance.enable"
		}
	}
	return path
}

// Enable writes the Nginx maintenance trigger file and HTML page.
func (m *MaintenanceManager) Enable(action, reason string, duration int, endTime time.Time, templateContent string) {
	triggerPath := m.getResolvedTriggerPath()
	if triggerPath == "" {
		log.Printf("[Nginx] Maintenance trigger path not resolved; skipping Nginx hard maintenance.")
		return
	}

	triggerDir := filepath.Dir(triggerPath)
	if err := os.MkdirAll(triggerDir, 0755); err != nil {
		log.Printf("[Nginx] Failed to create trigger directory: %v", err)
		return
	}
	if err := os.WriteFile(triggerPath, []byte("enabled"), 0644); err != nil {
		log.Printf("[Nginx] Failed to write Nginx maintenance trigger file: %v", err)
		return
	}

	htmlContent := templateContent
	durationStr := fmt.Sprintf("%d minutes", duration)
	if duration >= 60 {
		durationStr = fmt.Sprintf("%d hour(s)", (duration+59)/60)
	}

	htmlContent = strings.ReplaceAll(htmlContent, "__ACTION__", action)
	htmlContent = strings.ReplaceAll(htmlContent, "__REASON__", reason)
	htmlContent = strings.ReplaceAll(htmlContent, "__DURATION__", durationStr)
	htmlContent = strings.ReplaceAll(htmlContent, "__END_TIME__", strconv.FormatInt(endTime.Unix(), 10))

	htmlDestPath := filepath.Join(triggerDir, "maintenance.html")
	if err := os.WriteFile(htmlDestPath, []byte(htmlContent), 0644); err != nil {
		log.Printf("[Nginx] Failed to write Nginx maintenance HTML file: %v", err)
	} else {
		log.Printf("[Nginx] Maintenance HTML written successfully to %s", htmlDestPath)
	}

	vpsWebRoot := "/var/www/lfr-tunnel"
	if fi, err := os.Stat(vpsWebRoot); err == nil && fi.IsDir() {
		destFilePath := filepath.Join(vpsWebRoot, "maintenance.html")
		if err := os.WriteFile(destFilePath, []byte(htmlContent), 0644); err != nil {
			log.Printf("[Nginx] Could not write directly to %s: %v", destFilePath, err)
		} else {
			log.Printf("[Nginx] Custom maintenance page successfully copied to %s", destFilePath)
		}
	}
}

// Disable removes the Nginx maintenance trigger and HTML files.
func (m *MaintenanceManager) Disable() {
	triggerPath := m.getResolvedTriggerPath()
	if triggerPath == "" {
		return
	}

	_ = os.Remove(triggerPath)
	triggerDir := filepath.Dir(triggerPath)
	_ = os.Remove(filepath.Join(triggerDir, "maintenance.html"))
	_ = os.Remove("/var/www/lfr-tunnel/maintenance.html")
}

// IsActive checks if the Nginx maintenance trigger file is currently present.
func (m *MaintenanceManager) IsActive() bool {
	triggerPath := m.getResolvedTriggerPath()
	if triggerPath != "" {
		if _, err := os.Stat(triggerPath); err == nil {
			return true
		}
	}
	return false
}
