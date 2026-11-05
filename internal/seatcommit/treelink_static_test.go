package seatcommit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func parseStaticFile(t *testing.T, name string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTreeLinkResolverAndCallSitesStayObjectOnly(t *testing.T) {
	forbidden := map[string]bool{
		"os": true, "io/fs": true, "io/ioutil": true, "path/filepath": true, "syscall": true,
	}
	resolver := parseStaticFile(t, "treelink.go")
	for _, imp := range resolver.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if forbidden[path] {
			t.Fatalf("treelink.go imports filesystem package %q", path)
		}
	}

	for _, name := range []string{"commit.go", "verifier.go"} {
		f := parseStaticFile(t, name)
		filesystemNames := map[string]bool{}
		for _, imp := range f.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !forbidden[importPath] {
				continue
			}
			local := importPath
			if slash := len(local) - 1; slash >= 0 {
				for slash >= 0 && local[slash] != '/' {
					slash--
				}
				local = local[slash+1:]
			}
			if imp.Name != nil {
				local = imp.Name.Name
			}
			if local == "." {
				t.Fatalf("%s dot-imports filesystem package %q", name, importPath)
			}
			filesystemNames[local] = true
		}
		calls := 0
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "treeLinkRefusals" {
				return true
			}
			calls++
			if len(call.Args) == 0 {
				t.Fatalf("%s treeLinkRefusals call has no object source", name)
			}
			source, ok := call.Args[0].(*ast.Ident)
			if !ok || source.Name != "source" {
				t.Fatalf("%s treeLinkRefusals first argument is %T, want source", name, call.Args[0])
			}
			var statement ast.Stmt
			for i := len(stack) - 2; i >= 0; i-- {
				if stmt, ok := stack[i].(ast.Stmt); ok {
					if _, block := stmt.(*ast.BlockStmt); !block {
						statement = stmt
						break
					}
				}
			}
			if statement == nil {
				t.Fatalf("%s treeLinkRefusals call has no containing statement", name)
			}
			ast.Inspect(statement, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && filesystemNames[pkg.Name] {
					t.Fatalf("%s treeLinkRefusals statement also calls filesystem package %s", name, pkg.Name)
				}
				return true
			})
			return true
		})
		if calls != 1 {
			t.Fatalf("%s has %d treeLinkRefusals calls, want 1", name, calls)
		}
	}
}
