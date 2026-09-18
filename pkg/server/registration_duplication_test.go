package server

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// The class this file gates, stated once: **per-registration work written twice, once in
// handleRegister and once in handleEdgeRegister.**
//
// This is the fourth pass over it. #2005 named it, #2017 fixed the rate limit, #2018 the
// reservation policy, #2020 the random-subdomain grant and the client version/OS bookkeeping --
// and every pass fixed what its own enumeration found and missed the next member. #2020's body
// states its enumeration left "exactly two members"; re-running that same command on the tree that
// closed #2020 left another (#2032). That is github-workflow §5b's failure mode happening to the
// fixes for §5b's own class, and §5b rule 3 prescribes the way out: widen the gate first and let
// it enumerate, rather than hand-listing instances.
//
// So this is the enumeration, as a test. The fifth pass does not need a human to notice it is due.
//
// ---------------------------------------------------------------------------------------------
//
// **Why blocks and not lines.** The command everyone re-ran by hand was a line-for-line
// `grep -Fxf` between the two handler bodies. On a *correct* tree that reports fourteen lines, and
// almost none of them are defects: `s.maxActiveTunnelsFor(...)`, `s.recordClientVersionAndOS(...)`,
// `s.effectiveTunnelRateLimit(...)` and `s.isValidToken(...)` are calls into the shared helpers,
// which are the fix rather than the defect, and most of the rest is the tunnel counting #2018
// documented as legitimately per-path. A line gate would be red on a correct tree, and the first
// thing anyone would do is widen its exemption list until it said nothing -- which is this repo's
// own stated reason for keeping the EDR Markdown check narrow. A shared-helper call is one
// statement and never reaches the threshold; a copied loop is one statement whose whole subtree
// matches. TestTheGateDoesNotReportASharedHelperCall pins that distinction.
//
// **Why it normalises.** The two handlers spell the same concept differently -- `activeDomains`
// against `edgeReq.Domains`, `req.SubdomainPrefix` against `finalSubdomain`. Those are not merely
// different *names*, they are different AST *shapes* (an identifier against a selector), so
// comparing source text or raw structure finds nothing. Measured, not assumed: a draft that kept
// selector field names reported zero findings on a tree with a known duplicate in it. So every
// expression that is merely a *value reference* collapses to `_`, however it is spelled. What
// survives is what a copy preserves and what carries the meaning: control-flow shape, literals
// (`nil`/`true`/`false` included), type expressions, and the name of every function called.
//
// **Why exact matching, and what that deliberately does not see.** This gate requires the two
// blocks to be identical after normalisation. That is not a weaker choice than fuzzy clone
// detection, it is the right one: **a copy is identical at the moment it is created.** Copy-paste
// is how this class is born, so the gate fires at birth, before anyone has edited one side.
// Divergence is what happens *later* -- and it is the damage, not the signal.
//
// The cost is that a copy which diverged BEFORE this gate existed is invisible to it. That is not
// hypothetical: it is exactly the access-control stamping loop #2032 found, whose two halves
// differed only in that the direct path logged the `UpdateSubdomainReservation` error and the edge
// path suppressed it. Measured: with that divergence in place this gate reported nothing; the same
// tree with the divergence removed reported it immediately. #2032 closes that one instance by
// extracting it, and TestTheGateIsBlindToACopyThatAlreadyDiverged states the blind spot as a test
// rather than as prose, per §5b rule 6, so widening it is a decision rather than an accident.

// registrationDuplicationThreshold is the smallest block this gate calls a duplicate, counted in
// statements.
//
// Five. The smallest real duplicate this class has produced is the client version/OS bookkeeping
// #2020 extracted; TestTheGateCatchesTheBookkeepingBlockThatWasReal drives the gate at a faithful
// copy of it, so a threshold raised past a known defect turns the suite red rather than quietly
// stopping the gate from firing.
const registrationDuplicationThreshold = 5

// registrationDuplicationExemptions is a ratchet, not an exclusion list: every entry must match
// exactly one finding, and every finding must match an entry, so an entry that stops being true
// fails this test rather than rotting quietly.
//
// Keyed by a distinctive line of the block's own source, which survives the line moves a line
// number would not, and which changes if the block itself changes -- an exemption whose subject
// was rewritten should be re-read, not carried forward. The key is matched against source printed
// from the AST, so it cannot be satisfied by a comment: see
// TestAnAnchorThatAppearsOnlyInACommentExcusesNothing.
// It is EMPTY, and that is the intended state rather than an oversight. The one legitimate
// agreement between the two handlers -- the per-user tunnel COUNTING that #2018 documented as
// rightly per-path, since central must add s.edgeLeases for tunnels it is not itself serving and
// the direct path has no such leases to add -- is three statements, so the threshold excludes it
// without anyone having to excuse it. An exclusion that falls out of a stated rule is worth more
// than one that has to be maintained. TestTheGateDoesNotReportTheTunnelCounting pins that, so
// lowering the threshold past it is a decision rather than a surprise.
var registrationDuplicationExemptions = map[string]string{}

// registrationHandlerPair names the two functions whose bodies must not repeat each other.
var registrationHandlerPair = [2]string{"handleRegister", "handleEdgeRegister"}

// ---------------------------------------------------------------------------------------------
// The normaliser.
// ---------------------------------------------------------------------------------------------

// normalisedPredeclared are the identifiers that carry meaning as themselves rather than as a
// reference to whatever a local happens to be called. Erasing these would make `existing != nil`
// and `updated := false` the same shape as any other comparison or assignment.
var normalisedPredeclared = map[string]bool{"nil": true, "true": true, "false": true, "iota": true}

type normaliser struct{ fset *token.FileSet }

// text renders a node exactly as written. Used for type expressions and for the human-facing
// report, never to compare value expressions.
func (n normaliser) text(node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, n.fset, node); err != nil {
		return fmt.Sprintf("«unprintable %T»", node)
	}
	return buf.String()
}

// callee renders what a call calls: the receiver erased, the method name kept, so
// `s.db.GetSubdomainReservationByName(...)` matches a copy of itself without coupling the
// signature to the name of whatever variable holds the receiver.
func (n normaliser) callee(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return "." + f.Sel.Name
	case *ast.ParenExpr:
		return n.callee(f.X)
	default:
		return "_"
	}
}

func (n normaliser) exprs(list []ast.Expr) string {
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, n.expr(e))
	}
	return strings.Join(parts, ",")
}

// expr collapses every expression that is merely a reference to a value -- an identifier, or any
// selector chain rooted at one -- to `_`, because that is precisely the axis on which the two
// handlers legitimately differ.
func (n normaliser) expr(e ast.Expr) string {
	switch x := e.(type) {
	case nil:
		return ""
	case *ast.Ident:
		if normalisedPredeclared[x.Name] {
			return x.Name
		}
		return "_"
	case *ast.SelectorExpr:
		return "_"
	case *ast.BasicLit:
		return x.Kind.String() + ":" + x.Value
	case *ast.CallExpr:
		return n.callee(x.Fun) + "(" + n.exprs(x.Args) + ")"
	case *ast.BinaryExpr:
		return "(" + n.expr(x.X) + x.Op.String() + n.expr(x.Y) + ")"
	case *ast.UnaryExpr:
		return x.Op.String() + n.expr(x.X)
	case *ast.ParenExpr:
		return n.expr(x.X)
	case *ast.StarExpr:
		return "*" + n.expr(x.X)
	case *ast.IndexExpr:
		return "_[" + n.expr(x.Index) + "]"
	case *ast.SliceExpr:
		return "_[" + n.expr(x.Low) + ":" + n.expr(x.High) + "]"
	case *ast.CompositeLit:
		return n.text(x.Type) + "{" + n.exprs(x.Elts) + "}"
	case *ast.KeyValueExpr:
		return n.expr(x.Key) + ":" + n.expr(x.Value)
	case *ast.TypeAssertExpr:
		return "_.(" + n.text(x.Type) + ")"
	case *ast.FuncLit:
		return "func" + n.text(x.Type) + n.stmt(x.Body)
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.StructType, *ast.InterfaceType, *ast.FuncType, *ast.Ellipsis:
		return n.text(x)
	default:
		// Never silently empty. An expression kind this gate does not know must still
		// contribute something distinct, or two unlike expressions would compare equal and
		// it would report a duplicate that is not one (§5c).
		return fmt.Sprintf("«expr %T»", x)
	}
}

func (n normaliser) stmts(list []ast.Stmt) string {
	parts := make([]string, 0, len(list))
	for _, s := range list {
		parts = append(parts, n.stmt(s))
	}
	return strings.Join(parts, ";")
}

func (n normaliser) stmt(s ast.Stmt) string {
	switch x := s.(type) {
	case nil:
		return ""
	case *ast.AssignStmt:
		return n.exprs(x.Lhs) + x.Tok.String() + n.exprs(x.Rhs)
	case *ast.ExprStmt:
		return n.expr(x.X)
	case *ast.IfStmt:
		out := "if " + n.stmt(x.Init) + ";" + n.expr(x.Cond) + n.stmt(x.Body)
		if x.Else != nil {
			out += "else" + n.stmt(x.Else)
		}
		return out
	case *ast.ForStmt:
		return "for " + n.stmt(x.Init) + ";" + n.expr(x.Cond) + ";" + n.stmt(x.Post) + n.stmt(x.Body)
	case *ast.RangeStmt:
		return "range " + n.expr(x.Key) + "," + n.expr(x.Value) + x.Tok.String() + n.expr(x.X) + n.stmt(x.Body)
	case *ast.BlockStmt:
		return "{" + n.stmts(x.List) + "}"
	case *ast.ReturnStmt:
		return "return " + n.exprs(x.Results)
	case *ast.IncDecStmt:
		return n.expr(x.X) + x.Tok.String()
	case *ast.DeclStmt:
		return "decl " + n.text(x.Decl)
	case *ast.SwitchStmt:
		return "switch " + n.stmt(x.Init) + ";" + n.expr(x.Tag) + n.stmt(x.Body)
	case *ast.TypeSwitchStmt:
		return "typeswitch " + n.stmt(x.Init) + ";" + n.stmt(x.Assign) + n.stmt(x.Body)
	case *ast.CaseClause:
		return "case " + n.exprs(x.List) + ":" + n.stmts(x.Body)
	case *ast.CommClause:
		return "comm " + n.stmt(x.Comm) + ":" + n.stmts(x.Body)
	case *ast.SelectStmt:
		return "select" + n.stmt(x.Body)
	case *ast.BranchStmt:
		return x.Tok.String()
	case *ast.DeferStmt:
		return "defer " + n.expr(x.Call)
	case *ast.GoStmt:
		return "go " + n.expr(x.Call)
	case *ast.LabeledStmt:
		return "label:" + n.stmt(x.Stmt)
	case *ast.SendStmt:
		return n.expr(x.Chan) + "<-" + n.expr(x.Value)
	case *ast.EmptyStmt:
		return ";"
	default:
		// Same reasoning as expr's default.
		return fmt.Sprintf("«stmt %T»", x)
	}
}

// ---------------------------------------------------------------------------------------------
// The detector.
// ---------------------------------------------------------------------------------------------

type dupBlock struct {
	sig    string
	weight int
	line   int
	source string
}

// stmtWeight counts the statements in a subtree, which is the unit the threshold is expressed in.
// *ast.BlockStmt is skipped: a block's signature is its parent's minus the keyword, so counting
// both would double every statement.
func stmtWeight(s ast.Stmt) int {
	n := 0
	ast.Inspect(s, func(node ast.Node) bool {
		if st, ok := node.(ast.Stmt); ok {
			if _, isBlock := st.(*ast.BlockStmt); !isBlock {
				n++
			}
		}
		return true
	})
	return n
}

// blocksBySignature indexes every statement in a body, nested ones included -- the duplicate may
// be a loop several levels inside an `if`. Where one signature occurs more than once, the heaviest
// occurrence wins, so the report names the outermost copy.
func blocksBySignature(n normaliser, body *ast.BlockStmt) map[string]dupBlock {
	out := map[string]dupBlock{}
	ast.Inspect(body, func(node ast.Node) bool {
		s, ok := node.(ast.Stmt)
		if !ok {
			return true
		}
		if _, isBlock := s.(*ast.BlockStmt); isBlock {
			return true
		}
		sig := n.stmt(s)
		w := stmtWeight(s)
		if prev, seen := out[sig]; seen && prev.weight >= w {
			return true
		}
		out[sig] = dupBlock{sig: sig, weight: w, line: n.fset.Position(s.Pos()).Line, source: n.text(s)}
		return true
	})
	return out
}

// duplicatedBlocks reports the maximal blocks that appear, identically after normalisation, in
// both function bodies.
func duplicatedBlocks(n normaliser, a, b *ast.FuncDecl) []dupBlock {
	left := blocksBySignature(n, a.Body)
	right := blocksBySignature(n, b.Body)

	var found []dupBlock
	for sig, blk := range left {
		if _, ok := right[sig]; !ok || blk.weight < registrationDuplicationThreshold {
			continue
		}
		found = append(found, blk)
	}

	// Maximal only: a duplicated loop contains duplicated statements, and naming each of them
	// separately would bury the finding under its own children.
	var maximal []dupBlock
	for _, blk := range found {
		contained := false
		for _, other := range found {
			if other.sig != blk.sig && strings.Contains(other.sig, blk.sig) {
				contained = true
				break
			}
		}
		if !contained {
			maximal = append(maximal, blk)
		}
	}
	sort.Slice(maximal, func(i, j int) bool { return maximal[i].line < maximal[j].line })
	return maximal
}

// findFuncs locates named top-level functions, failing loudly when one is missing: a gate that
// finds neither handler reports the same green as a gate that found nothing wrong (§5c rule 5).
func findFuncs(t *testing.T, file *ast.File, names [2]string) [2]*ast.FuncDecl {
	t.Helper()
	var out [2]*ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for i, want := range names {
			if fn.Name.Name == want {
				out[i] = fn
			}
		}
	}
	for i, fn := range out {
		if fn == nil {
			t.Fatalf("could not find %s -- this gate is looking at the wrong thing and would "+
				"pass over any duplication at all", names[i])
		}
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// The gate.
// ---------------------------------------------------------------------------------------------

// TestNeitherRegistrationPathRepeatsTheOther is the class-level gate. See the comment at the top.
func TestNeitherRegistrationPathRepeatsTheOther(t *testing.T) {
	fset := token.NewFileSet()
	// Parsed WITHOUT parser.ParseComments deliberately: comments are then absent from the AST
	// entirely, so neither this gate's findings nor its exemption matching can be influenced by
	// prose -- the #2027 failure mode, where a gate stayed green because the hook's own text
	// still named the thing it had lost. TestAnAnchorThatAppearsOnlyInACommentExcusesNothing
	// proves it by mutation rather than leaving it as a claim.
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing server.go: %v", err)
	}

	n := normaliser{fset: fset}
	handlers := findFuncs(t, file, registrationHandlerPair)
	found := duplicatedBlocks(n, handlers[0], handlers[1])

	verdict := classifyFindings(found, registrationDuplicationExemptions)

	if len(verdict.unexpected) > 0 {
		var lines []string
		for _, blk := range verdict.unexpected {
			lines = append(lines, fmt.Sprintf(
				"server.go:%d  a %d-statement block written identically in both registration "+
					"handlers:\n%s\n    Extract it into registration_policy.go and have both paths "+
					"call it, the way #2017/#2018/#2020 did -- or, if the two paths genuinely must "+
					"differ, add it to registrationDuplicationExemptions saying why.",
				blk.line, blk.weight, indentBlock(blk.source)))
		}
		sort.Strings(lines)
		t.Errorf("%d block(s) are written twice, once per registration path. This is the class "+
			"#2005 named and #2017/#2018/#2020/#2032 each closed one more member of:\n\n%s",
			len(lines), strings.Join(lines, "\n\n"))
	}

	if len(verdict.stale) > 0 {
		t.Errorf("registrationDuplicationExemptions has %d entry/entries that no longer describe "+
			"anything -- delete them, so the list can only shrink:\n  %s",
			len(verdict.stale), strings.Join(verdict.stale, "\n  "))
	}

	for _, a := range verdict.ambiguous {
		t.Errorf("exemption trouble: %s", a)
	}
}

// findingVerdict is what classifyFindings decided, kept as data so the ratchet can be exercised
// on synthetic input rather than only against whatever server.go happens to contain today.
type findingVerdict struct {
	unexpected []dupBlock
	stale      []string
	ambiguous  []string
}

// classifyFindings matches findings against exemptions in BOTH directions, which is what makes
// the list a ratchet rather than an exclusion list: a finding nothing excuses is a failure, and an
// exemption excusing nothing is equally a failure, so the list can only shrink.
func classifyFindings(found []dupBlock, exemptions map[string]string) findingVerdict {
	var v findingVerdict
	matched := map[string]dupBlock{}

	for _, blk := range found {
		var hits []string
		for anchor := range exemptions {
			if strings.Contains(blk.source, anchor) {
				hits = append(hits, anchor)
			}
		}
		sort.Strings(hits)
		switch len(hits) {
		case 0:
			v.unexpected = append(v.unexpected, blk)
		case 1:
			if prev, dup := matched[hits[0]]; dup {
				v.ambiguous = append(v.ambiguous, fmt.Sprintf(
					"the exemption anchored on %q matches two findings (lines %d and %d) -- one "+
						"exemption must excuse exactly one block, or a new duplicate would be "+
						"excused by an old entry", hits[0], prev.line, blk.line))
				continue
			}
			matched[hits[0]] = blk
		default:
			v.ambiguous = append(v.ambiguous, fmt.Sprintf(
				"line %d matches %d exemption anchors (%s) -- an anchor must be distinctive "+
					"enough to name one block", blk.line, len(hits), strings.Join(hits, ", ")))
		}
	}

	for anchor := range exemptions {
		if _, ok := matched[anchor]; !ok {
			v.stale = append(v.stale, anchor)
		}
	}
	sort.Strings(v.stale)
	sort.Strings(v.ambiguous)
	return v
}

func indentBlock(s string) string {
	return "        " + strings.ReplaceAll(s, "\n", "\n        ")
}

// ---------------------------------------------------------------------------------------------
// The gate's own controls. Every case below is labelled FIRING (it fails if the detector stops
// detecting) or BOUNDING (it passes before and after, on purpose, pinning a deliberate edge so
// that crossing it turns the suite red) -- the convention tests/hooks/test-gate-scope-boundaries.sh
// uses, and the distinction §5b rule 6 turns on.
// ---------------------------------------------------------------------------------------------

// detectIn runs the real detector over two synthetic handlers, so the controls exercise the
// production code path rather than a re-implementation of it (§5c rule 4).
func detectIn(t *testing.T, src string) []dupBlock {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the synthetic source: %v", err)
	}
	n := normaliser{fset: fset}
	fns := findFuncs(t, file, [2]string{"handlerA", "handlerB"})
	return duplicatedBlocks(n, fns[0], fns[1])
}

// TestTheGateCatchesTheBookkeepingBlockThatWasReal is FIRING, and the block is not invented: it
// is the client version/OS bookkeeping as it actually stood in both handlers before #2020
// extracted it, down to the `req.`/`edgeReq.` split. If this gate had existed then, this is the
// report it would have produced.
func TestTheGateCatchesTheBookkeepingBlockThatWasReal(t *testing.T) {
	found := detectIn(t, `package p
func handlerA() {
	if (req.ClientVersion != "" || req.ClientOS != "") && userRec != nil {
		changed := false
		if req.ClientVersion != "" && userRec.LastClientVersion != req.ClientVersion {
			userRec.LastClientVersion = req.ClientVersion
			changed = true
		}
		if req.ClientOS != "" && userRec.LastClientOS != req.ClientOS {
			userRec.LastClientOS = req.ClientOS
			changed = true
		}
		if changed {
			_ = s.db.UpdateUser(userRec)
		}
	}
}
func handlerB() {
	if (edgeReq.ClientVersion != "" || edgeReq.ClientOS != "") && userRec != nil {
		changed := false
		if edgeReq.ClientVersion != "" && userRec.LastClientVersion != edgeReq.ClientVersion {
			userRec.LastClientVersion = edgeReq.ClientVersion
			changed = true
		}
		if edgeReq.ClientOS != "" && userRec.LastClientOS != edgeReq.ClientOS {
			userRec.LastClientOS = edgeReq.ClientOS
			changed = true
		}
		if changed {
			_ = s.db.UpdateUser(userRec)
		}
	}
}`)
	if len(found) != 1 {
		t.Fatalf("the gate found %d duplicate(s) in a faithful copy of the real #2020 defect, want 1 "+
			"-- with the threshold at %d it would not have caught the smallest member this class "+
			"has actually produced", len(found), registrationDuplicationThreshold)
	}
	if found[0].weight < registrationDuplicationThreshold {
		t.Errorf("reported a %d-statement block, below the threshold of %d", found[0].weight, registrationDuplicationThreshold)
	}
}

// TestTheGateSeesThroughRenamedLocalsAndReshapedOperands is FIRING, and is the normaliser's own
// control. The two copies below share no variable names at all, and one spells its operand as a
// bare identifier where the other uses a selector -- exactly the `activeDomains` against
// `edgeReq.Domains` split that makes raw structural comparison useless here.
func TestTheGateSeesThroughRenamedLocalsAndReshapedOperands(t *testing.T) {
	found := detectIn(t, `package p
func handlerA() {
	for _, d := range activeDomains {
		existing, err := s.db.GetSubdomainReservationByName(req.SubdomainPrefix, d)
		if err == nil && existing != nil {
			updated := false
			if req.Passcode != "" {
				existing.Passcode = req.Passcode
				updated = true
			}
			accessByDomain[d] = [3]string{existing.Passcode, existing.WhitelistIPs, existing.AccessMode}
		}
	}
}
func handlerB() {
	for _, name := range edgeReq.Domains {
		row, failure := s.db.GetSubdomainReservationByName(finalSubdomain, name)
		if failure == nil && row != nil {
			touched := false
			if edgeReq.Passcode != "" {
				row.Passcode = edgeReq.Passcode
				touched = true
			}
			edgeAccessByDomain[name] = [3]string{row.Passcode, row.WhitelistIPs, row.AccessMode}
		}
	}
}`)
	if len(found) != 1 {
		t.Fatalf("the gate found %d duplicate(s), want 1 -- renaming every local and respelling one "+
			"operand as a selector must not hide a copy, or the gate is defeated by the rename that "+
			"copy-paste performs anyway", len(found))
	}
}

// TestTheGateDoesNotReportASharedHelperCall is BOUNDING. Both paths calling one extracted helper
// is the FIX for this class; a gate that reported it would be red on a correct tree and would be
// silenced by exemptions until it said nothing. This case passes today and must keep passing.
func TestTheGateDoesNotReportASharedHelperCall(t *testing.T) {
	found := detectIn(t, `package p
func handlerA() {
	maxTunnels := s.maxActiveTunnelsFor(user, userRec)
	s.recordClientVersionAndOS(userRec, req.ClientVersion, req.ClientOS)
	effectiveLimit := s.effectiveTunnelRateLimit(req.RateLimit, userRec)
	if refusal := s.quotaRegistrationRefusal(user.ID); refusal != "" {
		return
	}
}
func handlerB() {
	maxTunnels := s.maxActiveTunnelsFor(user, userRec)
	s.recordClientVersionAndOS(userRec, edgeReq.ClientVersion, edgeReq.ClientOS)
	effectiveLimit := s.effectiveTunnelRateLimit(edgeReq.RateLimit, userRec)
	if refusal := s.quotaRegistrationRefusal(user.ID); refusal != "" {
		return
	}
}`)
	if len(found) != 0 {
		t.Fatalf("the gate reported %d duplicate(s) for two paths calling the same shared helpers, "+
			"want 0 -- that is the fix for this class, not an instance of it:\n%s",
			len(found), found[0].source)
	}
}

// TestTheGateIsBlindToACopyThatAlreadyDiverged is BOUNDING, and states this gate's blind spot as
// a test rather than as prose (§5b rule 6).
//
// The two blocks below are the access-control stamping loop as #2032 found it: identical except
// that one logs the write failure and the other discards it. The gate does NOT report this, and
// that is the deliberate consequence of exact matching -- which is the right trade, because a copy
// is identical at the moment it is created and this gate fires then. A copy only reaches this
// shape by being edited on one side AFTER the gate would already have caught it.
//
// If this case ever goes red, someone has made the matching tolerant of divergence. That may well
// be an improvement -- but it is a decision about false positives, so make it deliberately rather
// than discovering it here.
func TestTheGateIsBlindToACopyThatAlreadyDiverged(t *testing.T) {
	found := detectIn(t, `package p
func handlerA() {
	for _, d := range activeDomains {
		existing, err := s.db.GetSubdomainReservationByName(req.SubdomainPrefix, d)
		if err == nil && existing != nil && existing.UserID == user.ID {
			updated := false
			if req.Passcode != "" {
				existing.Passcode = req.Passcode
				updated = true
			}
			if updated {
				if err := s.db.UpdateSubdomainReservation(existing); err != nil {
					slog.Info("failed")
				}
			}
			accessByDomain[d] = [3]string{existing.Passcode, existing.WhitelistIPs, existing.AccessMode}
		}
	}
}
func handlerB() {
	for _, d := range edgeReq.Domains {
		existing, err := s.db.GetSubdomainReservationByName(finalSubdomain, d)
		if err == nil && existing != nil && existing.UserID == user.ID {
			updated := false
			if edgeReq.Passcode != "" {
				existing.Passcode = edgeReq.Passcode
				updated = true
			}
			if updated {
				_ = s.db.UpdateSubdomainReservation(existing)
			}
			edgeAccessByDomain[d] = [3]string{existing.Passcode, existing.WhitelistIPs, existing.AccessMode}
		}
	}
}`)
	for _, blk := range found {
		if strings.Contains(blk.source, "GetSubdomainReservationByName") {
			t.Fatalf("the gate now reports a copy that has diverged on one statement, at line %d. "+
				"That is a real widening and may be wanted -- but it changes this gate's false-positive "+
				"profile, so confirm it deliberately and update this case rather than deleting it:\n%s",
				blk.line, blk.source)
		}
	}
}

// TestAnAnchorThatAppearsOnlyInACommentExcusesNothing is FIRING, and is the self-match proof the
// coordinator asked for rather than the claim that "comments are not in the AST".
//
// #2027's failure mode was a gate kept green by prose that still named what it had lost. Here the
// hazard would be an exemption anchor satisfied by a COMMENT in server.go rather than by the code
// it is supposed to describe -- which would let someone excuse a real duplicate by writing about
// it. The gate parses without ParseComments and matches anchors against source printed from the
// AST, so the comment below cannot reach the matcher; this test fails if either of those changes.
func TestAnAnchorThatAppearsOnlyInACommentExcusesNothing(t *testing.T) {
	const anchor = "uniqueSubs[l.SubdomainPrefix] = true"
	found := detectIn(t, `package p
func handlerA() {
	// uniqueSubs[l.SubdomainPrefix] = true -- prose naming the exemption anchor, nothing more.
	for _, d := range domains {
		row, err := s.db.GetSubdomainReservationByName(prefix, d)
		if err == nil && row != nil {
			changed := false
			if req.Passcode != "" {
				row.Passcode = req.Passcode
				changed = true
			}
			out[d] = [3]string{row.Passcode, row.WhitelistIPs, row.AccessMode}
		}
	}
}
func handlerB() {
	// uniqueSubs[l.SubdomainPrefix] = true -- the same prose, on the other side.
	for _, name := range other.Domains {
		row, err := s.db.GetSubdomainReservationByName(other.Prefix, name)
		if err == nil && row != nil {
			changed := false
			if other.Passcode != "" {
				row.Passcode = other.Passcode
				changed = true
			}
			sink[name] = [3]string{row.Passcode, row.WhitelistIPs, row.AccessMode}
		}
	}
}`)
	if len(found) != 1 {
		t.Fatalf("the gate found %d duplicate(s), want 1 -- the copy itself must still be reported", len(found))
	}
	if strings.Contains(found[0].source, anchor) {
		t.Errorf("the reported block's source contains the exemption anchor %q, which appears in "+
			"this fixture ONLY inside a comment. The gate is reading comments, so a real duplicate "+
			"could be excused by writing about it rather than by fixing it (#2027's shape).", anchor)
	}
}

// TestTheGateDoesNotReportTheTunnelCounting is BOUNDING. The per-user tunnel counting is the one
// thing the two handlers legitimately agree on at more than one statement, and #2018 documented
// why: central must add s.edgeLeases for tunnels it is not itself serving, and the direct path has
// none to add. It is three statements, so the threshold excludes it and no exemption is needed --
// which is why registrationDuplicationExemptions is empty.
//
// If this goes red, the threshold has been lowered under it. That may be wanted, but it means
// re-introducing an exemption entry for a block that is correct as it stands.
func TestTheGateDoesNotReportTheTunnelCounting(t *testing.T) {
	found := detectIn(t, `package p
func handlerA() {
	for _, l := range leases {
		if l.UserID == user.ID {
			uniqueSubs[l.SubdomainPrefix] = true
		}
	}
}
func handlerB() {
	for _, l := range leases {
		if l.UserID == user.ID {
			uniqueSubs[l.SubdomainPrefix] = true
		}
	}
}`)
	if len(found) != 0 {
		t.Fatalf("the gate reported the tunnel-counting block (%d finding(s)), which #2018 "+
			"documented as legitimately per-path. Either raise registrationDuplicationThreshold "+
			"back above it, or add it to registrationDuplicationExemptions with that reasoning.",
			len(found))
	}
}

// TestTheExemptionListIsARatchetInBothDirections is FIRING, and exercises the mechanism rather
// than the tree: registrationDuplicationExemptions is empty today, so nothing about server.go
// would notice if the matching or the staleness check stopped working (§5c rule 3).
func TestTheExemptionListIsARatchetInBothDirections(t *testing.T) {
	block := dupBlock{sig: "x", weight: 9, line: 42, source: "for _, d := range domains {\n\tuniqueThing(d)\n}"}

	t.Run("a finding nothing excuses is reported", func(t *testing.T) {
		v := classifyFindings([]dupBlock{block}, map[string]string{})
		if len(v.unexpected) != 1 {
			t.Fatalf("got %d unexpected finding(s), want 1 -- an unexcused duplicate must fail", len(v.unexpected))
		}
		if len(v.stale) != 0 {
			t.Errorf("got %d stale entry/entries, want 0", len(v.stale))
		}
	})

	t.Run("a matching exemption excuses exactly it", func(t *testing.T) {
		v := classifyFindings([]dupBlock{block}, map[string]string{"uniqueThing(d)": "because"})
		if len(v.unexpected) != 0 || len(v.stale) != 0 || len(v.ambiguous) != 0 {
			t.Fatalf("an exemption that names the finding should leave nothing to report, got "+
				"unexpected=%d stale=%d ambiguous=%d", len(v.unexpected), len(v.stale), len(v.ambiguous))
		}
	})

	t.Run("an exemption that excuses nothing is stale", func(t *testing.T) {
		v := classifyFindings(nil, map[string]string{"somethingLongGone()": "because"})
		if len(v.stale) != 1 {
			t.Fatalf("got %d stale entry/entries, want 1 -- an entry that stopped being true must "+
				"fail rather than rot, which is the whole difference between a ratchet and an "+
				"exclusion list", len(v.stale))
		}
	})

	t.Run("one exemption may not excuse two findings", func(t *testing.T) {
		second := block
		second.line = 99
		v := classifyFindings([]dupBlock{block, second}, map[string]string{"uniqueThing(d)": "because"})
		if len(v.ambiguous) != 1 {
			t.Fatalf("got %d ambiguity report(s), want 1 -- otherwise a NEW duplicate would be "+
				"silently excused by an OLD entry, which is how an exclusion list rots", len(v.ambiguous))
		}
	})
}
