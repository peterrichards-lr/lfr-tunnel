package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jpillora/chisel/client"
	"gopkg.in/yaml.v3"
	"io"
	"io/fs"
	"lfr-tunnel/pkg/config"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// PortMapping matches the server DTO for port allocations.
type PortMapping struct {
	LocalPort  int    `json:"local_port"`
	NameSuffix string `json:"name_suffix,omitempty"`
}

// RegisterRequest matches the server's registration payload format.
type RegisterRequest struct {
	SubdomainPrefix string            `json:"subdomain_prefix"`
	Ports           []PortMapping     `json:"ports"`
	AuthToken       string            `json:"auth_token"`
	RateLimit       int               `json:"rate_limit,omitempty"`
	BasicAuth       string            `json:"basic_auth,omitempty"`
	AddedHeaders    map[string]string `json:"added_headers,omitempty"`
	ClientVersion   string            `json:"client_version,omitempty"`
	ClientOS        string            `json:"client_os,omitempty"`
}

// RegisterResponse matches the server DTO for response.
type RegisterResponse struct {
	Status          string   `json:"status"`
	SessionToken    string   `json:"session_token,omitempty"`
	SubdomainPrefix string   `json:"subdomain_prefix,omitempty"`
	Remotes         []string `json:"remotes,omitempty"`
	Domains         []string `json:"domains,omitempty"`
	Error           string   `json:"error,omitempty"`
	Warning         string   `json:"warning,omitempty"`
	PortalURL       string   `json:"portal_url,omitempty"`
}

type RegistrationError struct {
	StatusCode int
	Message    string
	PortalURL  string
}

func (e *RegistrationError) Error() string {
	if e.PortalURL != "" {
		return fmt.Sprintf("gateway error (%d): %s (Portal: %s)", e.StatusCode, e.Message, e.PortalURL)
	}
	return fmt.Sprintf("gateway error (%d): %s", e.StatusCode, e.Message)
}

// DetectWorkspacePorts walks the filesystem looking for client-extension.yaml files
// and extracts active developer ports.
func DetectWorkspacePorts(rootDir string) ([]PortMapping, error) {
	var mappings []PortMapping
	seenPorts := make(map[int]bool)

	// Always default to including local Liferay instance port 8080 as primary
	mappings = append(mappings, PortMapping{LocalPort: 8080})
	seenPorts[8080] = true

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip directory read errors
		}

		if d.IsDir() {
			// Skip common large development / build / configuration directories
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "build" ||
				name == "dist" || name == ".gradle" || name == "platform" ||
				name == "configs" || name == "osgi" {
				return filepath.SkipDir
			}
			return nil
		}

		// Look for Liferay Client Extension configurations
		if d.Name() == "client-extension.yaml" || d.Name() == "client-extension.yml" {
			file, err := os.Open(path)
			if err != nil {
				return nil // Skip files we cannot read
			}
			defer file.Close() //nolint:errcheck

			var data map[string]interface{}
			dec := yaml.NewDecoder(file)
			if err := dec.Decode(&data); err == nil {
				for extKey, extVal := range data {
					m, ok := extVal.(map[string]interface{})
					if !ok {
						continue
					}

					portVal, exists := m["port"]
					if !exists {
						continue
					}

					var port int
					switch v := portVal.(type) {
					case int:
						port = v
					case float64:
						port = int(v)
					case string:
						port, _ = strconv.Atoi(v)
					}

					if port > 0 && !seenPorts[port] {
						seenPorts[port] = true
						// Use the client-extension key as the subdomain suffix
						mappings = append(mappings, PortMapping{
							LocalPort:  port,
							NameSuffix: extKey,
						})
						log.Printf("[Client] Detected Liferay Client Extension port %d from: %s", port, path)
					}
				}
			}
		}
		return nil
	})

	return mappings, err
}

// RegisterTunnel performs the handshake with the server's registration endpoint.
func RegisterTunnel(serverURL string, authToken string, subdomain string, ports []PortMapping, rateLimit int, basicAuth string, addedHeaders map[string]string, clientOS string) (*RegisterResponse, error) {
	// Normalize server URL
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL
	}
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %v", err)
	}

	registerURL := fmt.Sprintf("%s://%s/api/register", parsedURL.Scheme, parsedURL.Host)

	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		Ports:           ports,
		AuthToken:       authToken,
		RateLimit:       rateLimit,
		BasicAuth:       basicAuth,
		AddedHeaders:    addedHeaders,
		ClientVersion:   config.Version,
		ClientOS:        clientOS,
	})
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(registerURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("registration request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return nil, fmt.Errorf("failed to decode server response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		if regResp.Error != "" {
			return nil, &RegistrationError{
				StatusCode: resp.StatusCode,
				Message:    regResp.Error,
				PortalURL:  regResp.PortalURL,
			}
		}
		return nil, &RegistrationError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("gateway returned status %d", resp.StatusCode),
		}
	}

	if regResp.Status != "success" {
		return nil, fmt.Errorf("registration status: %s, error: %s", regResp.Status, regResp.Error)
	}

	return &regResp, nil
}

// RunClient runs the embedded Chisel client.
func RunClient(ctx context.Context, serverURL string, token string, remotes []string, publicURLs []string, engine *InterceptorEngine) error {
	// 1. Ensure server URL starts with http/https
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL
	}

	// 2. Setup Chisel client config
	chiselCfg := &chclient.Config{
		Server:           serverURL + "/tunnel",
		Auth:             fmt.Sprintf("%s:%s", token, token),
		Remotes:          remotes,
		MaxRetryInterval: 10 * time.Second,
		MaxRetryCount:    -1, // Retry infinitely
	}

	// 3. Initialize Chisel client
	c, err := chclient.NewClient(chiselCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize chisel client: %v", err)
	}

	// Intercept logger to monitor connection state
	redirectChiselLogger(c, engine)

	// Log client status
	log.Printf("[Client] Establised lease. Connecting tunnels to %s...", serverURL)
	for _, remote := range remotes {
		log.Printf("[Client] Forwarding remote port: %s", remote)
	}

	// Start background latency tracker
	go func() {
		parsed, err := url.Parse(serverURL)
		if err != nil {
			return
		}
		host := parsed.Host
		if !strings.Contains(host, ":") {
			if parsed.Scheme == "https" {
				host = host + ":443"
			} else {
				host = host + ":80"
			}
		}

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				engine.mu.RLock()
				state := engine.ConnState
				engine.mu.RUnlock()

				if state == "connected" {
					t0 := time.Now()
					conn, err := net.DialTimeout("tcp", host, 3*time.Second)
					if err == nil {
						rtt := time.Since(t0).Milliseconds()
						_ = conn.Close()

						engine.mu.Lock()
						engine.LatencyLast = rtt
						engine.LatencyHistory = append(engine.LatencyHistory, rtt)
						if len(engine.LatencyHistory) > 60 {
							engine.LatencyHistory = engine.LatencyHistory[1:]
						}
						engine.mu.Unlock()
					}
				}
			}
		}
	}()

	// 4. Start the client
	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("chisel client error: %v", err)
	}

	// Print a clean, auto-clickable URL block after connection log sequence
	go func() {
		time.Sleep(500 * time.Millisecond)
		if ctx.Err() == nil {
			log.Println("[Client] ========================================================")
			log.Println("[Client] Tunnel is active and fully online!")
			log.Println("[Client] You can access your local environment at:")
			for _, u := range publicURLs {
				log.Printf("  %s", u)
			}
			log.Println("[Client] ========================================================")
		}
	}()

	// 5. Block until context done or wait error
	return c.Wait()
}

var latencyRegex = regexp.MustCompile(`Latency\s+([^)]+)`)

type logParserWriter struct {
	original io.Writer
	engine   *InterceptorEngine
}

func (w *logParserWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	w.parseMessage(msg)
	return w.original.Write(p)
}

func (w *logParserWriter) parseMessage(msg string) {
	w.engine.mu.Lock()
	defer w.engine.mu.Unlock()

	// Track transitions
	oldState := w.engine.ConnState

	if strings.Contains(msg, "Connecting to") {
		w.engine.ConnState = "connecting"
	} else if strings.Contains(msg, "Connected (Latency") {
		w.engine.ConnState = "connected"
		w.engine.UptimeStart = time.Now()
		w.engine.AuthValid = true
		w.engine.AuthErrorMessage = ""
		matches := latencyRegex.FindStringSubmatch(msg)
		if len(matches) > 1 {
			durStr := matches[1]
			dur, err := time.ParseDuration(durStr)
			if err == nil {
				ms := dur.Milliseconds()
				w.engine.LatencyLast = ms
				w.engine.LatencyHistory = append(w.engine.LatencyHistory, ms)
				if len(w.engine.LatencyHistory) > 60 {
					w.engine.LatencyHistory = w.engine.LatencyHistory[1:]
				}
			}
		}
	} else if strings.Contains(msg, "Disconnected") {
		w.engine.ConnState = "disconnected"
		w.engine.UptimeStart = time.Time{}
		if oldState == "connected" {
			w.engine.ReconnectCount++
		}
	} else if strings.Contains(msg, "Retrying in") {
		w.engine.ConnState = "reconnecting"
	} else if strings.Contains(msg, "Authentication failed") {
		w.engine.AuthValid = false
		w.engine.AuthErrorMessage = "Authentication failed"
		w.engine.ConnState = "disconnected"
	}
}

func redirectChiselLogger(c *chclient.Client, engine *InterceptorEngine) {
	if c == nil || c.Logger == nil {
		return
	}

	val := reflect.ValueOf(c.Logger).Elem()
	loggerField := val.FieldByName("logger")
	if !loggerField.IsValid() {
		return
	}

	ptrPtr := (**log.Logger)(unsafe.Pointer(loggerField.UnsafeAddr()))
	if ptrPtr == nil || *ptrPtr == nil {
		return
	}
	ptr := *ptrPtr

	parser := &logParserWriter{
		original: os.Stderr,
		engine:   engine,
	}
	ptr.SetOutput(parser)
}

// IsLiferayWorkspace checks if a directory contains structural signals of a Liferay workspace
// (such as client-extensions directory, gradlew, or gradle.properties).
func IsLiferayWorkspace(dir string) bool {
	// Check for client-extensions folder
	if fi, err := os.Stat(filepath.Join(dir, "client-extensions")); err == nil && fi.IsDir() {
		return true
	}
	// Check for gradlew file
	if _, err := os.Stat(filepath.Join(dir, "gradlew")); err == nil {
		return true
	}
	// Check for gradle.properties file
	if _, err := os.Stat(filepath.Join(dir, "gradle.properties")); err == nil {
		return true
	}
	return false
}

// ProbeLocalPorts scans the specified localhost ports and returns the ports that are active.
func ProbeLocalPorts(ports []int) []int {
	var active []int
	for _, port := range ports {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			active = append(active, port)
			_ = conn.Close()
		}
	}
	return active
}
