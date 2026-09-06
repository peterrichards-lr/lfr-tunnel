package ops

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"lfr-tunnel/pkg/config"

	"gopkg.in/yaml.v3"
)

// Checking a live gateway config against what this repo actually says (#1465).
//
// Registering an edge is a documented MANUAL step -- edge_setup_guide.md says outright that there
// is no automated tool, and that the operator should SSH to the control plane and hand-edit
// edge_nodes. #1449 was the predictable result: three of four urls still named the retired
// aws-edge-* hosts, which resolve through the wildcard to central itself, so central advertised
// its own address as three separate regions for weeks. Nothing checked, because there was
// nothing to check against.
//
// scripts/liferay/dns/lfr-demo-production.yaml IS the authoritative record of which edges exist
// and where (#941), and it is already committed. So the check needs no new secret, and records
// nothing about the deployment in this repo -- it reads the live file, compares, and reports.
//
// SECRECY RULE, and it is not negotiable: this reads a file holding token hashes, SMTP
// credentials, webhook URLs and an admin address. Findings are reported BY KEY. No value whose
// key looks like a credential is ever printed, logged, written to disk, or included in a diff.
// The schema is public -- pkg/config is an MIT-licensed Go package -- but the values are not.

// secretishKey matches config keys whose values must never be echoed.
//
// Deliberately broad: a false positive costs a redaction where none was needed, a false negative
// puts a credential in a log. `hash` is included because a token_hash is a credential artefact --
// publishing the hash of a shared secret invites an offline attack on it.
var secretishKey = regexp.MustCompile(`(?i)token|secret|password|passwd|key|hash|credential|dsn|smtp|api|webhook|slack|teams|email`)

// redact renders a value safely: its length only, never its content.
func redact(key, value string) string {
	if secretishKey.MatchString(key) {
		if value == "" {
			return "<empty>"
		}
		return fmt.Sprintf("<redacted, %d chars>", len(value))
	}
	return value
}

// DNSSpec is the committed DNS record set, read for the edge addresses it declares.
type DNSSpec struct {
	Domains []struct {
		Zone    string `yaml:"zone"`
		Records []struct {
			Name  string `yaml:"name"`
			Type  string `yaml:"type"`
			Value string `yaml:"value"`
		} `yaml:"records"`
	} `yaml:"domains"`
}

// hostAddresses maps every fully-qualified host the spec declares an A record for to its
// address. Wildcard and apex entries are skipped: they are what made #1449 invisible, because a
// name with no record of its own still resolves through `*` and answers -- as central.
func (s DNSSpec) hostAddresses() map[string]string {
	out := map[string]string{}
	for _, d := range s.Domains {
		for _, r := range d.Records {
			if r.Type != "A" || r.Name == "@" || r.Name == "*" || r.Name == "" {
				continue
			}
			out[strings.ToLower(r.Name+"."+d.Zone)] = r.Value
		}
	}
	return out
}

// centralPlaceholder is what the spec uses for records pointing at the control plane. It is a
// placeholder rather than a literal precisely because central's address is supplied at apply
// time -- so an edge url resolving to it means the url names central, which is #1449.
const centralPlaceholder = "${IPV4}"

// liveConfig is the subset of a gateway config this check reads. Deliberately narrow: anything
// it does not need, it does not parse, and therefore cannot accidentally print.
type liveConfig struct {
	EdgeNodes []struct {
		ID  string `yaml:"id"`
		URL string `yaml:"url"`
	} `yaml:"edge_nodes"`
}

// liveConfigWithCP reads only control_plane_url, which is set on an edge and empty on central and
// therefore doubles as the "is this an edge" signal. Narrow on purpose: see the secrecy rule.
type liveConfigWithCP struct {
	ControlPlaneURL string `yaml:"control_plane_url"`
}

// Severities. Named because they are compared as well as printed, and a typo in a comparison
// would silently stop counting a finding as fatal.
const (
	severityError   = "error"
	severityWarning = "warning"
)

// DriftFinding is one problem found. Message is already safe to print.
type DriftFinding struct {
	Severity string // severityError or severityWarning
	Key      string
	Message  string
}

// CheckEdgeNodeDrift compares a live config's edge_nodes against the committed DNS spec.
//
// Two distinct failures, and the second is the one that actually bit:
//
//  1. A url whose host the spec does not declare at all. Either the spec is stale or the config
//     names something that was never provisioned.
//  2. A url whose host the spec points at CENTRAL. This is #1449 exactly, and note that a
//     "does it resolve?" check passes it happily -- the name resolved fine, it just resolved to
//     the wrong machine through the wildcard.
func CheckEdgeNodeDrift(configYAML, specYAML []byte) ([]DriftFinding, error) {
	var live liveConfig
	if err := yaml.Unmarshal(configYAML, &live); err != nil {
		return nil, fmt.Errorf("parsing the live config: %w", err)
	}
	var spec DNSSpec
	if err := yaml.Unmarshal(specYAML, &spec); err != nil {
		return nil, fmt.Errorf("parsing the DNS spec: %w", err)
	}

	known := spec.hostAddresses()
	if len(known) == 0 {
		return nil, fmt.Errorf("the DNS spec declares no per-host A records, so there is nothing to check against")
	}

	var findings []DriftFinding
	for _, node := range live.EdgeNodes {
		if strings.TrimSpace(node.URL) == "" {
			findings = append(findings, DriftFinding{
				Severity: severityWarning,
				Key:      "edge_nodes[" + node.ID + "].url",
				Message:  "no url configured, so central cannot route to this edge at all",
			})
			continue
		}
		host := urlHost(node.URL)
		if host == "" {
			findings = append(findings, DriftFinding{
				Severity: severityError,
				Key:      "edge_nodes[" + node.ID + "].url",
				Message:  fmt.Sprintf("%q is not a parseable URL", node.URL),
			})
			continue
		}

		addr, declared := known[host]
		switch {
		case !declared:
			findings = append(findings, DriftFinding{
				Severity: severityError,
				Key:      "edge_nodes[" + node.ID + "].url",
				Message: fmt.Sprintf(
					"host %q has no A record in the DNS spec. If it resolves at all it is doing so "+
						"through the zone's wildcard, which points at the control plane -- so central "+
						"would route this edge's traffic to itself (#1449)", host),
			})
		case addr == centralPlaceholder:
			findings = append(findings, DriftFinding{
				Severity: severityError,
				Key:      "edge_nodes[" + node.ID + "].url",
				Message: fmt.Sprintf(
					"host %q is declared in the DNS spec as the control plane, not as an edge. "+
						"Central would route this edge's traffic to itself (#1449)", host),
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool { return findings[i].Key < findings[j].Key })
	return findings, nil
}

// urlHost extracts a lowercase hostname, tolerating a bare host with no scheme.
func urlHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// UnknownTopLevelKeys reports keys present in a live config that the given set does not know
// about. A typo'd key is silently inert -- yaml.v3 does not complain by default -- so a setting
// an operator believes is applied may never have been.
//
// Values are never returned, only key names, so this cannot leak.
func UnknownTopLevelKeys(configYAML []byte, knownKeys map[string]bool) ([]string, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(configYAML, &raw); err != nil {
		return nil, fmt.Errorf("parsing the live config: %w", err)
	}
	var unknown []string
	for k := range raw {
		if !knownKeys[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown, nil
}

// knownServerConfigKeys derives the valid top-level key set from the struct itself, so it cannot
// drift from the code the way a hand-maintained list would -- which is the entire complaint this
// check exists to answer.
func knownServerConfigKeys() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(config.ServerConfig{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		if name := strings.Split(tag, ",")[0]; name != "" {
			keys[name] = true
		}
	}
	return keys
}

// expectedConfigOwner reads the service user from the committed systemd unit rather than
// hardcoding it, because the setup guide is explicit that a deployment may run the daemon as
// some other user -- and because deriving it from the repo is the point of #1452.
//
// Returns "" if the unit cannot be read, in which case the owner check is skipped LOUDLY rather
// than silently assumed correct.
func expectedConfigOwner(unitPath string) string {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return ""
	}
	var user, group string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "User="):
			user = strings.TrimPrefix(line, "User=")
		case strings.HasPrefix(line, "Group="):
			group = strings.TrimPrefix(line, "Group=")
		}
	}
	if user == "" {
		return ""
	}
	if group == "" {
		group = user
	}
	return user + ":" + group
}

// realIPKey names the finding, so the three places that report on it cannot drift apart.
const realIPKey = "nginx set_real_ip_from"

// realIPDirective extracts the addresses an nginx config trusts as forwarding proxies. There is
// more than one of them since #1750, so every match has to be read -- taking only the first made
// this check depend on the order renderRealIPBlock happens to emit them in.
var realIPDirective = regexp.MustCompile(`(?m)^\s*set_real_ip_from\s+([^;]+);`)

// trustedRealIPAddrs is every address a live nginx config trusts, in the order it names them.
func trustedRealIPAddrs(nginxConf string) []string {
	var out []string
	for _, m := range realIPDirective.FindAllStringSubmatch(nginxConf, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// checkEdgeRealIP reports whether an edge trusts the control plane's address, so a request the
// control plane forwards is attributed to the visitor rather than to central (#1450).
//
// Baking an address into four edges' nginx is exactly the shape that goes stale unnoticed -- the
// same shape as the edge_nodes urls that named retired hosts for weeks (#1449) -- so it is
// checked rather than assumed.
//
// Only meaningful on an edge. control_plane_url is set there and empty on central, so its
// presence in the live config is the signal.
func checkEdgeRealIP(controlPlaneURL, nginxConf string) []DriftFinding {
	if strings.TrimSpace(controlPlaneURL) == "" {
		return nil // central: nothing forwards to it
	}

	trusted := trustedRealIPAddrs(nginxConf)
	if len(trusted) == 0 {
		return []DriftFinding{{
			Severity: severityWarning,
			Key:      realIPKey,
			Message: "this edge does not trust the control plane's address, so a request the control " +
				"plane forwards is attributed to CENTRAL rather than to the visitor -- the per-tunnel " +
				"IP whitelist, the rate limiter's auto-ban and audit entries all name central (#1450). " +
				"Re-run reconcile-nginx with -trusted-proxy",
		}}
	}

	findings := checkRealIPMatchesControlPlane(controlPlaneURL, trusted)

	// Trusting the control plane is necessary but not sufficient. nginx's recursive walk stops at
	// the first untrusted address from the right, and the chain the control plane sends ends with
	// its own loopback peer, so an edge that trusts central alone attributes the visitor to
	// 127.0.0.1 (#1750). An edge reconciled before that fix looks correct to the check above.
	if !slices.Contains(trusted, nginxLoopbackTrustedProxy) {
		findings = append(findings, DriftFinding{
			Severity: severityWarning,
			Key:      realIPKey,
			Message: fmt.Sprintf(
				"trusts %s but not %s, so nginx's recursive real_ip walk stops at the loopback entry "+
					"the control plane appends and attributes a forwarded request to %s rather than to "+
					"the visitor (#1750). Re-run reconcile-nginx to render the current template",
				strings.Join(trusted, ", "), nginxLoopbackTrustedProxy, nginxLoopbackTrustedProxy),
		})
	}
	return findings
}

// checkRealIPMatchesControlPlane reports whether any trusted address is still the control plane's.
func checkRealIPMatchesControlPlane(controlPlaneURL string, trusted []string) []DriftFinding {
	host := urlHost(controlPlaneURL)
	if host == "" {
		return nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return []DriftFinding{{
			Severity: severityWarning,
			Key:      realIPKey,
			Message: fmt.Sprintf("trusts %s, but %q could not be resolved to compare against (%v)",
				strings.Join(trusted, ", "), host, err),
		}}
	}
	for _, a := range addrs {
		if slices.Contains(trusted, a) {
			return nil
		}
	}
	// A warning rather than an error: name resolution from an operator's machine is not
	// authoritative, and a split-horizon or stale answer must not fail a check that is otherwise
	// reporting real problems.
	return []DriftFinding{{
		Severity: severityWarning,
		Key:      realIPKey,
		Message: fmt.Sprintf(
			"trusts %s, but %s currently resolves to %s. If the control plane moved, forwarded "+
				"requests are being attributed to the wrong address (#1450) -- re-run reconcile-nginx "+
				"with the new -trusted-proxy. Resolution from this machine is not authoritative, so "+
				"confirm before acting",
			strings.Join(trusted, ", "), host, strings.Join(addrs, ", ")),
	}}
}

// CheckConfigCommand reports drift between a live gateway config and this repo.
//
// Reads the live file over SSH and never writes it. Output is by key, with any secret-looking
// value redacted -- see the secrecy rule at the top of this file. Exits non-zero when it finds
// an error-severity problem, so this can gate a deploy later.
func CheckConfigCommand(args []string) {
	fs := flag.NewFlagSet("check-config", flag.ExitOnError)
	specPath := fs.String("dns-spec", "scripts/liferay/dns/lfr-demo-production.yaml",
		"committed DNS spec to validate edge_nodes urls against")
	remotePath := fs.String("remote-config", "/etc/lfr-tunneld/server-config.yaml",
		"path to the gateway config on the target")
	unitPath := fs.String("unit", "resources/server/lfr-tunneld.service",
		"committed systemd unit, read for the User=/Group= the config must be owned by")
	identityFile := fs.String("i", "", "path to SSH private key file")
	flagUser := fs.String("u", "", "SSH username on the target")
	flagHost := fs.String("s", "", "SSH host of the target")
	flagTarget := fs.String("target", "", "named target from a multi-target lfr-tunnel-ops.yaml")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops check-config [-dns-spec path] [-remote-config path] [-i key] [-u user] [-s host] [-target name]")
		fmt.Println("\nCompares a live gateway config against what this repo declares, and reports drift.")
		fmt.Println("\nRegistering an edge is a manual step (see docs/server/edge_setup_guide.md), so")
		fmt.Println("edge_nodes urls are typed by hand and nothing checked them -- which is how three")
		fmt.Println("of four came to name retired hosts that resolve, through the zone wildcard, to")
		fmt.Println("the control plane itself (#1449). This checks them against the committed DNS")
		fmt.Println("spec, which is already the authoritative record of which edges exist.")
		fmt.Println("\nReads the live file and never writes it. Findings are reported BY KEY: no value")
		fmt.Println("whose key looks like a credential is printed, because this file holds token")
		fmt.Println("hashes, SMTP credentials and webhook URLs.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	specYAML, err := os.ReadFile(*specPath)
	CheckFatal(err, "Failed to read the DNS spec at "+*specPath)

	target, err := ResolveDeployTarget(*flagUser, *flagHost, *identityFile, *flagTarget)
	CheckFatal(err, "Failed to resolve target")
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	fmt.Printf("=== Checking %s:%s against %s ===\n", sshTarget, *remotePath, *specPath)

	configYAML, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget,
		"sudo cat "+*remotePath)
	CheckFatal(err, "Failed to read the live config")

	errors, warnings := reportConfigFindings([]byte(configYAML), specYAML)

	// Ownership and mode, which edge_setup_guide.md flags as easy to get wrong and silently
	// fatal: root:root locks out the service user and crash-loops on the NEXT restart, so the
	// mistake surfaces long after it was made.
	errors += checkConfigFilePermissions(target, sshTarget, *remotePath, *unitPath)

	// Edge only: does this node still trust the control plane's address? Reads the nginx config
	// the reconcile writes, so it verifies what is actually serving rather than what was intended.
	var live liveConfigWithCP
	if err := yaml.Unmarshal([]byte(configYAML), &live); err == nil && live.ControlPlaneURL != "" {
		nginxTarget, _ := nginxRemotePaths(RoleEdge)
		if nginxConf, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget,
			"sudo cat "+nginxTarget+" 2>/dev/null || true"); err == nil {
			for _, f := range checkEdgeRealIP(live.ControlPlaneURL, nginxConf) {
				fmt.Printf("[%s] %s: %s\n", strings.ToUpper(f.Severity), f.Key, f.Message)
				if f.Severity == severityError {
					errors++
				} else {
					warnings++
				}
			}
		}
	}

	fmt.Println()
	if errors > 0 {
		fmt.Printf("FAILED: %d error-severity finding(s).\n", errors)
		os.Exit(1)
	}
	// "No drift found" under a warning would be a lie of omission: warnings are the findings an
	// operator most needs to read, precisely because they do not stop the exit code.
	if warnings > 0 {
		fmt.Printf("No errors, but %d warning(s) above -- read them.\n", warnings)
		return
	}
	fmt.Println("No drift found.")
}

// reportConfigFindings prints the findings and returns how many were error- and warning-severity.
func reportConfigFindings(configYAML, specYAML []byte) (int, int) {
	errors, warnings := 0, 0

	findings, err := CheckEdgeNodeDrift(configYAML, specYAML)
	CheckFatal(err, "Failed to check edge_nodes")
	if len(findings) == 0 {
		fmt.Println("edge_nodes: every url matches a host the DNS spec declares as an edge.")
	}
	for _, f := range findings {
		fmt.Printf("[%s] %s: %s\n", strings.ToUpper(f.Severity), f.Key, f.Message)
		if f.Severity == severityError {
			errors++
		} else {
			warnings++
		}
	}

	unknown, err := UnknownTopLevelKeys(configYAML, knownServerConfigKeys())
	CheckFatal(err, "Failed to check for unknown keys")
	for _, k := range unknown {
		// A warning, not an error: a key this binary does not know may simply be newer or older
		// than the config, and failing on that would make the check unusable mid-upgrade.
		fmt.Printf("[WARNING] %s: not a key this build recognises, so it is being ignored entirely -- check for a typo\n", k)
		warnings++
	}

	return errors, warnings
}

// checkConfigFilePermissions reports ownership and mode problems, returning how many were
// error-severity.
//
// Ownership matters more than mode and is the failure docs/server/edge_setup_guide.md calls out:
// root:root with mode 600 LOOKS correct, locks the service user out entirely, and crash-loops the
// daemon on its next restart -- so the mistake surfaces long after it was made.
func checkConfigFilePermissions(target DeployTarget, sshTarget, remotePath, unitPath string) int {
	errors := 0
	// sudo, because the file is mode 600 owned by the service user -- without it stat fails
	// with permission denied and this check silently does not happen, which is how the first
	// version of it behaved.
	if stat, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget,
		"sudo stat -c '%U:%G %a' "+remotePath); err == nil {
		fmt.Printf("\nfile: %s owner/mode %s\n", remotePath, strings.TrimSpace(stat))
		fields := strings.Fields(strings.TrimSpace(stat))
		if len(fields) == 2 && fields[1] != "600" {
			fmt.Printf("[ERROR] mode is %s; expected 600 -- this file holds token hashes and SMTP credentials\n", fields[1])
			errors++
		}
		// Ownership matters more than mode here, and is the failure the setup guide calls out:
		// root:root with mode 600 locks the service user out entirely, and the daemon crash-loops
		// on its NEXT restart -- long after the mistake was made. Mode 600 alone looks correct,
		// which is why this needs checking separately.
		want := expectedConfigOwner(unitPath)
		switch {
		case want == "":
			fmt.Printf("[WARNING] could not read %s, so the expected owner is unknown and ownership was NOT checked\n", unitPath)
		case len(fields) == 2 && fields[0] != want:
			fmt.Printf("[ERROR] owner is %s; %s runs as %s, so the daemon cannot read its own config and will crash-loop on the next restart\n",
				fields[0], unitPath, want)
			errors++
		}
	} else {
		fmt.Printf("\n[WARNING] could not stat %s, so ownership and mode were NOT checked: %v\n", remotePath, err)
	}

	return errors
}

// parseDNSSpec unmarshals the committed DNS spec, shared by the drift check and the edge_nodes
// renderer so the two cannot disagree about what the spec says.
func parseDNSSpec(data []byte) (DNSSpec, error) {
	var spec DNSSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return DNSSpec{}, fmt.Errorf("parsing the DNS spec: %w", err)
	}
	return spec, nil
}
