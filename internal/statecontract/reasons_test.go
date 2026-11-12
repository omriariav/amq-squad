package statecontract

import (
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type reasonUse struct {
	constant string
	value    string
	site     string
	function string
	door     string
	literal  bool
}

type reasonSurvey struct {
	packages  int
	functions map[string]*types.Signature
	uses      []reasonUse
}

type reasonRow struct {
	constant string
	function string
	door     string
}

type reasonListStats struct {
	entries     int
	writers     int
	doors       int
	openDoors   int
	uses        int
	literalUses int
}

func TestReasonsAreTiedToTheirWriters(t *testing.T) {
	survey := surveyReasonUses(t)
	notReasons := NotReasons()
	notSet := checkNotReasons(t, notReasons)
	block := checkReasonList(t, "block", BlockReasons(), notSet, survey)
	wait := checkReasonList(t, "wait", WaitReasons(), notSet, survey)
	entries := block.entries + wait.entries + len(notReasons)
	checkReasonCodes(t, survey, entries)

	t.Logf("block: entries=%d writers=%d doors=%d open_doors=%d uses_at_doors=%d literal_sites_at_doors=%d", block.entries, block.writers, block.doors, block.openDoors, block.uses, block.literalUses)
	t.Logf("wait: entries=%d writers=%d doors=%d open_doors=%d uses_at_doors=%d literal_sites_at_doors=%d", wait.entries, wait.writers, wait.doors, wait.openDoors, wait.uses, wait.literalUses)
	t.Logf("packages=%d entries=%d", survey.packages, entries)
}

func surveyReasonUses(t *testing.T) reasonSurvey {
	result := reasonSurvey{functions: map[string]*types.Signature{}}
	fset := token.NewFileSet()
	sourceImporter := importer.ForCompiler(fset, "source", nil)
	for _, directory := range moduleDirectories(t) {
		var files []*ast.File
		for _, file := range directory.files {
			matched, err := build.Default.MatchFile(directory.abs, filepath.Base(file.abs))
			if err != nil {
				t.Fatal(err)
			}
			if matched {
				files = append(files, parseModuleFile(t, fset, file, 0))
			}
		}
		if len(files) == 0 {
			continue
		}
		result.packages++
		info := &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		}
		config := &types.Config{
			Importer: sourceImporter,
			Error: func(err error) {
				t.Errorf("%s: %v", directory.rel, err)
			},
		}
		_, _ = config.Check(modulePackagePath(directory.rel), fset, files, info)
		for _, file := range files {
			parents := reasonParents(file)
			ast.Inspect(file, func(node ast.Node) bool {
				declaration, ok := node.(*ast.FuncDecl)
				if !ok {
					return true
				}
				if function, ok := info.Defs[declaration.Name].(*types.Func); ok {
					name := reasonFunctionName(function)
					signature, _ := function.Type().(*types.Signature)
					if name != "" && signature != nil {
						result.functions[name] = signature
					}
				}
				return true
			})
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.Ident:
					object, ok := info.Uses[node].(*types.Const)
					if !ok || object.Pkg() == nil || !isModuleImportPath(object.Pkg().Path()) || !reasonStringType(object.Type()) {
						return true
					}
					expression := widenReasonExpression(node, parents, info)
					result.uses = append(result.uses, reasonUse{
						constant: object.Pkg().Name() + "." + object.Name(),
						value:    constant.StringVal(object.Val()),
						site:     reasonSite(fset, node.Pos()),
						function: enclosingReasonFunction(expression, parents, info),
						door:     reasonPlace(expression, parents, info),
					})
				case *ast.BasicLit:
					if node.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(node.Value)
					if err != nil {
						t.Fatal(err)
					}
					expression := widenReasonExpression(node, parents, info)
					result.uses = append(result.uses, reasonUse{
						value:    value,
						site:     reasonSite(fset, node.Pos()),
						function: enclosingReasonFunction(expression, parents, info),
						door:     reasonPlace(expression, parents, info),
						literal:  true,
					})
				}
				return true
			})
		}
	}
	return result
}

func reasonParents(file *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) != 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func reasonStringType(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsString != 0
}

func reasonSite(fset *token.FileSet, pos token.Pos) string {
	position := fset.Position(pos)
	return filepath.ToSlash(position.Filename) + ":" + strconv.Itoa(position.Line)
}

func widenReasonExpression(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) ast.Expr {
	for {
		switch parent := parents[expression].(type) {
		case *ast.SelectorExpr:
			if parent.Sel == expression {
				expression = parent
				continue
			}
		case *ast.ParenExpr:
			if parent.X == expression {
				expression = parent
				continue
			}
		case *ast.CallExpr:
			if len(parent.Args) == 1 && parent.Args[0] == expression && info.Types[parent.Fun].IsType() {
				expression = parent
				continue
			}
		}
		return expression
	}
}

func enclosingReasonFunction(node ast.Node, parents map[ast.Node]ast.Node, info *types.Info) string {
	for current := node; current != nil; current = parents[current] {
		if declaration, ok := current.(*ast.FuncDecl); ok {
			if function, ok := info.Defs[declaration.Name].(*types.Func); ok {
				return reasonFunctionName(function)
			}
		}
	}
	return "package level"
}

func reasonFunctionName(function *types.Func) string {
	function = function.Origin()
	if function.Pkg() == nil {
		return ""
	}
	prefix := function.Pkg().Name() + "."
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return prefix + function.Name()
	}
	receiver := types.Unalias(signature.Recv().Type())
	pointer := false
	if ptr, ok := receiver.(*types.Pointer); ok {
		pointer = true
		receiver = types.Unalias(ptr.Elem())
	}
	named, ok := receiver.(*types.Named)
	if !ok {
		return prefix + function.Name()
	}
	if pointer {
		return prefix + "(*" + named.Obj().Name() + ")." + function.Name()
	}
	return prefix + "(" + named.Obj().Name() + ")." + function.Name()
}

func reasonPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	if place := reasonCallPlace(expression, parents, info); place != "" {
		return place
	}
	if place := reasonFieldPlace(expression, parents, info); place != "" {
		return place
	}
	if place := reasonLocalPlace(expression, parents, info); place != "" {
		return place
	}
	if place := reasonResultPlace(expression, parents, info); place != "" {
		return place
	}
	return reasonElementPlace(expression, parents, info)
}

func reasonCallPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	call, ok := parents[expression].(*ast.CallExpr)
	if !ok {
		return ""
	}
	argument := -1
	for i, candidate := range call.Args {
		if candidate == expression {
			argument = i
			break
		}
	}
	if argument < 0 {
		return ""
	}
	called := unparenExpression(call.Fun)
	function, methodExpression, functionName := reasonCalledFunction(called, info)
	if function == nil {
		return ""
	}
	if methodExpression {
		if argument == 0 {
			return ""
		}
		argument--
	}
	if functionName == "" {
		return ""
	}
	return "param " + functionName + "#" + strconv.Itoa(argument)
}

func reasonCalledFunction(expression ast.Expr, info *types.Info) (*types.Func, bool, string) {
	switch expression := expression.(type) {
	case *ast.Ident:
		function, _ := info.Uses[expression].(*types.Func)
		if function == nil {
			return nil, false, ""
		}
		return function, false, reasonFunctionName(function)
	case *ast.SelectorExpr:
		if selection := info.Selections[expression]; selection != nil {
			function, _ := selection.Obj().(*types.Func)
			if function == nil {
				return nil, false, ""
			}
			return function, selection.Kind() == types.MethodExpr, reasonSelectedFunctionName(function)
		}
		function, _ := info.Uses[expression.Sel].(*types.Func)
		if function == nil {
			return nil, false, ""
		}
		return function, false, reasonFunctionName(function)
	default:
		return nil, false, ""
	}
}

func reasonSelectedFunctionName(function *types.Func) string {
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return reasonFunctionName(function)
	}
	receiver := types.Unalias(signature.Recv().Type())
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(pointer.Elem())
	}
	underlying := receiver.Underlying()
	if _, ok := underlying.(*types.Interface); !ok {
		return reasonFunctionName(function)
	}
	if function.Pkg() == nil {
		return ""
	}
	receiverName := "interface"
	if named, ok := receiver.(*types.Named); ok {
		receiverName = named.Obj().Name()
	}
	return function.Pkg().Name() + ".(" + receiverName + ")." + function.Name()
}

func reasonFieldPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	if keyed, ok := parents[expression].(*ast.KeyValueExpr); ok && keyed.Value == expression {
		if literal, ok := parents[keyed].(*ast.CompositeLit); ok {
			name, _, ok := reasonNamedStruct(info.TypeOf(literal))
			if ok {
				if key, ok := keyed.Key.(*ast.Ident); ok {
					return "field " + name + "." + key.Name
				}
			}
		}
	}
	if literal, ok := parents[expression].(*ast.CompositeLit); ok {
		name, structure, ok := reasonNamedStruct(info.TypeOf(literal))
		if ok {
			for i, element := range literal.Elts {
				if element == expression && i < structure.NumFields() {
					return "field " + name + "." + structure.Field(i).Name()
				}
			}
		}
	}
	assignment, ok := parents[expression].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
		return ""
	}
	for i, right := range assignment.Rhs {
		if right != expression {
			continue
		}
		selector, ok := unparenExpression(assignment.Lhs[i]).(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		selection := info.Selections[selector]
		if selection == nil || selection.Kind() != types.FieldVal {
			return ""
		}
		name, field, ok := reasonSelectedField(selection)
		if ok {
			return "field " + name + "." + field
		}
	}
	return ""
}

func reasonNamedStruct(typ types.Type) (string, *types.Struct, bool) {
	if typ == nil {
		return "", nil, false
	}
	typ = reasonDereferenceType(typ)
	named, ok := typ.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return "", nil, false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return "", nil, false
	}
	return named.Obj().Pkg().Name() + "." + named.Obj().Name(), structure, true
}

func reasonDereferenceType(typ types.Type) types.Type {
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		return types.Unalias(pointer.Elem())
	}
	if named, ok := typ.(*types.Named); ok {
		if pointer, ok := named.Underlying().(*types.Pointer); ok {
			return types.Unalias(pointer.Elem())
		}
	}
	return typ
}

func reasonStruct(typ types.Type) (*types.Struct, bool) {
	if typ == nil {
		return nil, false
	}
	typ = reasonDereferenceType(typ)
	structure, ok := typ.Underlying().(*types.Struct)
	return structure, ok
}

func reasonSelectedField(selection *types.Selection) (string, string, bool) {
	current := selection.Recv()
	indices := selection.Index()
	for i, index := range indices {
		structure, ok := reasonStruct(current)
		if !ok || index >= structure.NumFields() {
			return "", "", false
		}
		field := structure.Field(index)
		if i == len(indices)-1 {
			name, _, ok := reasonNamedStruct(current)
			if !ok {
				return "", "", false
			}
			return name, field.Name(), true
		}
		current = field.Type()
	}
	return "", "", false
}

func reasonLocalPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	function := enclosingReasonFunction(expression, parents, info)
	if function == "package level" {
		return ""
	}
	if assignment, ok := parents[expression].(*ast.AssignStmt); ok && len(assignment.Lhs) == len(assignment.Rhs) {
		for i, right := range assignment.Rhs {
			if right != expression {
				continue
			}
			identifier, ok := unparenExpression(assignment.Lhs[i]).(*ast.Ident)
			if !ok || reasonPackageObject(identifier, info) {
				return ""
			}
			return "local " + identifier.Name + " in " + function
		}
	}
	if spec, ok := parents[expression].(*ast.ValueSpec); ok && len(spec.Names) == len(spec.Values) {
		declaration, ok := parents[spec].(*ast.GenDecl)
		if !ok || declaration.Tok != token.VAR {
			return ""
		}
		for i, value := range spec.Values {
			if value == expression && !reasonPackageObject(spec.Names[i], info) {
				return "local " + spec.Names[i].Name + " in " + function
			}
		}
	}
	return ""
}

func reasonPackageObject(identifier *ast.Ident, info *types.Info) bool {
	object := info.Defs[identifier]
	if object == nil {
		object = info.Uses[identifier]
	}
	return object == nil || object.Pkg() != nil && object.Parent() == object.Pkg().Scope()
}

func reasonResultPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	result, ok := parents[expression].(*ast.ReturnStmt)
	if !ok {
		return ""
	}
	index := -1
	for i, candidate := range result.Results {
		if candidate == expression {
			index = i
			break
		}
	}
	if index < 0 {
		return ""
	}
	for current := ast.Node(result); current != nil; current = parents[current] {
		switch current := current.(type) {
		case *ast.FuncLit:
			return ""
		case *ast.FuncDecl:
			if function, ok := info.Defs[current.Name].(*types.Func); ok {
				return "result " + reasonFunctionName(function) + "#" + strconv.Itoa(index)
			}
		}
	}
	return ""
}

func reasonElementPlace(expression ast.Expr, parents map[ast.Node]ast.Node, info *types.Info) string {
	literal, ok := parents[expression].(*ast.CompositeLit)
	if !ok {
		if keyed, keyedOK := parents[expression].(*ast.KeyValueExpr); keyedOK && keyed.Value == expression {
			literal, ok = parents[keyed].(*ast.CompositeLit)
		}
	}
	if !ok {
		return ""
	}
	typ := info.TypeOf(literal)
	if typ == nil {
		return ""
	}
	typ = reasonDereferenceType(typ)
	switch typ.Underlying().(type) {
	case *types.Array, *types.Slice:
		return "element in " + enclosingReasonFunction(expression, parents, info)
	default:
		return ""
	}
}

func checkNotReasons(t *testing.T, entries []ReasonEntry) map[string]bool {
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.Constant] {
			t.Errorf("not-a-reason list: %s is declared twice", entry.Constant)
		}
		seen[entry.Constant] = true
		if entry.Mark != "" || len(entry.Writers) != 0 {
			t.Errorf("not-a-reason list: %s carries a mark or a writer", entry.Constant)
		}
	}
	return seen
}

func checkReasonList(t *testing.T, name string, list ReasonList, notReasons map[string]bool, survey reasonSurvey) reasonListStats {
	stats := reasonListStats{entries: len(list.Entries)}
	doors := map[string]bool{}
	var doorOrder []string
	for _, door := range list.Doors {
		if doors[door] {
			t.Errorf("%s list: door %s is declared twice", name, door)
		} else {
			doorOrder = append(doorOrder, door)
		}
		doors[door] = true
	}
	stats.doors = len(doors)
	if !list.Open || len(list.OpenDoors) == 0 {
		t.Errorf("%s list: the list is not declared open with its open doors", name)
	}
	openDoors := map[string]bool{}
	for _, door := range list.OpenDoors {
		if openDoors[door.Name] {
			t.Errorf("%s list: the open door %q is declared twice", name, door.Name)
		}
		openDoors[door.Name] = true
		if len(door.Functions) == 0 {
			t.Errorf("%s list: the open door %q names no function", name, door.Name)
		}
		for _, function := range door.Functions {
			if survey.functions[function] == nil {
				t.Errorf("%s list: the open door %q names %s, and no such function is declared", name, door.Name, function)
			}
		}
	}
	stats.openDoors = len(openDoors)
	for _, door := range doorOrder {
		functionName, ok := reasonDoorFunction(door)
		if ok {
			if signature := survey.functions[functionName]; signature != nil && signature.TypeParams().Len() != 0 {
				t.Errorf("%s list: the door %s is in a generic function, which the test does not walk", name, door)
			}
		}
		functionName, parameter, ok := reasonParameterDoor(door)
		if !ok {
			continue
		}
		signature := survey.functions[functionName]
		if signature != nil && signature.Variadic() && signature.Params().Len() != 0 && parameter == signature.Params().Len()-1 {
			t.Errorf("%s list: the door %s is a variadic function's last parameter, which the test does not fold", name, door)
		}
	}

	entries := map[string]ReasonEntry{}
	rows := map[reasonRow]bool{}
	var rowOrder []reasonRow
	seenEntries := map[string]bool{}
	for _, entry := range list.Entries {
		if seenEntries[entry.Constant] {
			t.Errorf("%s list: %s is declared twice", name, entry.Constant)
		}
		seenEntries[entry.Constant] = true
		entries[entry.Constant] = entry
		if notReasons[entry.Constant] {
			t.Errorf("%s list: %s is also on the not-a-reason list", name, entry.Constant)
		}
		guarded := strings.HasPrefix(entry.Mark, "traced, guarded at ")
		if entry.Mark != "" && entry.Mark != "no writer" && !guarded {
			t.Errorf("%s list: %s carries the mark %q, which is not a declared mark", name, entry.Constant, entry.Mark)
		}
		if guarded {
			function := strings.TrimPrefix(entry.Mark, "traced, guarded at ")
			if survey.functions[function] == nil {
				t.Errorf("%s list: %s is marked guarded at %s, and no such function is declared", name, entry.Constant, function)
			}
		}
		if len(entry.Writers) == 0 && entry.Mark != "no writer" {
			t.Errorf("%s list: %s declares no writer and does not carry the no writer mark", name, entry.Constant)
		}
		if len(entry.Writers) != 0 && entry.Mark == "no writer" {
			t.Errorf("%s list: %s carries the no writer mark and declares a writer", name, entry.Constant)
		}
		for _, writer := range entry.Writers {
			row := reasonRow{constant: entry.Constant, function: writer.Function, door: writer.Door}
			if !doors[writer.Door] {
				t.Errorf("%s list: %s declares the writer %s at %s, a door outside the declared door set", name, entry.Constant, writer.Function, writer.Door)
			}
			if rows[row] {
				t.Errorf("%s list: %s declares the writer %s at %s twice", name, entry.Constant, writer.Function, writer.Door)
			} else {
				rows[row] = true
				rowOrder = append(rowOrder, row)
			}
		}
	}
	stats.writers = len(rows)

	matchedRows := map[reasonRow]bool{}
	matchedDoors := map[string]bool{}
	for _, use := range survey.uses {
		if use.literal {
			if doors[use.door] {
				stats.literalUses++
			}
			continue
		}
		if !doors[use.door] {
			continue
		}
		row := reasonRow{constant: use.constant, function: use.function, door: use.door}
		if !rows[row] {
			t.Errorf("%s: %s is used at the %s door %s in %s, and the list does not declare that writer", use.site, use.constant, name, use.door, use.function)
			continue
		}
		stats.uses++
		matchedRows[row] = true
		matchedDoors[use.door] = true
		if entry := entries[use.constant]; use.value != entry.Code {
			t.Errorf("%s: %s has the value %s and the %s list declares %q", use.site, use.constant, strconv.Quote(use.value), name, entry.Code)
		}
	}
	for _, row := range rowOrder {
		if !matchedRows[row] {
			t.Errorf("%s list: %s declares the writer %s at %s, and no use by name is there", name, row.constant, row.function, row.door)
		}
	}
	for _, door := range doorOrder {
		if !matchedDoors[door] {
			t.Errorf("%s list: the door %s has no listed use", name, door)
		}
	}
	return stats
}

func reasonDoorFunction(door string) (string, bool) {
	for _, prefix := range []string{"param ", "result "} {
		if strings.HasPrefix(door, prefix) {
			body := strings.TrimPrefix(door, prefix)
			index := strings.LastIndex(body, "#")
			if index < 0 {
				return "", false
			}
			return body[:index], true
		}
	}
	if strings.HasPrefix(door, "local ") {
		index := strings.LastIndex(door, " in ")
		if index < 0 {
			return "", false
		}
		return door[index+len(" in "):], true
	}
	if strings.HasPrefix(door, "element in ") {
		return strings.TrimPrefix(door, "element in "), true
	}
	return "", false
}

func reasonParameterDoor(door string) (string, int, bool) {
	if !strings.HasPrefix(door, "param ") {
		return "", 0, false
	}
	body := strings.TrimPrefix(door, "param ")
	index := strings.LastIndex(body, "#")
	if index < 0 {
		return "", 0, false
	}
	parameter, err := strconv.Atoi(body[index+1:])
	if err != nil {
		return "", 0, false
	}
	return body[:index], parameter, true
}

func checkReasonCodes(t *testing.T, survey reasonSurvey, entries int) {
	namedCodes := 0
	for _, use := range survey.uses {
		if use.door != "field statecontract.ReasonEntry.Code" {
			continue
		}
		if use.literal {
			t.Errorf("%s: a reason's Code is the string literal %s, never a constant's name", use.site, strconv.Quote(use.value))
			continue
		}
		namedCodes++
	}
	if namedCodes != entries {
		t.Errorf("the lists hold %d entries and %d of their Code fields are a constant used by name", entries, namedCodes)
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "reasons.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literals := 0
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		code := ""
		constantName := ""
		for _, element := range literal.Elts {
			keyed, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := keyed.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Code":
				selector, ok := keyed.Value.(*ast.SelectorExpr)
				if ok {
					qualifier, qualifierOK := selector.X.(*ast.Ident)
					if qualifierOK {
						code = qualifier.Name + "." + selector.Sel.Name
					} else {
						code = "(not a qualified name)"
					}
				} else {
					code = "(not a qualified name)"
				}
			case "Constant":
				value, ok := keyed.Value.(*ast.BasicLit)
				if ok && value.Kind == token.STRING {
					constantName, _ = strconv.Unquote(value.Value)
				}
			}
		}
		if code == "" && constantName == "" {
			return true
		}
		literals++
		if code != constantName {
			t.Errorf("reasons.go:%d: Code is %s and Constant is %q", fset.Position(literal.Pos()).Line, code, constantName)
		}
		return true
	})
	if literals != entries {
		t.Errorf("the lists hold %d entries and reasons.go holds %d literals with a Code or a Constant", entries, literals)
	}
}
