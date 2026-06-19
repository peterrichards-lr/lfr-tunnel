package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jpillora/chisel/client"
	"gopkg.in/yaml.v3"
	"io/fs"
	"lfr-tunnel/pkg/config"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	Status       string   `json:"status"`
	SessionToken string   `json:"session_token,omitempty"`
	Remotes      []string `json:"remotes,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	Error        string   `json:"error,omitempty"`
	Warning      string   `json:"warning,omitempty"`
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
			return nil, fmt.Errorf("gateway error (%d): %s", resp.StatusCode, regResp.Error)
		}
		return nil, fmt.Errorf("gateway returned status %d", resp.StatusCode)
	}

	if regResp.Status != "success" {
		return nil, fmt.Errorf("registration status: %s, error: %s", regResp.Status, regResp.Error)
	}

	return &regResp, nil
}

// RunClient runs the embedded Chisel client.
func RunClient(ctx context.Context, serverURL string, token string, remotes []string, publicURLs []string) error {
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

	// Log client status
	log.Printf("[Client] Establised lease. Connecting tunnels to %s...", serverURL)
	for _, remote := range remotes {
		log.Printf("[Client] Forwarding remote port: %s", remote)
	}

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
