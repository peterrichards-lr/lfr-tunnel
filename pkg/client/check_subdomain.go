package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The gateway side of `lfr-tunnel check-subdomain` (#2562): which domains to ask about, and the
// answer for each. Rendering and exit codes live with the subcommand in cmd/lfr-tunnel.
//
// The token goes in the Authorization header and nowhere else. The endpoint also accepts it as a
// `token` query parameter, and that is exactly what this must not do: a URL is what ends up in
// proxy and access logs, and in the error text Go's HTTP client builds from a failed request.

// SubdomainCheck is one domain's answer from /api/check-subdomain.
type SubdomainCheck struct {
	Available bool   `json:"available"`
	Subdomain string `json:"subdomain"`
	// Domain is the domain the gateway says it answered about. Empty from a gateway older than
	// #2562, which ignored the `domain` parameter and answered for a domain of its own choosing.
	Domain      string   `json:"domain,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// ErrTokenRefused is the gateway's 401: it did not accept the token. The gateway gives the same
// answer for a missing, unknown, revoked or expired token and an inactive account.
var ErrTokenRefused = errors.New("the gateway did not accept this access token")

// GatewayError is any other refusal: the status and whatever message the gateway gave.
type GatewayError struct {
	Status  int
	Message string
	// ControlPlane is set on a 421 from a regional edge, which cannot answer this and names the
	// gateway that can.
	ControlPlane string
}

func (e *GatewayError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the gateway answered HTTP %d", e.Status)
	}
	return fmt.Sprintf("the gateway answered HTTP %d: %s", e.Status, e.Message)
}

// UnreachableError means no HTTP answer came back at all.
type UnreachableError struct {
	ServerURL string
	Err       error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("could not reach the gateway at %s: %v", e.ServerURL, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// checkSubdomainClient is shared by both calls. Ten seconds: long enough for a cold TLS handshake
// to a far region, short enough that an unreachable gateway is reported rather than waited on.
var checkSubdomainClient = &http.Client{Timeout: 10 * time.Second}

// GatewayTunnelDomains returns the base domains the gateway issues tunnels on, from the
// `supported_domains` field of its public /api/version. An empty list with a nil error means
// the gateway did not say -- an older one -- and the caller should ask without naming a domain.
func GatewayTunnelDomains(serverURL string) ([]string, error) {
	base := strings.TrimRight(serverURL, "/")
	resp, err := checkSubdomainClient.Get(base + "/api/version")
	if err != nil {
		return nil, &UnreachableError{ServerURL: base, Err: err}
	}
	defer closeCheckSubdomainBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &GatewayError{Status: resp.StatusCode, Message: "while reading /api/version"}
	}
	var info struct {
		SupportedDomains []string `json:"supported_domains"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("could not read the gateway's /api/version: %w", err)
	}
	return info.SupportedDomains, nil
}

// CheckSubdomain asks the gateway whether subdomain is free on domain. An empty domain lets the
// gateway choose, which is all an older gateway does anyway.
//
// Only a 200 with a decodable body produces a result. Everything else is an error, so no failure
// can be read as "available".
func CheckSubdomain(serverURL, token, subdomain, domain string) (*SubdomainCheck, error) {
	base := strings.TrimRight(serverURL, "/")
	q := url.Values{}
	q.Set("subdomain", subdomain)
	if domain != "" {
		q.Set("domain", domain)
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/check-subdomain?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("could not build the request for %s: %w", base, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := checkSubdomainClient.Do(req)
	if err != nil {
		return nil, &UnreachableError{ServerURL: base, Err: err}
	}
	defer closeCheckSubdomainBody(resp.Body)

	// Bounded: an answer is a few hundred bytes, and an error page from a proxy in the way is
	// not worth reading whole.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, &UnreachableError{ServerURL: base, Err: err}
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var check SubdomainCheck
		if err := json.Unmarshal(body, &check); err != nil {
			return nil, fmt.Errorf("the gateway's answer could not be read: %w", err)
		}
		return &check, nil
	case http.StatusUnauthorized:
		return nil, ErrTokenRefused
	default:
		var refusal struct {
			Error        string `json:"error"`
			ControlPlane string `json:"control_plane"`
		}
		if json.Unmarshal(body, &refusal) != nil {
			refusal.Error = ""
		}
		return nil, &GatewayError{Status: resp.StatusCode, Message: refusal.Error, ControlPlane: refusal.ControlPlane}
	}
}

func closeCheckSubdomainBody(body io.Closer) {
	if err := body.Close(); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Closing the check-subdomain response: %v", err))
	}
}
