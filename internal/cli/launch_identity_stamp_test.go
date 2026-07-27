package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #572: the external-lead adoption path built bootstrapack.Expectation as a struct literal
// and so recorded an EMPTY LaunchID, while bootstrapack.NewExpectation always generates one.
// Live identity then refused with "no exact launch id" and an adopted lead could not receive
// goal delivery. A second site on the prepared-run path had the identical defect.
//
// This is the class fix: the constructor is the only sanctioned way to build an Expectation,
// because it is the only thing that guarantees the launch id.
//
// Implemented with go/ast rather than string matching. #575 review found that a
// substring-and-skip-the-line exemption hides a real literal sharing a line with the
// zero-value error return, e.g.
//
//	return bootstrapack.Expectation{}, errorFor(&bootstrapack.Expectation{Required: true})
//
// The AST sees each CompositeLit separately, so the whole string-matching hole class is gone
// rather than narrowed.
func TestNoProductionCodeBuildsExpectationLiterals(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	var offenders []string
	inspected := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		inspected++
		// Exempting EVERY empty literal was too broad (#575 round 3): production code could
		// write rec.BootstrapExpectation = &bootstrapack.Expectation{} and recreate the
		// exact empty-LaunchID defect while passing.
		//
		// The exemption requires a non-nil value in the ERROR POSITION, i.e. the LAST
		// result. Accepting any non-nil identifier ANYWHERE let
		// `return Expectation{}, nil, cached` through on the strength of `cached` while the
		// error result was nil (#575 round 4). Accepting any identifier merely NAMED
		// something other than nil then let `return Expectation{}, err` through in a
		// NAMED-RESULT function where err was still nil (#575 round 5). A variable's name
		// says nothing about its value, so the error position must be PROVEN non-nil rather
		// than merely occupied: see errorResultProvesNonNil. Proof comes from a dominating
		// `err != nil` guard, which is why this walk tracks branch context instead of using
		// a flat ast.Inspect.
		exempt := map[ast.Node]bool{}
		var scanNode func(ast.Node, map[string]bool)
		var scanIf func(*ast.IfStmt, map[string]bool)
		scanIf = func(node *ast.IfStmt, proven map[string]bool) {
			if node.Init != nil {
				scanNode(node.Init, proven)
			}
			body := proven
			if ident := nonNilGuardedIdent(node.Cond); ident != "" {
				body = make(map[string]bool, len(proven)+1)
				for k, v := range proven {
					body[k] = v
				}
				body[ident] = true
			}
			scanNode(node.Body, body)
			// The else arm does NOT inherit the proof: inside `else`, the guarded
			// identifier is precisely the one known to BE nil.
			switch els := node.Else.(type) {
			case *ast.IfStmt:
				scanIf(els, proven)
			case *ast.BlockStmt:
				scanNode(els, proven)
			}
		}
		scanNode = func(n ast.Node, proven map[string]bool) {
			ast.Inspect(n, func(x ast.Node) bool {
				if x == nil {
					return false
				}
				if ifs, isIf := x.(*ast.IfStmt); isIf {
					scanIf(ifs, proven)
					return false
				}
				ret, isRet := x.(*ast.ReturnStmt)
				if !isRet {
					return true
				}
				if len(ret.Results) >= 2 && errorResultProvesNonNil(ret, proven) {
					for _, res := range ret.Results {
						if lit, isLit := res.(*ast.CompositeLit); isLit && len(lit.Elts) == 0 {
							exempt[lit] = true
						}
					}
				}
				return false
			})
		}
		scanNode(file, map[string]bool{})
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Expectation" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "bootstrapack" {
				return true
			}
			if exempt[lit] {
				return true
			}
			offenders = append(offenders, fset.Position(lit.Pos()).String())
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: a broken walk or a parse that matched nothing would report compliance.
	if inspected < 50 {
		t.Fatalf("parsed only %d non-test .go files; the walk is broken", inspected)
	}
	if len(offenders) != 0 {
		t.Errorf("production code builds a populated bootstrapack.Expectation literal at %v; "+
			"use bootstrapack.NewExpectation so the launch id is always stamped (#572)", offenders)
	}
}

// The refusal must name the FAILURE CLASS: #571 counts a worker launched only after identity
// is verified and must distinguish "cannot verify" from "verifiably wrong".
func TestIncompleteRecordRefusalIsNotAMismatch(t *testing.T) {
	msg := incompleteLaunchRecordError().Error()
	for _, want := range []string{"incomplete launch record", "cannot be verified", "not an identity conflict"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal must contain %q so the reader knows the class:\n  %s", want, msg)
		}
	}
}

// #575 review finding 1: the helper said "not mismatched" while the OUTER wrapper still
// labelled it a mismatch, so the operator-visible string contradicted itself and the
// cannot-verify classification was destroyed at exactly the surface that matters.
//
// This asserts the COMPLETE RENDERED string on the promotion path, not the helper in
// isolation. Testing the helper alone is what let the contradiction ship.
func TestRenderedPromotionRefusalDoesNotCallAnIncompleteRecordAMismatch(t *testing.T) {
	project := t.TempDir()
	// A real roster is required or the resolver fails earlier on a missing team.json and the
	// assertion passes or fails for the wrong reason. First draft of this test did exactly
	// that: vacuous fixture, caught because the failure message named team.json.
	if err := team.WriteProfile(project, "default", team.Team{Project: project,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s1"}}}); err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{
		Role: "cto", Handle: "cto", Binary: "codex", Session: "s1",
		Root: filepath.Join(project, ".agent-mail", "s1"), TeamHome: project, TeamProfile: "default",
		PreparedRunGeneration: "g1", PreparedRunDigest: "d1", PreparedRunLaunchAttempt: "a1",
		// BootstrapExpectation deliberately nil: the incomplete-record case.
	}
	// The resolver reads launch.json from disk rather than trusting the passed record, so
	// the record must actually exist there with its BootstrapExpectation absent.
	root := filepath.Join(project, ".agent-mail", "s1")
	if err := launch.Write(filepath.Join(root, "agents", "cto"), rec); err != nil {
		t.Fatal(err)
	}
	_, _, err := verifyRuntimeActionWithRecord("status promotion", project, "default", "s1", "cto", rec)
	if err == nil {
		t.Fatal("an incomplete record must be refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "mismatch") {
		t.Errorf("rendered refusal must not label an incomplete record a mismatch:\n  %s", msg)
	}
	if !strings.Contains(msg, "could not be verified") {
		t.Errorf("rendered refusal must say identity could not be verified:\n  %s", msg)
	}
}

// Three review rounds found the same shape: the cannot-verify classification exists, and
// some surface routes around it and relabels the error a mismatch. Sites found so far:
// two in live_identity's wrapper, four in the refusal family, one pre-validator that ran
// BEFORE the sentinel, one control-continue wrapper, and one no-verdict case.
//
// This makes the acceptance criterion permanent instead of a grep someone remembers to run:
// every site that wraps an error with the mismatch label must first route the sentinel away.
func TestEveryMismatchWrapperGuardsTheIncompleteSentinel(t *testing.T) {
	const label = "verified live identity mismatch"
	const sentinel = "errIncompleteLaunchRecord"
	root := filepath.Join("..", "..", "internal")

	var unguarded []string
	sites := 0

	// guardsSentinel reports whether an if-statement tests errors.Is(..., sentinel) with
	// POSITIVE polarity.
	//
	// #575 round 6: matching the call anywhere in the condition accepted its own negation.
	//
	//	if !errors.Is(err, errIncompleteLaunchRecord) { return err }
	//	return fmt.Errorf("... verified live identity mismatch: %w", err)
	//
	// That terminates, so the block counted as guarded -- while routing away everything
	// EXCEPT the sentinel and dropping the sentinel itself straight into the wrap. The
	// bypass was the exact inverse of the property. So the walk stops at `!`: a negated
	// sentinel test is not a guard, it is the opposite of one.
	guardsSentinel := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(x ast.Node) bool {
			if unary, isUnary := x.(*ast.UnaryExpr); isUnary && unary.Op == token.NOT {
				return false
			}
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Is" {
				return true
			}
			for _, a := range call.Args {
				if id, isID := a.(*ast.Ident); isID && id.Name == sentinel {
					found = true
				}
			}
			return true
		})
		return found
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			guarded := false
			for _, stmt := range block.List {
				if ifs, isIf := stmt.(*ast.IfStmt); isIf && guardsSentinel(ifs.Cond) {
					// The guard must EXIT, not merely test. #575 round 5: a branch that
					// checks the sentinel and falls through
					//
					//	if errors.Is(err, errIncompleteLaunchRecord) { logSomething() }
					//	return fmt.Errorf("launch id mismatch: ...")
					//
					// reaches the mismatch wrap anyway, so an incomplete record is still
					// reported as a verifiable disagreement. Testing the sentinel is not the
					// property being enforced; DECLINING TO REACH the wrap is.
					if blockTerminates(ifs.Body) {
						guarded = true
					}
					continue
				}
				// Does this statement ITSELF wrap with the mismatch label? Stop at nested
				// block boundaries: an enclosing `if err != nil { ... }` contains the label
				// in a child block that this walk visits separately, and judging the
				// enclosing statement flagged correctly-guarded code.
				wraps := false
				ast.Inspect(stmt, func(x ast.Node) bool {
					if x == nil {
						return false
					}
					if b, isBlock := x.(*ast.BlockStmt); isBlock && b != stmt {
						return false
					}
					if lit, isLit := x.(*ast.BasicLit); isLit && strings.Contains(lit.Value, label) {
						wraps = true
					}
					return true
				})
				if !wraps {
					continue
				}
				sites++
				if !guarded {
					unguarded = append(unguarded, fset.Position(stmt.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Anti-vacuity: if the label is renamed this must fail rather than pass by finding nothing.
	if sites == 0 {
		t.Fatalf("found no %q wrap sites; the label was renamed and this test is now blind", label)
	}
	if len(unguarded) != 0 {
		t.Errorf("these sites wrap an error as %q without an errors.Is(%s) guard DOMINATING them "+
			"in the same block: %v. An incomplete record is cannot-verify, not verifiably-wrong",
			label, sentinel, unguarded)
	}
}

// blockTerminates reports whether control cannot fall out of the bottom of block.
//
// Only the LAST statement is examined, because that is what decides fall-through: an early
// return nested inside an inner branch does not stop the block from completing. A block
// ending in another if/else counts only when BOTH arms terminate, since a bare `if` with no
// else always has a path that falls through.
//
// RECORDED RESIDUAL (accepted, not a gap this test will chase). This recognises the
// terminating forms that appear in this package -- return, break, continue, panic, and a
// fully-terminating if/else. It does NOT model goto-target reachability, os.Exit, log.Fatal,
// or a helper whose body always panics. Every one of those is judged NON-terminating, so the
// error direction is a FALSE POSITIVE: the test complains about code that is in fact guarded.
// That direction is safe -- it fails loud and a human reads it -- whereas modelling every
// exit shape would mean reimplementing reachability analysis inside a guard test.
//
// This claim was FALSE when first written (#575 round 6): goto was accepted as terminating,
// making unmodelled labelled flow a silent false NEGATIVE, the opposite of what the sentence
// above promised. Judging goto non-terminating is what makes the promise true. Recording the
// correction here rather than quietly rewording it, because the declaration is the only
// thing a future reader has to tell them where the check's edges are, and a declaration
// nobody re-derives is trusted exactly as far as it is accurate.
func blockTerminates(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	switch last := block.List[len(block.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		// #575 round 6: treating EVERY branch as terminating accepted
		//
		//	if errors.Is(err, errIncompleteLaunchRecord) { goto mismatch }
		//
		// as a guard, when the label it jumps to may be the mismatch wrap itself. That was
		// a SILENT FALSE NEGATIVE, and it directly contradicted this function's own declared
		// residual, which claimed unmodelled labelled flow could only produce fail-loud
		// false positives. A wrong honesty statement is worse than the hole it described.
		//
		// break and continue provably leave this block. goto does not: where it lands is
		// exactly the unmodelled part. fallthrough transfers to the next case rather than
		// leaving. Both are now judged NON-terminating, which makes the declared residual
		// true instead of merely stated -- the conservative direction, and the one that
		// fails loud.
		return last.Tok == token.BREAK || last.Tok == token.CONTINUE
	case *ast.ExprStmt:
		call, ok := last.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		return ok && id.Name == "panic"
	case *ast.IfStmt:
		elseBlock, ok := last.Else.(*ast.BlockStmt)
		if !ok {
			// No else, or an else-if chain this check does not unroll. Either way it
			// is not proof that every path leaves.
			return false
		}
		return blockTerminates(last.Body) && blockTerminates(elseBlock)
	default:
		return false
	}
}

// errorResultProvesNonNil reports whether the last result of ret is provably a non-nil error
// at that point, given the identifiers a dominating `x != nil` guard has already proven
// non-nil.
//
// #575 round 5: accepting any identifier that is not the literal `nil` exempted
// `return Expectation{}, err` inside a NAMED-RESULT function, where err can still be nil at
// the return -- the zero-value Expectation is then the caller's value and the empty-LaunchID
// defect is back. The name of a variable says nothing about its value.
//
// Two shapes count as proof:
//   - an explicit construction in the error position (fmt.Errorf(...), someErr()), which
//     cannot be nil by inspection of the call site
//   - an identifier a dominating `<ident> != nil` guard has proven, i.e. the ordinary
//     `if err != nil { return Expectation{}, err }` shape
//
// RECORDED RESIDUALS (declared per the #575 convergence bound and the #567 enumeration-test
// precedent: state what the check proves and what it cannot, rather than iterating on
// meta-test rigor while the production fix waits).
//
// Not proven, so reported as offenders even when correct -- FALSE POSITIVES, which fail loud
// and get read by a human:
//   - the inverted early-return shape, `if err == nil { return v, nil }` followed by
//     `return Expectation{}, err`, where err is non-nil by elimination
//   - proof carried across a helper, a switch arm, or a for/range condition rather than an
//     `if x != nil`
//   - exit shapes blockTerminates does not model: goto targets, os.Exit, log.Fatal, an
//     always-panicking helper. All judged non-terminating, so they fail loud.
//
// Not caught, so a bypass survives -- FALSE NEGATIVES, both requiring deliberate effort:
//   - an identifier proven non-nil by the guard and then REASSIGNED to nil before the
//     return inside the same branch
//   - a CallExpr in the error position that returns a nil error, e.g. `wrapMaybe(nil)`;
//     "an explicit construction cannot be nil" is true of fmt.Errorf and false in general
//
// The bound is deliberate. Closing these means reimplementing nil-flow analysis inside a
// guard test, and the property that actually matters -- production classification -- has been
// verified clean directly for two review rounds. This test is defense-in-depth.
func errorResultProvesNonNil(ret *ast.ReturnStmt, provenNonNil map[string]bool) bool {
	if len(ret.Results) == 0 {
		return false
	}
	switch last := ret.Results[len(ret.Results)-1].(type) {
	case *ast.CallExpr:
		return true
	case *ast.Ident:
		return last.Name != "nil" && provenNonNil[last.Name]
	default:
		return false
	}
}

// nonNilGuardedIdent returns the identifier a condition of the form `x != nil` proves
// non-nil inside the guarded branch, or "" when cond is not that shape.
func nonNilGuardedIdent(cond ast.Expr) string {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return ""
	}
	left, okLeft := bin.X.(*ast.Ident)
	right, okRight := bin.Y.(*ast.Ident)
	if okLeft && okRight && right.Name == "nil" {
		return left.Name
	}
	// Written the other way round: `nil != err`.
	if okLeft && okRight && left.Name == "nil" {
		return right.Name
	}
	return ""
}
