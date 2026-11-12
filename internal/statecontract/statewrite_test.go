package statecontract

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const moduleImportPath = "github.com/omriariav/amq-squad/v3"

const taskImportPath = moduleImportPath + "/internal/task"

var permittedHazardImports = map[string]string{
	"internal/cli/pathnorm_case_darwin.go": "unsafe",
}

type moduleFile struct {
	abs string
	rel string
}

type moduleDirectory struct {
	abs   string
	rel   string
	files []moduleFile
}

func TestOnlyTheListedFilesImportUnsafeOrC(t *testing.T) {
	directories := moduleDirectories(t)
	fset := token.NewFileSet()
	found := map[string]bool{}
	files := 0
	for _, directory := range directories {
		for _, file := range directory.files {
			parsed := parseModuleFile(t, fset, file, parser.ImportsOnly)
			files++
			for _, spec := range parsed.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path != "unsafe" && path != "C" {
					continue
				}
				if permittedHazardImports[file.rel] == path {
					found[file.rel] = true
					continue
				}
				t.Errorf("%s:%d: import of %s: a write through it is not declared", file.rel, fset.Position(spec.Pos()).Line, path)
			}
		}
	}
	for path, imported := range permittedHazardImports {
		if !found[path] {
			t.Errorf("%s is listed as importing %s and does not", path, imported)
		}
	}
	if len(directories) < 2 {
		t.Fatalf("the module walk found %d directories", len(directories))
	}
	t.Logf("directories=%d files=%d", len(directories), files)
}

func TestNoPackageOutsideTaskWritesTaskState(t *testing.T) {
	directories := moduleDirectories(t)
	walkedPackagePaths := map[string]bool{}
	for _, directory := range directories {
		walkedPackagePaths[modulePackagePath(directory.rel)] = true
	}
	fset := token.NewFileSet()
	sourceImporter := importer.ForCompiler(fset, "source", nil)
	taskPackage, err := sourceImporter.Import(taskImportPath)
	if err != nil {
		t.Fatal(err)
	}
	taskObject, ok := taskPackage.Scope().Lookup("Task").(*types.TypeName)
	if !ok {
		t.Fatal("task.Task is missing or is not a type")
	}
	taskStructure, ok := types.Unalias(taskObject.Type()).Underlying().(*types.Struct)
	if !ok {
		t.Fatal("task.Task is not a struct")
	}
	var taskState *types.Var
	for i := 0; i < taskStructure.NumFields(); i++ {
		if taskStructure.Field(i).Name() == "State" {
			taskState = taskStructure.Field(i)
			break
		}
	}
	if taskState == nil {
		t.Fatal("task.Task has no field named State")
	}

	packages := 0
	stateSelections := 0
	wholeTaskAssignments := 0
	excludedFiles := 0
	visitedImportedPackages := map[string]bool{}
	for _, directory := range directories {
		if directory.rel == "internal/task" {
			continue
		}
		var parsedFiles []*ast.File
		for _, file := range directory.files {
			matched, err := build.Default.MatchFile(directory.abs, filepath.Base(file.abs))
			if err != nil {
				t.Fatal(err)
			}
			if !matched {
				excludedFiles++
				checkExcludedModuleFile(t, fset, file, walkedPackagePaths)
				continue
			}
			parsedFiles = append(parsedFiles, parseModuleFile(t, fset, file, 0))
		}
		if len(parsedFiles) == 0 {
			continue
		}
		packages++
		info := &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		}
		config := &types.Config{
			Importer: sourceImporter,
			Error: func(err error) {
				t.Errorf("%s: %v", directory.rel, err)
			},
		}
		packagePath := modulePackagePath(directory.rel)
		checkedPackage, _ := config.Check(packagePath, fset, parsedFiles, info)
		if checkedPackage != nil {
			checkImportedPackages(t, checkedPackage, walkedPackagePaths, visitedImportedPackages)
		}

		for _, file := range parsedFiles {
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.SelectorExpr:
					if isTaskStateSelection(node, info, taskStructure, taskState) {
						stateSelections++
					}
				case *ast.AssignStmt:
					for _, left := range node.Lhs {
						expression := unparenExpression(left)
						if selector, ok := expression.(*ast.SelectorExpr); ok && isTaskStateSelection(selector, info, taskStructure, taskState) {
							reportStateForm(t, fset, left.Pos(), "a write of a task's state")
						}
						if node.Tok == token.ASSIGN && isTaskStructType(info.TypeOf(expression), taskStructure) {
							wholeTaskAssignments++
						}
					}
				case *ast.RangeStmt:
					for _, expression := range []ast.Expr{node.Key, node.Value} {
						expression = unparenExpression(expression)
						if selector, ok := expression.(*ast.SelectorExpr); ok && isTaskStateSelection(selector, info, taskStructure, taskState) {
							reportStateForm(t, fset, node.For, "a write of a task's state")
						}
					}
				case *ast.UnaryExpr:
					if node.Op == token.AND {
						if selector, ok := unparenExpression(node.X).(*ast.SelectorExpr); ok && isTaskStateSelection(selector, info, taskStructure, taskState) {
							reportStateForm(t, fset, node.OpPos, "the address of a task's state")
						}
					}
				case *ast.TypeSpec:
					if object, ok := info.Defs[node.Name].(*types.TypeName); ok && isTypeOverTask(object.Type(), taskStructure, map[types.Type]bool{}) {
						reportStateForm(t, fset, node.Name.Pos(), "a named type over a task")
					}
				case *ast.CallExpr:
					function := info.Types[node.Fun]
					if len(node.Args) == 1 && function.IsType() {
						argumentType := info.TypeOf(node.Args[0])
						targetType := function.Type
						if argumentType != nil && targetType != nil && !types.Identical(argumentType, targetType) &&
							((isTaskType(argumentType, taskStructure) && isStructLike(targetType)) ||
								(isTaskType(targetType, taskStructure) && isStructLike(argumentType))) {
							reportStateForm(t, fset, node.Pos(), "a conversion of a task")
						}
					}
				case *ast.CompositeLit:
					if len(node.Elts) == 0 {
						break
					}
					literalType := info.TypeOf(node)
					if isTypeParameterAfterOnePointer(literalType) {
						reportStateForm(t, fset, node.Pos(), "a literal of a type parameter")
						break
					}
					if !isTaskType(literalType, taskStructure) {
						break
					}
					if _, keyed := node.Elts[0].(*ast.KeyValueExpr); !keyed {
						reportStateForm(t, fset, node.Pos(), "an unkeyed task literal")
						break
					}
					for _, element := range node.Elts {
						keyed, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if ident, ok := keyed.Key.(*ast.Ident); ok && ident.Name == "State" {
							reportStateForm(t, fset, keyed.Pos(), "a task literal that names its state")
						}
					}
				}
				return true
			})
		}
	}
	if packages < 2 || stateSelections == 0 {
		t.Fatalf("the scan read %d packages and %d uses of a task's state", packages, stateSelections)
	}
	t.Logf("packages=%d uses_of_state=%d whole_task_assignments=%d files_skipped_by_build_constraint=%d", packages, stateSelections, wholeTaskAssignments, excludedFiles)
}

func moduleDirectories(t *testing.T) []moduleDirectory {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]*moduleDirectory{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			info, statErr := os.Stat(path)
			if statErr == nil && info.IsDir() {
				t.Errorf("%s: a symbolic link to a directory: the walk does not follow it", rel)
				return nil
			}
			if strings.HasSuffix(entry.Name(), ".go") {
				if statErr == nil && !info.Mode().IsRegular() {
					return fmt.Errorf("%s: not a regular file and named like a Go file: the walk does not read it", rel)
				}
				t.Errorf("%s: a symbolic link named like a Go file: the walk does not read it", rel)
				return nil
			}
			if entry.Name() == "go.mod" {
				t.Errorf("%s: a symbolic link named go.mod: the walk does not read it", rel)
			}
			return nil
		}
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			info, err := os.Lstat(filepath.Join(path, "go.mod"))
			if err == nil && info.Mode().IsRegular() {
				return filepath.SkipDir
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			if strings.HasSuffix(entry.Name(), ".go") {
				return fmt.Errorf("%s: not a regular file and named like a Go file: the walk does not read it", rel)
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		dirAbs := filepath.Dir(path)
		dirRel, err := filepath.Rel(root, dirAbs)
		if err != nil {
			return err
		}
		directory := directories[dirRel]
		if directory == nil {
			directory = &moduleDirectory{abs: dirAbs, rel: filepath.ToSlash(dirRel)}
			directories[dirRel] = directory
		}
		directory.files = append(directory.files, moduleFile{abs: path, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make([]moduleDirectory, 0, len(directories))
	for _, directory := range directories {
		sort.Slice(directory.files, func(i, j int) bool { return directory.files[i].rel < directory.files[j].rel })
		result = append(result, *directory)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].rel < result[j].rel })
	return result
}

func parseModuleFile(t *testing.T, fset *token.FileSet, file moduleFile, mode parser.Mode) *ast.File {
	t.Helper()
	source, err := os.ReadFile(file.abs)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(fset, file.rel, source, mode)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func checkExcludedModuleFile(t *testing.T, fset *token.FileSet, file moduleFile, walkedPackagePaths map[string]bool) {
	t.Helper()
	parsed := parseModuleFile(t, fset, file, 0)
	for _, spec := range parsed.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if path == taskImportPath {
			t.Errorf("%s:%d: a file this host's build constraints exclude imports internal/task", file.rel, fset.Position(spec.Pos()).Line)
		}
		if isModuleImportPath(path) && !walkedPackagePaths[path] {
			t.Errorf("%s:%d: a file this host's build constraints exclude imports %s, a package of this module's path that the walk did not read", file.rel, fset.Position(spec.Pos()).Line, path)
		}
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name == "State" {
				t.Errorf("%s:%d: a file this host's build constraints exclude names a State field", file.rel, fset.Position(node.Pos()).Line)
			}
		case *ast.KeyValueExpr:
			if ident, ok := node.Key.(*ast.Ident); ok && ident.Name == "State" {
				t.Errorf("%s:%d: a file this host's build constraints exclude names a State key", file.rel, fset.Position(node.Pos()).Line)
			}
		case *ast.StructType:
			for _, field := range node.Fields.List {
				for _, name := range field.Names {
					if name.Name == "State" {
						t.Errorf("%s:%d: a file this host's build constraints exclude declares a State field", file.rel, fset.Position(name.Pos()).Line)
					}
				}
			}
		}
		return true
	})
}

func modulePackagePath(directory string) string {
	if directory == "." {
		return moduleImportPath
	}
	return moduleImportPath + "/" + directory
}

func isModuleImportPath(path string) bool {
	return path == moduleImportPath || strings.HasPrefix(path, moduleImportPath+"/")
}

func checkImportedPackages(t *testing.T, importedBy *types.Package, walkedPackagePaths map[string]bool, visited map[string]bool) {
	t.Helper()
	for _, imported := range importedBy.Imports() {
		path := imported.Path()
		if visited[path] {
			continue
		}
		visited[path] = true
		if isModuleImportPath(path) && !walkedPackagePaths[path] {
			t.Errorf("%s: a package of this module's path that the walk did not read, imported by %s", path, importedBy.Path())
		}
		checkImportedPackages(t, imported, walkedPackagePaths, visited)
	}
}

func reportStateForm(t *testing.T, fset *token.FileSet, pos token.Pos, what string) {
	t.Helper()
	position := fset.Position(pos)
	t.Errorf("%s:%d: %s outside internal/task is not declared", filepath.ToSlash(position.Filename), position.Line, what)
}

func unparenExpression(expression ast.Expr) ast.Expr {
	for {
		paren, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = paren.X
	}
}

func isTaskStateSelection(expression *ast.SelectorExpr, info *types.Info, taskStructure *types.Struct, taskState *types.Var) bool {
	selection := info.Selections[expression]
	if selection == nil || selection.Kind() != types.FieldVal {
		return false
	}
	current := selection.Recv()
	indices := selection.Index()
	for i, index := range indices {
		structure, ok := structureAfterPointers(current)
		if !ok || index >= structure.NumFields() {
			return false
		}
		field := structure.Field(index)
		if i == len(indices)-1 {
			return field.Name() == "State" && (field == taskState || types.IdenticalIgnoreTags(structure, taskStructure))
		}
		current = field.Type()
	}
	return false
}

func structureAfterPointers(typ types.Type) (*types.Struct, bool) {
	seen := map[types.Type]bool{}
	for typ != nil {
		typ = types.Unalias(typ)
		if seen[typ] {
			return nil, false
		}
		seen[typ] = true
		if pointer, ok := typ.(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		if pointer, ok := typ.Underlying().(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		structure, ok := typ.Underlying().(*types.Struct)
		return structure, ok
	}
	return nil, false
}

func isTaskStructType(typ types.Type, taskStructure *types.Struct) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	structure, ok := typ.Underlying().(*types.Struct)
	return ok && types.IdenticalIgnoreTags(structure, taskStructure)
}

func isTaskType(typ types.Type, taskStructure *types.Struct) bool {
	seen := map[types.Type]bool{}
	for typ != nil {
		typ = types.Unalias(typ)
		if seen[typ] {
			return false
		}
		seen[typ] = true
		if pointer, ok := typ.(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		if pointer, ok := typ.Underlying().(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		return isTaskStructType(typ, taskStructure)
	}
	return false
}

func isStructLike(typ types.Type) bool {
	seen := map[types.Type]bool{}
	for typ != nil {
		typ = types.Unalias(typ)
		if seen[typ] {
			return false
		}
		seen[typ] = true
		if _, ok := typ.(*types.TypeParam); ok {
			return true
		}
		if pointer, ok := typ.(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		if pointer, ok := typ.Underlying().(*types.Pointer); ok {
			typ = pointer.Elem()
			continue
		}
		_, ok := typ.Underlying().(*types.Struct)
		return ok
	}
	return false
}

func isTypeOverTask(typ types.Type, taskStructure *types.Struct, seen map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if seen[typ] {
		return false
	}
	seen[typ] = true
	underlying := typ.Underlying()
	if structure, ok := underlying.(*types.Struct); ok {
		return types.IdenticalIgnoreTags(structure, taskStructure)
	}
	switch underlying := underlying.(type) {
	case *types.Pointer:
		return isTypeOverTask(underlying.Elem(), taskStructure, seen)
	case *types.Slice:
		return isTypeOverTask(underlying.Elem(), taskStructure, seen)
	case *types.Array:
		return isTypeOverTask(underlying.Elem(), taskStructure, seen)
	case *types.Map:
		return isTypeOverTask(underlying.Elem(), taskStructure, seen)
	default:
		return false
	}
}

func isTypeParameterAfterOnePointer(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	} else if pointer, ok := typ.Underlying().(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	_, ok := typ.(*types.TypeParam)
	return ok
}
