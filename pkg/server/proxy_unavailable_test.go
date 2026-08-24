package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// TestServeNoTunnelDistinguishesTransientFromGone is the regression test for #1251. Every
// lease miss used to answer 502, which asserts the upstream is broken. Monitoring pages on
// it and some proxies and CDNs treat it as a hard failure -- neither of which is true of a
// tunnel that moved during a failover or a scheduled node stop.
func TestServeNoTunnelDistinguishesTransientFromGone(t *testing.T) {
	const host = "peters.lfr-demo.se"

	t.Run("a host never leased is gone", func(t *testing.T) {
		p := NewProxyHandler(newTestRegistry(t), &config.ServerConfig{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		req.Host = host

		p.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Errorf("a tunnel that is not coming back must not invite a retry, got Retry-After: %q", got)
		}
	})

	t.Run("a host released moments ago is transient", func(t *testing.T) {
		r := newTestRegistry(t)
		r.Lock()
		r.rememberReleasedHost(host)
		r.Unlock()

		p := NewProxyHandler(r, &config.ServerConfig{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		req.Host = host

		p.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		if got := rec.Header().Get("Retry-After"); got == "" {
			t.Error("a transient failure must carry Retry-After so callers know to come back")
		}
	})
}

// TestCleanLeaseRemembersReleasedHost checks the recording itself: tearing a lease down is
// what makes a subsequent miss transient rather than gone.
func TestCleanLeaseRemembersReleasedHost(t *testing.T) {
	const host = "peters.lfr-demo.se"

	r := newTestRegistry(t)
	lease := &TunnelLease{FullHost: host, SessionToken: "tok", LocalPort: 40001, CreatedAt: time.Now()}
	r.sessionLeases["tok"] = []*TunnelLease{lease}
	r.usedPorts[lease.LocalPort] = true
	r.leases[host] = lease

	if r.RecentlyReleased(host) {
		t.Fatal("a live host must not be reported as released")
	}

	r.CleanLease("tok")

	if !r.RecentlyReleased(host) {
		t.Error("expected the host to be remembered as released after its lease was cleaned")
	}
}

// TestCleanLeaseDoesNotReleaseHostOwnedByAnotherSession is the interaction with #1147.
// r.leases is keyed by host, so cleaning a stale session whose host a newer session now
// owns must not remove the route -- and equally must not record a release. Marking it
// released would tell visitors a live tunnel was briefly away, during a failover, which is
// precisely when the route is being handed over and traffic must keep flowing.
func TestCleanLeaseDoesNotReleaseHostOwnedByAnotherSession(t *testing.T) {
	const host = "peters.lfr-demo.se"

	r := newTestRegistry(t)
	stale := &TunnelLease{FullHost: host, SessionToken: "stale", LocalPort: 40002, CreatedAt: time.Now().Add(-time.Minute)}
	live := &TunnelLease{FullHost: host, SessionToken: "live", LocalPort: 40003, CreatedAt: time.Now()}

	r.sessionLeases["stale"] = []*TunnelLease{stale}
	r.sessionLeases["live"] = []*TunnelLease{live}
	r.usedPorts[stale.LocalPort] = true
	r.usedPorts[live.LocalPort] = true
	r.leases[host] = live // the newer session owns the route

	r.CleanLease("stale")

	if r.RecentlyReleased(host) {
		t.Error("cleaning a session that no longer owns the host must not mark it released")
	}
	if _, ok := r.GetLease(host); !ok {
		t.Error("the live session's route must survive, per #1147")
	}
}

// TestRecentlyReleasedExpires checks the TTL, so a subdomain nobody returns to stops
// promising it is about to come back.
func TestRecentlyReleasedExpires(t *testing.T) {
	const host = "peters.lfr-demo.se"

	r := newTestRegistry(t)
	r.Lock()
	r.releasedHosts[host] = time.Now().Add(-releasedHostTTL - time.Second)
	r.Unlock()

	if r.RecentlyReleased(host) {
		t.Error("a release older than the TTL must no longer count as transient")
	}
}

// TestStatusTextAndRetryScript covers what the offline page is told. The page used to
// hardcode "502 Bad Gateway", which would now be wrong on two of the three paths.
func TestStatusTextAndRetryScript(t *testing.T) {
	cases := []struct {
		status    int
		wantText  string
		wantRetry string
	}{
		{http.StatusServiceUnavailable, "503 Service Unavailable", "5"},
		// A tunnel that is genuinely gone must not re-fetch forever.
		{http.StatusNotFound, "404 Not Found", "0"},
		// The upstream-failure path is still a real bad gateway and keeps its status.
		{http.StatusBadGateway, "502 Bad Gateway", "0"},
	}

	for _, tc := range cases {
		if got := statusText(tc.status); got != tc.wantText {
			t.Errorf("statusText(%d) = %q, want %q", tc.status, got, tc.wantText)
		}
		if got := retryScript(tc.status); got != tc.wantRetry {
			t.Errorf("retryScript(%d) = %q, want %q", tc.status, got, tc.wantRetry)
		}
	}
}

// TestOfflinePageHasNoUnsubstitutedPlaceholders guards the templating: a placeholder that
// reaches the browser is visible to the visitor, and the retry script would parse it as
// NaN and silently never retry.
func TestOfflinePageHasNoUnsubstitutedPlaceholders(t *testing.T) {
	r := newTestRegistry(t)
	r.Lock()
	r.rememberReleasedHost("peters.lfr-demo.se")
	r.Unlock()

	p := NewProxyHandler(r, &config.ServerConfig{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://peters.lfr-demo.se/", nil)
	req.Host = "peters.lfr-demo.se"

	p.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, placeholder := range []string{"__STATUS__", "__RETRY_SECONDS__", "loading..."} {
		if strings.Contains(body, placeholder) {
			t.Errorf("offline page still contains the placeholder %q", placeholder)
		}
	}
	if !strings.Contains(body, "503 Service Unavailable") {
		t.Error("offline page should state the status it was served with")
	}
}
