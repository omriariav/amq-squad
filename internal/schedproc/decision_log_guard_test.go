package schedproc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSchedulerDecisionLogWritersUseNamedFileLock(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	writers := map[string]string{
		filepath.FromSlash("internal/schedproc/process.go"):   "o.Log = flock.JSONLWriter{Path: path}",
		filepath.FromSlash("internal/schedproc/decisions.go"): "flock.JSONLWriter{Path: DecisionsPath(root)}",
		filepath.FromSlash("internal/sealchain/migration.go"): "flock.WithJSONLFile(path",
	}
	for name, lockedCall := range writers {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), lockedCall) {
			t.Errorf("decision-log writer %s does not take the named JSONL file lock through %q", name, lockedCall)
		}
	}

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return unlockedDecisionLogOpener(path, raw)
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("planted unlocked opener is rejected", func(t *testing.T) {
		planted := []byte(`package planted
import "os"
func write(root string) { os.OpenFile(root + "/scheduler/decisions.jsonl", os.O_APPEND, 0600) }
`)
		if err := unlockedDecisionLogOpener("internal/planted/opener.go", planted); err == nil {
			t.Fatal("guard accepted a planted unlocked decision-log opener")
		}
	})
}

func unlockedDecisionLogOpener(path string, raw []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		return err
	}
	tainted := map[string]bool{}
	for changed := true; changed; {
		changed = false
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.AssignStmt:
				for i, rhs := range n.Rhs {
					if !decisionPathExpr(rhs, tainted) {
						continue
					}
					for _, lhs := range n.Lhs[min(i, len(n.Lhs)-1):] {
						if id, ok := lhs.(*ast.Ident); ok && !tainted[id.Name] {
							tainted[id.Name], changed = true, true
						}
					}
				}
			case *ast.ValueSpec:
				for i, value := range n.Values {
					if decisionPathExpr(value, tainted) && i < len(n.Names) && !tainted[n.Names[i].Name] {
						tainted[n.Names[i].Name], changed = true, true
					}
				}
			}
			return true
		})
	}
	var unsafe bool
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, pkgOK := sel.X.(*ast.Ident)
		if pkgOK && pkg.Name == "os" && (sel.Sel.Name == "OpenFile" || sel.Sel.Name == "WriteFile" || sel.Sel.Name == "Create") && decisionPathExpr(call.Args[0], tainted) {
			unsafe = true
		}
		return true
	})
	if unsafe {
		return fmt.Errorf("unlocked scheduler decision-log opener in %s", path)
	}
	return nil
}

func decisionPathExpr(expr ast.Expr, tainted map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, _ := strconv.Unquote(n.Value)
				found = found || strings.Contains(value, "decisions.jsonl")
			}
		case *ast.Ident:
			found = found || n.Name == "DecisionsName" || tainted[n.Name]
		case *ast.CallExpr:
			switch fn := n.Fun.(type) {
			case *ast.Ident:
				found = found || fn.Name == "DecisionsPath"
			case *ast.SelectorExpr:
				found = found || fn.Sel.Name == "DecisionsPath"
			}
		}
		return !found
	})
	return found
}
