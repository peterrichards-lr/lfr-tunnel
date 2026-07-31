package provisioner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is lfr-tunneld's side of the versioned API contract (issue #888).
// It talks to the edge-provisioner sidecar over loopback HTTP and never
// depends on AWS (or any provider) directly -- this is the one piece of the
// design that must remain stable across whatever runs behind baseURL.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a Client. A 10-second timeout is used for every call --
// slightly more than the 5s used for edge health checks elsewhere in this
// codebase, since these calls also make one synchronous round trip to a
// cloud provider on the sidecar's side. Start/Stop/Restart are async on the
// sidecar's end (202 Accepted once submitted, not once completed), so this
// timeout is headroom for "submit the action," never "wait for it to finish."
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ErrBackendUnavailable wraps any transport-level failure talking to the
// sidecar (connection refused, timeout, DNS, etc.) so callers can
// distinguish "sidecar unreachable" from "sidecar returned an error."
type ErrBackendUnavailable struct {
	Err error
}

func (e *ErrBackendUnavailable) Error() string {
	return "edge-provisioner unavailable: " + e.Err.Error()
}
func (e *ErrBackendUnavailable) Unwrap() error { return e.Err }

// ErrRemote is returned when the sidecar responds with a non-2xx status and
// a parseable {"error": {"code", "message"}} envelope.
type ErrRemote struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ErrRemote) Error() string {
	return fmt.Sprintf("edge-provisioner returned %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return &ErrBackendUnavailable{Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return &ErrRemote{StatusCode: resp.StatusCode, Code: errBody.Error.Code, Message: errBody.Error.Message}
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response body: %w", err)
		}
	}
	return nil
}

func (c *Client) Versions(ctx context.Context) ([]string, error) {
	var out struct {
		Supported []string `json:"supported"`
	}
	if err := c.do(ctx, http.MethodGet, "/versions", nil, &out); err != nil {
		return nil, err
	}
	return out.Supported, nil
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &out)
	return out, err
}

func (c *Client) Start(ctx context.Context, nodeID string) error {
	return c.do(ctx, http.MethodPost, "/v1/nodes/"+nodeID+"/start", nil, nil)
}

func (c *Client) Stop(ctx context.Context, nodeID string) error {
	return c.do(ctx, http.MethodPost, "/v1/nodes/"+nodeID+"/stop", nil, nil)
}

func (c *Client) Restart(ctx context.Context, nodeID string) error {
	return c.do(ctx, http.MethodPost, "/v1/nodes/"+nodeID+"/restart", nil, nil)
}

func (c *Client) GetSchedule(ctx context.Context, nodeID string) (Schedule, error) {
	var out Schedule
	err := c.do(ctx, http.MethodGet, "/v1/nodes/"+nodeID+"/schedule", nil, &out)
	return out, err
}

func (c *Client) SetSchedule(ctx context.Context, nodeID string, s Schedule) error {
	return c.do(ctx, http.MethodPut, "/v1/nodes/"+nodeID+"/schedule", s, nil)
}
