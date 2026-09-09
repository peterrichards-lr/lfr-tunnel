package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// The class this file gates, stated once: **a goroutine that reaches the database and is not
// counted by Server.bgWG.** Stop cancels the context, waits on bgWG, and closes the database;
// a goroutine bgWG never heard of is still inside database/sql when that happens.
//
// It surfaced as #1833 -- TestServer_HandleAdminRetryVanityDomain failing on windows-latest in
// t.TempDir cleanup, "The process cannot access the file because it is being used by another
// process", after its body had passed -- because the pool is capped at one connection and
// sql.DB.Close does not wait for a connection that is checked out. On Unix the same leak is
// invisible: an open file can still be unlinked.
//
// Two goroutines in that one handler were untracked, and pinning that handler would have left
// the rest. So this gate is over the *shape*: every `go` statement in package server must be
// one whose target provably never touches s.db, or a deliberate exception listed below. New
// handlers are covered without anyone remembering this file exists.
//
// What this gate does NOT see, stated as an exception list rather than as prose, because prose
// does not fail: a call through a field or local whose type it cannot resolve (see
// untrackedGoroutineExceptions). It also does not follow calls into other packages. The dynamic
// half of the property -- that Stop actually leaves nothing inside the database -- is
// TestStopLeavesNothingInsideTheDatabase in server_stop_test.go; the two together are the guard,
// and neither is sufficient alone.

// untrackedGoroutineExceptions is a ratchet, not an exclusion list: it is compared for exact
// equality, so an entry that stops being true fails this test rather than rotting quietly.
// Keyed by "<file> <enclosing function>", which survives the line moves that a line number
// would not.
var untrackedGoroutineExceptions = map[string]string{
	"server_domain.go runVanityDomainHook": "the hook's output scanner. It writes stage rows, " +
		"but runVanityDomainHook joins it on scanDone before returning, so it cannot outlive " +
		"its own spawner -- and that spawner is what the call sites now track.",
	"server.go NewServer": "the edge control channel, which does its own bgWG.Add/Done around " +
		"this exact goroutine (the pattern predates goTracked).",
}

type goSite struct {
	file    string
	line    int
	fn      string
	target  string
	reason  string // "reaches the database" or "target could not be resolved"
	tracked bool
}

func (g goSite) key() string { return g.file + " " + g.fn }

// TestNoUntrackedGoroutineReachesTheDatabase is the class-level gate. See the comment above.
func TestNoUntrackedGoroutineReachesTheDatabase(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}
	// A gate that scans nothing reports the same green as a gate that found nothing wrong; this
	// package has shipped that twice (github-workflow §5c). api.go alone is 5,000 lines.
	if len(files) < 20 {
		t.Fatalf("only %d source files were scanned -- the gate is looking at the wrong "+
			"directory, and would pass over any defect at all", len(files))
	}

	sites := analyseGoStatements(fset, files)

	found := map[string]goSite{}
	for _, s := range sites {
		if s.tracked {
			continue
		}
		if prev, ok := found[s.key()]; ok {
			// Same function, several goroutines: keep the first, they share a verdict.
			_ = prev
			continue
		}
		found[s.key()] = s
	}

	var unexpected []string
	for k, s := range found {
		if _, allowed := untrackedGoroutineExceptions[k]; !allowed {
			unexpected = append(unexpected, fmt.Sprintf(
				"%s:%d  go %s  -- %s.\n    Wrap it: s.goTracked(func() { ... }), so Stop waits for it "+
					"before closing the database (#1833).", s.file, s.line, s.target, s.reason))
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("%d goroutine(s) can reach the database without bgWG knowing:\n%s",
			len(unexpected), strings.Join(unexpected, "\n"))
	}

	var stale []string
	for k := range untrackedGoroutineExceptions {
		if _, ok := found[k]; !ok {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("untrackedGoroutineExceptions has %d entry/entries that no longer describe "+
			"anything -- delete them, so the list can only shrink:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestTheGateCatchesTheShapeItExistsFor is the gate's own mutation test. A checker that reports
// green over source it failed to understand is indistinguishable from one that found nothing
// wrong, and this package has shipped that twice (see github-workflow §5c). The fixture is the
// #1833 defect in miniature, plus its fix, plus the indirection that made the real one hard to
// see: the database access is two calls deep, not in the goroutine's own body.
func TestTheGateCatchesTheShapeItExistsFor(t *testing.T) {
	const fixture = `package server

type Server struct{ db *DB }

func (s *Server) readSetting() string { return s.db.Get("x") }
func (s *Server) runHook(a string)    { s.readSetting() }
func (s *Server) harmless()           {}

func (s *Server) leaks(a string)       { go s.runHook(a) }
func (s *Server) leaksInClosure()      { go func() { s.runHook("x") }() }
func (s *Server) fixed(a string)       { s.goTracked(func() { s.runHook(a) }) }
func (s *Server) fine()                { go s.harmless() }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	flagged := map[string]bool{}
	for _, s := range analyseGoStatements(fset, []*ast.File{f}) {
		if !s.tracked {
			flagged[s.fn] = true
		}
	}

	if !flagged["leaks"] {
		t.Error("the gate did not flag `go s.runHook(a)`, which reaches s.db two calls deep -- " +
			"exactly the api.go:1251 defect. It would have reported green on #1833.")
	}
	if !flagged["leaksInClosure"] {
		t.Error("the gate did not flag a closure calling into the database, which is the shape " +
			"writeAudit and touchPAT both had.")
	}
	if flagged["fixed"] {
		t.Error("the gate flagged a goTracked call site, so the fix it demands would not satisfy it.")
	}
	if flagged["fine"] {
		t.Error("the gate flagged a goroutine that never touches the database -- it is reporting " +
			"on `go`, not on database access, and would force noise into the exception list.")
	}
}

// --- the analysis -------------------------------------------------------------------------
//
// Deliberately name-based rather than type-checked: go/types would need the whole build, and
// this has to run as an ordinary test. It resolves a call target three ways -- the enclosing
// receiver, a field of Server whose type is declared in this package, and a method name unique
// in the package -- and reports anything else as unresolved rather than assuming it is safe.

type fnKey struct{ typ, name string }

type fnInfo struct {
	touchesDB bool
	calls     []fnKey
}

func analyseGoStatements(fset *token.FileSet, files []*ast.File) []goSite {
	methodOwners := map[string][]string{} // method name -> receiver type names
	serverFields := map[string]string{}   // Server field name -> in-package type name

	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if typ := receiverType(d); typ != "" {
					methodOwners[d.Name.Name] = append(methodOwners[d.Name.Name], typ)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != "Server" {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range st.Fields.List {
						name := typeName(field.Type)
						for _, id := range field.Names {
							serverFields[id.Name] = name
						}
					}
				}
			}
		}
	}

	funcs := map[fnKey]*fnInfo{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := fnKey{receiverType(fd), fd.Name.Name}
			info := analyseBody(fd.Body, receiverName(fd), key.typ, methodOwners, serverFields)
			if prev, ok := funcs[key]; ok {
				prev.touchesDB = prev.touchesDB || info.touchesDB
				prev.calls = append(prev.calls, info.calls...)
			} else {
				funcs[key] = info
			}
		}
	}

	for changed := true; changed; {
		changed = false
		for _, info := range funcs {
			if info.touchesDB {
				continue
			}
			for _, c := range info.calls {
				if callee, ok := funcs[c]; ok && callee.touchesDB {
					info.touchesDB, changed = true, true
					break
				}
			}
		}
	}

	var sites []goSite
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			recv, typ := receiverName(fd), receiverType(fd)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				gs, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				pos := fset.Position(gs.Pos())
				site := goSite{
					file: pos.Filename[strings.LastIndex(pos.Filename, "/")+1:],
					line: pos.Line,
					fn:   fd.Name.Name,
				}

				switch target := gs.Call.Fun.(type) {
				case *ast.FuncLit:
					site.target = "func() { ... }"
					info := analyseBody(target.Body, recv, typ, methodOwners, serverFields)
					site.tracked = bodyDoesItsOwnBGWait(target.Body)
					if info.touchesDB || reachesDB(info.calls, funcs) {
						site.reason = "it reaches the database"
					}
					if unresolved(target.Body, recv, methodOwners, serverFields) && site.reason == "" {
						site.reason = "it calls something this gate cannot resolve"
					}
				default:
					name, key, resolved := resolveCallee(gs.Call.Fun, recv, typ, methodOwners, serverFields)
					site.target = name + "()"
					switch {
					case !resolved:
						site.reason = "its target could not be resolved, so it cannot be shown not to touch the database"
					case funcs[key] != nil && funcs[key].touchesDB:
						site.reason = "it reaches the database"
					}
				}
				if site.reason == "" {
					site.tracked = true // nothing to track: it cannot reach the database
				}
				sites = append(sites, site)
				return true
			})
		}
	}
	return sites
}

func reachesDB(calls []fnKey, funcs map[fnKey]*fnInfo) bool {
	for _, c := range calls {
		if info, ok := funcs[c]; ok && info.touchesDB {
			return true
		}
	}
	return false
}

// bodyDoesItsOwnBGWait recognises the pre-goTracked spelling, `defer s.bgWG.Done()`, so the one
// call site that still uses it is not reported as a leak.
func bodyDoesItsOwnBGWait(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Done" {
			return true
		}
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "bgWG" {
			found = true
		}
		return true
	})
	return found
}

func analyseBody(body ast.Node, recv, typ string, owners map[string][]string, fields map[string]string) *fnInfo {
	info := &fnInfo{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := e.X.(*ast.Ident); ok {
				// s.db.Anything(...) on the server, or the `dbConn := s.db` handoff two
				// call sites use to survive the server being torn down.
				if (id.Name == recv && e.Sel.Name == "db") || id.Name == "dbConn" {
					info.touchesDB = true
				}
			}
		case *ast.CallExpr:
			if _, key, resolved := resolveCallee(e.Fun, recv, typ, owners, fields); resolved {
				info.calls = append(info.calls, key)
			}
		}
		return true
	})
	return info
}

// unresolved reports whether the body starts a call this analysis cannot follow at all -- a
// method on a value whose type it cannot name. Only consulted for goroutine bodies, where an
// unfollowable call is a hole in the gate rather than ordinary code.
func unresolved(body ast.Node, recv string, owners map[string][]string, fields map[string]string) bool {
	hole := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name == recv {
			return true // package qualifier or the receiver: handled by resolveCallee
		}
		if len(owners[sel.Sel.Name]) == 1 {
			return true
		}
		if _, isMethod := owners[sel.Sel.Name]; isMethod {
			hole = true // ambiguous method name on an unknown value
		}
		return true
	})
	return hole
}

// resolveCallee names the function a call expression targets, as a (receiver type, name) pair.
func resolveCallee(fun ast.Expr, recv, typ string, owners map[string][]string, fields map[string]string) (string, fnKey, bool) {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name, fnKey{"", f.Name}, true
	case *ast.SelectorExpr:
		switch x := f.X.(type) {
		case *ast.Ident:
			if x.Name == recv {
				return recv + "." + f.Sel.Name, fnKey{typ, f.Sel.Name}, true
			}
			if o := owners[f.Sel.Name]; len(o) == 1 {
				return x.Name + "." + f.Sel.Name, fnKey{o[0], f.Sel.Name}, true
			}
			// A package-qualified call (slog.Info, time.Now): not a method here, so it
			// cannot reach s.db through anything this package owns.
			if _, isMethod := owners[f.Sel.Name]; !isMethod {
				return x.Name + "." + f.Sel.Name, fnKey{}, true
			}
			return x.Name + "." + f.Sel.Name, fnKey{}, false
		case *ast.SelectorExpr:
			// s.metrics.Start(...) -- resolvable when the field's type is declared here.
			if id, ok := x.X.(*ast.Ident); ok && id.Name == recv {
				if ft, ok := fields[x.Sel.Name]; ok && ft != "" {
					if _, isMethod := owners[f.Sel.Name]; isMethod {
						return recv + "." + x.Sel.Name + "." + f.Sel.Name, fnKey{ft, f.Sel.Name}, true
					}
				}
			}
			return exprString(f), fnKey{}, false
		}
	}
	return exprString(fun), fnKey{}, false
}

func receiverType(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	return typeName(fd.Recv.List[0].Type)
}

func receiverName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 || len(fd.Recv.List[0].Names) == 0 {
		return ""
	}
	return fd.Recv.List[0].Names[0].Name
}

// typeName reduces *T / T to "T", and anything from another package to "" -- this analysis
// cannot follow those, and says so rather than assuming they are safe.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	}
	return "?"
}
