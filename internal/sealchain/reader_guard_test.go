package sealchain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
	// seatcommit ledger/head/quarantine path or schedproc exits-path builder
	// belongs here; scheduler.Config.ExitsPath is recognized by its field name.
	exportedPathIdentifiers := []string{
		"seatcommit.LedgerDir",
		"seatcommit.QuarantineDir",
		"scheduler.Config.ExitsPath",
	}
	want := map[string]guardedPathUse{
		"internal/mergetrain/gh.go:(*GH).Checks":        {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/mergetrain/gh.go:(*GH).FirstParent":   {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/releaseflow/gh.go:(*GH).CommitExists": {note: "GitHub commits API URL, not a sealed-document path"},
		"internal/schedproc/process.go:Run":             {note: "constructs scheduler.Config.ExitsPath; the scheduler owns the reader"},
		"internal/status/collect.go:pushURLMissing":     {reader: true, note: "reads commit ledgers for status"},
		"internal/uninstall/busy.go:commitSteps":        {reader: true, note: "reads the exits ledger for the busy check"},
		"internal/uninstall/plan.go:(*plan).commits":    {reader: true, note: "reads commit ledgers for worktree candidates"},
		"internal/uninstall/plan.go:(*plan).scheduler":  {note: "classifies exits.json for removal"},
		"internal/uninstall/plan.go:discover":           {note: "consults the protected-name tables and classifies the commits directory for removal"},
		"internal/uninstall/plan.go:sealNote":           {note: "maps sealed directory names to removal-plan descriptions"},
		"internal/uninstall/plan.go:sealedNames":        {note: "lists sealed directory names for removal-plan classification"},
	}

	got, err := protectedPathTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if problems := guardedPathProblems(got, want); len(problems) != 0 {
		t.Fatalf("sealed-document path guard (%s):\n%s", strings.Join(exportedPathIdentifiers, ", "), strings.Join(problems, "\n"))
	}
}

func TestExternalSealedDocumentPathGuardRejectsPlantedUses(t *testing.T) {
	reader := func(note string) guardedPathUse { return guardedPathUse{reader: true, note: note} }
	plants := []struct {
		name        string
		source      string
		declared    map[string]guardedPathUse
		wantProblem string
	}{
		{
			name: "os.ReadFile and json.Unmarshal",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); _ = json.Unmarshal(raw, out) }
`,
			declared:    map[string]guardedPathUse{},
			wantProblem: "undeclared protected-path use plant.go:read",
		},
		{
			name: "os.Open and json.NewDecoder",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { f, _ := os.Open(filepath.Join(root, "scheduler/exits.json")); _ = json.NewDecoder(f).Decode(out) }
`,
			declared:    map[string]guardedPathUse{},
			wantProblem: "undeclared protected-path use plant.go:read",
		},
		{
			name: "literal and decode split across helpers",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); decode(raw, out) }
func decode(raw []byte, out any) { _ = json.Unmarshal(raw, out) }
`,
			declared:    map[string]guardedPathUse{},
			wantProblem: "undeclared protected-path use plant.go:read",
		},
		{
			name: "exported ledger path and decode helper",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath"; "github.com/omriariav/amq-squad/v3/internal/seatcommit")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(seatcommit.LedgerDir(root), "qa.json")); decode(raw, out) }
func decode(raw []byte, out any) { _ = json.Unmarshal(raw, out) }
`,
			declared:    map[string]guardedPathUse{},
			wantProblem: "undeclared protected-path use plant.go:read",
		},
		{
			name: "package const and reading function",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath")
const exitsName = "exits.json"
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, exitsName)); _ = json.Unmarshal(raw, out) }
`,
			declared: map[string]guardedPathUse{
				"plant.go:exitsName": {note: "names the exits ledger"},
				"plant.go:read":      reader("reads the exits ledger"),
			},
			wantProblem: "declared reader does not call sealchain.SplitRaw plant.go:read",
		},
		{
			name: "package var function literal",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath")
var read = func(root string, out any) { raw, _ := os.ReadFile(filepath.Join(root, "commits", "qa.json")); _ = json.Unmarshal(raw, out) }
`,
			declared: map[string]guardedPathUse{
				"plant.go:read": reader("reads a commit ledger"),
			},
			wantProblem: "declared reader does not call sealchain.SplitRaw plant.go:read",
		},
		{
			name: "same-named methods keep receiver",
			source: `package planted
import ("encoding/json"; "github.com/omriariav/amq-squad/v3/internal/sealchain")
type bad struct{}; type good struct{}
func (*bad) read(raw []byte, out any) { const name = "exits.json"; _ = name; _ = json.Unmarshal(raw, out) }
func (*good) read(raw []byte, out any) { const name = "exits.json"; _ = name; body, _, _ := sealchain.SplitRaw(raw); _ = json.Unmarshal(body, out) }
`,
			declared: map[string]guardedPathUse{
				"plant.go:(*bad).read":  reader("reads without the split"),
				"plant.go:(*good).read": reader("reads through the split"),
			},
			wantProblem: "declared reader does not call sealchain.SplitRaw plant.go:(*bad).read",
		},
		{
			name: "declared reader loses SplitRaw",
			source: `package planted
import "encoding/json"
func read(raw []byte, out any) { const name = "commits"; _ = name; _ = json.Unmarshal(raw, out) }
`,
			declared: map[string]guardedPathUse{
				"plant.go:read": reader("reads a commit ledger"),
			},
			wantProblem: "declared reader does not call sealchain.SplitRaw plant.go:read",
		},
		{
			name:   "stale declared entry",
			source: "package planted\nfunc nothing() {}\n",
			declared: map[string]guardedPathUse{
				"plant.go:read": reader("used to read a commit ledger"),
			},
			wantProblem: "stale declared protected-path use plant.go:read",
		},
		{
			name: "declared entry without explanation",
			source: `package planted
func classify() { const name = "commits"; _ = name }
`,
			declared: map[string]guardedPathUse{
				"plant.go:classify": {},
			},
			wantProblem: "declared protected-path use has no explanation plant.go:classify",
		},
		{
			name: "QuarantineDir slot reader",
			source: `package planted
import ("encoding/json"; "os"; "path/filepath"; "github.com/omriariav/amq-squad/v3/internal/seatcommit")
func read(root string, out any) { raw, _ := os.ReadFile(filepath.Join(seatcommit.QuarantineDir(root), "slot", "ledger.json")); _ = json.Unmarshal(raw, out) }
`,
			declared: map[string]guardedPathUse{
				"plant.go:read": reader("reads a quarantined ledger slot"),
			},
			wantProblem: "declared reader does not call sealchain.SplitRaw plant.go:read",
		},
	}

	for _, plant := range plants {
		t.Run(plant.name, func(t *testing.T) {
			uses, err := protectedPathUses("plant.go", []byte(plant.source))
			if err != nil {
				t.Fatal(err)
			}
			problems := guardedPathProblems(qualifyPathUses("plant.go", uses), plant.declared)
			if strings.Join(problems, "\n") != plant.wantProblem {
				t.Fatalf("guard problems = %v, want %q; uses=%v", problems, plant.wantProblem, uses)
			}
		})
	}
}

type parsedProtectedFile struct {
	path       string
	rel        string
	packageKey string
	file       *ast.File
}

func protectedPathTree(root string) (map[string]bool, error) {
	var files []parsedProtectedFile
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
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, parsedProtectedFile{
			path:       path,
			rel:        filepath.ToSlash(rel),
			packageKey: filepath.Dir(path) + "\x00" + file.Name.Name,
			file:       file,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	selectors := protectedSelectorNames()
	packageNames := map[string]map[string]bool{}
	for _, source := range files {
		names := packageNames[source.packageKey]
		if names == nil {
			names = map[string]bool{}
			packageNames[source.packageKey] = names
		}
		for name := range protectedDeclarationNames(source.file, selectors) {
			names[name] = true
			if ast.IsExported(name) {
				selectors[name] = true
			}
		}
	}

	uses := map[string]bool{}
	for _, source := range files {
		for name, split := range scanProtectedFile(source.file, packageNames[source.packageKey], selectors) {
			uses[source.rel+":"+name] = split
		}
	}
	return uses, nil
}

func protectedPathUses(path string, raw []byte) (map[string]bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		return nil, err
	}
	selectors := protectedSelectorNames()
	names := protectedDeclarationNames(file, selectors)
	for name := range names {
		if ast.IsExported(name) {
			selectors[name] = true
		}
	}
	return scanProtectedFile(file, names, selectors), nil
}

func protectedSelectorNames() map[string]bool {
	return map[string]bool{
		"ExitsPath":     true,
		"LedgerDir":     true,
		"QuarantineDir": true,
	}
}

func protectedDeclarationNames(file *ast.File, selectors map[string]bool) map[string]bool {
	names := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST && gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok || !expressionsReferenceProtectedPath(values.Values, nil, selectors) {
				continue
			}
			for _, name := range values.Names {
				names[name.Name] = true
			}
		}
	}
	return names
}

func scanProtectedFile(file *ast.File, packageNames, selectors map[string]bool) map[string]bool {
	uses := map[string]bool{}
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Body != nil && nodeReferencesProtectedPath(node.Body, packageNames, selectors) {
				uses[functionName(node)] = nodeCalls(node.Body, "sealchain", "SplitRaw")
			}
		case *ast.GenDecl:
			if node.Tok != token.CONST && node.Tok != token.VAR {
				continue
			}
			for _, spec := range node.Specs {
				values, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range values.Names {
					if !packageNames[name.Name] {
						continue
					}
					if i < len(values.Values) {
						if literal, ok := values.Values[i].(*ast.FuncLit); ok {
							uses[name.Name] = nodeCalls(literal.Body, "sealchain", "SplitRaw")
							continue
						}
					}
					uses[name.Name] = false
				}
			}
		}
	}
	return uses
}

func expressionsReferenceProtectedPath(expressions []ast.Expr, names, selectors map[string]bool) bool {
	for _, expression := range expressions {
		if nodeReferencesProtectedPath(expression, names, selectors) {
			return true
		}
	}
	return false
}

func nodeReferencesProtectedPath(root ast.Node, names, selectors map[string]bool) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, _ := strconv.Unquote(n.Value)
				found = found || protectedPathLiteral(value)
			}
		case *ast.Ident:
			found = found || names[n.Name]
		case *ast.SelectorExpr:
			found = found || selectors[n.Sel.Name]
		case *ast.KeyValueExpr:
			key, _ := n.Key.(*ast.Ident)
			found = found || key != nil && key.Name == "ExitsPath"
		}
		return true
	})
	return found
}

func protectedPathLiteral(value string) bool {
	return value == "commits" || value == "exits.json" || strings.Contains(value, "/commits/") || strings.Contains(value, "scheduler/exits.json")
}

func functionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	return receiverName(function.Recv.List[0].Type) + "." + function.Name.Name
}

func receiverName(receiver ast.Expr) string {
	switch node := receiver.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.StarExpr:
		return "(*" + receiverName(node.X) + ")"
	case *ast.IndexExpr:
		return receiverName(node.X)
	case *ast.IndexListExpr:
		return receiverName(node.X)
	case *ast.SelectorExpr:
		return receiverName(node.X) + "." + node.Sel.Name
	default:
		return "<receiver>"
	}
}

func nodeCalls(root ast.Node, packageName, function string) bool {
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return !found
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != function {
			return !found
		}
		pkg, ok := selector.X.(*ast.Ident)
		found = ok && pkg.Name == packageName
		return !found
	})
	return found
}

func qualifyPathUses(path string, uses map[string]bool) map[string]bool {
	qualified := make(map[string]bool, len(uses))
	for name, split := range uses {
		qualified[path+":"+name] = split
	}
	return qualified
}

func guardedPathProblems(got map[string]bool, declared map[string]guardedPathUse) []string {
	var problems []string
	for key, entry := range declared {
		if entry.note == "" {
			problems = append(problems, "declared protected-path use has no explanation "+key)
		}
	}
	for key, split := range got {
		entry, ok := declared[key]
		if !ok {
			problems = append(problems, "undeclared protected-path use "+key)
			continue
		}
		if entry.reader && !split {
			problems = append(problems, "declared reader does not call sealchain.SplitRaw "+key)
		}
	}
	for key := range declared {
		if _, ok := got[key]; !ok {
			problems = append(problems, "stale declared protected-path use "+key)
		}
	}
	sort.Strings(problems)
	return problems
}
