package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// TestSelfUpgradeGitHubReleaseWithoutSignature is the regression test for #1265.
//
// No release has ever published a checksums.txt.minisig asset, but nobody reached that path
// until v1.48.0: until then DefaultServerURL was a compiled-in const, so SelfUpgrade always
// took the gateway branch. Once #1210 made the defaults build-injected and they were left
// unset, clients fell through to GitHub and hit this.
//
// When the asset is absent the code used to synthesise "<asset-download-url>.minisig", which
// cannot resolve -- release assets are addressed by identifier, not filename -- so the user
// was shown "server returned status 404" and had to read the source to find out why.
func TestSelfUpgradeGitHubReleaseWithoutSignature(t *testing.T) {
	assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/peterrichards-lr/lfr-tunnel/releases/latest":
			// Exactly what the real releases carry: the binaries and checksums.txt, and
			// deliberately no checksums.txt.minisig.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[
                {"name":%q,"browser_download_url":"http://%s/download/asset"},
                {"name":"checksums.txt","browser_download_url":"http://%s/download/checksums"}
            ]}`, assetName, r.Host, r.Host)
		case "/download/checksums":
			h := sha256.Sum256([]byte("irrelevant"))
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), assetName)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	// Empty serverURL is the whole point: it is what sends a client down the GitHub path.
	err := SelfUpgrade("v1.0.0", "")
	if err == nil {
		t.Fatal("expected an error: an unverifiable download must not be installed")
	}

	msg := err.Error()

	// The failure has to name the problem rather than an HTTP status.
	if !strings.Contains(msg, "no checksums.txt.minisig") {
		t.Errorf("error should say the release publishes no signature, got: %v", err)
	}
	// And it has to say what to do instead. The gateway route is how deployments that must
	// control where the binary lands get their client, so pointing at it is both the fix
	// and a hint about the supported path.
	if !strings.Contains(msg, "-server") {
		t.Errorf("error should point at the gateway alternative, got: %v", err)
	}
	// Guard the specific regression: a bare status code is what made this need a code read.
	if strings.Contains(msg, "server returned status 404") {
		t.Errorf("error should not be a bare status code, got: %v", err)
	}
}

// TestSelfUpgradeSignatureFetchNotFound covers the other way the signature can be missing:
// the URL is known -- a gateway builds it from its static directory -- but nothing is served
// there. That is still a publishing gap rather than a transport failure, and should say so
// and name the URL it tried.
func TestSelfUpgradeSignatureFetchNotFound(t *testing.T) {
	assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/peterrichards-lr/lfr-tunnel/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[
                {"name":%q,"browser_download_url":"http://%s/download/asset"},
                {"name":"checksums.txt","browser_download_url":"http://%s/download/checksums"},
                {"name":"checksums.txt.minisig","browser_download_url":"http://%s/download/missing-signature"}
            ]}`, assetName, r.Host, r.Host, r.Host)
		case "/download/checksums":
			h := sha256.Sum256([]byte("irrelevant"))
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), assetName)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	err := SelfUpgrade("v1.0.0", "")
	if err == nil {
		t.Fatal("expected an error when the signature URL serves nothing")
	}

	msg := err.Error()
	if !strings.Contains(msg, "signature is missing") {
		t.Errorf("error should describe the missing signature, got: %v", err)
	}
	if !strings.Contains(msg, "/download/missing-signature") {
		t.Errorf("error should name the URL it tried, got: %v", err)
	}
}
