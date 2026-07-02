package client

import (
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// AutoDiscoverResult holds the results of an auto-discovery attempt
type AutoDiscoverResult struct {
	Host  string
	Ports []int
	Type  string // e.g. "Docker (LDM)", "Native Tomcat"
}

// AutoDiscoverTarget attempts to dynamically find running LDM or Liferay instances.
// It checks Docker containers first, then falls back to probing localhost ports.
func AutoDiscoverTarget() (*AutoDiscoverResult, error) {
	// 1. Try Docker Discovery
	result, err := discoverDocker()
	if err == nil && result != nil && len(result.Ports) > 0 {
		return result, nil
	}

	// 2. Try Native Probing on typical Liferay ports
	slog.Debug("[Discovery] No Docker instances found, falling back to local port probes...")
	activePorts := ProbeLocalPorts([]int{8080, 13000, 3000})
	if len(activePorts) > 0 {
		return &AutoDiscoverResult{
			Host:  "localhost",
			Ports: activePorts,
			Type:  "Native Service",
		}, nil
	}

	return nil, nil // Nothing discovered
}

// discoverDocker executes `docker ps` to find containers that look like Liferay/LDM.
func discoverDocker() (*AutoDiscoverResult, error) {
	cmd := exec.Command("docker", "ps", "--format", "{{.Names}}||{{.Image}}||{{.Ports}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err // Docker not installed or daemon not running
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var liferayPorts []int
	var containerType string

	// Regex to extract local published port from docker port mappings like:
	// "0.0.0.0:8080->8080/tcp" or ":::8080->8080/tcp"
	portRegex := regexp.MustCompile(`(?:0\.0\.0\.0|:::):(\d+)->`)

	for _, line := range lines {
		parts := strings.Split(line, "||")
		if len(parts) != 3 {
			continue
		}
		name, image, portMap := parts[0], parts[1], parts[2]

		isLiferay := false
		nameImage := strings.ToLower(name + " " + image)

		if strings.Contains(nameImage, "ldm") {
			isLiferay = true
			containerType = "Docker (LDM)"
		} else if strings.Contains(nameImage, "liferay") || strings.Contains(nameImage, "dxp") {
			isLiferay = true
			containerType = "Docker (Liferay)"
		}

		if isLiferay && portMap != "" {
			matches := portRegex.FindAllStringSubmatch(portMap, -1)
			for _, match := range matches {
				if len(match) == 2 {
					if p, err := strconv.Atoi(match[1]); err == nil {
						// Only collect HTTP/HTTPS typical ports, ignore internal DB/JMX ports if possible,
						// but usually LDM maps 8080 or 443. For now collect all published ports.
						// Actually, Liferay typically uses 8080.
						if p == 8080 || p == 443 || p == 80 || p == 3000 {
							liferayPorts = append(liferayPorts, p)
						}
					}
				}
			}
		}
	}

	if len(liferayPorts) > 0 {
		return &AutoDiscoverResult{
			Host:  "localhost",
			Ports: liferayPorts,
			Type:  containerType,
		}, nil
	}

	return nil, nil
}
