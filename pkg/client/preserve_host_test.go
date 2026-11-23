package client

import (
	"fmt"
	"net/http"
	"testing"
)

// sendThrough sends one GET with the given public Host through an interceptor listener and
// returns nothing; the backend records what it received.
func sendThrough(t *testing.T, interceptPort int, publicHost string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", interceptPort), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = publicHost
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request through interceptor: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
}

// The automatic preserve_host is per port (#2560): the Liferay port gets the visitor's Host, and
// a client-extension port published beside it in the same engine still gets its own.
//
// Both backends sit behind ONE engine on purpose. An engine-wide bool, which is what the
// interceptor had before, can satisfy either half of this test but not both at once.
func TestPreserveHostPorts_OnlyTheLiferayPortKeepsTheHost(t *testing.T) {
	_, liferay, liferayPort := captureTarget(t)
	_, extension, extensionPort := captureTarget(t)

	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.PreserveHost = false
	engine.SetPreserveHostPorts([]int{liferayPort})

	liferayListener, err := engine.InterceptPort(liferayPort)
	if err != nil {
		t.Fatalf("InterceptPort(liferay): %v", err)
	}
	extensionListener, err := engine.InterceptPort(extensionPort)
	if err != nil {
		t.Fatalf("InterceptPort(extension): %v", err)
	}

	const public = "dev-tomcat.lfr-demo.se"
	sendThrough(t, liferayListener, public)
	sendThrough(t, extensionListener, public)

	if liferay.Host != public {
		t.Errorf("Liferay port received Host %q, want the public %q", liferay.Host, public)
	}
	if want := fmt.Sprintf("127.0.0.1:%d", extensionPort); extension.Host != want {
		t.Errorf("client-extension port received Host %q, want the rewritten %q", extension.Host, want)
	}
	if !engine.PreservesHostFor(liferayPort) || engine.PreservesHostFor(extensionPort) {
		t.Errorf("PreservesHostFor: liferay=%t extension=%t, want true/false",
			engine.PreservesHostFor(liferayPort), engine.PreservesHostFor(extensionPort))
	}
}

// The explicit every-port setting still covers every port, whatever the per-port set says.
func TestPreserveHost_ExplicitOnCoversEveryPort(t *testing.T) {
	_, first, firstPort := captureTarget(t)
	_, second, secondPort := captureTarget(t)

	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.PreserveHost = true
	engine.SetPreserveHostPorts(nil)

	a, err := engine.InterceptPort(firstPort)
	if err != nil {
		t.Fatal(err)
	}
	b, err := engine.InterceptPort(secondPort)
	if err != nil {
		t.Fatal(err)
	}
	sendThrough(t, a, "x.lfr-demo.se")
	sendThrough(t, b, "x.lfr-demo.se")
	if first.Host != "x.lfr-demo.se" || second.Host != "x.lfr-demo.se" {
		t.Errorf("explicit preserve_host: hosts %q and %q, want both public", first.Host, second.Host)
	}
}
