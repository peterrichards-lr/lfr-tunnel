package ops

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// realSection3Rules reads the rules from the committed docs/infosec.md, so these tests fail when
// §3 and the defaults part company.
func realSection3Rules(t *testing.T) map[string][]string {
	t.Helper()
	md, err := os.ReadFile("../../" + defaultInfosecPath)
	if err != nil {
		t.Fatalf("reading infosec.md: %v", err)
	}
	rules, err := parseSection3Rules(md)
	if err != nil {
		t.Fatalf("parsing §3: %v", err)
	}
	return rules
}

// versionServer serves /api/version with the given client_platforms JSON.
func versionServer(t *testing.T, status int, platformsJSON string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"version":"v1","client_platforms":%s}`, platformsJSON)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func askAll(t *testing.T, gateways ...string) []installDirAnswer {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	var out []installDirAnswer
	for _, g := range gateways {
		dirs, err := fetchInstallDirs(client, g)
		out = append(out, installDirAnswer{Gateway: g, Dirs: dirs, Err: err})
	}
	return out
}

const servedDefaults = `{
	"macos_arm64": {"install_dir": "~/liferay/lfr-tunnel"},
	"macos_amd64": {"install_dir": "~/liferay/lfr-tunnel"},
	"linux_amd64": {"install_dir": "~/liferay/lfr-tunnel"},
	"windows_amd64": {"install_dir": "~\\liferay\\lfr-tunnel"}
}`

func TestSection3RulesParseFromTheCommittedDoc(t *testing.T) {
	rules := realSection3Rules(t)
	for osName, want := range map[string]string{
		"macOS":   "/Users/*/liferay/lfr-tunnel/lfr-tunnel",
		"Linux":   "/home/*/liferay/lfr-tunnel/lfr-tunnel",
		"Windows": `C:\Users\*\liferay\lfr-tunnel\lfr-tunnel.exe`,
	} {
		found := false
		for _, r := range rules[osName] {
			found = found || r == want
		}
		if !found {
			t.Errorf("%s rules %v do not include %s", osName, rules[osName], want)
		}
	}
}

// The defaults every gateway and installer fall back to must be covered by §3, or a fresh install
// lands outside the exclusion with no YAML change at all.
func TestDefaultInstallDirsAreCoveredBySection3(t *testing.T) {
	rules := realSection3Rules(t)
	for key, dir := range defaultInstallDirs {
		if rule := coveredBy(dir, platformOS(key), rules); rule == "" {
			t.Errorf("default %s for %s is not covered by any §3 rule", dir, key)
		}
	}
}

func TestCoveredByRefusesPathsOutsideTheRules(t *testing.T) {
	rules := realSection3Rules(t)
	for _, tc := range []struct{ dir, osName string }{
		{"~/Liferay/lfr-tunnel", "macOS"},        // case: the EDR may match case-sensitively
		{"~/runningpoc/bin", "macOS"},            // the workstation in §3's IMPORTANT note
		{"/opt/lfr-tunnel", "Linux"},             // absolute, outside /home
		{"liferay/lfr-tunnel", "Linux"},          // relative
		{"~/liferay/lfr-tunnel/sub", "Linux"},    // one level too deep
		{`~\liferay\tunnel`, "Windows"},          // last folder name is load-bearing
		{"/Users/*/liferay/lfr-tunnel", "Linux"}, // another OS's root
	} {
		if rule := coveredBy(tc.dir, tc.osName, rules); rule != "" {
			t.Errorf("%s on %s should be uncovered, matched %s", tc.dir, tc.osName, rule)
		}
	}
	// An absolute path under one user's home is still under the wildcard.
	if rule := coveredBy(`C:\Users\alice\liferay\lfr-tunnel`, "Windows", rules); rule == "" {
		t.Error("an absolute path in one user's home should be covered by the Windows rule")
	}
}

func TestCheckInstallDirAgreement(t *testing.T) {
	rules := realSection3Rules(t)
	a := versionServer(t, http.StatusOK, servedDefaults)
	// An edge that lists no platforms: its clients use the default, which agrees.
	b := versionServer(t, http.StatusOK, `{}`)

	r := evaluateInstallDirs(askAll(t, a, b), rules)
	if r.Errors != 0 || len(r.NotVerified) != 0 {
		t.Fatalf("expected a clean report, got %d errors, not verified %v:\n%s",
			r.Errors, r.NotVerified, strings.Join(r.Lines, "\n"))
	}
}

func TestCheckInstallDirReportsADisagreement(t *testing.T) {
	rules := realSection3Rules(t)
	a := versionServer(t, http.StatusOK, servedDefaults)
	// Covered by no rule either, but the disagreement must be named in its own right.
	b := versionServer(t, http.StatusOK, `{"linux_amd64": {"install_dir": "~/other/lfr-tunnel"}}`)

	r := evaluateInstallDirs(askAll(t, a, b), rules)
	out := strings.Join(r.Lines, "\n")
	if !strings.Contains(out, "linux_amd64: gateways disagree") {
		t.Fatalf("disagreement not reported:\n%s", out)
	}
	if !strings.Contains(out, "~/other/lfr-tunnel\" <- "+b) {
		t.Errorf("the disagreeing gateway is not named:\n%s", out)
	}
}

// Two gateways agreeing on a value §3 does not cover is still a failure.
func TestCheckInstallDirReportsAnUncoveredPathTheyAgreeOn(t *testing.T) {
	rules := realSection3Rules(t)
	wrong := `{"macos_arm64": {"install_dir": "~/Liferay/lfr-tunnel"}}`
	a := versionServer(t, http.StatusOK, wrong)
	b := versionServer(t, http.StatusOK, wrong)

	r := evaluateInstallDirs(askAll(t, a, b), rules)
	out := strings.Join(r.Lines, "\n")
	if r.Errors != 1 || !strings.Contains(out, "which no macOS rule in §3 covers") {
		t.Fatalf("expected one uncovered-path error, got %d:\n%s", r.Errors, out)
	}
}

// Unknown is not absent: a gateway that cannot be asked must not read as agreement.
func TestCheckInstallDirUnreachableIsNotVerified(t *testing.T) {
	rules := realSection3Rules(t)
	a := versionServer(t, http.StatusOK, servedDefaults)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	// A non-200 with a body that would decode cleanly is unknown too.
	unavailable := versionServer(t, http.StatusServiceUnavailable, `{"linux_amd64": {"install_dir": "~/liferay/lfr-tunnel"}}`)

	r := evaluateInstallDirs(askAll(t, a, deadURL, unavailable), rules)
	if len(r.NotVerified) != 2 {
		t.Fatalf("expected both the dead and the 503 gateway NOT VERIFIED, got %v:\n%s",
			r.NotVerified, strings.Join(r.Lines, "\n"))
	}
	if r.Errors != 0 {
		t.Errorf("an unknown gateway is not an error by itself, got %d", r.Errors)
	}
}

func TestGatewaysFromSpecNamesCentralAndEveryEdge(t *testing.T) {
	data, err := os.ReadFile("../../" + defaultDNSSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := parseDNSSpec(data)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(gatewaysFromSpec(spec), " ")
	for _, want := range []string{"https://tunnel.lfr-demo.se", "https://us.lfr-demo.se", "https://apac.lfr-demo.se", "https://sa.lfr-demo.se", "https://in.lfr-demo.se"} {
		if !strings.Contains(got, want) {
			t.Errorf("gateway list %q is missing %s", got, want)
		}
	}
	if strings.Contains(got, "portal.") || strings.Contains(got, "*.") {
		t.Errorf("gateway list %q names a host that is not a gateway", got)
	}
}
