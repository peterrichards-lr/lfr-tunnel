package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClientState represents the static configuration and process metadata of a running tunnel.
type ClientState struct {
	PID           int      `json:"pid"`
	InspectorPort int      `json:"inspector_port"`
	InspectorURL  string   `json:"inspector_url"`
	Subdomain     string   `json:"subdomain"`
	PublicURLs    []string `json:"public_urls"`
	Ports         []int    `json:"ports"`
	StartTime     string   `json:"start_time"`
}

// StatusOutput represents the combined output returned by the status-json flag.
type StatusOutput struct {
	Running         bool     `json:"running"`
	PID             int      `json:"pid,omitempty"`
	InspectorPort   int      `json:"inspector_port,omitempty"`
	InspectorURL    string   `json:"inspector_url,omitempty"`
	Subdomain       string   `json:"subdomain,omitempty"`
	PublicURLs      []string `json:"public_urls,omitempty"`
	Ports           []int    `json:"ports,omitempty"`
	StartTime       string   `json:"start_time,omitempty"`
	ConnectionState string   `json:"connection_state,omitempty"`
	Status          string   `json:"status,omitempty"`
	BytesIn         int64    `json:"bytes_in,omitempty"`
	BytesOut        int64    `json:"bytes_out,omitempty"`
	RequestsTotal   int64    `json:"requests_total,omitempty"`
}

// GetStateFilePath returns the absolute path to a subdomain-specific state file.
func GetStateFilePath(subdomain string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	safeSub := strings.ReplaceAll(subdomain, "/", "-")
	safeSub = strings.ReplaceAll(safeSub, "\\", "-")
	return filepath.Join(dir, fmt.Sprintf("lfr-tunnel-%s.state", safeSub)), nil
}

// WriteState serializes and saves the ClientState into the subdomain-specific file.
func WriteState(subdomain string, state *ClientState) error {
	path, err := GetStateFilePath(subdomain)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// DeleteState deletes the subdomain-specific state file.
func DeleteState(subdomain string) {
	path, err := GetStateFilePath(subdomain)
	if err == nil {
		_ = os.Remove(path)
	}
}

// QueryStatusJSON queries the state file and the active Inspector HTTP API to return the live JSON status.
func QueryStatusJSON(statePath string, isRunningFunc func(int) bool) ([]byte, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return json.Marshal(StatusOutput{Running: false})
	}

	var state ClientState
	if err := json.Unmarshal(data, &state); err != nil {
		return json.Marshal(StatusOutput{Running: false})
	}

	if isRunningFunc != nil && !isRunningFunc(state.PID) {
		_ = os.Remove(statePath)
		return json.Marshal(StatusOutput{Running: false})
	}

	out := StatusOutput{
		Running:         true,
		PID:             state.PID,
		InspectorPort:   state.InspectorPort,
		InspectorURL:    state.InspectorURL,
		Subdomain:       state.Subdomain,
		PublicURLs:      state.PublicURLs,
		Ports:           state.Ports,
		StartTime:       state.StartTime,
		ConnectionState: "unknown",
		Status:          "starting",
	}

	// Fetch live details from Inspector API with short timeout
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("%s/api/info", state.InspectorURL))
	if err == nil {
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode == http.StatusOK {
			var info struct {
				Status     string `json:"status"`
				Connection struct {
					State string `json:"state"`
				} `json:"connection"`
				Traffic struct {
					BytesIn       int64 `json:"bytes_in"`
					BytesOut      int64 `json:"bytes_out"`
					RequestsTotal int64 `json:"requests_total"`
				} `json:"traffic"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
				out.Status = info.Status
				out.ConnectionState = info.Connection.State
				out.BytesIn = info.Traffic.BytesIn
				out.BytesOut = info.Traffic.BytesOut
				out.RequestsTotal = info.Traffic.RequestsTotal
			}
		}
	}

	return json.MarshalIndent(out, "", "  ")
}
