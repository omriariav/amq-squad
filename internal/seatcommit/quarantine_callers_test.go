package seatcommit

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

// The writers are the A1 row's fixture set, not the guard; this test holds
// the set to the source so a new writer cannot miss the row. From this
// package's files it enumerates every function that calls writeLedger and
// every exported function that reaches one; from the rest of the module
// (files outside the task's declared set, read and not changed) every
// reference to such an entry. Each must be in quarantineWriters, or be a
// name this test explains.
func TestEveryLedgerWriterHasAQuarantineRow(t *testing.T) {
	calls, exported := packageCallGraph(t, ".")
	reaches := func(name string) bool {
		seen := map[string]bool{}
		var walk func(string) bool
		walk = func(n string) bool {
			if n == "writeLedger" {
				return true
			}
			if seen[n] {
				return false
			}
			seen[n] = true
			for c := range calls[n] {
				if walk(c) {
					return true
				}
			}
			return false
		}
		return walk(name)
	}
	var direct []string
	for name, callees := range calls {
		if callees["writeLedger"] && name != "writeLedger" {
			direct = append(direct, name)
		}
	}
	sort.Strings(direct)
	// The direct callers and the A1 row that drives each.
	rowOf := map[string]string{
		"recordBase":     "RecordBase, RecordWorktreeBase",
		"RecordTarget":   "RecordTarget",
		"ArchiveAttempt": "ArchiveAttempt",
		"cleanSeat":      "CleanSeat",
		"StartTask":      "StartTask",
		"settle":         "Run",
		"commitAndPush":  "Run",
	}
	for _, name := range direct {
		if rowOf[name] == "" {
			t.Errorf("%s calls writeLedger and has no A1 row (quarantineWriters): add one", name)
		}
	}
	for name := range rowOf {
		if !calls[name]["writeLedger"] {
			t.Errorf("rowOf names %s, which no longer calls writeLedger directly", name)
		}
	}
	// The exported entries that reach a write, and the row of each.
	// CheckWiden reaches Run as a dry run (Options.DryRun: the step reads,
	// never writes); its refusal is the Run row's.
	entryRow := map[string]string{"CheckWiden": "Run"}
	for _, w := range quarantineWriters {
		entryRow[w.name] = w.name
	}
	var entries []string
	for _, name := range exported {
		if reaches(name) {
			entries = append(entries, name)
			if entryRow[name] == "" {
				t.Errorf("exported %s reaches writeLedger and has no A1 row (quarantineWriters): add one", name)
			}
		}
	}
	sort.Strings(entries)
	for name := range entryRow {
		if !reaches(name) {
			t.Errorf("%s has a row but no longer reaches writeLedger", name)
		}
	}
	t.Logf("direct callers of writeLedger: %s", strings.Join(direct, ", "))
	t.Logf("exported entries reaching writeLedger: %s", strings.Join(entries, ", "))
	// The rest of the module: every reference to an entry, by file and line.
	refs := moduleReferences(t, "../..", "internal/seatcommit", entryRow)
	if len(refs) == 0 {
		t.Fatal("no file outside this package references a ledger writer: the scan is broken")
	}
	for _, r := range refs {
		t.Logf("%s", r)
	}
	for _, entry := range []string{"RecordBase", "RecordTarget", "RecordWorktreeBase", "ArchiveAttempt", "CleanSeat"} {
		found := false
		for _, r := range refs {
			found = found || strings.HasSuffix(r, " seatcommit."+entry)
		}
		if !found {
			t.Errorf("the brief's writer %s is referenced nowhere outside this package", entry)
		}
	}
}

// packageCallGraph parses the package's non-test files: for each function
// (a method as Type.Name) the set of plain in-package calls in its body
// (function literals included), and the exported functions.
func packageCallGraph(t *testing.T, dir string) (calls map[string]map[string]bool, exported []string) {
	t.Helper()
	calls = map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range goFiles(t, dir) {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := fd.Name.Name
			if fd.Recv != nil {
				key = recvName(fd.Recv) + "." + key
			} else if fd.Name.IsExported() {
				exported = append(exported, key)
			}
			set := map[string]bool{}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok {
						set[id.Name] = true
					}
				}
				return true
			})
			calls[key] = set
		}
	}
	return calls, exported
}

func recvName(r *ast.FieldList) string {
	if len(r.List) == 0 {
		return "?"
	}
	switch x := r.List[0].Type.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return "?"
}

func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			names = append(names, n)
		}
	}
	return names
}

// moduleReferences lists, as "path:line seatcommit.Name", every selector on
// this package's import name in the module's non-test Go files outside
// skip that names an entry (a call or a value, as task widen's
// archiveWidenAttempt = seatcommit.ArchiveAttempt).
func moduleReferences(t *testing.T, root, skip string, entries map[string]string) []string {
	t.Helper()
	const importPath = "github.com/omriariav/amq-squad/v3/internal/seatcommit"
	var out []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel == skip || d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		alias := ""
		for _, imp := range file.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == importPath {
				alias = "seatcommit"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
			}
		}
		if alias == "" {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == alias && entries[sel.Sel.Name] != "" {
				out = append(out, filepath.ToSlash(rel)+":"+strconv.Itoa(fset.Position(sel.Pos()).Line)+" seatcommit."+sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
