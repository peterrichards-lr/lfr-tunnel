package main

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// A bare word used to be discarded by flag.Parse and never looked at again, so `lfr-tunnel
// upgrade` opened a tunnel and failed with an unrelated 403 about subdomain reservation
// (#1945). Each case asserts the *message*, not merely that an error came back -- "returned an
// error" is shared by every refusal branch here, so it would not tell them apart.
func TestCheckBareArgs(t *testing.T) {
	cases := []struct {
		name    string
		osArgs  []string
		rest    []string
		wantErr bool
		want    []string // substrings the message must contain
		notWant []string
	}{
		{
			name:   "no bare arguments at all",
			osArgs: []string{"lfr-tunnel", "-subdomain", "alpha-se"},
			rest:   nil,
		},
		{
			name:   "a dispatched subcommand in first position",
			osArgs: []string{"lfr-tunnel", "login"},
			rest:   []string{"login"},
		},
		{
			name:   "mcp, dispatched and now advertised",
			osArgs: []string{"lfr-tunnel", "mcp"},
			rest:   []string{"mcp"},
		},
		{
			name:   "uninstall-service, advertised and now dispatched",
			osArgs: []string{"lfr-tunnel", "uninstall-service"},
			rest:   []string{"uninstall-service"},
		},
		{
			name:   "a subcommand may take arguments of its own",
			osArgs: []string{"lfr-tunnel", "login", "extra"},
			rest:   []string{"login", "extra"},
		},
		{
			// The reported defect.
			name:    "the upgrade typo names the flag it meant",
			osArgs:  []string{"lfr-tunnel", "upgrade"},
			rest:    []string{"upgrade"},
			wantErr: true,
			want:    []string{`"upgrade"`, "-upgrade"},
		},
		{
			name:    "the same class: refresh-region",
			osArgs:  []string{"lfr-tunnel", "refresh-region"},
			rest:    []string{"refresh-region"},
			wantErr: true,
			want:    []string{`"refresh-region"`, "-refresh-region"},
		},
		{
			name:    "the same class: a value-taking flag",
			osArgs:  []string{"lfr-tunnel", "region", "eu"},
			rest:    []string{"region", "eu"},
			wantErr: true,
			want:    []string{`"region"`, "-region"},
		},
		{
			// No suggestion may be invented for a word that is not a registered flag:
			// flag.Lookup is an exact match, so there is nothing fuzzy to mis-fire.
			name:    "a word matching no flag gets no suggestion",
			osArgs:  []string{"lfr-tunnel", "upgrayedd"},
			rest:    []string{"upgrayedd"},
			wantErr: true,
			want:    []string{`"upgrayedd"`, "-h"},
			notWant: []string{"Did you mean"},
		},
		{
			// executeSubcommands dispatches on os.Args[1] only, so this does NOT log in
			// today -- it starts a tunnel. Refusing it says which part was ignored.
			name:    "a known subcommand behind a flag is not dispatched",
			osArgs:  []string{"lfr-tunnel", "-config", "foo.yaml", "login"},
			rest:    []string{"login"},
			wantErr: true,
			want:    []string{`"login"`, "first argument"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBareArgs(tc.osArgs, tc.rest)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("checkBareArgs(%q, %q) = %v, want nil -- a working invocation was refused",
						tc.osArgs, tc.rest, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkBareArgs(%q, %q) = nil; the argument was silently accepted",
					tc.osArgs, tc.rest)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err.Error(), want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("message %q must not contain %q", err.Error(), notWant)
				}
			}
		})
	}
}

// The property, not the instances: the allowlist, the dispatch table and the help output have
// to name the same subcommands. `uninstall-service` sat in the usage text with no dispatch
// branch behind it, so the documented command opened a tunnel -- and a word in the allowlist
// but not in the dispatch table would recreate exactly that (#1945).
func TestBareArgs_AllowlistMatchesDispatchAndUsage(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	dispatched := matchSet(t, `os\.Args\[1\] == "([a-z][a-z-]*)"`, string(src))
	// The subcommand lines of the custom usage block: two leading spaces, the name, then the
	// column of description text. Go's own flag dump is printed by oldUsage(), not by a
	// literal here, so this cannot pick up flag names.
	advertised := matchSet(t, `"  ([a-z][a-z-]*) {2,}[A-Z]`, string(src))

	allowed := append([]string(nil), knownSubcommands...)
	sort.Strings(allowed)

	if strings.Join(dispatched, ",") != strings.Join(allowed, ",") {
		t.Errorf("dispatch table and allowlist disagree:\n  dispatched: %v\n  knownSubcommands: %v\n"+
			"a word in one and not the other is either refused while documented, or waved through to a tunnel",
			dispatched, allowed)
	}
	if strings.Join(advertised, ",") != strings.Join(allowed, ",") {
		t.Errorf("usage text and allowlist disagree:\n  advertised: %v\n  knownSubcommands: %v",
			advertised, allowed)
	}
}

// matchSet returns the sorted, deduplicated capture group 1 of every match, and fails if there
// are none -- a regex that silently stops matching would otherwise make the comparison above
// pass over two empty sets.
func matchSet(t *testing.T, pattern, src string) []string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatalf("pattern %q matched nothing in main.go -- the test is measuring itself, not the code", pattern)
	}
	sort.Strings(out)
	return out
}

// End to end, through the real main(): the reported invocation must refuse and exit non-zero
// rather than opening a tunnel.
//
// The child is given a server and ports on purpose. Without them main() exits at "No tunnel
// server configured", which is non-zero for a reason that has nothing to do with this fix; with
// them, the pre-fix binary gets past configuration and goes on to build a tunnel. So the
// assertion is on the message, never on the exit status alone -- against the silent-ignore
// behaviour this test goes red because the output says something else entirely.
func TestMain_RefusesAnUnknownBareArgument(t *testing.T) {
	if os.Getenv("BE_CRASHER_BARE_ARG") == "1" {
		main()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMain_RefusesAnUnknownBareArgument", "upgrade")
	cmd.Env = append(os.Environ(),
		"BE_CRASHER_BARE_ARG=1",
		// Unreachable on purpose: registration never has to succeed, and a refused
		// connection is the fastest way to prove the client got as far as trying.
		"LFT_SERVER_URL=http://127.0.0.1:1",
		// Explicit ports skip auto-discovery, which would shell out to `docker ps` and probe
		// whatever happens to be listening on this machine.
		"LFT_CLIENT_PORTS=8080",
	)
	output, err := cmd.CombinedOutput()

	if ctx.Err() != nil {
		t.Fatalf("child never exited -- it was still running %v after the typo.\nOutput:\n%s", 30*time.Second, output)
	}
	if err == nil {
		t.Fatalf("child exited 0 after `lfr-tunnel upgrade`; a typo must not start a tunnel.\nOutput:\n%s", output)
	}

	for _, want := range []string{`"upgrade"`, "-upgrade"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("child exited non-zero, but not because the bare argument was refused.\n"+
				"want output containing %q\ngot:\n%s", want, output)
		}
	}
}
