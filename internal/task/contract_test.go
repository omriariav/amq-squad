package task

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
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

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
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
			inspectStateWrites(t, fset, fn.Body, fn.Name.Name, false, &writes)
		}
		ast.Inspect(source.file, func(node ast.Node) bool {
			if fn, ok := node.(*ast.FuncDecl); ok {
				if fn.Recv != nil {
					inspectStateWrites(t, fset, fn.Recv, "", true, &writes)
				}
				inspectStateWrites(t, fset, fn.Type, "", true, &writes)
				return false
			}
			inspectStateWrite(t, fset, node, "", true, &writes)
			return true
		})
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

func inspectStateWrites(t *testing.T, fset *token.FileSet, root ast.Node, writer string, outside bool, writes *[]stateWrite) {
	t.Helper()
	ast.Inspect(root, func(node ast.Node) bool {
		inspectStateWrite(t, fset, node, writer, outside, writes)
		return true
	})
}

func inspectStateWrite(t *testing.T, fset *token.FileSet, node ast.Node, writer string, outside bool, writes *[]stateWrite) {
	t.Helper()
	report := func(expr ast.Expr) {
		pos := fset.Position(expr.Pos())
		if outside {
			t.Errorf("%s:%d: a state written outside a declared function is not declared", pos.Filename, pos.Line)
			return
		}
		t.Errorf("%s:%d: a state written from an expression is not declared", pos.Filename, pos.Line)
	}
	switch node := node.(type) {
	case *ast.UnaryExpr:
		selector, ok := unparen(node.X).(*ast.SelectorExpr)
		if node.Op == token.AND && ok && selector.Sel.Name == "State" {
			if outside {
				report(node.X)
				return
			}
			pos := fset.Position(node.Pos())
			t.Errorf("%s:%d: the address of a state field is taken; the write that follows is not declared", pos.Filename, pos.Line)
		}
	case *ast.AssignStmt:
		for i, lhs := range node.Lhs {
			sel, ok := unparen(lhs).(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "State" {
				continue
			}
			if outside || (node.Tok != token.ASSIGN && node.Tok != token.DEFINE) || i >= len(node.Rhs) {
				report(lhs)
				continue
			}
			*writes = appendStateWrite(t, fset, *writes, writer, node.Rhs[i])
		}
	case *ast.RangeStmt:
		if node.Tok != token.ASSIGN {
			return
		}
		for _, target := range []ast.Expr{node.Key, node.Value} {
			if target == nil {
				continue
			}
			sel, ok := unparen(target).(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "State" {
				report(target)
			}
		}
	case *ast.KeyValueExpr:
		key, ok := node.Key.(*ast.Ident)
		if !ok || key.Name != "State" {
			return
		}
		if outside {
			report(key)
			return
		}
		*writes = appendStateWrite(t, fset, *writes, writer, node.Value)
	}
}

type parsedFunction struct {
	key        string
	name       string
	receiver   string
	recvObject types.Object
	pointer    bool
	pos        token.Pos
	body       *ast.BlockStmt
	edges      map[string]bool
}

func TestEveryStoreMethodIsTiedToItsMoves(t *testing.T) {
	fset, files := parseNonTestPackage(t)
	astFiles := make([]*ast.File, len(files))
	for i := range files {
		astFiles[i] = files[i].file
	}
	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Types:      map[ast.Expr]types.TypeAndValue{},
	}
	config := &types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := config.Check("github.com/omriariav/amq-squad/v3/internal/task", fset, astFiles, info)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*parsedFunction{}
	functionKeys := map[*types.Func]string{}
	packageFuncs := map[string]string{}
	storeMethods := map[string]string{}
	for _, source := range files {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fnObject, ok := info.Defs[fn.Name].(*types.Func)
			if !ok {
				t.Fatalf("%s does not resolve to a function", fn.Name.Name)
			}
			if (fn.Recv == nil && fn.Name.Name == "init") || fn.Name.Name == "_" {
				pos := fset.Position(fn.Pos())
				t.Errorf("%s:%d: %s is not a declared root", pos.Filename, pos.Line, fn.Name.Name)
				continue
			}
			receiver, pointer, namedReceiver := functionReceiver(fnObject)
			if !namedReceiver {
				pos := fset.Position(fn.Pos())
				t.Errorf("%s:%d: %s has a receiver that is not a named type", pos.Filename, pos.Line, fn.Name.Name)
				continue
			}
			key := "func:" + fn.Name.Name
			if receiver != "" {
				key = "method:" + receiver + ":" + fn.Name.Name
			}
			if fn.Body == nil {
				pos := fset.Position(fn.Pos())
				t.Errorf("%s:%d: %s is declared without a body", pos.Filename, pos.Line, fn.Name.Name)
			}
			var recvObject types.Object
			if fn.Recv != nil && len(fn.Recv.List) > 0 && len(fn.Recv.List[0].Names) > 0 {
				recvObject = info.Defs[fn.Recv.List[0].Names[0]]
			}
			nodes[key] = &parsedFunction{key: key, name: fn.Name.Name, receiver: receiver, recvObject: recvObject, pointer: pointer, pos: fn.Pos(), body: fn.Body, edges: map[string]bool{}}
			functionKeys[fnObject] = key
			if receiver == "" {
				packageFuncs[fn.Name.Name] = key
			} else if receiver == "Store" {
				storeMethods[fn.Name.Name] = key
			}
		}
	}
	for _, source := range files {
		ast.Inspect(source.file, func(n ast.Node) bool {
			literal, ok := n.(*ast.CompositeLit)
			if !ok || len(literal.Elts) == 0 {
				return true
			}
			if _, keyed := literal.Elts[0].(*ast.KeyValueExpr); keyed {
				return true
			}
			pos := fset.Position(literal.Pos())
			typeAndValue, recorded := info.Types[literal]
			if !recorded || typeAndValue.Type == nil {
				t.Errorf("%s:%d: an unkeyed literal has no recorded type", pos.Filename, pos.Line)
				return true
			}
			typ := types.Unalias(typeAndValue.Type)
			if pointer, ok := typ.Underlying().(*types.Pointer); ok {
				typ = types.Unalias(pointer.Elem())
			}
			if _, ok := typ.(*types.TypeParam); ok {
				t.Errorf("%s:%d: an unkeyed literal of a type parameter is not declared", pos.Filename, pos.Line)
				return true
			}
			structure, ok := typ.Underlying().(*types.Struct)
			if !ok {
				return true
			}
			for i := 0; i < structure.NumFields(); i++ {
				if structure.Field(i).Name() == "State" {
					t.Errorf("%s:%d: an unkeyed literal of a struct with a State field is not declared", pos.Filename, pos.Line)
					break
				}
			}
			return true
		})
	}
	selectors := map[*ast.Ident]*ast.SelectorExpr{}
	for _, source := range files {
		ast.Inspect(source.file, func(n ast.Node) bool {
			if selector, ok := n.(*ast.SelectorExpr); ok {
				selectors[selector.Sel] = selector
			}
			return true
		})
	}
	insideFunction := map[*ast.Ident]bool{}
	for _, node := range nodes {
		if node.body == nil {
			continue
		}
		ast.Inspect(node.body, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			insideFunction[ident] = true
			used, ok := info.Uses[ident].(*types.Func)
			if !ok || used.Pkg() != pkg {
				return true
			}
			used = used.Origin()
			key := functionKeys[used]
			if key == "" {
				pos := fset.Position(ident.Pos())
				t.Errorf("%s:%d: %s uses %s, which has no parsed declaration", pos.Filename, pos.Line, node.name, used.Name())
				return true
			}
			if receiver, _, _ := functionReceiver(used); receiver != "Store" {
				node.edges[key] = true
				return true
			}
			selector := selectors[ident]
			var operand *ast.Ident
			if selector != nil {
				operand, _ = unparen(selector.X).(*ast.Ident)
			}
			if node.receiver == "Store" && operand != nil && node.recvObject != nil && info.Uses[operand] == node.recvObject {
				node.edges[key] = true
				return true
			}
			pos := fset.Position(ident.Pos())
			t.Errorf("%s:%d: %s uses Store.%s on a value that is not the enclosing method's receiver", pos.Filename, pos.Line, node.name, used.Name())
			return true
		})
	}
	for ident, object := range info.Uses {
		used, ok := object.(*types.Func)
		if !ok || used.Pkg() != pkg || insideFunction[ident] {
			continue
		}
		used = used.Origin()
		pos := fset.Position(ident.Pos())
		t.Errorf("%s:%d: %s is used outside a function body", pos.Filename, pos.Line, used.Name())
	}
	writerNames := map[string]bool{}
	for _, move := range Moves() {
		writerNames[move.Writer] = true
	}
	for _, node := range nodes {
		if node.receiver == "" || node.receiver == "Store" || !writerNames[node.name] {
			continue
		}
		pos := fset.Position(node.pos)
		t.Errorf("%s:%d: method of %s is named like the writer %s", pos.Filename, pos.Line, node.receiver, node.name)
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
		if node.pointer {
			t.Errorf("Store method %s has a pointer receiver", name)
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

func functionReceiver(fn *types.Func) (name string, pointer bool, named bool) {
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return "", false, true
	}
	typ := types.Unalias(signature.Recv().Type())
	if indirect, ok := typ.(*types.Pointer); ok {
		pointer = true
		typ = types.Unalias(indirect.Elem())
	}
	declared, ok := typ.(*types.Named)
	if !ok {
		return "", pointer, false
	}
	return declared.Obj().Name(), pointer, true
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
