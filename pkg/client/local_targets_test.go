package client

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// listenLocal opens a TCP listener on a free loopback port and returns the port. It accepts
// and closes connections so a dial to it succeeds.
func listenLocal(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a local listener: %v", err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Logf("closing the local listener: %v", err)
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if err := c.Close(); err != nil {
				t.Logf("closing an accepted connection: %v", err)
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// closedLocalPort returns a loopback port nothing is listening on.
func closedLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to release the reserved port: %v", err)
	}
	return port
}

// healthyEngine is an engine whose every health condition holds except the local target,
// so /api/healthz's answer turns on the dial alone.
func healthyEngine(t *testing.T, targets []PortMapping) *InterceptorEngine {
	t.Helper()
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetLocalTargets(targets, "from -ports")
	engine.mu.Lock()
	engine.ConnState = "connected"
	engine.AuthValid = true
	engine.SubdomainLeased = true
	engine.mu.Unlock()
	return engine
}

func getStatus(t *testing.T, port int, path string) int {
	t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
	return resp.StatusCode
}

// TestHealthzDialsTheResolvedPort is #2478: /api/healthz dialled engine.DestPort, which
// nothing in production ever set, so it probed 8080 whatever -ports said. Reproduced as a
// 502 tunnel reported healthy because an unrelated app answered on 8080.
//
// Both directions, so the test cannot pass on a machine where 8080 happens to be open or
// happens to be closed: the old code's answer depends only on 8080, and these two cases need
// opposite answers.
func TestHealthzDialsTheResolvedPort(t *testing.T) {
	open := listenLocal(t)
	closed := closedLocalPort(t)

	cases := []struct {
		name    string
		targets []PortMapping
		want    int
	}{
		{"resolved port is listening", []PortMapping{{LocalPort: open}}, http.StatusOK},
		{"resolved port is not listening", []PortMapping{{LocalPort: closed}}, http.StatusServiceUnavailable},
		// Matches the gateway heartbeat (localTargetStatus): any published port down is down.
		{"one of several published ports is down", []PortMapping{{LocalPort: open}, {LocalPort: closed, NameSuffix: "x"}}, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := healthyEngine(t, tc.targets)
			inspector := startInspectorForTest(t, engine, 47100)
			if got := getStatus(t, inspector, "/api/healthz"); got != tc.want {
				t.Errorf("/api/healthz = %d, want %d for targets %+v (DestPort %d)",
					got, tc.want, tc.targets, engine.DestPort)
			}
		})
	}
}

// TestInfoReportsTheResolvedPort is the /api/info half (and so the MCP tool): the
// destination port it reports, and the dial behind "responsive", must be the -ports value.
func TestInfoReportsTheResolvedPort(t *testing.T) {
	open := listenLocal(t)
	engine := healthyEngine(t, []PortMapping{{LocalPort: open}})
	inspector := startInspectorForTest(t, engine, 47200)

	_, body := getInspector(t, inspector, "/api/info")
	var info struct {
		Status      string `json:"status"`
		Destination struct {
			Port       int  `json:"port"`
			Responsive bool `json:"responsive"`
		} `json:"destination"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("decoding /api/info: %v\n%s", err, body)
	}
	if info.Destination.Port != open {
		t.Errorf("/api/info destination.port = %d, want %d (the resolved -ports value)",
			info.Destination.Port, open)
	}
	if !info.Destination.Responsive || info.Status != "healthy" {
		t.Errorf("/api/info says responsive=%v status=%q for a listening resolved port",
			info.Destination.Responsive, info.Status)
	}

	// The Settings panel's "effective" column, which reads the same engine field.
	_, body = getInspector(t, inspector, "/api/config")
	var settings struct {
		Effective struct {
			DestPort int `json:"dest_port"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("decoding /api/config: %v\n%s", err, body)
	}
	if settings.Effective.DestPort != open {
		t.Errorf("/api/config effective.dest_port = %d, want %d (the resolved -ports value)",
			settings.Effective.DestPort, open)
	}
}

// TestTUIShowsOneLocalLinePerResolvedPort is the TUI half of #2478: every frame showed
// "Local: 127.0.0.1:8080 (Primary)" while the tunnel served -ports 9999.
func TestTUIShowsOneLocalLinePerResolvedPort(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetLocalTargets([]PortMapping{{LocalPort: 9999}, {LocalPort: 8095, NameSuffix: "8095"}}, "from -ports")

	out := renderToString(t, engine)

	for _, want := range []string{
		"127.0.0.1:9999 (Primary, from -ports)",
		"127.0.0.1:8095 (Suffix: -8095)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the TUI frame does not show %q", want)
		}
	}
	if strings.Contains(out, "127.0.0.1:8080") {
		t.Errorf("the TUI frame still shows 127.0.0.1:8080, which -ports never named")
	}
	if n := strings.Count(out, "Local:"); n != 2 {
		t.Errorf("the TUI frame has %d \"Local:\" lines, want one per mapping (2)", n)
	}
}

func renderToString(t *testing.T, engine *InterceptorEngine) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		buf, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("reading the rendered frame: %v", err)
		}
		done <- string(buf)
	}()
	render(engine, []string{"https://peter-dev.lfr-demo.se"}, nil)
	os.Stdout = origStdout
	if err := w.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	return <-done
}
