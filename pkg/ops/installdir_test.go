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
		// `*` must not match a segment the client resolves elsewhere: filepath.Join cleans dot
		// segments, and os.ExpandEnv turns an unset variable into nothing.
		{"/Users/../liferay/lfr-tunnel", "macOS"},
		{"/home/./liferay/lfr-tunnel", "Linux"},
		{`C:\Users\..\liferay\lfr-tunnel`, "Windows"},
		{"/home/$X/liferay/lfr-tunnel", "Linux"},
		{`C:\Users\%USERNAME%\liferay\lfr-tunnel`, "Windows"},
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
	// So is a 200 that is not the API: a maintenance or nginx page.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<html><body>%s</body></html>", "maintenance")
	}))
	t.Cleanup(html.Close)

	r := evaluateInstallDirs(askAll(t, a, deadURL, unavailable, html.URL), rules)
	if len(r.NotVerified) != 3 {
		t.Fatalf("expected the dead, the 503 and the HTML gateway NOT VERIFIED, got %v:\n%s",
			r.NotVerified, strings.Join(r.Lines, "\n"))
	}
	if r.Errors != 0 {
		t.Errorf("an unknown gateway is not an error by itself, got %d", r.Errors)
	}
	if got := installDirExitCode(r); got != 2 {
		t.Errorf("exit code for an unknown gateway = %d, want 2", got)
	}
}

func TestInstallDirExitCode(t *testing.T) {
	for _, tc := range []struct {
		r    installDirReport
		want int
	}{
		{installDirReport{}, 0},
		{installDirReport{NotVerified: []string{"g"}}, 2},
		{installDirReport{Errors: 1}, 1},
		// An error outranks an unknown: a known-wrong gateway must not read as "edges asleep".
		{installDirReport{Errors: 1, NotVerified: []string{"g"}}, 1},
	} {
		if got := installDirExitCode(tc.r); got != tc.want {
			t.Errorf("installDirExitCode(%+v) = %d, want %d", tc.r, got, tc.want)
		}
	}
}

// A platform the installers do not template has no default: a gateway that lists nothing for it
// has no opinion, rather than disagreeing with one that does.
func TestCheckInstallDirUnknownPlatformIsNotADisagreement(t *testing.T) {
	rules := realSection3Rules(t)
	a := versionServer(t, http.StatusOK, `{"linux_arm64": {"install_dir": "~/liferay/lfr-tunnel"}}`)
	b := versionServer(t, http.StatusOK, `{}`)

	r := evaluateInstallDirs(askAll(t, a, b), rules)
	out := strings.Join(r.Lines, "\n")
	if r.Errors != 0 {
		t.Fatalf("expected no errors, got %d:\n%s", r.Errors, out)
	}
	if !strings.Contains(out, "linux_arm64: the installers do not template this platform") {
		t.Errorf("missing the warning that installers ignore this key:\n%s", out)
	}
}

// defaultInstallDirs copies the gateway's fallbacks. Pinned to the source so the two cannot drift:
// a check that assumed a different default would pass a gateway its clients disagree with.
func TestDefaultInstallDirsMatchTheGatewayFallbacks(t *testing.T) {
	src, err := os.ReadFile("../../pkg/server/server.go")
	if err != nil {
		t.Fatal(err)
	}
	for key, dir := range defaultInstallDirs {
		want := fmt.Sprintf("%q: %s,", key, "\""+dir+"\"")
		if strings.Contains(dir, `\`) {
			want = fmt.Sprintf("%q: `%s`,", key, dir)
		}
		if !strings.Contains(strings.Join(strings.Fields(string(src)), " "), want) {
			t.Errorf("pkg/server/server.go has no fallback %s", want)
		}
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
	// Central once: its other zone's name is the same box, and would report one outage twice.
	if n := strings.Count(got, "https://tunnel."); n != 1 {
		t.Errorf("gateway list %q names central %d times, want 1", got, n)
	}
}
