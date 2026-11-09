package sealchain

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestExternalSealedDocumentReadersUseSplitRaw(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	want := []string{
		"internal/status/collect.go:pushURLMissing",
		"internal/uninstall/busy.go:commitSteps",
	}
	var got []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(filepath.Join(root, "internal"), path)
			if err != nil {
				return err
			}
			if rel == "sealchain" || rel == "seatcommit" || rel == "scheduler" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		readers, unsafe, err := sealedDocumentReaders(path, raw)
		if err != nil {
			return err
		}
		for _, reader := range readers {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			got = append(got, filepath.ToSlash(rel)+":"+reader)
		}
		if len(unsafe) != 0 {
			return fmt.Errorf("direct sealed-document decode in %s: %s", path, strings.Join(unsafe, ", "))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sealed document readers = %v, want %v", got, want)
	}

	for name, planted := range map[string]string{
		"commits": `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); _ = json.Unmarshal(raw, out) }
`,
		"exits": `package planted
import ("encoding/json"; "github.com/omriariav/amq-squad/v3/internal/fsanchor")
func read(sd *fsanchor.AnchoredDir, out any) { raw, _ := fsanchor.ReadEntry(sd, "exits.json"); _ = json.Unmarshal(raw, out) }
`,
	} {
		t.Run("planted "+name+" decoder", func(t *testing.T) {
			readers, unsafe, err := sealedDocumentReaders(name+".go", []byte(planted))
			if err != nil {
				t.Fatal(err)
			}
			if len(readers) != 1 || len(unsafe) != 1 || unsafe[0] != "read" {
				t.Fatalf("guard accepted planted direct decoder: readers=%v unsafe=%v", readers, unsafe)
			}
		})
	}
}

func sealedDocumentReaders(path string, raw []byte) (readers, unsafe []string, err error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		return nil, nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !functionReadsProtectedDocument(fn) {
			continue
		}
		readers = append(readers, fn.Name.Name)
		if !functionCalls(fn, "sealchain", "SplitRaw") {
			unsafe = append(unsafe, fn.Name.Name)
		}
	}
	return readers, unsafe, nil
}

func functionReadsProtectedDocument(fn *ast.FuncDecl) bool {
	protected, reads, decodes := false, false, false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, _ := strconv.Unquote(n.Value)
				protected = protected || value == "commits" || value == "exits.json" || strings.Contains(value, "/commits/") || strings.Contains(value, "scheduler/exits.json")
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				pkg, _ := sel.X.(*ast.Ident)
				if pkg != nil && ((pkg.Name == "os" && sel.Sel.Name == "ReadFile") || (pkg.Name == "fsanchor" && sel.Sel.Name == "ReadEntry")) {
					reads = true
				}
				if pkg != nil && pkg.Name == "json" && (sel.Sel.Name == "Unmarshal" || sel.Sel.Name == "NewDecoder") {
					decodes = true
				}
			}
		}
		return true
	})
	return protected && reads && decodes
}

func functionCalls(fn *ast.FuncDecl, pkgName, function string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != function {
			return !found
		}
		pkg, ok := sel.X.(*ast.Ident)
		found = ok && pkg.Name == pkgName
		return !found
	})
	return found
}
