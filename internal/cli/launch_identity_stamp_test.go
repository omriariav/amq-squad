package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #572: the external-lead adoption path built bootstrapack.Expectation as a struct literal
// and so recorded an EMPTY LaunchID, while bootstrapack.NewExpectation always generates one.
// Live identity then refused with "no exact launch id" and an adopted lead could not receive
// goal delivery.
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
