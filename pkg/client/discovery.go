package client

import (
	"errors"
	"fmt"
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
	// Container names the container the ports belong to, so the log can say what is being
	// published (#2462). Empty for a native service.
	Container string
}

// DiscoveredContainer is one container that looked like a Liferay instance.
type DiscoveredContainer struct {
	Name  string
	Ports []int
}

// AmbiguousDiscoveryError is returned when more than one container qualifies (#2462). Publishing
// all of them shared environments the user never meant to -- another LDM project on the same
// machine -- and picking one would be a guess, so discovery refuses and asks for -ports.
type AmbiguousDiscoveryError struct {
	Candidates []DiscoveredContainer
}

func (e *AmbiguousDiscoveryError) Error() string {
	parts := make([]string, 0, len(e.Candidates))
	for _, c := range e.Candidates {
		parts = append(parts, fmt.Sprintf("%s on %s", c.Name, joinPorts(c.Ports)))
	}
	return fmt.Sprintf("auto-discovery found %d Liferay containers (%s) and will not choose between them; "+
		"name the port to publish with -ports, e.g. -ports %d", len(e.Candidates), strings.Join(parts, "; "), e.Candidates[0].Ports[0])
}

func joinPorts(ports []int) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

// ldmInfrastructure are the containers LDM runs for every project on the machine: its global
// Traefik proxy (80/443), search, and the Docker socket proxy. They match "liferay" by name but
// are never the instance a user means to share (#2462).
var ldmInfrastructure = map[string]bool{
	"liferay-proxy-global":  true,
	"liferay-search-global": true,
	"liferay-docker-proxy":  true,
}

// CommandExecutor abstracts the execution of shell commands for easier testing
type CommandExecutor interface {
	Output(command string, args ...string) ([]byte, error)
}

type defaultExecutor struct{}

func (d defaultExecutor) Output(command string, args ...string) ([]byte, error) {
	return exec.Command(command, args...).Output()
}

var cmdExecutor CommandExecutor = defaultExecutor{}

// AutoDiscoverTarget attempts to dynamically find running LDM or Liferay instances.
// It checks Docker containers first, then falls back to probing localhost ports.
func AutoDiscoverTarget() (*AutoDiscoverResult, error) {
	// 1. Try Docker Discovery
	result, err := discoverDocker()
	var ambiguous *AmbiguousDiscoveryError
	if errors.As(err, &ambiguous) {
		// Not a reason to fall back to probing: 8080 is very likely one of the candidates.
		return nil, err
	}
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
	out, err := cmdExecutor.Output("docker", "ps", "--format", "{{.Names}}||{{.Image}}||{{.Ports}}")
	if err != nil {
		return nil, err // Docker not installed or daemon not running
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var candidates []DiscoveredContainer
	var types []string

	// Regex to extract local published port from docker port mappings like:
	// "0.0.0.0:8080->8080/tcp", ":::8080->8080/tcp" or "[::]:8080->8080/tcp". The IPv6 forms
	// were never matched before (the pattern wanted four colons); now they are, so a port
	// Docker lists for both families is de-duplicated below.
	portRegex := regexp.MustCompile(`(?:0\.0\.0\.0:|:::|\[::\]:)(\d+)->`)

	for _, line := range lines {
		parts := strings.Split(line, "||")
		if len(parts) != 3 {
			continue
		}
		name, image, portMap := parts[0], parts[1], parts[2]
		if ldmInfrastructure[name] {
			continue
		}

		containerType := ""
		nameImage := strings.ToLower(name + " " + image)
		if strings.Contains(nameImage, "ldm") {
			containerType = "Docker (LDM)"
		} else if strings.Contains(nameImage, "liferay") || strings.Contains(nameImage, "dxp") {
			containerType = "Docker (Liferay)"
		}
		if containerType == "" || portMap == "" {
			continue
		}

		// Only the typical HTTP(S) ports, not a container's database or JMX ports. Once each:
		// Docker lists a port for IPv4 and IPv6 alike.
		var ports []int
		seen := map[int]bool{}
		for _, match := range portRegex.FindAllStringSubmatch(portMap, -1) {
			p, err := strconv.Atoi(match[1])
			if err != nil || seen[p] {
				continue
			}
			if p == 8080 || p == 443 || p == 80 || p == 3000 {
				seen[p] = true
				ports = append(ports, p)
			}
		}
		if len(ports) > 0 {
			candidates = append(candidates, DiscoveredContainer{Name: name, Ports: ports})
			types = append(types, containerType)
		}
	}

	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return &AutoDiscoverResult{
			Host:      "localhost",
			Ports:     candidates[0].Ports,
			Type:      types[0],
			Container: candidates[0].Name,
		}, nil
	default:
		return nil, &AmbiguousDiscoveryError{Candidates: candidates}
	}
}
