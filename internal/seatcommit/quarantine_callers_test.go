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

// On the 0e layout every production ledger write, including recovery, goes
// through saveRawLedger. The legacy embedded-seal and legacy-head writers are
// fixtures only and must not reappear in non-test seatcommit code.
func TestLedgerProductionWritersUseOnlyTheRawPath(t *testing.T) {
	calls, _ := packageCallGraph(t, ".")
	if !calls["writeLedger"]["saveRawLedger"] || !calls["saveRawLedger"]["saveRawLedgerBeforeHead"] || !calls["recoverWrite"]["saveRawLedgerBeforeHead"] {
		t.Fatalf("raw writer callers: writeLedger=%v saveRawLedger=%v recoverWrite=%v", calls["writeLedger"], calls["saveRawLedger"], calls["recoverWrite"])
	}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, problem := range legacyLedgerWriterProblems(path, source) {
			if problem != "ledger.go:saveRawLedgerBeforeHead: atomicfile.Write(ledgerPath)" {
				t.Error(problem)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller(t *testing.T) {
	want := map[string]bool{
		"seatcommit/ledger.go:writeLedger:saveRawLedger":                            true,
		"seatcommit/ledger.go:saveRawLedger:saveRawLedgerBeforeHead":                true,
		"seatcommit/recover.go:recoverWrite:saveRawLedgerBeforeHead":                true,
		"seatcommit/ledger.go:saveRawLedgerBeforeHead:sealchain.WriteRawAfter":      true,
		"seatcommit/ledger.go:saveRawLedgerBeforeHead:sealchain.WriteRawBeforeHead": true,
		"scheduler/exits.go:writeExits:sealchain.WriteRawBeforeHead":                true,
	}
	got := map[string]bool{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		for _, call := range rawWriterCalls(t, name, source) {
			got[call] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for call := range want {
		if !got[call] {
			t.Errorf("missing raw writer caller %s; got %v", call, got)
		}
	}
	for call := range got {
		if !want[call] {
			t.Errorf("unreviewed raw writer caller %s", call)
		}
	}
	plant := []byte(`package schedproc

	import "github.com/omriariav/amq-squad/v3/internal/sealchain"

	func planted() { _, _ = sealchain.WriteRawAfter("", sealchain.Seed{}, nil, nil) }`)
	if calls := rawWriterCalls(t, "schedproc/plant.go", plant); len(calls) != 1 || calls[0] != "schedproc/plant.go:planted:sealchain.WriteRawAfter" {
		t.Fatalf("planted schedproc caller passed: %v", calls)
	}
}

func rawWriterCalls(t *testing.T, name string, source []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[string]bool{"sealchain": true}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path != "github.com/omriariav/amq-squad/v3/internal/sealchain" {
			continue
		}
		alias := "sealchain"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = true
	}
	var out []string
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "saveRawLedger" || fun.Name == "saveRawLedgerBeforeHead" {
					callee = fun.Name
				}
			case *ast.SelectorExpr:
				if id, ok := fun.X.(*ast.Ident); ok && aliases[id.Name] && (fun.Sel.Name == "WriteRaw" || fun.Sel.Name == "WriteRawAfter" || fun.Sel.Name == "WriteRawBeforeHead") {
					callee = "sealchain." + fun.Sel.Name
				}
			}
			if callee != "" {
				out = append(out, filepath.ToSlash(name)+":"+fd.Name.Name+":"+callee)
			}
			return true
		})
	}
	return out
}

func TestLedgerWriterGuardRejectsPlantedLegacyCalls(t *testing.T) {
	plants := []string{
		`package seatcommit; import "github.com/omriariav/amq-squad/v3/internal/sealchain"; func bad(l Ledger) { _ = sealchain.Seal(nil, "x", nil, l) }`,
		`package seatcommit; import "github.com/omriariav/amq-squad/v3/internal/atomicfile"; func bad(root string) { _ = atomicfile.Write(ledgerPath(root, "s"), nil, 0600) }`,
		`package seatcommit; import sc "github.com/omriariav/amq-squad/v3/internal/sealchain"; func bad(root string, s sc.Seed) { _ = sc.WriteHead(root, ledgerHeadName("s"), s, nil, time.Time{}) }`,
		`package seatcommit; import af "github.com/omriariav/amq-squad/v3/internal/atomicfile"; func bad(root string) { _ = af.Write(ledgerPath(root, "s"), nil, 0600) }`,
	}
	for i, source := range plants {
		if got := legacyLedgerWriterProblems("plant.go", []byte(source)); len(got) == 0 {
			t.Errorf("plant %d passed the writer guard", i+1)
		}
	}
}

func TestRecoveredMarkReaderAndWriterGuards(t *testing.T) {
	calls, _ := packageCallGraph(t, ".")
	if !calls["markRecoveredSlot"]["recoveredMarkCreate"] {
		t.Fatal("markRecoveredSlot is no longer the recovered mark's writer")
	}
	for fn, callees := range calls {
		if fn != "markRecoveredSlot" && callees["recoveredMarkCreate"] {
			t.Errorf("unreviewed recovered mark writer %s", fn)
		}
	}
	for _, name := range goFiles(t, ".") {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range recoveredMarkReaderProblems(name, source) {
			t.Error(problem)
		}
		if name != "quarantine.go" && strings.Contains(string(source), `"recovered.txt"`) {
			t.Errorf("%s learns the recovered mark's literal name", name)
		}
	}
	plant := []byte(`package seatcommit; func bad(d slotDir) { _, _, _ = d.read(quarantineRecovered) }`)
	if got := recoveredMarkReaderProblems("plant.go", plant); len(got) != 1 {
		t.Fatalf("planted recovered mark reader passed: %v", got)
	}
}

func recoveredMarkReaderProblems(name string, source []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		return []string{name + ": parse: " + err.Error()}
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		mark, okMark := call.Args[0].(*ast.Ident)
		if ok && okMark && sel.Sel.Name == "read" && mark.Name == "quarantineRecovered" {
			out = append(out, filepath.Base(name)+":"+strconv.Itoa(fset.Position(call.Pos()).Line)+": opens the recovered mark")
		}
		return true
	})
	return out
}

func legacyLedgerWriterProblems(name string, source []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		return []string{name + ": parse: " + err.Error()}
	}
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		alias := filepath.Base(path)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = path
	}
	containsLedgerPath := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && (id.Name == "ledgerPath" || id.Name == "ledgerHeadName") {
					found = true
				}
			}
			return !found
		})
		return found
	}
	var out []string
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path := aliases[x.Name]
			kind := ""
			switch {
			case path == "github.com/omriariav/amq-squad/v3/internal/sealchain" && (sel.Sel.Name == "Seal" || sel.Sel.Name == "WriteHead"):
				kind = "sealchain." + sel.Sel.Name
			case path == "github.com/omriariav/amq-squad/v3/internal/atomicfile" && sel.Sel.Name == "Write" && len(call.Args) > 0 && containsLedgerPath(call.Args[0]):
				kind = "atomicfile.Write(ledgerPath)"
			case path == "os" && (sel.Sel.Name == "WriteFile" || sel.Sel.Name == "Create" || sel.Sel.Name == "OpenFile" || sel.Sel.Name == "Rename"):
				for _, arg := range call.Args {
					if containsLedgerPath(arg) {
						kind = "os." + sel.Sel.Name + "(ledgerPath)"
					}
				}
			}
			if kind != "" {
				out = append(out, filepath.Base(name)+":"+fd.Name.Name+": "+kind)
			}
			return true
		})
	}
	return out
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
