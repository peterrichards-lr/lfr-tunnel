package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

func headerServer(t *testing.T, withHeader bool) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if withHeader {
			w.Header().Set("Liferay-Portal", "Liferay Digital Experience Platform")
		}
	}))
	t.Cleanup(srv.Close)
	_, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func fastWatch(t *testing.T) {
	t.Helper()
	oldI, oldD := liferayWatchInterval, liferayWatchDeadline
	liferayWatchInterval, liferayWatchDeadline = time.Millisecond, 2*time.Second
	t.Cleanup(func() { liferayWatchInterval, liferayWatchDeadline = oldI, oldD })
}

// runWatch resolves preserve_host the way a tunnel start does, then runs the watch to completion.
func runWatch(t *testing.T, cfg *config.ClientConfig) *client.InterceptorEngine {
	t.Helper()
	engine := client.NewInterceptorEngine("127.0.0.1", nil)
	applyPreserveHost(engine, cfg)
	w, ok := newLiferayWatch(engine, cfg)
	if ok {
		w.Run(context.Background())
	}
	return engine
}

// The case #2571 exists for: ports named with -ports, or a native bundle the port probe found.
// No heuristic identifies the port, so before this it stayed off and Liferay generated
// localhost links.
func TestLiferayWatch_NamedPortGetsPreserveHostFromTheHeader(t *testing.T) {
	resetFacts(t)
	fastWatch(t)
	port := headerServer(t, true)
	facts.liferayCandidates = liferayWatchCandidates([]client.PortMapping{{LocalPort: port}})

	engine := runWatch(t, &config.ClientConfig{})
	if !engine.IsLiferayPort(port) || !engine.PreservesHostFor(port) {
		t.Errorf("port answering with Liferay-Portal: IsLiferayPort=%t PreservesHostFor=%t, want both true",
			engine.IsLiferayPort(port), engine.PreservesHostFor(port))
	}
}

// Identified, but preserve_host_auto: false: the port is known as Liferay (for the hint's
// wording) and its Host is still rewritten.
func TestLiferayWatch_RespectsPreserveHostAutoFalse(t *testing.T) {
	resetFacts(t)
	fastWatch(t)
	port := headerServer(t, true)
	facts.liferayCandidates = []int{port}
	off := false

	engine := runWatch(t, &config.ClientConfig{PreserveHostAuto: &off})
	if !engine.IsLiferayPort(port) || engine.PreservesHostFor(port) {
		t.Errorf("auto disabled: IsLiferayPort=%t PreservesHostFor=%t, want true/false",
			engine.IsLiferayPort(port), engine.PreservesHostFor(port))
	}
}

// The workspace guess is withdrawn when its port answers without the header: a Gradle project
// that is not Liferay no longer gets preserve_host on its 8080.
func TestLiferayWatch_WithdrawsTheWorkspaceGuessWithoutTheHeader(t *testing.T) {
	resetFacts(t)
	fastWatch(t)
	port := headerServer(t, false)
	facts.liferayPort, facts.liferayWeak = port, true
	facts.liferayCandidates = liferayWatchCandidates([]client.PortMapping{{LocalPort: port}})

	engine := runWatch(t, &config.ClientConfig{})
	if engine.PreservesHostFor(port) || engine.IsLiferayPort(port) {
		t.Errorf("non-Liferay app on the workspace port: PreservesHostFor=%t IsLiferayPort=%t, want both false",
			engine.PreservesHostFor(port), engine.IsLiferayPort(port))
	}
}

// ...and confirmed when it answers with it.
func TestLiferayWatch_ConfirmsTheWorkspaceGuessWithTheHeader(t *testing.T) {
	resetFacts(t)
	fastWatch(t)
	port := headerServer(t, true)
	facts.liferayPort, facts.liferayWeak = port, true
	facts.liferayCandidates = []int{port}

	engine := runWatch(t, &config.ClientConfig{})
	if !engine.PreservesHostFor(port) || !engine.IsLiferayPort(port) {
		t.Error("a workspace port that answers as Liferay lost preserve_host")
	}
}

// A strong heuristic (the image, LDM's label) is not second-guessed: Liferay with the header
// switched off must keep the port. No candidates means no watch.
func TestLiferayWatchCandidates_NoneAfterAStrongIdentification(t *testing.T) {
	resetFacts(t)
	facts.liferayPort = 8081 // from Docker discovery: not weak
	if got := liferayWatchCandidates([]client.PortMapping{{LocalPort: 8081}, {LocalPort: 443}}); len(got) != 0 {
		t.Errorf("candidates %v after a Docker identification, want none", got)
	}
}

// The startup line says what will happen, not a bare "off" that is about to change.
func TestPreserveHostSummary_SaysTheWatchIsRunning(t *testing.T) {
	resetFacts(t)
	facts.liferayCandidates = []int{8080}
	if got := preserveHostSummary(&config.ClientConfig{}); !strings.Contains(got, "watching 8080 for its Liferay-Portal header") {
		t.Errorf("summary %q does not say the port is being watched", got)
	}
	resetFacts(t)
	facts.liferayPort, facts.liferayWeak, facts.liferayCandidates = 8080, true, []int{8080}
	if got := preserveHostSummary(&config.ClientConfig{}); !strings.HasPrefix(got, "on for 8080 (Liferay workspace;") {
		t.Errorf("summary %q does not say the workspace guess is being confirmed", got)
	}
	resetFacts(t)
	facts.liferayCandidates = []int{8080}
	off := false
	if got := preserveHostSummary(&config.ClientConfig{PreserveHostAuto: &off}); strings.Contains(got, "watching") {
		t.Errorf("summary %q claims a watch that cannot change anything (auto is off)", got)
	}
}
