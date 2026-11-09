package sealchain

import (
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

type guardedPathUse struct {
	reader bool
	note   string
}

func TestExternalSealedDocumentPathUsesAreDeclared(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	// These are the exported path identifiers at this revision. A new exported
	// seatcommit ledger/head path or schedproc exits-path builder belongs here;
	// scheduler.Config.ExitsPath is recognized by its field name below.
	exportedPathIdentifiers := []string{
		"seatcommit.LedgerDir",
		"scheduler.Config.ExitsPath",
	}
	want := map[string]guardedPathUse{
		"internal/mergetrain/gh.go:Checks":          {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/mergetrain/gh.go:FirstParent":     {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/releaseflow/gh.go:CommitExists":   {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/schedproc/process.go:Run":         {note: "constructs scheduler.Config.ExitsPath; the scheduler owns the reader"},
		"internal/status/collect.go:pushURLMissing": {reader: true, note: "reads commit ledgers for status"},
		"internal/uninstall/busy.go:commitSteps":    {reader: true, note: "reads the exits ledger for the busy check"},
		"internal/uninstall/plan.go:commits":        {reader: true, note: "reads commit ledgers for worktree candidates"},
		"internal/uninstall/plan.go:discover":       {note: "classifies the commits directory for removal"},
		"internal/uninstall/plan.go:scheduler":      {note: "classifies exits.json for removal"},
	}

	got := map[string]bool{}
	var problems []string
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
		uses, err := protectedPathFunctions(path, raw)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for function, split := range uses {
			key := filepath.ToSlash(rel) + ":" + function
			decl, ok := want[key]
			if !ok {
				problems = append(problems, "undeclared protected-path use "+key)
				continue
			}
			got[key] = true
			if decl.note == "" {
				problems = append(problems, "declared use has no explanation "+key)
			}
			if decl.reader && !split {
				problems = append(problems, "declared reader does not call sealchain.SplitRaw "+key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range want {
		if !got[key] {
			problems = append(problems, "stale declared protected-path use "+key)
		}
	}
	sort.Strings(problems)
	if len(problems) != 0 {
		t.Fatalf("sealed-document path guard (%s):\n%s", strings.Join(exportedPathIdentifiers, ", "), strings.Join(problems, "\n"))
	}
}

func TestExternalSealedDocumentPathGuardRejectsPlantedUses(t *testing.T) {
	plants := []struct {
		name   string
		source string
	}{
		{"os.ReadFile and json.Unmarshal", `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); _ = json.Unmarshal(raw, out) }
`},
		{"os.Open and json.NewDecoder", `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { f, _ := os.Open(filepath.Join(root, "scheduler/exits.json")); _ = json.NewDecoder(f).Decode(out) }
`},
		{"literal and decode split across helpers", `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); decode(raw, out) }
func decode(raw []byte, out any) { _ = json.Unmarshal(raw, out) }
`},
		{"exported ledger path and decode helper", `package planted
import ("encoding/json"; "os"; "github.com/omriariav/amq-squad/v3/internal/seatcommit")
func read(root string, out any) { raw, _ := os.ReadFile(seatcommit.LedgerDir(root)); decode(raw, out) }
func decode(raw []byte, out any) { _ = json.Unmarshal(raw, out) }
		`},
	}
	for _, plant := range plants {
		t.Run(plant.name, func(t *testing.T) {
			uses, err := protectedPathFunctions(plant.name+".go", []byte(plant.source))
			if err != nil {
				t.Fatal(err)
			}
			if split, ok := uses["read"]; !ok || split {
				t.Fatalf("guard accepted planted protected-path use: %v", uses)
			}
		})
	}
}

func protectedPathFunctions(path string, raw []byte) (map[string]bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		return nil, err
	}
	uses := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !functionReferencesProtectedPath(fn) {
			continue
		}
		uses[fn.Name.Name] = functionCalls(fn, "sealchain", "SplitRaw")
	}
	return uses, nil
}

func functionReferencesProtectedPath(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, _ := strconv.Unquote(n.Value)
				found = found || value == "commits" || value == "exits.json" || strings.Contains(value, "/commits/") || strings.Contains(value, "scheduler/exits.json")
			}
		case *ast.SelectorExpr:
			found = found || n.Sel.Name == "LedgerDir" || n.Sel.Name == "ExitsPath"
		case *ast.KeyValueExpr:
			key, _ := n.Key.(*ast.Ident)
			found = found || key != nil && key.Name == "ExitsPath"
		}
		return !found
	})
	return found
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
