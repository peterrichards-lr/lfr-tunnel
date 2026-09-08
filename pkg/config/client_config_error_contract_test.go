package config

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Tests for LoadClientConfig's error-return contract (#1777).
//
// The defect was not a wrong return value; it was three error returns that disagreed, with a
// comment on one of them asserting a safety property the function did not have ("returned
// non-nil so pkg/gui cannot panic" -- while the two commoner failures returned nil). The
// contract is now stated on the function AND asserted here, because the comment is the part
// that failed.
//
// The chosen contract: **every error path returns a nil config.**

// TestLoadClientConfigReturnsNilOnEveryErrorPath covers each distinct cause that can make
// LoadClientConfig fail. Each case asserts the specific error as well as the nil, so a row
// cannot be satisfied by a different failure -- "returned an error" is shared by all five
// (#5c), and an os.Open failure would otherwise silently stand in for the token_file cases.
func TestLoadClientConfigReturnsNilOnEveryErrorPath(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the config path to load, or skips the case.
		setup func(t *testing.T) string
		// wantIn are substrings the error must contain, identifying the cause uniquely.
		// Only ever strings this repository writes -- never text the operating system
		// produces. Windows says "The system cannot find the path specified." where Unix
		// says "no such file", and asserting on the latter made this suite red on
		// windows-latest only. For an OS-produced failure, assert wantIs instead.
		wantIn []string
		// wantIs identifies the cause by sentinel rather than by message, for the cases whose
		// error text belongs to the platform. errors.Is is the portable half of "this case
		// must fail for its own reason".
		wantIs error
	}{
		{
			name: "the config file cannot be opened",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "no-such-dir", "config.yaml")
			},
			// "config.yaml" is ours (it is the path we passed in); the rest of the message
			// is the platform's, so the cause is pinned with a sentinel instead.
			wantIn: []string{"config.yaml"},
			wantIs: fs.ErrNotExist,
		},
		{
			name: "the config file is not valid YAML",
			setup: func(t *testing.T) string {
				// An unterminated quoted scalar: rejected by the scanner outright, which is
				// the shape of a real typo rather than a schema mismatch yaml tolerates.
				return writeClientConfigFile(t, "server_url: \"https://example.com\nsubdomain: mine\n")
			},
			wantIn: []string{"yaml"},
		},
		{
			name: "token_file: names a path that does not exist",
			setup: func(t *testing.T) string {
				missing := filepath.Join(t.TempDir(), "not-there", "token")
				return writeClientConfigFile(t, "token_file: "+yamlPath(missing)+"\n")
			},
			wantIn: []string{"token", "Personal Access Token"},
		},
		{
			name: "token_file: names an empty file",
			setup: func(t *testing.T) string {
				tokenPath := writeTokenFile(t, "\n\n  \n")
				return writeClientConfigFile(t, "token_file: "+yamlPath(tokenPath)+"\n")
			},
			wantIn: []string{"empty"},
		},
		{
			name: "token_file: names a file that cannot be read",
			setup: func(t *testing.T) string {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits do not control readability on Windows")
				}
				tokenPath := writeTokenFile(t, "lft_pat_unreadable\n")
				if err := os.Chmod(tokenPath, 0000); err != nil {
					t.Fatalf("failed to chmod: %v", err)
				}
				if _, err := os.ReadFile(tokenPath); err == nil {
					t.Skip("running as a user that ignores permission bits (root)")
				}
				return writeClientConfigFile(t, "token_file: "+yamlPath(tokenPath)+"\n")
			},
			wantIn: []string{"readable"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateTokenEnvironment(t)
			path := tc.setup(t)

			cfg, err := LoadClientConfig(path)

			if err == nil {
				t.Fatalf("expected this case to fail to load; it proves nothing otherwise")
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("this case must fail for its own reason: expected the error to "+
						"mention %q, got: %v", want, err)
				}
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("this case must fail for its own reason: expected an error matching "+
					"%v, got: %v", tc.wantIs, err)
			}
			if cfg != nil {
				t.Errorf("LoadClientConfig must return a nil config on every error path "+
					"(#1777); this path returned %+v alongside %v", cfg, err)
			}
		})
	}
}

// TestEveryConfigLoaderIsNilOnError is the class-level guard, and the durable half of this fix.
//
// The behavioural test above covers the error paths that exist today; this one covers the ones
// that do not exist yet. It reads pkg/config's own source and requires every exported
// Load*Config function returning (*T, error) to return the literal nil beside a non-nil error,
// and a non-nil value beside a nil error. A future branch that writes `return cfg, err` fails
// here whether or not anyone remembers this issue -- which is the thing a comment could not do.
//
// Widened past LoadClientConfig deliberately (§5b): the defect's class is "loaders in this
// package that disagree about what accompanies an error", and LoadServerConfig is the other
// member. It already complies, so pinning it costs nothing and stops the two drifting apart.
func TestEveryConfigLoaderIsNilOnError(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to list pkg/config: %v", err)
	}

	type loader struct {
		name       string
		errReturns int
		okReturns  int
		violations []string
	}
	loaders := map[string]*loader{}
	scanned := 0

	for _, entry := range entries {
		fileName := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(fileName, ".go") || strings.HasSuffix(fileName, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, fileName, nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", fileName, err)
		}
		scanned++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Load") || !strings.HasSuffix(fn.Name.Name, "Config") {
				continue
			}
			if !returnsPointerAndError(fn) {
				continue
			}
			l := &loader{name: fn.Name.Name}
			loaders[fn.Name.Name] = l

			// Descend the body but not into function literals: a closure's own returns are
			// not this function's returns, and counting them would let an unrelated
			// two-result callback satisfy or break the check.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if _, isLit := n.(*ast.FuncLit); isLit {
					return false
				}
				ret, isRet := n.(*ast.ReturnStmt)
				if !isRet || len(ret.Results) != 2 {
					return true
				}
				where := fileName + ":" + strconv.Itoa(fset.Position(ret.Pos()).Line)
				if isNilIdent(ret.Results[1]) {
					l.okReturns++
					if isNilIdent(ret.Results[0]) {
						l.violations = append(l.violations,
							where+": returns a nil config with a nil error")
					}
					return true
				}
				l.errReturns++
				if !isNilIdent(ret.Results[0]) {
					l.violations = append(l.violations,
						where+": returns a non-nil config alongside an error "+
							"(`return "+exprText(ret.Results[0])+", ...`) -- "+
							"every error path must return nil (#1777)")
				}
				return true
			})
		}
	}
	// Walk sanity: a derivation that quietly matched nothing must fail loudly rather than
	// read as a clean pass over an empty set (§5b).
	if scanned < 2 {
		t.Fatalf("the walk parsed only %d non-test file(s) in pkg/config; it is not scanning "+
			"the package", scanned)
	}
	// Floors, not exact counts: consolidating error paths behind a helper is a legitimate
	// refactor, but it should lower these numbers deliberately rather than by accident. Named
	// per loader because they genuinely differ -- LoadClientConfig has the three this issue
	// enumerated, LoadServerConfig has two.
	wantMinErrReturns := map[string]int{
		"LoadClientConfig": 3,
		"LoadServerConfig": 2,
	}
	for want, min := range wantMinErrReturns {
		l, ok := loaders[want]
		if !ok {
			t.Fatalf("%s was not found by the source walk; the check scanned the wrong "+
				"thing and would pass over anything", want)
		}
		if l.errReturns < min {
			t.Errorf("%s: found only %d error return(s); expected at least %d. Either the "+
				"error paths moved behind a helper (lower this floor and check the helper "+
				"obeys the contract) or the walk stopped seeing them",
				l.name, l.errReturns, min)
		}
	}

	for _, l := range loaders {
		if l.errReturns < 1 {
			t.Errorf("%s: found no error return at all; the walk is not seeing this "+
				"function's body", l.name)
		}
		if l.okReturns < 1 {
			t.Errorf("%s: found no success return; the walk is not seeing this function's body", l.name)
		}
		for _, v := range l.violations {
			t.Errorf("%s: %s", l.name, v)
		}
	}
}

// TestLoadClientConfigErrorPathCannotLeakATokenIntoTheConfigFile keeps #1772 closed across this
// change. #1790 fixed the class "a token resolved from anywhere other than auth_token: in this
// very file must never be persisted into auth_token:", and flagged the adjacency: on a
// readClientTokenFile error the old code handed back a config carrying AuthToken, TokenFile and
// TokenSource together, and only the retained token_file clause in clientTokenBelongsInConfigFile
// stopped a save from writing it.
//
// Both halves are asserted, because they fail independently:
//   - the load half is now structural -- the error path returns nothing, so there is no object
//     for a caller to save;
//   - the save half is a regression pin on the object the old error path produced. It is green
//     before and after this change by design: its job is to go red if someone restores the
//     non-nil return by deleting the token_file clause instead of keeping it.
func TestLoadClientConfigErrorPathCannotLeakATokenIntoTheConfigFile(t *testing.T) {
	isolateTokenEnvironment(t)

	const secret = "lft_pat_must_not_reach_config_yaml"
	missing := filepath.Join(t.TempDir(), "not-there", "token")

	t.Run("the error path hands back no config to save", func(t *testing.T) {
		cfgPath := writeClientConfigFile(t,
			"auth_token: \""+secret+"\"\ntoken_file: "+yamlPath(missing)+"\n")

		cfg, err := LoadClientConfig(cfgPath)
		if err == nil {
			t.Fatal("an unreadable token_file must be an error")
		}
		if !strings.Contains(err.Error(), "Personal Access Token") {
			t.Fatalf("expected the token_file error, got: %v", err)
		}
		if cfg != nil {
			t.Fatalf("the error path returned a config carrying AuthToken=%q TokenSource=%q "+
				"TokenFile=%q; nothing may come back from an error path for a caller to "+
				"persist (#1772, #1777)", cfg.AuthToken, cfg.TokenSource, cfg.TokenFile)
		}
	})

	t.Run("the save guard still refuses the config the old error path returned", func(t *testing.T) {
		// Reconstructed exactly as LoadClientConfig used to return it: the inline token was
		// seen, so TokenSource says "config file", and token_file: is still set.
		cfg := DefaultClientConfig()
		cfg.AuthToken = secret
		cfg.TokenSource = TokenSourceConfigFile
		cfg.TokenFile = missing

		out := filepath.Join(t.TempDir(), "config.yaml")
		if err := SaveClientConfig(out, cfg); err != nil {
			t.Fatalf("failed to save: %v", err)
		}
		written, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("failed to read back: %v", err)
		}
		if strings.Contains(string(written), secret) {
			t.Error("the token_file clause in clientTokenBelongsInConfigFile is gone: a token " +
				"beside a token_file: was written into the config file (#1772)")
		}
		if !strings.Contains(string(written), "token_file") {
			t.Error("saving dropped token_file, so the next load would find no token at all")
		}
	})

	t.Run("a caller that falls back to the defaults writes no token", func(t *testing.T) {
		// The shape pkg/client/inspector.go uses at both its LoadClientConfig call sites.
		// Green before this change too -- it pins the caller contract, it does not prove it.
		cfgPath := writeClientConfigFile(t,
			"auth_token: \""+secret+"\"\ntoken_file: "+yamlPath(missing)+"\n")

		cfg, err := LoadClientConfig(cfgPath)
		if err != nil {
			cfg = DefaultClientConfig()
		}
		cfg.Subdomain = "changed-by-the-settings-form"

		out := filepath.Join(t.TempDir(), "config.yaml")
		if err := SaveClientConfig(out, cfg); err != nil {
			t.Fatalf("failed to save: %v", err)
		}
		written, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("failed to read back: %v", err)
		}
		if strings.Contains(string(written), secret) {
			t.Error("a save after a failed load copied the token into the config file (#1772)")
		}
	})
}

func returnsPointerAndError(fn *ast.FuncDecl) bool {
	res := fn.Type.Results
	if res == nil || len(res.List) != 2 {
		return false
	}
	if _, ok := res.List[0].Type.(*ast.StarExpr); !ok {
		return false
	}
	id, ok := res.List[1].Type.(*ast.Ident)
	return ok && id.Name == "error"
}

func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func exprText(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "<expr>"
}
