package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

func TestGatewayParentDomain(t *testing.T) {
	cases := map[string]string{
		"https://eu.lfr-demo.se":       "lfr-demo.se",
		"https://lfr-demo.se":          "lfr-demo.se",
		"tunnel.example.co.uk:8443":    "example.co.uk",
		"http://127.0.0.1:8080":        "",
		"http://localhost":             "",
		"":                             "",
		"https://central.lfr-demo.se/": "lfr-demo.se",
	}
	for in, want := range cases {
		if got := gatewayParentDomain(in); got != want {
			t.Errorf("gatewayParentDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// End to end through runDoctor with a Liferay stand-in that refuses the tunnel hostname. The
// gateway is unreachable on purpose: the token and install checks must come out UNKNOWN, not
// pass, and the run must not exit 0.
func TestRunDoctor_RejectedHostnameAndUnreachableGateway(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)

	liferay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "dev-tomcat.") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(liferay.Close)
	u, err := url.Parse(liferay.URL)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.ClientConfig{ServerURL: "http://127.0.0.1:1", AuthToken: "abc", TokenSource: "-token flag"}
	var out bytes.Buffer
	code := runDoctor(cfg, "ignored", []string{"-ports", u.Port(), "-hostname", "dev-tomcat.lfr-demo.se", "-preserve-host"}, &out)
	report := out.String()

	for _, want := range []string{
		"[UNKNOWN] Token",
		"[UNKNOWN] Install path",
		"[PASS   ] preserve_host",
		"[FAIL   ] Liferay accepts the tunnel hostname",
		"virtual.hosts.valid.hosts",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1 for a FAIL\n%s", code, report)
	}
}

// Nothing to check Liferay against: the Liferay checks are UNKNOWN, never PASS.
func TestRunDoctor_NoLiferayIsUnknown(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { return false }
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) { return nil, nil }
	oldPortsStr := *portsStr
	*portsStr = ""
	t.Cleanup(func() { *portsStr = oldPortsStr })

	cfg := &config.ClientConfig{ServerURL: "http://127.0.0.1:1"}
	var out bytes.Buffer
	code := runDoctor(cfg, "demo", nil, &out)
	report := out.String()
	for _, want := range []string{"[UNKNOWN] preserve_host", "[UNKNOWN] Liferay accepts the tunnel hostname", "[FAIL   ] Token"} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
	if code == 0 {
		t.Errorf("exit 0 although checks could not run:\n%s", report)
	}
}

// #2571: doctor asks a named port for Liferay's header, as a tunnel start's watch does. A port
// that answers with it gets preserve_host PASS with no -preserve-host -- before #2571 doctor
// reported "off (ports given explicitly ...)" for every named port.
func TestRunDoctor_NamedPortIdentifiedByItsHeader(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)
	port := headerServer(t, true)

	cfg := &config.ClientConfig{ServerURL: "http://127.0.0.1:1"}
	var out bytes.Buffer
	runDoctor(cfg, "demo", []string{"-ports", strconv.Itoa(port), "-hostname", "demo.lfr-demo.se"}, &out)
	report := out.String()
	for _, want := range []string{"[PASS   ] preserve_host", "Liferay-Portal header"} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
}

// A named port that answers without the header: preserve_host is reported off, and why.
func TestRunDoctor_NamedPortWithoutTheHeader(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)
	port := headerServer(t, false)

	cfg := &config.ClientConfig{ServerURL: "http://127.0.0.1:1"}
	var out bytes.Buffer
	runDoctor(cfg, "demo", []string{"-ports", strconv.Itoa(port), "-hostname", "demo.lfr-demo.se"}, &out)
	if report := out.String(); !strings.Contains(report, "answered without Liferay's Liferay-Portal header") {
		t.Errorf("report does not explain the missing header:\n%s", report)
	}
}

// A named port nothing answers on yet: not a verdict -- a tunnel start would keep watching.
func TestRunDoctor_NamedPortNotAnsweringYet(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ClientConfig{ServerURL: "http://127.0.0.1:1"}
	var out bytes.Buffer
	runDoctor(cfg, "demo", []string{"-ports", strconv.Itoa(closed), "-hostname", "demo.lfr-demo.se"}, &out)
	if report := out.String(); !strings.Contains(report, "a tunnel start watches for its Liferay-Portal header") {
		t.Errorf("report does not say a tunnel start would keep watching:\n%s", report)
	}
}
