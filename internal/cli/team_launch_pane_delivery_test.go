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

// TestEveryPaneCommandDeliveryIsVerified asserts that every deliverPaneCommand call is
// followed, in the same statement list, by a verifyPaneProcessLaunched call.
//
// WHY STRUCTURAL, AND WHY THIS TEST EXISTS AT ALL. #571 replaced typed delivery with
// respawn-pane at all three sites but added the pane-pid check to only two of them: the
// tmux-session backend delivered unverified, so "pane creation can no longer count as
// success" was true for two paths and false for the third. Nothing failed, because no test
// asserted the two calls travel together -- each site was reviewed on its own.
//
// A per-path execution test would prove the path I remembered to write. This proves the
// PAIRING at every site, including sites added later, which is the property the missing
// one violated.
//
// Delivery and verification share one function each, so the behaviour cannot differ per
// site once the call is present; enforcing "called at every delivery site" is therefore
// sufficient. That is the same shared-predicate argument #573 turns on: one predicate
// called everywhere beats N reimplementations that agree today.
func TestEveryPaneCommandDeliveryIsVerified(t *testing.T) {
	const (
		deliver = "deliverPaneCommand"
		verify  = "verifyPaneProcessLaunched"
	)

	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var unverified []string
	sites := 0

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i, stmt := range block.List {
				if !callsAtThisLevel(stmt, deliver) {
					continue
				}
				sites++
				verified := false
				// Only LATER siblings count. Verification before delivery would be
				// reading a pid the command has not replaced yet.
				for _, later := range block.List[i+1:] {
					if callsAtThisLevel(later, verify) {
						verified = true
						break
					}
				}
				if !verified {
					unverified = append(unverified,
						fset.Position(stmt.Pos()).String())
				}
			}
			return true
		})
	}

	// Anti-vacuity: three delivery sites exist (current-window, new-window, session).
	// A walk that stopped finding them would otherwise report a clean pass having
	// checked nothing -- the check-passes-when-the-instrument-breaks flavour.
	if sites < 3 {
		t.Fatalf("found only %d %s call site(s); want >= 3, so the AST walk is broken", sites, deliver)
	}

	if len(unverified) != 0 {
		t.Errorf("%d %s call site(s) are not followed by %s:\n  %s\n"+
			"Delivering without reading #{pane_pid} lets a pane with no process count as a launched worker (#571).",
			len(unverified), deliver, verify, strings.Join(unverified, "\n  "))
	}
}

// callsAtThisLevel reports whether stmt calls name in its OWN structure rather than
// somewhere inside a nested block.
//
// The nested-block cutoff is not incidental. Without it, an enclosing statement is judged
// by its descendants: `if err := deliverPaneCommand(...); err != nil { ... }` and the whole
// enclosing function body would both "contain" the call, so a verification sitting in one
// block would appear to cover a delivery in another. Each statement list is judged on its
// own members.
func callsAtThisLevel(stmt ast.Stmt, name string) bool {
	found := false
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		// Do not descend into a nested statement list; whatever happens in there
		// belongs to that list's own delivery/verification pairing.
		if _, ok := n.(*ast.BlockStmt); ok && n != ast.Node(stmt) {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
				found = true
				return false
			}
		}
		return true
	}
	ast.Inspect(stmt, walk)
	return found
}
