package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// TestEdgeRegistrationRecordsRegionSourceAndProbes is the control for #2368.
//
// A registration relayed by an edge never recorded how the client chose its gateway, or the
// latency probes it took. Most of the fleet registers that way, so the node-placement report's
// region_sources and latency figures described almost nobody: in production the audit log held
// 43 failback registrations and client_region_source held one.
//
// This asserts the TABLES the report reads, through the same repository methods the report
// calls. TestEdgeTunnelStartAuditRecordsAcceptingNode already asserted that the edge session's
// audit text names the region source -- which was true throughout, while the table stayed
// empty. An assertion on the audit sink could not see a missing write to the analytics sink.
func TestEdgeRegistrationRecordsRegionSourceAndProbes(t *testing.T) {
	tmpDir := t.TempDir()
	hash := sha256.Sum256([]byte("region-edge-secret"))

	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(tmpDir, "control.db")
	cfg.Domains = []string{"control.lfr-demo.se"}
	cfg.DisableBackupScheduler = true
	cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "us-edge", TokenHash: hex.EncodeToString(hash[:])}}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond)
	})

	if err := srv.db.CreateUser(&db.User{
		ID: "region-src-user", Email: "region-src@example.com", Role: "user", Status: "approved",
	}); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	patHash := sha256.Sum256([]byte("pat-region-1"))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID: "region-src-user", TokenHash: hex.EncodeToString(patHash[:]),
		TokenPrefix: "pat-re", Name: "region pat",
	}); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}
	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID: "region-src-user", Subdomain: "region-sub", Domain: "us.lfr-demo.se",
	}); err != nil {
		t.Fatalf("failed to create reservation: %v", err)
	}

	// Every source starts at zero users, so a pass cannot be a row some other path left behind.
	// (GetRegionSources lists each known source with its count, zeros included.)
	before, err := srv.db.GetRegionSources(1)
	if err != nil {
		t.Fatalf("GetRegionSources: %v", err)
	}
	for _, s := range before {
		if s.Users != 0 {
			t.Fatalf("expected no recorded sources before registering, got %+v", before)
		}
	}

	payload := []byte(`{
		"subdomain_prefix": "region-sub",
		"auth_token": "pat-region-1",
		"ports": [{"local_port": 8080}],
		"domains": ["us.lfr-demo.se"],
		"client_ip": "8.8.8.8",
		"client_version": "v1.52.2",
		"client_os": "linux",
		"region_source": "failback",
		"region_probes": [{"region": "us", "rtt_ms": 42}, {"region": "apac", "unreachable": true}]
	}`)
	req := httptest.NewRequest("POST", "http://control.lfr-demo.se/api/internal/edge-register", bytes.NewReader(payload))
	req.Header.Set("X-Edge-Token", "region-edge-secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected edge registration 200, got %d: %s", rec.Code, rec.Body.String())
	}

	sources, err := srv.db.GetRegionSources(1)
	if err != nil {
		t.Fatalf("GetRegionSources: %v", err)
	}
	var failback int
	for _, s := range sources {
		if s.Source == "failback" {
			failback = s.Users
		}
	}
	if failback != 1 {
		t.Errorf("an edge-relayed registration with region_source failback was not recorded in "+
			"client_region_source; GetRegionSources(1) = %+v (#2368)", sources)
	}

	latency, err := srv.db.GetRegionLatency(1)
	if err != nil {
		t.Fatalf("GetRegionLatency: %v", err)
	}
	seen := map[string]bool{}
	if latency != nil {
		for _, r := range latency.Regions {
			seen[r.Region] = true
		}
	}
	if !seen["us"] || !seen["apac"] {
		t.Errorf("an edge-relayed registration's region probes were not recorded; regions in the "+
			"latency report: %v (#2368)", seen)
	}
}

// TestBothRegistrationPathsRecordTheSameBookkeeping fixes the class #2368 belongs to: per-
// registration bookkeeping present in one handler and ABSENT from the other.
//
// registration_duplication_test.go gates the opposite failure -- a block copied into both
// handlers -- and says plainly that its design cannot see a copy that has diverged. An omission is
// the limiting case of divergence, so that gate could never have caught this: the edge path
// called recordClientVersionAndOS (#2020) and nothing else, and the report built on the missing
// writes was wrong for every edge-relayed client.
//
// So: every `s.record*` call in handleRegister must also appear in handleEdgeRegister, unless it
// is listed in edgeExemptRecorders with the reason it does not apply to a relayed registration.
func TestBothRegistrationPathsRecordTheSameBookkeeping(t *testing.T) {
	direct, edge := recorderCalls(t, "handleRegister"), recorderCalls(t, "handleEdgeRegister")

	// Floor: an empty set on either side means the parse found nothing, not that the paths agree.
	if len(direct) < 3 {
		t.Fatalf("found only %d s.record* calls in handleRegister -- the parse is not seeing the "+
			"handler, so any comparison would pass over nothing: %v", len(direct), direct)
	}

	// Recorders that legitimately run on one path only. Empty today; an entry needs a reason a
	// reviewer can check, not just a name.
	edgeExemptRecorders := map[string]string{}

	var missing []string
	for name := range direct {
		if !edge[name] {
			if _, ok := edgeExemptRecorders[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("handleRegister calls %s but handleEdgeRegister does not, so every client "+
			"registering through an edge -- most of the fleet -- skips that bookkeeping (#2368). "+
			"Call it from handleEdgeRegister too, or add it to edgeExemptRecorders with the reason "+
			"it cannot apply to a relayed registration.", strings.Join(missing, ", "))
	}
}

// recorderCalls returns the names of the `s.record*` methods called inside the named Server
// method in server.go.
func recorderCalls(t *testing.T, method string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	found := map[string]bool{}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.Name == method {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatalf("server.go has no method %s", method)
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "s" && strings.HasPrefix(sel.Sel.Name, "record") {
			found[sel.Sel.Name] = true
		}
		return true
	})
	return found
}
