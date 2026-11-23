package ops

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Clients follow the install_dir their gateway publishes on /api/version (#2552), so a wrong value
// in one gateway's YAML moves every client of that gateway outside the EDR exclusion on its next
// upgrade. Each gateway has its own config copy and a client asks whichever one it is configured
// for, which may be an edge -- so two gateways that disagree send their users to two paths
// (#2554). This check compares what every gateway actually serves, with each other and with the
// rules requested in docs/infosec.md §3.

// defaultInfosecPath is the single source for the requested exclusion rules.
const defaultInfosecPath = "docs/infosec.md"

// defaultInstallDirs is where a client installs when its gateway publishes no install_dir for its
// platform: the installers' and the client's own fallback, and the gateway's (pkg/server/server.go).
var defaultInstallDirs = map[string]string{
	"macos_arm64":   posixDefaultInstallDir,
	"macos_amd64":   posixDefaultInstallDir,
	"linux_amd64":   posixDefaultInstallDir,
	"windows_amd64": `~\liferay\lfr-tunnel`,
}

const posixDefaultInstallDir = "~/liferay/lfr-tunnel"

// The OS names §3 uses in its per-platform headings.
const (
	osMacOS   = "macOS"
	osLinux   = "Linux"
	osWindows = "Windows"
)

// installDirAnswer is one gateway's answer. Err set means the gateway's setting is UNKNOWN: an
// unreachable gateway is not evidence that it agrees.
type installDirAnswer struct {
	Gateway string
	// Dirs maps a platform key to the install_dir the gateway serves for it. A platform missing
	// from the map is one the gateway publishes nothing for, so its clients use the default.
	Dirs map[string]string
	Err  error
}

// fetchInstallDirs asks one gateway's /api/version for its per-platform install_dir.
func fetchInstallDirs(client *http.Client, gatewayURL string) (dirs map[string]string, err error) {
	resp, err := client.Get(strings.TrimRight(gatewayURL, "/") + "/api/version")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			dirs, err = nil, cerr
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/api/version answered %s", resp.Status)
	}
	var body struct {
		ClientPlatforms map[string]struct {
			InstallDir string `json:"install_dir"`
		} `json:"client_platforms"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding /api/version: %w", err)
	}
	dirs = map[string]string{}
	for key, p := range body.ClientPlatforms {
		if p.InstallDir != "" {
			dirs[key] = p.InstallDir
		}
	}
	return dirs, nil
}

// alertsHeading matches the per-platform headings in §3, which tests/hooks/test-install-paths.sh
// already pins, so a rewording that would blind this check fails there first.
var alertsHeading = regexp.MustCompile(`^\*\*(macOS|Linux|Windows)\*\* \(requested, Alerts\):`)

// parseSection3Rules reads the requested rules per OS from docs/infosec.md §3: the fenced block
// that follows each platform's Alerts heading.
func parseSection3Rules(md []byte) (map[string][]string, error) {
	rules := map[string][]string{}
	inSection, inFence := false, false
	current := ""
	sc := bufio.NewScanner(bytes.NewReader(md))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			inSection = strings.HasPrefix(line, "## 3.")
			current = ""
			continue
		}
		if !inSection {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence, current = false, ""
			} else if current != "" {
				inFence = true
			}
			continue
		}
		if inFence {
			if trimmed != "" {
				rules[current] = append(rules[current], trimmed)
			}
			continue
		}
		if m := alertsHeading.FindStringSubmatch(trimmed); m != nil {
			current = m[1]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, osName := range []string{osMacOS, osLinux, osWindows} {
		if len(rules[osName]) == 0 {
			return nil, fmt.Errorf("no %s rules found under \"**%s** (requested, Alerts):\" in §3", osName, osName)
		}
	}
	return rules, nil
}

// platformOS maps a gateway platform key to the OS whose §3 rules cover it.
func platformOS(key string) string {
	switch {
	case strings.HasPrefix(key, "macos_"):
		return osMacOS
	case strings.HasPrefix(key, "linux_"):
		return osLinux
	case strings.HasPrefix(key, "windows_"):
		return osWindows
	}
	return ""
}

// executableRuleForm turns an install_dir into the path of the executable it installs, written
// the way §3 writes rules: the home folder becomes the root plus the `*` username wildcard, and
// Windows separators become `/` so path.Match can compare it. Case is kept, because §3 requests
// lowercase rules and the EDR may match case-sensitively.
func executableRuleForm(dir, osName string) string {
	root, exe := "/Users/*", "lfr-tunnel"
	switch osName {
	case osLinux:
		root = "/home/*"
	case osWindows:
		root, exe = "C:/Users/*", "lfr-tunnel.exe"
		dir = strings.ReplaceAll(dir, `\`, "/")
	}
	if dir == "~" {
		dir = root
	} else if strings.HasPrefix(dir, "~/") {
		dir = root + dir[1:]
	}
	return strings.TrimRight(dir, "/") + "/" + exe
}

// coveredBy reports the §3 rule that covers a platform's install_dir, or "" if none does. `*`
// matches one path segment, which is the narrow reading: §3 notes the Windows `*` may span more.
func coveredBy(dir, osName string, rules map[string][]string) string {
	exe := executableRuleForm(dir, osName)
	for _, rule := range rules[osName] {
		pattern := rule
		if osName == osWindows {
			pattern = strings.ReplaceAll(rule, `\`, "/")
		}
		if ok, err := path.Match(pattern, exe); err == nil && ok {
			return rule
		}
	}
	return ""
}

// installDirReport is the verdict. Errors are disagreements and uncovered paths; NotVerified
// names the gateways whose setting is unknown.
type installDirReport struct {
	Lines       []string
	Errors      int
	NotVerified []string
}

// evaluateInstallDirs compares every reachable gateway's effective install_dir per platform, and
// checks each value against the §3 rules.
func evaluateInstallDirs(answers []installDirAnswer, rules map[string][]string) installDirReport {
	var r installDirReport

	platforms := map[string]bool{}
	for key := range defaultInstallDirs {
		platforms[key] = true
	}
	for _, a := range answers {
		if a.Err != nil {
			r.NotVerified = append(r.NotVerified, a.Gateway)
			r.Lines = append(r.Lines, fmt.Sprintf("[NOT VERIFIED] %s: %v", a.Gateway, a.Err))
			continue
		}
		for key := range a.Dirs {
			platforms[key] = true
		}
	}
	keys := make([]string, 0, len(platforms))
	for key := range platforms {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		lines, errs := checkPlatformInstallDir(key, servedInstallDirs(answers, key), rules)
		r.Lines = append(r.Lines, lines...)
		r.Errors += errs
	}
	return r
}

// servedInstallDirs maps each effective install_dir for one platform to the reachable gateways
// serving it. A gateway that lists nothing for the platform serves the default.
func servedInstallDirs(answers []installDirAnswer, key string) map[string][]string {
	served := map[string][]string{}
	for _, a := range answers {
		if a.Err != nil {
			continue
		}
		dir, ok := a.Dirs[key]
		if !ok {
			dir = defaultInstallDirs[key]
		}
		served[dir] = append(served[dir], a.Gateway)
	}
	return served
}

// checkPlatformInstallDir reports one platform: a disagreement between gateways, and every value
// that §3 does not cover.
func checkPlatformInstallDir(key string, served map[string][]string, rules map[string][]string) (lines []string, errs int) {
	values := make([]string, 0, len(served))
	for v := range served {
		values = append(values, v)
	}
	sort.Strings(values)

	if len(values) > 1 {
		errs++
		lines = append(lines, fmt.Sprintf("[ERROR] %s: gateways disagree, so clients move to different paths:", key))
		for _, v := range values {
			lines = append(lines, fmt.Sprintf("          %s <- %s", displayDir(v), strings.Join(served[v], ", ")))
		}
	}

	osName := platformOS(key)
	for _, v := range values {
		switch {
		case v == "":
			errs++
			lines = append(lines, fmt.Sprintf("[ERROR] %s: no install_dir and no default for this platform", key))
		case osName == "":
			errs++
			lines = append(lines, fmt.Sprintf("[ERROR] %s: %s -- no §3 rules exist for this platform", key, displayDir(v)))
		default:
			rule := coveredBy(v, osName, rules)
			if rule == "" {
				errs++
				lines = append(lines, fmt.Sprintf("[ERROR] %s: %s installs %s, which no %s rule in §3 covers",
					key, displayDir(v), executableRuleForm(v, osName), osName))
			} else if len(values) == 1 {
				lines = append(lines, fmt.Sprintf("[OK] %s: %s on every reachable gateway, covered by %s", key, displayDir(v), rule))
			}
		}
	}
	return lines, errs
}

// displayDir quotes a value without %q, which would double every Windows backslash.
func displayDir(dir string) string {
	return `"` + dir + `"`
}

// gatewaysFromSpec lists every gateway a client may be configured for: central under each zone's
// `tunnel` name, and every edge the DNS spec declares.
func gatewaysFromSpec(spec DNSSpec) []string {
	seen := map[string]bool{}
	for _, d := range spec.Domains {
		for _, r := range d.Records {
			if r.Type == "A" && strings.EqualFold(r.Name, "tunnel") && r.Value == centralPlaceholder {
				seen["https://"+strings.ToLower(r.Name+"."+d.Zone)] = true
			}
		}
	}
	for host := range spec.edgeAddresses() {
		seen["https://"+host] = true
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// CheckInstallDirCommand reports whether every gateway serves the same install_dir, covered by
// §3. Exit 1 on a disagreement or an uncovered path, 2 when nothing is wrong but a gateway could
// not be asked.
func CheckInstallDirCommand(args []string) {
	fs := flag.NewFlagSet("check-install-dir", flag.ExitOnError)
	specPath := fs.String("dns-spec", defaultDNSSpecPath, "committed DNS spec the gateway list is derived from")
	infosecPath := fs.String("infosec", defaultInfosecPath, "the doc whose §3 holds the requested exclusion rules")
	gatewaysFlag := fs.String("gateways", "", "comma-separated gateway URLs to ask instead of the DNS spec's")
	timeout := fs.Duration("timeout", 15*time.Second, "per-gateway request timeout")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops check-install-dir [-dns-spec path] [-infosec path] [-gateways url,url] [-timeout 15s]")
		fmt.Println("\nAsks every gateway's /api/version for the install_dir it publishes per platform,")
		fmt.Println("and fails if two gateways disagree or a path is not covered by a rule requested in")
		fmt.Println("docs/infosec.md §3. Clients follow that value on install and on upgrade (#2552), so")
		fmt.Println("a disagreement sends the users of different gateways to different paths.")
		fmt.Println("\nA gateway that cannot be asked is NOT VERIFIED, never counted as agreeing.")
		fmt.Println("Exit 0: all agree and are covered. 1: disagreement or uncovered path. 2: not verified.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	md, err := os.ReadFile(*infosecPath)
	CheckFatal(err, "Failed to read "+*infosecPath)
	rules, err := parseSection3Rules(md)
	CheckFatal(err, "Failed to read the §3 rules from "+*infosecPath)

	var gateways []string
	if *gatewaysFlag != "" {
		for _, g := range strings.Split(*gatewaysFlag, ",") {
			if g = strings.TrimSpace(g); g != "" {
				gateways = append(gateways, g)
			}
		}
	} else {
		specData, err := os.ReadFile(*specPath)
		CheckFatal(err, "Failed to read the DNS spec at "+*specPath)
		spec, err := parseDNSSpec(specData)
		CheckFatal(err, "Failed to parse the DNS spec")
		gateways = gatewaysFromSpec(spec)
	}
	if len(gateways) == 0 {
		CheckFatal(fmt.Errorf("no gateways to ask"), "Nothing to check")
	}

	fmt.Printf("=== Checking install_dir on %d gateway(s) against %s §3 ===\n", len(gateways), *infosecPath)
	client := &http.Client{Timeout: *timeout}
	answers := make([]installDirAnswer, 0, len(gateways))
	for _, g := range gateways {
		dirs, err := fetchInstallDirs(client, g)
		answers = append(answers, installDirAnswer{Gateway: g, Dirs: dirs, Err: err})
	}

	report := evaluateInstallDirs(answers, rules)
	for _, l := range report.Lines {
		fmt.Println(l)
	}
	fmt.Println()
	if report.Errors > 0 {
		fmt.Printf("FAILED: %d error(s). Do not change install_dir until this is clean.\n", report.Errors)
		os.Exit(1)
	}
	if len(report.NotVerified) > 0 {
		fmt.Printf("NOT VERIFIED: %d gateway(s) could not be asked (%s). No reachable gateway is wrong; these are unknown.\n",
			len(report.NotVerified), strings.Join(report.NotVerified, ", "))
		os.Exit(2)
	}
	fmt.Println("All gateways agree, and every path is covered by §3.")
}
