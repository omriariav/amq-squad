//go:build unix

package seatlaunch

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// multilineCanary is AMQ stderr with a credential key on one line and its
// quoted value on the next, built at run time; it matches no real
// credential shape (briefs ruling 18).
func multilineCanary(t *testing.T, tag string) (stderr, value string) {
	t.Helper()
	value = "t17" + tag + "value" + "0093"
	stderr = "api" + "_key:\n  \"" + value + "\""
	for _, re := range redact.RealCredentialShapes {
		if re.MatchString(stderr) {
			t.Fatalf("the canary matches a real credential shape: %s", re)
		}
	}
	return stderr, value
}

// invalidJSONAt answers the named amq launch step ("prepare" or "apply")
// with output that is not JSON and the canary on stderr; any other call
// goes to the fake.
func invalidJSONAt(f *fakeAMQ, step, stderr string) func(context.Context, string, []string, []byte, []string, string) ([]byte, []byte, int, error) {
	return func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
		if slices.Contains(args, "--"+step) {
			f.calls = append(f.calls, args)
			return []byte("amq: not json"), []byte(stderr), 2, nil
		}
		return f.run(ctx, amq, args, stdin, env, dir)
	}
}

// Codex finding 1 on t17, the per-seat paths: AMQ output that is not JSON
// made the launch prefix its error message to AMQ's stderr before the
// redaction, which then missed a key whose quoted value is on the next
// line. Through prepare and through apply, the value is in neither the
// seat_launch_failed decision nor the state file (status reads it there).
// Reverted (the stderr wrapped raw), both carry it.
func TestInvalidJSONStderrIsRedactedBeforeItIsWrapped(t *testing.T) {
	for _, step := range []string{"prepare", "apply"} {
		t.Run(step, func(t *testing.T) {
			stderr, value := multilineCanary(t, step)
			f := newLauncherFixture(t, "applied")
			f.publish = false
			f.l.Options.RunAMQ = invalidJSONAt(f.fake, step, stderr)
			store, err := task.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(t.TempDir(), "scheduler.json")
			s := &scheduler.Scheduler{Store: store, Now: time.Now, Launcher: f.l, Incarnation: "t", Config: scheduler.Config{MaxSeats: 1, StatePath: statePath}}
			f.l.Post = s.Post
			if _, err := store.Add(task.AddInput{Title: "a", Class: "script", Pool: &poolA}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Tick(); err != nil {
				t.Fatal(err)
			}
			f.l.Wait()
			ds, err := s.Tick()
			if err != nil {
				t.Fatal(err)
			}
			got := kinds(ds, scheduler.KindSeatLaunchFailed)
			if len(got) != 1 || !strings.Contains(got[0].Reason, "without a") || !strings.Contains(got[0].Reason, redact.Placeholder) {
				t.Fatalf("decisions %+v", ds)
			}
			if strings.Contains(got[0].Reason, value) {
				t.Fatalf("the decision carries the credential value: %s", got[0].Reason)
			}
			raw, err := os.ReadFile(statePath)
			if err != nil || !strings.Contains(string(raw), "launch_failures") || strings.Contains(string(raw), value) {
				t.Fatalf("the state file (%v):\n%s", err, raw)
			}
		})
	}
}

// Codex finding 1, the roster path: an Apply whose output is not JSON
// ends the roster launch; neither roster_launch_failed, nor each seat's
// seat_launch_failed, nor the state file carries the value. Reverted, all
// three do.
func TestInvalidJSONRosterFailureIsRedacted(t *testing.T) {
	stderr, value := multilineCanary(t, "roster")
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}, false)
	f.l.Options.RunAMQ = invalidJSONAt(f.fake, "apply", stderr)
	statePath := filepath.Join(t.TempDir(), "scheduler.json")
	f.s.Config.StatePath = statePath
	ds, err := f.s.Tick()
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) || strings.Contains(err.Error(), value) {
		t.Fatalf("tick error %v", err)
	}
	rl := kinds(ds, "roster_launch_failed")
	sl := kinds(ds, scheduler.KindSeatLaunchFailed)
	if len(rl) != 1 || len(sl) != 2 {
		t.Fatalf("decisions %+v", ds)
	}
	for _, d := range append(rl, sl...) {
		if strings.Contains(d.Reason, value) || !strings.Contains(d.Reason, redact.Placeholder) {
			t.Fatalf("the decision %+v", d)
		}
	}
	raw, err := os.ReadFile(statePath)
	if err != nil || strings.Contains(string(raw), value) {
		t.Fatalf("the state file (%v):\n%s", err, raw)
	}
}

// The reviewer's chokepoint, the Apply's FailureDetail: an Apply AMQ
// refuses with valid JSON and the canary on stderr (the key on stderr's
// first line). The value is in none of roster_launch_failed, either
// seat_launch_failed, the state file (status's only source of the detail),
// the staged records' apply (recordApply), the Result's two FailureDetails
// or the operator's report. Reverted (any carrier around amqDetail), the
// value reaches it.
func TestApplyFailureDetailGoesThroughTheChokepoint(t *testing.T) {
	stderr, value := multilineCanary(t, "applydetail")
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", Disposition: "not_applied",
		ReasonCode: scheduler.AMQBindingPresent}, false)
	f.fake.applyErr = stderr
	statePath := filepath.Join(t.TempDir(), "scheduler.json")
	f.s.Config.StatePath = statePath
	ds, err := f.s.Tick()
	if err == nil {
		t.Fatal("the refused apply did not fail the roster launch")
	}
	for _, d := range append(kinds(ds, "roster_launch_failed"), kinds(ds, scheduler.KindSeatLaunchFailed)...) {
		if strings.Contains(d.Reason, value) {
			t.Fatalf("the decision %+v", d)
		}
	}
	files := []string{statePath}
	for _, h := range []string{"cto", "dev"} {
		files = append(files, filepath.Join(launch.StagedDir(f.q.SessionRoot(), h), launch.FileName))
	}
	for _, p := range files {
		raw, err := os.ReadFile(p)
		if err != nil || strings.Contains(string(raw), value) {
			t.Fatalf("%s (%v):\n%s", p, err, raw)
		}
	}

	g := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", Disposition: "not_applied", ReasonCode: scheduler.AMQBindingPresent}, applyErr: stderr}
	res, err := Launch(context.Background(), testSquad(t), launchOpts(t, g, map[string]string{"sha256:a1": "trust_exact_subject"}))
	if err != nil || res.Stage != "apply_failed" {
		t.Fatalf("launch %s %v", res.Stage, err)
	}
	if strings.Contains(res.FailureDetail, value) || strings.Contains(res.Apply.FailureDetail, value) || res.FailureDetail == "" {
		t.Fatalf("the result %q / %q", res.FailureDetail, res.Apply.FailureDetail)
	}
}

// The chokepoint redacts AMQ's multi-line text and is idempotent (the
// operator report it fed was deleted with the launch verb, brief 31-1).
func TestAMQDetailRedactsAndIsIdempotent(t *testing.T) {
	stderr, value := multilineCanary(t, "report")
	if once := amqDetail(stderr); strings.Contains(once, value) || !strings.Contains(once, redact.Placeholder) {
		t.Fatalf("the chokepoint: %q", once)
	}
	if once := amqDetail(stderr); amqDetail(once) != once {
		t.Fatalf("the chokepoint is not idempotent: %q then %q", once, amqDetail(once))
	}
}

// The chokepoint holds (the reviewer's row): over the package's non-test
// Go files, RunAMQ's stderr is used only as amqDetail's argument (directly
// or through string(...)), every read of a FailureDetail is amqDetail's
// argument, and every write of one (an assignment or a composite literal
// field) is an amqDetail call. A new caller that carries AMQ's output
// around the chokepoint fails here, naming the file and line.
func TestAMQOutputHasOneChokepoint(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	isChoke := func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "amqDetail"
	}
	isConv := func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "string"
	}
	// throughChoke: the node at stack's top is amqDetail's argument, or the
	// argument of string(...) that is.
	throughChoke := func(stack []ast.Node) bool {
		n := len(stack)
		if n >= 2 && isChoke(stack[n-2]) {
			return true
		}
		return n >= 3 && isConv(stack[n-2]) && isChoke(stack[n-3])
	}
	var bad []string
	where := func(n ast.Node, what string) { bad = append(bad, fmt.Sprintf("%s: %s", fset.Position(n.Pos()), what)) }
	sites := 0
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				stderrVars := map[string]bool{}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					as, ok := n.(*ast.AssignStmt)
					if !ok || len(as.Rhs) != 1 || len(as.Lhs) < 2 {
						return true
					}
					if c, ok := as.Rhs[0].(*ast.CallExpr); ok {
						if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "RunAMQ" {
							sites++
							if id, ok := as.Lhs[1].(*ast.Ident); ok && id.Name != "_" {
								stderrVars[id.Name] = true
							}
						}
					}
					return true
				})
				var stack []ast.Node
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if n == nil {
						stack = stack[:len(stack)-1]
						return true
					}
					stack = append(stack, n)
					switch x := n.(type) {
					case *ast.AssignStmt:
						for i, l := range x.Lhs {
							if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "FailureDetail" && (len(x.Rhs) != len(x.Lhs) || !isChoke(x.Rhs[i])) {
								where(x, "a FailureDetail written around amqDetail")
							}
						}
					case *ast.KeyValueExpr:
						if k, ok := x.Key.(*ast.Ident); ok && k.Name == "FailureDetail" && !isChoke(x.Value) {
							where(x, "a FailureDetail field written around amqDetail")
						}
					case *ast.SelectorExpr:
						if x.Sel.Name != "FailureDetail" {
							break
						}
						if p, ok := stack[len(stack)-2].(*ast.AssignStmt); ok {
							for _, l := range p.Lhs {
								if l == x {
									return true // a write, checked above
								}
							}
						}
						if !throughChoke(stack) {
							where(x, "a FailureDetail read around amqDetail")
						}
					case *ast.Ident:
						if !stderrVars[x.Name] {
							break
						}
						if p, ok := stack[len(stack)-2].(*ast.AssignStmt); ok {
							for _, l := range p.Lhs {
								if l == x {
									return true // the RunAMQ assignment itself
								}
							}
						}
						if !throughChoke(stack) {
							where(x, "RunAMQ's stderr used around amqDetail")
						}
					}
					return true
				})
			}
		}
	}
	if sites != 2 {
		t.Fatalf("RunAMQ is called at %d sites, want 2 (prepareCLI, applyCLI); a new site must bind its stderr and pass it to amqDetail", sites)
	}
	if len(bad) > 0 {
		t.Fatalf("AMQ's output bypasses the chokepoint:\n%s", strings.Join(bad, "\n"))
	}
}
