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
	// No example port: suggesting the first candidate's would be the very guess this refuses
	// to make, and for a container listing 443 first it would suggest a TLS port.
	return fmt.Sprintf("auto-discovery found %d Liferay containers (%s) and will not choose between them; "+
		"name the port of the one to share with -ports", len(e.Candidates), strings.Join(parts, "; "))
}

func joinPorts(ports []int) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

// ldmProjectLabel is the label LDM puts on every container of a project, naming the project.
const ldmProjectLabel = "com.liferay.ldm.project"

// publishedPortRegex extracts the host port from a `docker ps` mapping reachable on this machine:
// all interfaces ("0.0.0.0:8080->8080/tcp", ":::8080->", "[::]:8080->") or loopback
// ("127.0.0.1:8080->", "[::1]:8080->"). Loopback matters most: LDM binds a project's portal to
// 127.0.0.1, so before #2462 the instance itself was invisible to discovery. Anchored at the
// start of an entry, so a host address such as 10.0.0.0 is not read as 0.0.0.0. Ranges
// ("0.0.0.0:8080-8081->") are not matched. Captures the host port and the container port.
var publishedPortRegex = regexp.MustCompile(`(?:^|, )(?:0\.0\.0\.0:|127\.0\.0\.1:|:::|\[::\]:|\[::1\]:)(\d+)->(\d+)/`)

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
	out, err := cmdExecutor.Output("docker", "ps", "--format", `{{.Names}}||{{.Image}}||{{.Ports}}||{{.Label "`+ldmProjectLabel+`"}}`)
	if err != nil {
		return nil, err // Docker not installed or daemon not running
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var candidates []DiscoveredContainer
	var types []string

	for _, line := range lines {
		parts := strings.Split(line, "||")
		if len(parts) != 4 {
			continue
		}
		name, image, portMap, project := parts[0], parts[1], parts[2], parts[3]
		if ldmInfrastructure[name] {
			continue
		}
		// An LDM project is one instance however many containers it runs: its database, its
		// tunnel sidecar and one container per client extension all carry the project label
		// too. Only the container named after the project is the Liferay instance, so the
		// others are never candidates -- a project is not ambiguous with itself, and an
		// extension on 8080 is not mistaken for the portal.
		if project != "" && name != project {
			continue
		}

		containerType := ""
		nameImage := strings.ToLower(name + " " + image)
		if project != "" || strings.Contains(nameImage, "ldm") {
			containerType = "Docker (LDM)"
		} else if strings.Contains(nameImage, "liferay") || strings.Contains(nameImage, "dxp") {
			containerType = "Docker (Liferay)"
		}
		if containerType == "" || portMap == "" {
			continue
		}

		// Only HTTP(S) ports, not a container's database or JMX ports: a typical host port, or
		// any host port in front of Liferay's own 8080 -- LDM gives a second project 8081, and
		// filtering on the host port alone hid it, so two projects looked like one (#2462).
		// Once each: Docker lists a port for IPv4 and IPv6 alike.
		var ports []int
		seen := map[int]bool{}
		for _, match := range publishedPortRegex.FindAllStringSubmatch(portMap, -1) {
			p, err := strconv.Atoi(match[1])
			if err != nil || seen[p] {
				continue
			}
			if p == 8080 || p == 443 || p == 80 || p == 3000 || match[2] == "8080" {
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
