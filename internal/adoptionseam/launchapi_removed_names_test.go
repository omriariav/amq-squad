package adoptionseam

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

const launchapiImportPath = "github.com/avivsinai/agent-message-queue/launchapi"

// TestNoRemovedLaunchapiNames guards the thirteen launchapi names AMQ v0.139.0
// removed: six functions and seven types. Remove this test after the module pin
// is past v0.138.0, when the compiler enforces the same boundary.
func TestNoRemovedLaunchapiNames(t *testing.T) {
	removed := map[string]bool{
		"Inspect":                true,
		"Focus":                  true,
		"Close":                  true,
		"InspectRequestV1":       true,
		"InspectResultV1":        true,
		"FocusRequestV1":         true,
		"FocusResultV1":          true,
		"CloseRequestV1":         true,
		"CloseResultV1":          true,
		"LifecycleResultV1":      true,
		"DecodeInspectRequestV1": true,
		"DecodeFocusRequestV1":   true,
		"DecodeCloseRequestV1":   true,
	}

	root := moduleRoot(t)
	importers := 0
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".worktrees":
				if path != root {
					return filepath.SkipDir
				}
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		aliases := map[string]bool{}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || importPath != launchapiImportPath {
				continue
			}
			importers++
			name := "launchapi"
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name != "." && name != "_" {
				aliases[name] = true
			}
		}
		if len(aliases) == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok || !removed[sel.Sel.Name] {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || !aliases[ident.Name] {
				return true
			}
			pos := fset.Position(sel.Sel.Pos())
			t.Errorf("%s:%d: removed launchapi selector %s", rel, pos.Line, sel.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if importers == 0 {
		t.Fatal("walk visited no Go file that imports launchapi")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) did not resolve this test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root")
		}
		dir = parent
	}
}
