package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError represents a JSON-RPC error.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ClientState mirrors client.ClientState.
type ClientState struct {
	PID           int      `json:"pid"`
	InspectorPort int      `json:"inspector_port"`
	InspectorURL  string   `json:"inspector_url"`
	Subdomain     string   `json:"subdomain"`
	PublicURLs    []string `json:"public_urls"`
	Ports         []int    `json:"ports"`
	StartTime     string   `json:"start_time"`
}

var outputWriter io.Writer = os.Stdout

// StartMCPServer runs the main stdio JSON-RPC loop.
func StartMCPServer() {
	RunMCPLoop(os.Stdin, os.Stdout)
}

// RunMCPLoop runs the main JSON-RPC loop over custom reader/writer.
func RunMCPLoop(r io.Reader, w io.Writer) {
	outputWriter = w
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			sendError(nil, -32700, "Parse error")
			continue
		}
		handleRequest(&req)
	}
}

func handleRequest(req *Request) {
	switch req.Method {
	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"serverInfo": map[string]string{
				"name":    "lfr-tunnel",
				"version": config.Version,
			},
		}
		sendResult(req.ID, result)

	case "initialized":
		// Notification - no response required

	case "tools/list":
		tools := []map[string]interface{}{
			{
				"name":        "get_tunnel_status",
				"description": "Returns the status of any active tunnels, active leases, configuration, and public URLs.",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			{
				"name":        "start_tunnel",
				"description": "Establishes a new secure background tunnel to the gateway.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"subdomain": map[string]interface{}{
							"type":        "string",
							"description": "The requested subdomain prefix.",
						},
						"ports": map[string]interface{}{
							"type":        "string",
							"description": "Comma-separated ports to expose (e.g. '8080,3000').",
						},
						"target_host": map[string]interface{}{
							"type":        "string",
							"description": "Local hostname or IP to route traffic to.",
						},
					},
				},
			},
			{
				"name":        "stop_tunnel",
				"description": "Terminates any active background tunnel connections.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"subdomain": map[string]interface{}{
							"type":        "string",
							"description": "The specific subdomain of the tunnel to stop. If omitted, stops all active tunnels.",
						},
					},
				},
			},
			{
				"name":        "list_requests",
				"description": "Retrieves recent HTTP requests routed through the client interceptor for debugging.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of requests to return (default: 10).",
						},
					},
				},
			},
			{
				"name":        "replay_request",
				"description": "Replays a previously captured request against the local target host.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"request_id": map[string]interface{}{
							"type":        "string",
							"description": "The ID of the request to replay.",
						},
					},
					"required": []string{"request_id"},
				},
			},
		}
		sendResult(req.ID, map[string]interface{}{"tools": tools})

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(req.ID, -32602, "Invalid params")
			return
		}
		handleToolCall(req.ID, params.Name, params.Arguments)

	default:
		sendError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func handleToolCall(id interface{}, name string, args json.RawMessage) {
	switch name {
	case "get_tunnel_status":
		status, err := getTunnelStatus()
		if err != nil {
			sendToolError(id, err.Error())
		} else {
			sendToolSuccess(id, status)
		}

	case "start_tunnel":
		var params struct {
			Subdomain  string `json:"subdomain"`
			Ports      string `json:"ports"`
			TargetHost string `json:"target_host"`
		}
		_ = json.Unmarshal(args, &params) // Optional params //nolint:errcheck

		res, err := startTunnel(params.Subdomain, params.Ports, params.TargetHost)
		if err != nil {
			sendToolError(id, err.Error())
		} else {
			sendToolSuccess(id, res)
		}

	case "stop_tunnel":
		var params struct {
			Subdomain string `json:"subdomain"`
		}
		_ = json.Unmarshal(args, &params) // Optional param //nolint:errcheck

		res, err := stopTunnel(params.Subdomain)
		if err != nil {
			sendToolError(id, err.Error())
		} else {
			sendToolSuccess(id, res)
		}

	case "list_requests":
		var params struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(args, &params) //nolint:errcheck
		if params.Limit <= 0 {
			params.Limit = 10
		}

		res, err := listRequests(params.Limit)
		if err != nil {
			sendToolError(id, err.Error())
		} else {
			sendToolSuccess(id, res)
		}

	case "replay_request":
		var params struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(args, &params); err != nil || params.RequestID == "" {
			sendToolError(id, "Missing required argument 'request_id'")
			return
		}

		res, err := replayRequest(params.RequestID)
		if err != nil {
			sendToolError(id, err.Error())
		} else {
			sendToolSuccess(id, res)
		}

	default:
		sendError(id, -32601, fmt.Sprintf("Tool not found: %s", name))
	}
}

// getTunnelStatus retrieves the status of all active state files.
func getTunnelStatus() (interface{}, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home dir: %w", err)
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{"active_tunnels": []interface{}{}}, nil
		}
		return nil, err
	}

	var activeTunnels []interface{}
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "lfr-tunnel-") && strings.HasSuffix(f.Name(), ".state") {
			statePath := filepath.Join(dir, f.Name())
			data, err := os.ReadFile(statePath)
			if err != nil {
				continue
			}
			var state ClientState
			if err := json.Unmarshal(data, &state); err != nil {
				continue
			}

			// Verify PID running status
			if !isPIDRunning(state.PID) {
				_ = os.Remove(statePath) //nolint:errcheck
				continue
			}

			// Query inspector info
			info := queryInspectorInfo(state.InspectorURL)
			activeTunnels = append(activeTunnels, map[string]interface{}{
				"pid":           state.PID,
				"subdomain":     state.Subdomain,
				"public_urls":   state.PublicURLs,
				"ports":         state.Ports,
				"start_time":    state.StartTime,
				"inspector_url": state.InspectorURL,
				"live_status":   info,
			})
		}
	}

	return map[string]interface{}{"active_tunnels": activeTunnels}, nil
}

// startTunnel starts a background tunnel.
func startTunnel(subdomain, ports, targetHost string) (interface{}, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to locate executable: %w", err)
	}

	args := []string{"-background"}
	if subdomain != "" {
		args = append(args, "-subdomain", subdomain)
	}
	if ports != "" {
		args = append(args, "-ports", ports)
	}
	if targetHost != "" {
		args = append(args, "-target-host", targetHost)
	}

	cmd := exec.Command(execPath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start background tunnel: %w", err)
	}

	// Wait up to 2 seconds for state file to materialize
	var state *ClientState
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".lfr-tunnel")

	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		files, err := os.ReadDir(stateDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasPrefix(f.Name(), "lfr-tunnel-") && strings.HasSuffix(f.Name(), ".state") {
				var cs ClientState
				statePath := filepath.Join(stateDir, f.Name())
				data, err := os.ReadFile(statePath)
				if err == nil && json.Unmarshal(data, &cs) == nil && cs.PID == cmd.Process.Pid {
					state = &cs
					break
				}
			}
		}
		if state != nil {
			break
		}
	}

	if state == nil {
		return map[string]interface{}{
			"status":  "pending",
			"message": "Tunnel spawned in background, status unknown.",
			"pid":     cmd.Process.Pid,
		}, nil
	}

	return map[string]interface{}{
		"status":      "success",
		"pid":         state.PID,
		"subdomain":   state.Subdomain,
		"public_urls": state.PublicURLs,
	}, nil
}

// stopTunnel terminates background tunnels.
func stopTunnel(subdomain string) (interface{}, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to locate executable: %w", err)
	}

	args := []string{"-stop"}
	if subdomain != "" {
		args = append(args, "-subdomain", subdomain)
	}

	cmd := exec.Command(execPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute stop: %w, output: %s", err, string(output))
	}

	return map[string]interface{}{
		"status":  "success",
		"message": strings.TrimSpace(string(output)),
	}, nil
}

// listRequests gets the interceptor traffic list from the active inspector.
func listRequests(limit int) (interface{}, error) {
	url, err := getFirstActiveInspectorURL()
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("%s/api/state", url))
	if err != nil {
		return nil, fmt.Errorf("failed to query inspector history: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inspector responded with status %d", resp.StatusCode)
	}

	var state struct {
		History []interface{} `json:"history"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to decode inspector response: %w", err)
	}

	history := state.History
	if len(history) > limit {
		history = history[:limit]
	}

	return map[string]interface{}{"requests": history}, nil
}

// replayRequest requests request replay on the active inspector.
func replayRequest(requestID string) (interface{}, error) {
	url, err := getFirstActiveInspectorURL()
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]string{"id": requestID})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("%s/api/replay", url), "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to call replay on inspector: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inspector replay failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode replay response: %w", err)
	}

	return result, nil
}

// Helper methods

func getFirstActiveInspectorURL() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no active tunnels found (no state directory)")
	}

	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "lfr-tunnel-") && strings.HasSuffix(f.Name(), ".state") {
			statePath := filepath.Join(dir, f.Name())
			data, err := os.ReadFile(statePath)
			if err != nil {
				continue
			}
			var state ClientState
			if err := json.Unmarshal(data, &state); err == nil && isPIDRunning(state.PID) {
				return state.InspectorURL, nil
			}
		}
	}

	return "", fmt.Errorf("no active running tunnels found")
}

func queryInspectorInfo(url string) interface{} {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("%s/api/info", url))
	if err != nil {
		return map[string]interface{}{"status": "offline", "error": err.Error()}
	}
	defer resp.Body.Close() //nolint:errcheck

	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err == nil {
		return info
	}
	return map[string]interface{}{"status": "error"}
}

func isPIDRunning(pid int) bool {
	return client.IsPIDRunning(pid)
}

// JSON-RPC helper responses

func sendResult(id interface{}, result interface{}) {
	res := Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}
	b, _ := json.Marshal(res)
	if _, err := fmt.Fprintln(outputWriter, string(b)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func sendError(id interface{}, code int, message string) {
	res := Response{
		JSONRPC: "2.0",
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
		ID: id,
	}
	b, _ := json.Marshal(res)
	if _, err := fmt.Fprintln(outputWriter, string(b)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func sendToolSuccess(id interface{}, content interface{}) {
	text, _ := json.MarshalIndent(content, "", "  ")
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(text),
			},
		},
	}
	sendResult(id, result)
}

func sendToolError(id interface{}, errMsg string) {
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("ERROR: %s", errMsg),
			},
		},
		"isError": true,
	}
	sendResult(id, result)
}
