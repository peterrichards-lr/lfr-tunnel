package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"lfr-tunnel/pkg/db"
)

// The property #1325 established, applied to every address this gateway RECORDS (#1818):
//
//	A forwarding header may only be believed when the request arrived from an address in
//	trusted_proxies.
//
// Five sites read X-Real-IP / X-Forwarded-For directly instead, so the boundary did not apply to
// them. Four wrote the result into the audit log and the portal session, which means anyone who
// could reach the portal could make token.created, token.revoked and user.login.sso rows name any
// address they chose -- the precise thing an audit log exists to prevent. #1357 had already fixed
// the OPPOSITE defect in these same records (they recorded r.RemoteAddr, so 4,896 of 4,951
// production rows read 127.0.0.1) by routing them through clientIP, and missed these.
//
// The fifth, edge_control_ws.go, took the LEFTMOST X-Forwarded-For entry. nginx's
// $proxy_add_x_forwarded_for APPENDS, so the leftmost entry is whatever the caller sent -- the one
// rule #1325 wrote down, implemented backwards -- and the value is a routing target, not just a
// display string.

// forgedIP is an address the test never arranges to be the peer, so an assertion naming it can
// only be satisfied by the header having been believed.
const forgedIP = "203.0.113.9"

// TestAuditAddressIgnoresAForgedHeaderFromAnUntrustedPeer is the pair that matters. Asserting
// only "the row does not say 203.0.113.9" would be satisfied by a gateway that never reads the
// header at all, or writes an empty string -- so the trusted half asserts the header IS honoured
// where it should be. Together they pin the boundary rather than the absence of a value.
func TestAuditAddressIgnoresAForgedHeaderFromAnUntrustedPeer(t *testing.T) {
	tests := []struct {
		name string
		// remoteAddr is the immediate peer, i.e. who actually connected.
		remoteAddr string
		wantIP     string
		why        string
	}{
		{
			name:       "untrusted peer: the header is a claim, not evidence",
			remoteAddr: "198.51.100.7:41234",
			wantIP:     "198.51.100.7",
			why: "a caller that is not a trusted proxy set X-Real-IP itself, so the row must " +
				"name the peer we actually observed",
		},
		{
			name:       "trusted peer: the header is what nginx resolved",
			remoteAddr: "127.0.0.1:41234",
			wantIP:     forgedIP,
			why: "loopback is the default trusted set and nginx proxies to it, so X-Real-IP is " +
				"the real visitor and must still be honoured -- this is the half that fails if " +
				"the fix merely stopped reading the header",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupTestServerForAPI(t)
			sessionToken := newAdminSession(t, srv, "admin@example.com")

			body := []byte(`{"name":"boundary-probe","expires_in_days":30}`)
			req := adminRequest("POST", "http://example.com/api/tokens", body, sessionToken)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Real-IP", forgedIP)

			w := httptest.NewRecorder()
			srv.handleCreateToken(w, req)
			if w.Code != 201 && w.Code != 200 {
				t.Fatalf("fixture did not create a token (status %d, body %s) -- the audit "+
					"assertion below would pass for the wrong reason", w.Code, w.Body.String())
			}

			entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "token.created"})
			if err != nil {
				t.Fatalf("reading audit entries: %v", err)
			}
			if len(entries) == 0 {
				t.Fatal("no token.created audit entry was written -- nothing to assert against")
			}

			got := entries[0].IPAddress
			if got != tc.wantIP {
				t.Errorf("audit row recorded IPAddress %q, want %q.\n%s", got, tc.wantIP, tc.why)
			}
		})
	}
}

// TestNoHandlerReadsAForwardingHeaderOutsideTheResolver is the class-level guard (§5b.4): it
// fails on ANY new site, not on the five that were found. Without it the sixth arrives silently,
// which is exactly how these five outlived #1325 and #1357.
//
// Three shapes are legitimate and are allowed by rule rather than by listing file names:
//
//   - the resolver itself (client_ip.go), which is where the boundary lives;
//   - a PRESENCE test -- `== ""` or `!= ""` -- which asks "did this arrive through a proxy?"
//     and reads no address (server.go's localhost-only refusal, drain.go's);
//   - a read from an OUTBOUND request, conventionally named `req` here, which is a header this
//     gateway is setting rather than one it is trusting (proxy.go).
//
// Its blind spot, stated as a test rather than a comment (§5b.6): the outbound allowance is
// keyed on the variable name, so a handler that named its inbound request `req` would be
// permitted. TestTheOutboundAllowanceIsNameBased pins that, so widening it is a decision.
func TestNoHandlerReadsAForwardingHeaderOutsideTheResolver(t *testing.T) {
	root := "../.."
	pattern := regexp.MustCompile(`(\w+)\.Header\.Get\("X-(?:Real-IP|Forwarded-For)"\)`)

	var offenders []string
	scanned := 0
	sawResolver := false

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "ui-dist", "bin", "dist":
				return filepath.SkipDir
			}
			if path != root && isNestedWorktreeRootServer(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path) //nolint:gosec
		if readErr != nil {
			return readErr
		}
		scanned++
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../../"))
		if strings.HasSuffix(rel, "pkg/server/client_ip.go") {
			if pattern.Match(body) {
				sawResolver = true
			}
			return nil // the resolver is where the boundary is implemented
		}

		for i, line := range strings.Split(string(body), "\n") {
			m := pattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// A presence test asks "did this arrive through a proxy?" and reads no address.
			// It only counts as one if the value is NOT bound: `if x := r.Header.Get(...);
			// x != ""` both tests presence AND keeps the value, which is precisely the shape
			// edge_control_ws.go had. Checking for `!= ""` alone let that site through when
			// this guard was first written -- caught by mutation, not by review.
			binds := strings.Contains(line, ":=") || regexp.MustCompile(`\w\s*=\s*\w*\.Header\.Get`).MatchString(line)
			if !binds && (strings.Contains(line, `== ""`) || strings.Contains(line, `!= ""`)) {
				continue
			}
			if m[1] == "req" {
				continue // outbound: a header this gateway sets, not one it believes
			}
			offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// Anti-vacuity: a scan that read nothing, or a repo where the resolver stopped reading these
	// headers, would report a clean pass while checking nothing.
	if scanned < 50 {
		t.Fatalf("only %d .go files scanned -- the walk is not reaching the tree, so a clean "+
			"result here means nothing", scanned)
	}
	if !sawResolver {
		t.Fatal("client_ip.go no longer reads X-Real-IP/X-Forwarded-For -- either the resolver " +
			"moved, in which case this guard is pointed at the wrong file, or the boundary is " +
			"gone entirely")
	}

	if len(offenders) > 0 {
		t.Errorf("a forwarding header is read outside the resolver, so the trusted_proxies "+
			"boundary does not apply to it (#1818):\n  %s\n\n"+
			"Use s.clientIP(r) (or p.clientIP(r)). It honours X-Real-IP and walks "+
			"X-Forwarded-For right-to-left, but only when the immediate peer is a trusted "+
			"proxy; otherwise it returns the peer. Reading the header directly means an "+
			"attacker names the address you record.",
			strings.Join(offenders, "\n  "))
	}
}

// TestTheOutboundAllowanceIsNameBased states the guard's blind spot as an assertion rather than
// as prose, so widening it is a decision rather than an accident (§5b.6).
func TestTheOutboundAllowanceIsNameBased(t *testing.T) {
	pattern := regexp.MustCompile(`(\w+)\.Header\.Get\("X-(?:Real-IP|Forwarded-For)"\)`)

	inbound := `x := r.Header.Get("X-Real-IP")`
	outbound := `if req.Header.Get("X-Real-IP") == "x" {`

	mi := pattern.FindStringSubmatch(inbound)
	mo := pattern.FindStringSubmatch(outbound)
	if mi == nil || mo == nil {
		t.Fatal("the pattern no longer matches its own examples -- the guard above is inert")
	}
	if mi[1] != "r" || mo[1] != "req" {
		t.Fatalf("the guard distinguishes inbound from outbound by VARIABLE NAME (got %q, %q). "+
			"A handler whose inbound request is named `req` would be allowed through. That is "+
			"the accepted limit of this check; if it ever matters, key it on type or on package "+
			"rather than widening the name list silently.", mi[1], mo[1])
	}
}

// isNestedWorktreeRootServer mirrors the rule in pkg/config (#1815): a nested worktree root
// carries .git as a FILE, a real repository root as a directory. Duplicated rather than exported
// because it is three lines and a test helper; if a third copy appears, promote it.
func isNestedWorktreeRootServer(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.Mode().IsRegular()
}
