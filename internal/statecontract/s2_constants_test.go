package statecontract

import (
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// S2-2 (3.3.0): every code that leaves the process is a constant, and every
// reason constant is on a list. Both tests read the module type-checked, so
// a constant is found by its resolved value and an expression by its folded
// value, never by the shape of its source.

// s2DeclaringPackages are the packages whose constants the reason lists name
// (reasons.go's imports).
var s2DeclaringPackages = []string{"claudeseat", "registry", "routing", "schedproc", "scheduler", "seatcommit", "seatexec", "task"}

// s2FixedFamilies are name words that make a constant a reason constant
// whatever the lists hold. The lists add the leading word of each listed
// constant's name (s2Families), so a new constant of an existing family (a
// GoneHeartbeatLost beside GoneProcessExited) is found too.
var s2FixedFamilies = []string{"Reason", "Block", "Wait", "Refusal", "Skip"}

// s2NotTaskReasons are the string constants of a reason family in the
// declaring packages that are no task's block or wait reason, each with what
// it is instead. TestReasonsAreTiedToTheirWriters shows none of them is used
// at a declared block or wait door. Adding an entry is a plan ruling, never a
// seat's own edit.
var s2NotTaskReasons = map[string]string{
	"routing.SkipUsedBefore":                   "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipProbation":                    "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipInputOver32k":                 "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipInputOver100k":                "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipRuntimeNotPermitted":          "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipNotInRegistry":                "a router skip reason (routing.Skip.Reason), never a task field",
	"routing.SkipUnverified":                   "a router skip reason (routing.Skip.Reason), never a task field",
	"schedproc.ReasonBaseFetchUnauthenticated": "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonBaseTipMoved":             "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonLeadRunning":              "the lead lock's refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatBranchUnmerged":       "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatBranchUnpushed":       "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatGitdirForeign":        "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatHeadUnreadable":       "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatWorktreeCheckFailed":  "a seat-prepare refusal code (schedproc), never a task field",
	"schedproc.ReasonSeatWorktreeDirty":        "a seat-prepare refusal code (schedproc), never a task field",
	"claudeseat.TokenVar":                      "the name of the environment variable that carries the Claude token, not a code",
}

// s2Expression is one maximal constant string expression outside a const
// declaration that has a string literal in it, and where it reaches: its
// folded value, the literal's site, and the door (directly, or through one
// local variable it is assigned to).
type s2Expression struct {
	value, site, door string
}

type s2Module struct {
	constants   map[string]string // "pkg.Name" -> site, every string constant in the scope of a declaring package
	locals      map[string]string // "pkg.Name" -> site, every string constant declared inside a function of a declaring package
	values      map[string]string // "pkg.Name" (scope or function) -> its value, for both maps above
	aliases     map[string]string // "pkg.Name" -> the "pkg.Name" of the constant its initializer names (X = other.Y)
	expressions []s2Expression
}

var (
	s2Once sync.Once
	s2Read s2Module
)

// s2ReadModule type-checks the module's product packages once (as
// surveyReasonUses does) and collects what both tests read.
func s2ReadModule(t *testing.T) s2Module {
	s2Once.Do(func() {
		s2Read = s2Module{constants: map[string]string{}, locals: map[string]string{}, values: map[string]string{}, aliases: map[string]string{}}
		declaring := map[string]bool{}
		for _, name := range s2DeclaringPackages {
			declaring["internal/"+name] = true
		}
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
			info := &types.Info{
				Types:      map[ast.Expr]types.TypeAndValue{},
				Defs:       map[*ast.Ident]types.Object{},
				Uses:       map[*ast.Ident]types.Object{},
				Selections: map[*ast.SelectorExpr]*types.Selection{},
			}
			config := &types.Config{Importer: sourceImporter, Error: func(err error) { t.Errorf("%s: %v", directory.rel, err) }}
			pkg, _ := config.Check(modulePackagePath(directory.rel), fset, files, info)
			if declaring[directory.rel] && pkg != nil {
				for _, name := range pkg.Scope().Names() {
					if object, ok := pkg.Scope().Lookup(name).(*types.Const); ok && object.Val().Kind() == constant.String {
						s2Read.constants[pkg.Name()+"."+name] = reasonSite(fset, object.Pos())
						s2Read.values[pkg.Name()+"."+name] = constant.StringVal(object.Val())
					}
				}
				for _, file := range files {
					for _, declaration := range file.Decls {
						general, ok := declaration.(*ast.GenDecl)
						if !ok || general.Tok != token.CONST {
							continue
						}
						for _, spec := range general.Specs {
							value := spec.(*ast.ValueSpec)
							for i, name := range value.Names {
								if i >= len(value.Values) {
									continue
								}
								var named *ast.Ident
								switch initializer := unparenExpression(value.Values[i]).(type) {
								case *ast.Ident:
									named = initializer
								case *ast.SelectorExpr:
									named = initializer.Sel
								}
								if named == nil {
									continue
								}
								if target, ok := info.Uses[named].(*types.Const); ok && target.Pkg() != nil {
									s2Read.aliases[pkg.Name()+"."+name.Name] = target.Pkg().Name() + "." + target.Name()
								}
							}
						}
					}
				}
				for identifier, object := range info.Defs {
					if local, ok := object.(*types.Const); ok && local.Parent() != pkg.Scope() && local.Val().Kind() == constant.String {
						// keyed by its site too: two functions may each declare a
						// local constant of one name
						site := reasonSite(fset, identifier.Pos())
						key := pkg.Name() + "." + identifier.Name + "@" + site
						s2Read.locals[key] = site
						s2Read.values[key] = constant.StringVal(local.Val())
					}
				}
			}
			for _, file := range files {
				parents := reasonParents(file)
				// the literal expressions assigned to each local variable
				locals := map[*types.Var][]s2Expression{}
				ast.Inspect(file, func(node ast.Node) bool {
					expression, ok := node.(ast.Expr)
					if !ok || !s2StringValue(info, expression) || s2StringValue(info, s2ParentExpr(parents, expression)) {
						return true
					}
					if s2InConstDecl(parents, expression) || !s2HasLiteral(expression) {
						return true
					}
					found := s2Expression{
						value: constant.StringVal(info.Types[expression].Value),
						site:  reasonSite(fset, expression.Pos()),
						door:  reasonPlace(widenReasonExpression(expression, parents, info), parents, info),
					}
					s2Read.expressions = append(s2Read.expressions, found)
					if local := s2AssignedLocal(parents, info, expression); local != nil {
						locals[local] = append(locals[local], found)
					}
					return true
				})
				if len(locals) == 0 {
					continue
				}
				ast.Inspect(file, func(node ast.Node) bool {
					identifier, ok := node.(*ast.Ident)
					if !ok {
						return true
					}
					local, ok := info.Uses[identifier].(*types.Var)
					if !ok {
						return true
					}
					// every literal ever assigned to the variable is taken to
					// reach each of its uses (no order is followed), and a use
					// inside a concatenation, parentheses or a conversion
					// reaches the door the whole expression reaches
					var use ast.Expr = identifier
				climb:
					for {
						switch parent := parents[use].(type) {
						case *ast.BinaryExpr:
							use = parent
						case *ast.ParenExpr:
							use = parent
						case *ast.CallExpr:
							if len(parent.Args) != 1 || parent.Args[0] != use || !info.Types[parent.Fun].IsType() {
								break climb
							}
							use = parent
						default:
							break climb
						}
					}
					door := reasonPlace(widenReasonExpression(use, parents, info), parents, info)
					for _, assigned := range locals[local] {
						assigned.door = door
						s2Read.expressions = append(s2Read.expressions, assigned)
					}
					return true
				})
			}
		}
	})
	return s2Read
}

// s2AssignedLocal is the function-local variable an expression is assigned
// to (x := e, x = e, var x = e), or nil.
func s2AssignedLocal(parents map[ast.Node]ast.Node, info *types.Info, expression ast.Expr) *types.Var {
	var target *ast.Ident
	switch parent := parents[expression].(type) {
	case *ast.AssignStmt:
		for i, rhs := range parent.Rhs {
			if rhs == expression && i < len(parent.Lhs) && len(parent.Lhs) == len(parent.Rhs) {
				target, _ = unparenExpression(parent.Lhs[i]).(*ast.Ident)
			}
		}
	case *ast.ValueSpec:
		for i, value := range parent.Values {
			if value == expression && i < len(parent.Names) {
				target = parent.Names[i]
			}
		}
	}
	if target == nil {
		return nil
	}
	object := info.Defs[target]
	if object == nil {
		object = info.Uses[target]
	}
	local, ok := object.(*types.Var)
	if !ok || local.Parent() == nil || local.Parent() == local.Pkg().Scope() {
		return nil
	}
	return local
}

func s2StringValue(info *types.Info, expression ast.Expr) bool {
	if expression == nil {
		return false
	}
	value := info.Types[expression].Value
	return value != nil && value.Kind() == constant.String
}

// s2ParentExpr is the expression a node is part of, if its parent is one
// (nil otherwise).
func s2ParentExpr(parents map[ast.Node]ast.Node, node ast.Node) ast.Expr {
	parent, _ := parents[node].(ast.Expr)
	return parent
}

func s2InConstDecl(parents map[ast.Node]ast.Node, node ast.Node) bool {
	for current := parents[node]; current != nil; current = parents[current] {
		if declaration, ok := current.(*ast.GenDecl); ok {
			return declaration.Tok == token.CONST
		}
	}
	return false
}

func s2HasLiteral(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			found = true
		}
		return !found
	})
	return found
}

// s2LeadingWord is the first word of a Go name: "Pin" of PinMissing, "AMQ"
// of AMQBindingPresent, "Gone" of GoneProcessExited.
func s2LeadingWord(name string) string {
	runes := []rune(name)
	i := 1
	for i < len(runes) && unicode.IsUpper(runes[i]) {
		i++
	}
	if i > 1 {
		if i < len(runes) && unicode.IsLower(runes[i]) {
			return string(runes[:i-1])
		}
		return string(runes[:i])
	}
	for i < len(runes) && !unicode.IsUpper(runes[i]) {
		i++
	}
	return string(runes[:i])
}

// s2Families is the fixed families and the leading word of each listed
// constant's name.
func s2Families() map[string]bool {
	families := map[string]bool{}
	for _, word := range s2FixedFamilies {
		families[word] = true
	}
	for _, list := range []ReasonList{BlockReasons(), WaitReasons()} {
		for _, entry := range list.Entries {
			families[s2LeadingWord(entry.Constant[strings.Index(entry.Constant, ".")+1:])] = true
		}
	}
	return families
}

// s2InFamily: the name begins with a family word followed by an upper-case
// letter or a digit (a whole word, never a prefix of one).
func s2InFamily(name string, families map[string]bool) bool {
	for word := range families {
		if strings.HasPrefix(name, word) && len(name) > len(word) {
			next := []rune(name[len(word):])[0]
			if unicode.IsUpper(next) || unicode.IsDigit(next) {
				return true
			}
		}
	}
	return false
}

// TestEveryReasonConstantIsListed finds the string constants of a reason
// family in the declaring packages (by the type checker: any initializer
// form), and requires each to be on the block, wait or not-a-reason list, or
// on s2NotTaskReasons, and no entry of s2NotTaskReasons to be stale or listed.
// A reason family's constant declared inside a function fails, and so does
// any constant of a declaring package, of any name, whose value is a listed
// reason's code and that is not itself listed or named by a listed alias.
func TestEveryReasonConstantIsListed(t *testing.T) {
	read := s2ReadModule(t)
	listed := map[string]bool{}
	for _, list := range []ReasonList{BlockReasons(), WaitReasons()} {
		for _, entry := range list.Entries {
			listed[entry.Constant] = true
		}
	}
	for _, entry := range NotReasons() {
		listed[entry.Constant] = true
	}
	families := s2Families()
	found := map[string]string{}
	for name, site := range read.constants {
		if s2InFamily(name[strings.Index(name, ".")+1:], families) {
			found[name] = site
		}
	}
	if len(found) == 0 {
		t.Fatal("no constant of a reason family was found; the walk read nothing")
	}
	var names []string
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, excluded := s2NotTaskReasons[name]
		switch {
		case listed[name] && excluded:
			t.Errorf("%s is on a reason list and on s2NotTaskReasons", name)
		case !listed[name] && !excluded:
			t.Errorf("%s: %s is of a reason family and is on no reason list (block, wait, not-a-reason) and not on s2NotTaskReasons", found[name], name)
		}
	}
	for _, name := range sortedKeys(s2NotTaskReasons) {
		if _, ok := found[name]; !ok {
			t.Errorf("s2NotTaskReasons is stale: %s is no string constant of a reason family in the declaring packages", name)
		}
	}
	for _, list := range []ReasonList{BlockReasons(), WaitReasons()} {
		for _, entry := range list.Entries {
			if _, ok := found[entry.Constant]; !ok {
				t.Errorf("the listed %s is not found by the walk", entry.Constant)
			}
		}
	}
	// a constant declared inside a function is on no list: one of a reason
	// family is declared at package scope and listed instead
	for _, name := range sortedKeys(read.locals) {
		if s2InFamily(name[strings.Index(name, ".")+1:], families) {
			t.Errorf("%s: %s is a reason family's constant declared inside a function; declare it at package scope and list it", read.locals[name], name)
		}
	}
	// any constant of a declaring package, of any name, at package scope or
	// in a function, whose value is a listed reason's code is itself listed
	codes := map[string]string{}
	for _, list := range []ReasonList{BlockReasons(), WaitReasons()} {
		for _, entry := range list.Entries {
			codes[entry.Code] = entry.Constant
		}
	}
	for _, entry := range NotReasons() {
		codes[entry.Code] = entry.Constant
	}
	// the constant a listed alias names is that alias's code under its
	// first name (seatexec.ClaudeHooksMissing = claudeseat.HooksMissing)
	covered := map[string]bool{}
	for name := range listed {
		covered[name] = true
		for current := name; read.aliases[current] != ""; current = read.aliases[current] {
			covered[read.aliases[current]] = true
		}
	}
	for _, name := range sortedKeys(read.values) {
		if covered[name] {
			continue
		}
		if other := codes[read.values[name]]; other != "" {
			site := read.constants[name]
			if site == "" {
				site = read.locals[name]
			}
			t.Errorf("%s: %s has the value %q of the listed %s and is on no list", site, name, read.values[name], other)
		}
	}
	t.Logf("families: %s; string constants of a reason family: %d; not task reasons %d", strings.Join(sortedKeys(families), " "), len(found), len(s2NotTaskReasons))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestNoLiteralRepeatsAListedReason: no constant string expression with a
// literal in it (a literal, a parenthesized one, a concatenation, a
// conversion), outside a const declaration, reaches a declared block or wait
// door with the value of a listed reason, directly or through one local
// variable it is assigned to (every literal ever assigned to the variable is
// taken to reach each of its uses). Where a listed constant exists, the site names
// it. The spike programs under spike/ are records, not the product, and are
// not read (spike/pi-r19 repeats commit_failed and outside_declared_files at
// a commit result's ReasonCode). A literal that reaches a door through more
// than one assignment, or through a function's parameter, is not followed.
func TestNoLiteralRepeatsAListedReason(t *testing.T) {
	read := s2ReadModule(t)
	for _, list := range []struct {
		name string
		list ReasonList
	}{{"block", BlockReasons()}, {"wait", WaitReasons()}} {
		doors, codes := map[string]bool{}, map[string]string{}
		for _, door := range list.list.Doors {
			doors[door] = true
		}
		for _, entry := range list.list.Entries {
			codes[entry.Code] = entry.Constant
		}
		var problems []string
		for _, expression := range read.expressions {
			if doors[expression.door] && codes[expression.value] != "" && !strings.HasPrefix(expression.site, "spike/") {
				problems = append(problems, expression.site+": "+strconv.Quote(expression.value)+" at the "+list.name+" door "+expression.door+"; use "+codes[expression.value])
			}
		}
		sort.Strings(problems)
		if len(problems) > 0 {
			t.Errorf("%d literal expression(s) repeat a listed %s reason:\n%s", len(problems), list.name, strings.Join(problems, "\n"))
		}
	}
}
