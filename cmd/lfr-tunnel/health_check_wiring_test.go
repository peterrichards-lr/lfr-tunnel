package main

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"testing"
)

// #2270 was not a broken function. Both slices were populated correctly and main passed the
// wrong one: portMappings, whose LocalPort is rewritten to the interceptor's dynamic port, where
// regPortMappings holds the target's. Every unit test passed, because they tested the port list
// in isolation and the defect was in the argument.
//
// This is the same shape as #1708 and it gets the same guard, next to it in this directory: an
// assertion against main.go's syntax tree that the call site passes the un-mutated slice. Text
// matching would be satisfied by a comment; the tree is not.
//
// It deliberately proves less than it looks like it does. It says the right name is passed, not
// that the name still means what it meant -- if somebody made regPortMappings the rewritten one,
// this would pass. What makes that safe is that the two are created three lines apart with the
// copy between them, and the e2e spec drives the whole thing against a real target host.
func TestHealthCheckPortsComeFromTheUnmutatedMappings(t *testing.T) {
	const (
		helper   = "healthCheckPortsFor"
		wantArg  = "regPortMappings"
		wrongArg = "portMappings"
	)

	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	found := 0
	goast.Inspect(file, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*goast.Ident)
		if !ok || ident.Name != helper {
			return true
		}
		found++

		if len(call.Args) != 1 {
			t.Errorf("%s is called with %d arguments at %s; want exactly one",
				helper, len(call.Args), fset.Position(call.Pos()))
			return true
		}
		arg, ok := call.Args[0].(*goast.Ident)
		if !ok {
			t.Errorf("%s is called with a %T at %s; want the identifier %s",
				helper, call.Args[0], fset.Position(call.Pos()), wantArg)
			return true
		}
		if arg.Name == wrongArg {
			t.Errorf("%s at %s is passed %s, whose LocalPort main rewrites to the interceptor's "+
				"dynamic port. The health check dials TargetHost:port, so this addresses a socket "+
				"that is open on 127.0.0.1 and closed on the target -- #2270, exactly as it was.",
				helper, fset.Position(call.Pos()), wrongArg)
			return true
		}
		if arg.Name != wantArg {
			t.Errorf("%s at %s is passed %q; want %s, the copy taken before the rewrite",
				helper, fset.Position(call.Pos()), arg.Name, wantArg)
		}
		return true
	})

	// Anti-vacuity. A renamed helper, or a call deleted outright, would otherwise make every
	// assertion above unreachable and this test silently green -- which is #1708's failure
	// mode, not just its subject.
	if found == 0 {
		t.Fatalf("main.go contains no call to %s; the health check is either unwired or the "+
			"helper was renamed and this guard now watches nothing", helper)
	}
}

// The rewrite this depends on must still be there. If main stopped mutating portMappings, the
// test above would keep passing while guarding a distinction that no longer exists -- and the
// reader would be told a story about a hazard that had gone.
func TestMainStillRewritesThePortMappings(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	rewritten := false
	goast.Inspect(file, func(n goast.Node) bool {
		assign, ok := n.(*goast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			sel, ok := lhs.(*goast.SelectorExpr)
			if !ok || sel.Sel.Name != "LocalPort" {
				continue
			}
			index, ok := sel.X.(*goast.IndexExpr)
			if !ok {
				continue
			}
			if ident, ok := index.X.(*goast.Ident); ok && ident.Name == "portMappings" {
				rewritten = true
			}
		}
		return true
	})

	if !rewritten {
		t.Error("main.go no longer assigns portMappings[i].LocalPort. If the rewrite is gone, " +
			"the regPortMappings copy and the guard above are guarding nothing, and both should " +
			"go with it rather than be left describing a hazard that no longer exists.")
	}
}
