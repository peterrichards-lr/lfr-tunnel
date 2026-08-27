package main

import (
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// -check-config exists so a bad config is found BEFORE a restart rather than during one (#1455).
// Editing server-config.yaml and restarting used to be a bet that the file parsed: if it did not,
// LoadServerConfig failed at startup and the control plane did not come back.
//
// These test the reporting rather than the parsing -- LoadServerConfig is covered in pkg/config.
// What matters here is that the summary answers "is this the config I meant" without ever
// answering "and here are the credentials".

// TestReportConfigSummaryNeverPrintsSecrets is the hard requirement.
//
// An operator runs this in a terminal and pastes the output into tickets and chat. The file holds
// edge token hashes, an edge token, SMTP credentials and webhook URLs, so presence may be
// reported but never the value.
func TestReportConfigSummaryNeverPrintsSecrets(t *testing.T) {
	const (
		fakeHash  = "deadbeefcafebabe1111222233334444555566667777888899990000aaaabbbb"
		fakeToken = "edge-token-plaintext-must-never-appear"
	)
	cfg := &config.ServerConfig{
		Domains:      []string{"example.com"},
		HTTPBindAddr: "127.0.0.1:8080",
		DBPath:       "/etc/lfr-tunneld/lfr-tunnel.db",
		EdgeToken:    fakeToken,
		EdgeNodes: []config.EdgeNodeConfig{
			{ID: "edge-us", URL: "https://us.example.com", TokenHash: fakeHash},
		},
	}

	got := configSummary(cfg, "/etc/lfr-tunneld/server-config.yaml")

	if strings.Contains(got, fakeHash) {
		t.Error("a token_hash reached the output; it is a credential artefact and must only be reported as set")
	}
	if strings.Contains(got, fakeToken) {
		t.Error("the edge_token reached the output")
	}
	// Presence still has to be reported, or the check cannot answer "did I forget a token".
	if !strings.Contains(got, "token_hash:set") {
		t.Errorf("expected the hash to be reported as set, got:\n%s", got)
	}
}

// TestReportConfigSummaryWarnsOnUnroutableEdge covers the shapes that parse cleanly and are still
// wrong. The gateway would start; it just would not do what the operator intended.
func TestReportConfigSummaryWarnsOnUnroutableEdge(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:      []string{"example.com"},
		HTTPBindAddr: "127.0.0.1:8080",
		DBPath:       "/db",
		EdgeNodes: []config.EdgeNodeConfig{
			{ID: "edge-nourl", TokenHash: "x"},
			{ID: "edge-notoken", URL: "https://us.example.com"},
		},
	}

	got := configSummary(cfg, "cfg.yaml")

	if !strings.Contains(got, `edge "edge-nourl" has no url`) {
		t.Errorf("an edge with no url cannot be routed to and must be flagged, got:\n%s", got)
	}
	if !strings.Contains(got, `edge "edge-notoken" has no token_hash`) {
		t.Errorf("an edge that can never authenticate must be flagged, got:\n%s", got)
	}
}

// TestReportConfigSummaryWarnsOnEdgeConfigWithEdgeNodes catches a control-plane config placed on
// an edge -- stateless mode (empty db_path) while also listing edges to route to, which an edge
// never does.
func TestReportConfigSummaryWarnsOnEdgeConfigWithEdgeNodes(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:      []string{"example.com"},
		HTTPBindAddr: "127.0.0.1:8090",
		DBPath:       "",
		EdgeNodes:    []config.EdgeNodeConfig{{ID: "edge-us", URL: "https://us.example.com", TokenHash: "x"}},
	}

	got := configSummary(cfg, "cfg.yaml")
	if !strings.Contains(got, "stateless edge mode") {
		t.Errorf("expected a warning about edge_nodes on a stateless node, got:\n%s", got)
	}
}

// TestReportConfigSummaryCleanConfigIsQuiet — a correct config must produce no warnings, or the
// check becomes noise and gets ignored.
func TestReportConfigSummaryCleanConfigIsQuiet(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:      []string{"example.com", "example.net"},
		HTTPBindAddr: "127.0.0.1:8080",
		DBPath:       "/etc/lfr-tunneld/lfr-tunnel.db",
		EdgeNodes: []config.EdgeNodeConfig{
			{ID: "edge-us", URL: "https://us.example.com", TokenHash: "x"},
		},
	}

	got := configSummary(cfg, "cfg.yaml")

	if strings.Contains(got, "WARNING") {
		t.Errorf("a correct config must be quiet, got:\n%s", got)
	}
	if !strings.Contains(got, "OK: cfg.yaml parses and loads.") {
		t.Errorf("expected the OK line to name the file checked, got:\n%s", got)
	}
	// The summary has to be useful as well as safe: the operator is checking they edited the
	// right thing, and edge_nodes is the field that keeps going stale (#1449).
	if !strings.Contains(got, "https://us.example.com") {
		t.Errorf("expected the edge url to be shown, got:\n%s", got)
	}
}

// TestReportConfigSummaryHandlesNoEdges — a plain single-node deployment is a normal shape and
// must not be reported as a problem.
func TestReportConfigSummaryHandlesNoEdges(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:      []string{"example.com"},
		HTTPBindAddr: "127.0.0.1:8080",
		DBPath:       "/db",
	}

	got := configSummary(cfg, "cfg.yaml")
	if strings.Contains(got, "WARNING") {
		t.Errorf("a single-node deployment with no edges is normal, got:\n%s", got)
	}
}
