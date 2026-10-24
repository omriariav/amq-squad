package redact

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the module root: the nearest parent with go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = up
	}
}

// skipDir: generated or foreign trees the scans never read.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "build", "vendor", ".claude", ".agent-mail", ".amq-squad":
		return true
	}
	return false
}

// evidenceDir reports whether a repo-relative directory path is docs/ or an
// evidence directory (a component named evidence or ending in -evidence).
func evidencePath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 0 && parts[0] == "docs" {
		return true
	}
	for _, p := range parts[:max(len(parts)-1, 0)] {
		if p == "evidence" || strings.HasSuffix(p, "-evidence") || strings.HasSuffix(p, "_evidence") {
			return true
		}
	}
	return false
}

// scanTree reports every credential-shaped value under the docs and
// evidence paths of root, as "path:line rule name" (never the value).
func scanTree(root string) ([]string, int, error) {
	var hits []string
	files := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if !d.Type().IsRegular() || !evidencePath(rel) {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.IndexByte(string(raw), 0) >= 0 {
			return nil
		}
		files++
		for _, f := range Find(string(raw)) {
			hits = append(hits, fmt.Sprintf("%s:%d %s %s", filepath.ToSlash(rel), f.Line, f.Rule, f.Name))
		}
		return nil
	})
	return hits, files, err
}

// Briefs ruling 18, item 1: no credential-shaped value anywhere under docs/
// or an evidence directory, by the hook ledger's secret names plus
// NAME=value for credential-like names and the provider prefixes. It runs
// in the normal go test ./..., so every gate checks it. A failure names the
// file, the line and the rule, never the value.
func TestNoCredentialShapedValueUnderDocsOrEvidence(t *testing.T) {
	hits, files, err := scanTree(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if files < 50 {
		t.Fatalf("only %d files scanned under docs/ and the evidence directories", files)
	}
	for _, h := range hits {
		t.Errorf("credential-shaped value: %s", h)
	}
}

// The scan finds what it must, in the shapes the 2b-2 B exposure had (an
// environment slice printed into a revert log) and the provider prefixes;
// it passes placeholders, references, paths and counts.
func TestFindRules(t *testing.T) {
	fake := "AIza" + strings.Repeat("Q7", 16) // built here so this file carries no literal
	must := []string{
		"[GEMINI_API_KEY=" + fake + " HOME=/x]",
		"CLAUDE_CODE_MESSAGING_TOKEN=abcd1234efgh5678",
		`{"api_key": "abcdefghijkl0123"}`,
		"export DB_PASSWORD='hunter22hunter'",
		"DB_PASSWORD=12345678", // no numeric exemption under a credential name (second pass)
		"token " + "ghp_" + strings.Repeat("a1", 12),
		"sk-" + "ant-oat01-" + strings.Repeat("z9", 6),
		"NOSTR_NSEC=nsec1" + strings.Repeat("q", 40),
		"--api-key=abcdefgh12345678",
	}
	for _, s := range must {
		if len(Find(s)) == 0 {
			t.Errorf("not found: %d-byte line %q...", len(s), s[:min(len(s), 12)])
		}
		if out := Line(s); len(Find(out)) != 0 || !strings.Contains(out, Placeholder) {
			t.Errorf("Line left a finding in %q...", s[:min(len(s), 12)])
		}
	}
	pass := []string{
		"GH_TOKEN=$(gh auth token)", "TOKEN=<redacted>", `"token": "[redacted]"`, "CLAUDE_TOKEN_FILE=/home/u/.amq-squad/claude-token",
		"input_tokens=123456789", "API_KEY=xxxxxxxxxxxx", "SECRET=${SECRET}", "SSH_AUTH_SOCK=/tmp/ssh-x/agent.1", "task-sk-abcdefghijk",
		"MONKEY=short", "KEY=...", `"max_tokens": 4096`,
	}
	for _, s := range pass {
		if f := Find(s); len(f) != 0 {
			t.Errorf("false positive on %q: %+v", s, f)
		}
	}
}

// Briefs ruling 18, item 2: an evidence file passes through the redaction
// before it is committed; Tree rewrites it in place, a second pass changes
// nothing, and the result passes the scan.
func TestTreeRedactsEvidenceInPlace(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c3-revert-check.txt")
	body := "--- FAIL: TestX\n    env [PATH=/bin GEMINI_API_KEY=AIza" + strings.Repeat("Z", 30) + " LANG=C]\nok\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Tree(dir)
	if err != nil || got[p] != 1 {
		t.Fatalf("tree: %v %v", got, err)
	}
	raw, _ := os.ReadFile(p)
	if len(Find(string(raw))) != 0 || !strings.Contains(string(raw), "GEMINI_API_KEY="+Placeholder) || !strings.Contains(string(raw), "PATH=/bin") {
		t.Fatal("the evidence file was not redacted to the placeholder, or lost a plain entry")
	}
	if again, _ := Tree(dir); len(again) != 0 {
		t.Fatalf("a second pass changed %v", again)
	}
}

// StripEnv drops credential names and keeps the rest and the kept names.
func TestStripEnv(t *testing.T) {
	got := StripEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=x", "CLAUDE_CODE_OAUTH_TOKEN=y", "GH_TOKEN=z", "AM_ME=s1", "SSH_AUTH_SOCK=/s"}, "GH_TOKEN")
	if strings.Join(got, " ") != "PATH=/bin GH_TOKEN=z AM_ME=s1 SSH_AUTH_SOCK=/s" {
		t.Fatalf("strip kept the wrong names (%d entries)", len(got))
	}
}

// failureMethods are the testing.T/B methods whose arguments reach the test
// output.
var failureMethods = map[string]bool{"Fatal": true, "Fatalf": true, "Error": true, "Errorf": true, "Log": true, "Logf": true, "Skip": true, "Skipf": true}

// envFunc matches a function whose name says it builds an environment
// (NoUpdateCheckEnv, harnessEnv, SeatEnv, BuildEnvironment, scrubAMQ,
// StripEnv), not an envelope or a single variable lookup (Getenv).
var envFunc = regexp.MustCompile(`(?i)((^|[a-z])env|environ|environment)$|^(scrub|strip|drop|filter|merge)`)

// envName matches an identifier that names an environment.
// A constant such as contextSourceEnv is not one.
var envName = regexp.MustCompile(`^(env|environ|environment|[a-z]+Env|[A-Z]+_ENV)$`)

// envFinding is one test that formats an environment into its output.
type envFinding struct {
	Pos  token.Position
	Func string
	Expr string // the formatted argument's source
}

// envScope is one function's knowledge of its variables: assigned holds
// every name assigned in it, env the ones whose value is an environment.
type envScope struct{ assigned, env map[string]bool }

// envValue reports whether an expression's value is an environment, or an
// entry of one: os.Environ(); a field named Env or Environ (an exec.Cmd's,
// a launch request's); append onto an environment; a call of an
// env-building function on nil (the inherit convention) or on an
// environment; a slice or an index of one; a variable assigned one; and an
// env-named variable this function never assigned (a parameter, a package
// variable). len() of anything is a count.
func (sc envScope) envValue(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return sc.envValue(x.X)
	case *ast.StarExpr:
		return sc.envValue(x.X)
	case *ast.UnaryExpr:
		return sc.envValue(x.X)
	case *ast.Ident:
		if sc.assigned[x.Name] {
			return sc.env[x.Name]
		}
		return envName.MatchString(x.Name)
	case *ast.SelectorExpr:
		return x.Sel.Name == "Env" || x.Sel.Name == "Environ"
	case *ast.IndexExpr:
		return sc.envValue(x.X)
	case *ast.SliceExpr:
		return sc.envValue(x.X)
	case *ast.CallExpr:
		name := ""
		switch f := x.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			if pkg, ok := f.X.(*ast.Ident); ok && pkg.Name == "os" && f.Sel.Name == "Environ" {
				return true
			}
			name = f.Sel.Name
		}
		switch {
		case name == "len" || name == "cap":
			return false
		case name == "append" || name == "Join" || name == "Sprint" || name == "Sprintf" || name == "Sprintln":
			for _, a := range x.Args {
				if sc.envValue(a) {
					return true
				}
			}
		case envFunc.MatchString(name):
			for _, a := range x.Args {
				if id, ok := a.(*ast.Ident); ok && id.Name == "nil" {
					return true
				}
				if sc.envValue(a) {
					return true
				}
			}
		}
	}
	return false
}

// scanEnvFormatting parses one test file and reports every failure-method
// call that formats an environment.
func scanEnvFormatting(fset *token.FileSet, path string) ([]envFinding, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []envFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		sc := envScope{assigned: map[string]bool{}, env: map[string]bool{}}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || id.Name == "_" {
						continue
					}
					if len(x.Rhs) != len(x.Lhs) {
						sc.assigned[id.Name], sc.env[id.Name] = true, false // a multi-value call: no environment
						continue
					}
					sc.env[id.Name] = sc.envValue(x.Rhs[i])
					sc.assigned[id.Name] = true
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || !failureMethods[sel.Sel.Name] {
					return true
				}
				for _, arg := range x.Args {
					if _, lit := arg.(*ast.BasicLit); lit {
						continue
					}
					if sc.envValue(arg) {
						var b strings.Builder
						printer.Fprint(&b, fset, arg)
						out = append(out, envFinding{Pos: fset.Position(x.Pos()), Func: fn.Name.Name, Expr: b.String()})
						break
					}
				}
			}
			return true
		})
	}
	return out, nil
}

// readAllowlist reads testdata/env-format-allowlist.txt: "path FuncName"
// lines (repo-relative, slash form), each with a reason after a #.
func readAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "env-format-allowlist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if fields := strings.Fields(line); len(fields) == 2 {
			out[fields[0]+" "+fields[1]] = true
		}
	}
	return out
}

// Briefs ruling 18, item 3: no test in the repository formats os.Environ
// or an environment slice into a failure message (the 2b-2 B exposure: a
// test printed the whole inherited environment into a committed revert
// log). A legitimate case goes into testdata/env-format-allowlist.txt with
// its reason.
func TestNoTestFormatsTheEnvironment(t *testing.T) {
	root := repoRoot(t)
	allow := readAllowlist(t)
	fset := token.NewFileSet()
	files := 0
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (skipDir(d.Name()) || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		files++
		found, err := scanEnvFormatting(fset, p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, f := range found {
			key := rel + " " + f.Func
			seen[key] = true
			if !allow[key] {
				t.Errorf("%s:%d in %s formats an environment (%s) into the test output; name only the failing variable", rel, f.Pos.Line, f.Func, f.Expr)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("only %d test files scanned", files)
	}
	for key := range allow {
		if !seen[key] {
			t.Errorf("stale allowlist entry %q: no such formatting left", key)
		}
	}
}

// The scan catches the shapes of the exposure: os.Environ() in a Fatalf,
// an env slice of unknown origin (a parameter), cmd.Env, a variable
// assigned from an env-building call on nil (the amqexec shape, got :=
// NoUpdateCheckEnv(nil)), and an entry or a join of an environment. A test
// literal, a count, an envelope and a single Getenv are not environments.
func TestEnvScanRules(t *testing.T) {
	src := `package x
import ("os"; "testing")
func TestA(t *testing.T) { t.Fatalf("env %v", os.Environ()) }
func TestB(t *testing.T, env []string) { t.Errorf("%q", env) }
func TestC(t *testing.T, cmd struct{ Env []string }) { t.Fatal(cmd.Env) }
func TestD(t *testing.T) { got := NoUpdateCheckEnv(nil); t.Fatalf("%#v", got) }
func TestE(t *testing.T) { e := append(os.Environ(), "X=1"); t.Log(strings.Join(e[1:], " ")) }
func TestF(t *testing.T) { childEnv := harnessEnv(os.Environ()); t.Fatal(childEnv[0]) }
func TestOK(t *testing.T) { env := []string{"A=1"}; t.Fatalf("%d entries %v", len(os.Environ()), env); e := NewAuthorizationEnvelope(); t.Fatal(e, os.Getenv("HOME")) }
`
	dir := t.TempDir()
	p := filepath.Join(dir, "x_test.go")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := scanEnvFormatting(token.NewFileSet(), p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range found {
		names = append(names, f.Func)
	}
	if strings.Join(names, ",") != "TestA,TestB,TestC,TestD,TestE,TestF" {
		t.Fatalf("found %v", names)
	}
}
