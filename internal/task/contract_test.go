package task

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type stateWrite struct {
	key  string
	file string
	line int
}

type parsedSource struct {
	name string
	file *ast.File
}

func parseNonTestPackage(t *testing.T) (*token.FileSet, []parsedSource) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []parsedSource
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, parsedSource{name: entry.Name(), file: file})
	}
	return fset, files
}

func TestEveryStateWriteIsDeclared(t *testing.T) {
	fset, files := parseNonTestPackage(t)
	var writes []stateWrite
	for _, source := range files {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.UnaryExpr:
					selector, ok := node.X.(*ast.SelectorExpr)
					if node.Op == token.AND && ok && selector.Sel.Name == "State" {
						pos := fset.Position(node.Pos())
						t.Errorf("%s:%d: the address of a state field is taken; the write that follows is not declared", pos.Filename, pos.Line)
					}
				case *ast.AssignStmt:
					for i, lhs := range node.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok || sel.Sel.Name != "State" {
							continue
						}
						if i >= len(node.Rhs) {
							pos := fset.Position(lhs.Pos())
							t.Errorf("%s:%d: a state written from an expression is not declared", pos.Filename, pos.Line)
							continue
						}
						writes = appendStateWrite(t, fset, writes, fn.Name.Name, node.Rhs[i])
					}
				case *ast.KeyValueExpr:
					key, ok := node.Key.(*ast.Ident)
					if ok && key.Name == "State" {
						writes = appendStateWrite(t, fset, writes, fn.Name.Name, node.Value)
					}
				}
				return true
			})
		}
	}
	if len(writes) == 0 {
		t.Fatal("state-write scan found no sites")
	}
	found := map[string][]stateWrite{}
	for _, write := range writes {
		found[write.key] = append(found[write.key], write)
	}
	declared := map[string]bool{}
	for _, move := range Moves() {
		declared[move.Writer+"->"+move.To] = true
	}
	for key, sites := range found {
		if !declared[key] {
			t.Errorf("%s:%d: state-write key %s is not declared", sites[0].file, sites[0].line, key)
		}
		if len(sites) != 1 {
			t.Errorf("state-write key %s has %d sites: %+v", key, len(sites), sites)
		}
	}
	for key := range declared {
		if len(found[key]) == 0 {
			t.Errorf("declared state-write key %s has no site", key)
		}
	}
}

type parsedFunction struct {
	key       string
	name      string
	receiver  string
	recvIdent string
	pointer   bool
	body      *ast.BlockStmt
	edges     map[string]bool
}

func TestEveryStoreMethodIsTiedToItsMoves(t *testing.T) {
	_, files := parseNonTestPackage(t)
	nodes := map[string]*parsedFunction{}
	packageFuncs := map[string]string{}
	storeMethods := map[string]string{}
	for _, source := range files {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			receiver, recvIdent, pointer := receiver(fn)
			key := "func:" + fn.Name.Name
			if receiver != "" {
				key = "method:" + receiver + ":" + fn.Name.Name
			}
			nodes[key] = &parsedFunction{key: key, name: fn.Name.Name, receiver: receiver, recvIdent: recvIdent, pointer: pointer, body: fn.Body, edges: map[string]bool{}}
			if receiver == "" {
				packageFuncs[fn.Name.Name] = key
			} else if receiver == "Store" {
				storeMethods[fn.Name.Name] = key
			}
		}
	}
	for _, node := range nodes {
		ast.Inspect(node.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if key := packageFuncs[fun.Name]; key != "" {
					node.edges[key] = true
				}
			case *ast.SelectorExpr:
				recv, ok := fun.X.(*ast.Ident)
				if node.receiver == "Store" && ok && recv.Name == node.recvIdent {
					if key := storeMethods[fun.Sel.Name]; key != "" {
						node.edges[key] = true
					}
				}
			}
			return true
		})
	}
	writerNames := map[string]bool{}
	for _, move := range Moves() {
		writerNames[move.Writer] = true
	}
	writers := map[string]string{}
	for name := range writerNames {
		var matches []string
		if key := packageFuncs[name]; key != "" {
			matches = append(matches, key)
		}
		if key := storeMethods[name]; key != "" {
			matches = append(matches, key)
		}
		if len(matches) != 1 {
			t.Errorf("writer %s names %d parsed functions", name, len(matches))
			continue
		}
		writers[matches[0]] = name
	}
	reflected := map[string]bool{}
	typ := reflect.TypeOf(Store{})
	for i := 0; i < typ.NumMethod(); i++ {
		reflected[typ.Method(i).Name] = true
	}
	for name, key := range storeMethods {
		node := nodes[key]
		if node.pointer && ast.IsExported(name) {
			t.Errorf("exported Store method %s has a pointer receiver", name)
		}
		if ast.IsExported(name) && !reflected[name] {
			t.Errorf("exported parsed Store method %s is absent from Store's value method set", name)
		}
	}
	reaching := 0
	methodNames := sortedKeys(reflected)
	for _, name := range methodNames {
		key := storeMethods[name]
		if key == "" {
			t.Errorf("reflected Store method %s was not parsed", name)
			continue
		}
		got := reachableWriters(key, nodes, writers)
		wantSet := map[string]bool{}
		for _, move := range Moves() {
			if contains(move.Via, name) {
				wantSet[move.Writer] = true
			}
		}
		want := sortedKeys(wantSet)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Store.%s reaches writers %v; Moves declares %v", name, got, want)
		}
		if len(got) > 0 {
			reaching++
			t.Logf("%s -> %s", name, strings.Join(got, ", "))
		}
	}
	for _, node := range nodes {
		if !ast.IsExported(node.name) || node.receiver == "Store" {
			continue
		}
		if got := reachableWriters(node.key, nodes, writers); len(got) > 0 {
			kind := "package function"
			if node.receiver != "" {
				kind = "method of " + node.receiver
			}
			t.Errorf("exported %s %s reaches writers %v", kind, node.name, got)
		}
	}
	if reaching == 0 {
		t.Fatal("no Store method reaches a writer")
	}
}

func receiver(fn *ast.FuncDecl) (typ, ident string, pointer bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", "", false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		pointer, expr = true, star.X
	}
	if name, ok := expr.(*ast.Ident); ok {
		typ = name.Name
	}
	if len(fn.Recv.List[0].Names) > 0 {
		ident = fn.Recv.List[0].Names[0].Name
	}
	return typ, ident, pointer
}

func reachableWriters(start string, nodes map[string]*parsedFunction, writers map[string]string) []string {
	seen, found := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(key string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		if writer := writers[key]; writer != "" {
			found[writer] = true
		}
		if node := nodes[key]; node != nil {
			for edge := range node.edges {
				visit(edge)
			}
		}
	}
	visit(start)
	return sortedKeys(found)
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func appendStateWrite(t *testing.T, fset *token.FileSet, writes []stateWrite, fn string, expr ast.Expr) []stateWrite {
	t.Helper()
	ident, ok := expr.(*ast.Ident)
	pos := fset.Position(expr.Pos())
	if !ok || !map[string]bool{"StatePending": true, "StateInProgress": true, "StateBlocked": true, "StateCompleted": true, "StateCancelled": true}[ident.Name] {
		t.Errorf("%s:%d: a state written from an expression is not declared", pos.Filename, pos.Line)
		return writes
	}
	value := map[string]string{"StatePending": StatePending, "StateInProgress": StateInProgress, "StateBlocked": StateBlocked, "StateCompleted": StateCompleted, "StateCancelled": StateCancelled}[ident.Name]
	return append(writes, stateWrite{key: fn + "->" + value, file: pos.Filename, line: pos.Line})
}

func TestContractListsMatchTheConstants(t *testing.T) {
	checks := []struct {
		prefix string
		got    []string
	}{{"State", stateNames()}, {"Outcome", Outcomes()}, {"Route", Routes()}}
	for _, check := range checks {
		names, values := declaredConstants(t, check.prefix)
		if !reflect.DeepEqual(check.got, values) {
			t.Errorf("%s constants %v have values %v; contract has %v", check.prefix, names, values, check.got)
		}
	}
}

func declaredConstants(t *testing.T, prefix string) ([]string, []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "class.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names, values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec := spec.(*ast.ValueSpec)
			for i, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, prefix) {
					continue
				}
				if i >= len(valueSpec.Values) {
					t.Fatalf("%s has no explicit value", name.Name)
				}
				lit, ok := valueSpec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s is not an explicit string constant", name.Name)
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				names, values = append(names, name.Name), append(values, value)
			}
		}
	}
	return names, values
}

func stateNames() []string {
	states := States()
	out := make([]string, len(states))
	for i := range states {
		out[i] = states[i].Name
	}
	return out
}

func TestMovesAreWellFormed(t *testing.T) {
	states := map[string]StateContract{}
	for _, state := range States() {
		states[state.Name] = state
	}
	who := map[string]bool{WhoLiveHolder: true, WhoCreator: true, WhoProvenLead: true, WhoScheduler: true, WhoHarnessShell: true, WhoAssertedHandle: true}
	outgoing, reachable := map[string]bool{}, map[string]bool{}
	for _, move := range Moves() {
		if move.From != "" {
			state, ok := states[move.From]
			if !ok {
				t.Errorf("move %+v has undeclared from state", move)
			} else if state.Terminal {
				t.Errorf("move %+v leaves terminal state", move)
			}
			outgoing[move.From] = true
		}
		if _, ok := states[move.To]; !ok {
			t.Errorf("move %+v has undeclared to state", move)
		}
		for _, method := range move.Via {
			if _, ok := reflect.TypeOf(Store{}).MethodByName(method); !ok {
				t.Errorf("move %+v names no Store.%s method", move, method)
			}
		}
		if len(move.Who) == 0 {
			t.Errorf("move %+v names no actor check", move)
		}
		for _, actor := range move.Who {
			if !who[actor] {
				t.Errorf("move %+v has unknown actor check %q", move, actor)
			}
		}
	}
	for name, state := range states {
		if !state.Terminal && !outgoing[name] {
			t.Errorf("non-terminal state %s has no outgoing move", name)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, move := range Moves() {
			if (move.From == "" || reachable[move.From]) && !reachable[move.To] {
				reachable[move.To], changed = true, true
			}
		}
	}
	for name := range states {
		if !reachable[name] {
			t.Errorf("state %s is unreachable", name)
		}
	}
}

func TestLeavingBlockedClearsTheBlockGroup(t *testing.T) {
	type disagreement struct{ From, Via, To, Detail, File, Func string }
	// Adding an entry is a plan ruling, never a seat's own edit. An S4 task starts by deleting its entry.
	knownDisagreements := []disagreement{{StateBlocked, "Cancel", StateCancelled, "survey candidate 2: Cancel leaves the five block fields set", "internal/task/lease.go:968", "Cancel"}}
	matched := make([]bool, len(knownDisagreements))
	for _, move := range Moves() {
		if move.From != StateBlocked || move.To == StateBlocked || move.ClearsBlockGroup {
			continue
		}
		known := false
		for i, item := range knownDisagreements {
			if item.From == move.From && item.To == move.To && contains(move.Via, item.Via) {
				known, matched[i] = true, true
				break
			}
		}
		if !known {
			t.Errorf("blocked move via %s to %s does not clear the block group", strings.Join(move.Via, ","), move.To)
		}
	}
	for i, item := range knownDisagreements {
		if !matched[i] {
			t.Errorf("stale known disagreement: %s %s (%s, %s)", item.Func, item.Detail, item.File, item.Via)
		}
	}
}

func TestOutcomeSetsAreWellFormed(t *testing.T) {
	sets := OutcomeSets()
	if len(sets) != 6 {
		t.Fatalf("got %d outcome sets, want 6", len(sets))
	}
	declared, names := map[string]bool{}, map[string]bool{}
	for _, outcome := range Outcomes() {
		declared[outcome] = true
	}
	for _, set := range sets {
		if names[set.Name] {
			t.Errorf("duplicate outcome-set name %q", set.Name)
		}
		names[set.Name] = true
		if len(set.Outcomes) == 0 {
			t.Errorf("outcome set %q is empty", set.Name)
		}
		for _, outcome := range set.Outcomes {
			if !declared[outcome] {
				t.Errorf("outcome set %q contains undeclared outcome %q", set.Name, outcome)
			}
		}
		conditionalOutcomes := map[string]bool{}
		for _, conditional := range set.Conditional {
			if !declared[conditional.Outcome] || !contains(set.Outcomes, conditional.Outcome) || strings.TrimSpace(conditional.Condition) == "" {
				t.Errorf("outcome set %q has malformed conditional %+v", set.Name, conditional)
			}
			if conditionalOutcomes[conditional.Outcome] {
				t.Errorf("outcome set %q has duplicate conditional for %q", set.Name, conditional.Outcome)
			}
			conditionalOutcomes[conditional.Outcome] = true
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
