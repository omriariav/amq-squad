package routing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// refused is an attempt the commit step refused with code.
func refused(pool string, n int, code string) task.Attempt {
	return task.Attempt{N: n, Holder: "s", Pool: pl(pool), GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeCommitRefused, Detail: code}
}

// Brief 3.1.1-2 item 1, row 1: an attempt the commit step refused
// size_cap_exceeded (t27 on 2026-11-01) is not a failed attempt: the retry
// stays on the same tier and may take the same entry, and refusals past
// MaxAttempts never exhaust the task. Each policy refusal, binding refusal
// and data guard does the same; an unclassified commit code too.
func TestPolicyRefusalIsNotAFailedAttempt(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	first := f.decide(newTask("t27", "implement", "a.go"))
	if first.Entry == nil {
		t.Fatalf("first attempt: %+v", first)
	}
	codes := []string{seatcommit.ReasonSizeCap, seatcommit.ReasonOutside, seatcommit.ReasonProtected, seatcommit.ReasonGrowthGate,
		seatcommit.ReasonPRPending, seatcommit.ReasonPRBoundElsewhere, seatcommit.ReasonWorktree, seatcommit.ReasonBaseUnrecorded, "a_code_no_table_names"}
	for _, code := range codes {
		tk := newTask("t27", "implement", "a.go")
		for n := 1; n <= DefaultMaxAttempts+1; n++ {
			tk.Attempt, tk.Attempts = n, append(tk.Attempts, refused(first.Entry.Key(), n, code))
		}
		d, route := f.router().Decide(tk, nil)
		if route.Pool == nil || d.UsedTier != first.UsedTier || d.Entry.Key() != first.Entry.Key() || hasSkip(d, first.Entry.Key(), SkipUsedBefore) {
			t.Fatalf("%s: after %d refusals the retry is %+v (%+v), want tier %s entry %s", code, len(tk.Attempts), d, route, first.UsedTier, first.Entry.Key())
		}
	}
}

// Row 2: a work failure still escalates: a verdict_failed refusal, a
// verifier timeout and a failed attempt each move the retry one tier up,
// never onto the failed entry, and three of them exhaust the task.
func TestWorkFailureStillEscalates(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	first := f.decide(newTask("t1", "implement", "a.go"))
	for _, a := range []task.Attempt{refused(first.Entry.Key(), 1, task.BlockVerdictFailed), refused(first.Entry.Key(), 1, seatcommit.ReasonVerifierTimeout), failed(first.Entry.Key(), 1)} {
		tk := newTask("t1", "implement", "a.go")
		tk.Attempt, tk.Attempts = 1, []task.Attempt{a}
		d := f.decide(tk)
		if d.UsedTier != up(first.UsedTier) || d.Entry == nil || d.Entry.Key() == first.Entry.Key() || !hasSkip(d, first.Entry.Key(), SkipUsedBefore) {
			t.Fatalf("%s %s: retry %+v, want tier %s", a.Outcome, a.Detail, d, up(first.UsedTier))
		}
	}
	tk := newTask("t2", "extract")
	tk.Attempt = 3
	tk.Attempts = []task.Attempt{refused("claude:haiku:low", 1, task.BlockVerdictFailed), refused("claude:sonnet:medium", 2, task.BlockVerdictFailed), refused("claude:opus:high", 3, task.BlockVerdictFailed)}
	if _, route := f.router().Decide(tk, nil); route.Block != "retries_exhausted" {
		t.Fatalf("three verdict_failed refusals: %+v", route)
	}
}

// Row 3: every reason code seatcommit can emit is classified in the one
// table. The emitters are read from seatcommit's source: every Reason*
// constant, and every expression handed to Result.refuse, assigned to a
// ReasonCode, or set as a Violation's Reason. A constant of another package
// or a variable must be one this row knows, so a new emitter fails here
// until it is classified; a code missing from the table fails here. The
// scheduler's own commit refusals are classified too.
func TestEveryCommitRefusalCodeIsClassified(t *testing.T) {
	codes := seatcommitCodes(t)
	codes = append(codes, scheduler.ReasonCommitFailed, scheduler.ReasonVerifierInterrupted, task.RefusalPRBoundElsewhere)
	if missing := unclassified(RefusalWorkFailure, codes); len(missing) > 0 {
		t.Fatalf("commit refusal codes no row of RefusalWorkFailure classifies: %v", missing)
	}
	for _, c := range []string{seatcommit.ReasonSizeCap, seatcommit.ReasonOutside, seatcommit.ReasonProtected, seatcommit.ReasonGrowthGate,
		seatcommit.ReasonPRPending, seatcommit.ReasonPRBoundElsewhere, seatcommit.ReasonWorktree, seatcommit.ReasonRecordedDiffMismatch,
		seatcommit.ReasonLinkTarget, seatcommit.ReasonWidenSnapshotMissing, seatcommit.ReasonWidenSnapshotMismatch} {
		if RefusalWorkFailure[c] {
			t.Errorf("%s is classified a work failure", c)
		}
	}
	// the check itself: a table without one code names it
	short := maps.Clone(RefusalWorkFailure)
	delete(short, seatcommit.ReasonSizeCap)
	if got := unclassified(short, codes); !slices.Equal(got, []string{seatcommit.ReasonSizeCap}) {
		t.Fatalf("a table without size_cap_exceeded: unclassified %v", got)
	}
}

func unclassified(table map[string]bool, codes []string) []string {
	var out []string
	for _, c := range codes {
		if _, ok := table[c]; !ok && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// seatcommitCodes reads every reason code seatcommit's non-test source can
// emit; an emitter it cannot resolve fails the test.
func seatcommitCodes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "seatcommit", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("seatcommit source: %v", err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	consts := map[string]string{}
	aliases := map[string]string{} // Reason* constants defined as another package's constant
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i < len(vs.Values) && strings.HasPrefix(n.Name, "Reason") {
						switch v := vs.Values[i].(type) {
						case *ast.BasicLit:
							if v.Kind == token.STRING {
								consts[n.Name], _ = strconv.Unquote(v.Value)
							}
						case *ast.SelectorExpr:
							if id, ok := v.X.(*ast.Ident); ok {
								aliases[n.Name] = id.Name + "." + v.Sel.Name
							}
						}
					}
				}
			}
		}
	}
	// Constants of other packages seatcommit refuses with, and the
	// variables it passes on: each with every value it can carry.
	foreign := map[string][]string{
		"task.BlockVerdictFailed":       {task.BlockVerdictFailed},
		"task.ReasonGrowthGateRequired": {task.ReasonGrowthGateRequired},
		"task.BlockVerdictTreeMismatch": {task.BlockVerdictTreeMismatch},
		"registry.PinMismatch":          {registry.PinMismatch},
		// planVerifier's code: the grant's, or registry.OpenPinned's
		"code": {seatcommit.ReasonGrants, registry.PinMissing, registry.PinMismatch},
		// the first violation's reason: a Violation literal's, scanned below
		"v.Reason": nil,
		// a widening's copy of its dry run's code, scanned as the step's
		"res.ReasonCode": nil,
	}
	for name, sel := range aliases {
		vs, ok := foreign[sel]
		if !ok || len(vs) != 1 {
			t.Errorf("seatcommit.%s = %s: a constant of another package this row does not know (classify it and name it here)", name, sel)
			continue
		}
		consts[name] = vs[0]
	}
	var codes []string
	for _, v := range consts {
		codes = append(codes, v)
	}
	var resolve func(e ast.Expr)
	resolve = func(e ast.Expr) {
		pos := fset.Position(e.Pos())
		switch x := e.(type) {
		case *ast.BasicLit:
			s, _ := strconv.Unquote(x.Value)
			codes = append(codes, s)
			return
		case *ast.Ident:
			if v, ok := consts[x.Name]; ok {
				codes = append(codes, v)
				return
			}
			if vs, ok := foreign[x.Name]; ok {
				codes = append(codes, vs...)
				return
			}
		case *ast.CallExpr:
			if fn, ok := x.Fun.(*ast.Ident); ok && fn.Name == "firstNonEmpty" {
				for _, a := range x.Args {
					resolve(a)
				}
				return
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if vs, ok := foreign[id.Name+"."+x.Sel.Name]; ok {
					codes = append(codes, vs...)
					return
				}
			}
		}
		t.Errorf("%s: a reason code this row cannot resolve (classify it in RefusalWorkFailure and name it here)", pos)
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "refuse" && len(x.Args) > 0 {
					resolve(x.Args[0])
				}
			case *ast.AssignStmt:
				for i, l := range x.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReasonCode" && i < len(x.Rhs) {
						resolve(x.Rhs[i])
					}
				}
			case *ast.CompositeLit:
				typ, _ := x.Type.(*ast.Ident)
				for _, el := range x.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					k, _ := kv.Key.(*ast.Ident)
					if k != nil && (k.Name == "ReasonCode" || (k.Name == "Reason" && typ != nil && typ.Name == "Violation")) {
						resolve(kv.Value)
					}
				}
			}
			return true
		})
	}
	if len(consts) < 10 {
		t.Fatalf("only %d Reason constants read from seatcommit: the scan is broken", len(consts))
	}
	return codes
}

type rosterLauncher struct {
	recordingLauncher
	s *scheduler.Scheduler
}

func (l *rosterLauncher) LaunchRoster(reqs []scheduler.SeatRequest) scheduler.RosterOutcome {
	out := scheduler.RosterOutcome{Stage: scheduler.RosterApplied, LaunchID: "L1"}
	for _, r := range reqs {
		l.s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: l.s.Now(), Handle: r.Handle, Pool: r.Pool, RequestID: r.ID})
		out.Seats = append(out.Seats, scheduler.RosterSeatState{Handle: r.Handle, Up: true})
	}
	return out
}

type capCommitter struct{}

func (capCommitter) Commit(string, task.Task) scheduler.CommitResult {
	return scheduler.CommitResult{ReasonCode: seatcommit.ReasonSizeCap, Detail: "over the size cap"}
}

// Row 1 end to end, t27's day: the roster is one seat on the task tier's
// entry, the plan tier is an entry no roster seat serves. The first
// attempt's change is refused size_cap_exceeded; nothing dispatches until
// the lead's reset, and then attempt 2 goes to the same seat on the same
// tier. Reverted (every commit refusal a failed attempt), attempt 2 moved to
// claude:opus:high and waited for a seat no roster serves.
func TestSizeCapRefusalRetriesOnTheSameSeat(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	clock := func() time.Time { return now }
	terra := task.Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\ntiers:\n  task:\n    chain: [\"codex:gpt-5.6-terra:medium\"]\n  plan:\n    chain: [\"claude:opus:high\"]\n")
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: clock,
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }, RosterPools: []task.Pool{terra}})
	if err != nil {
		t.Fatal(err)
	}
	l := &rosterLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: clock, Launcher: l, Router: r, Committer: capCommitter{},
		Config: scheduler.Config{MaxSeats: 2, IdleTimeout: time.Hour, Roster: []scheduler.RosterSeat{{Handle: "dev", Pool: terra}}}})
	if err != nil {
		t.Fatal(err)
	}
	l.s = s
	tick := func() []scheduler.Decision {
		t.Helper()
		ds, err := s.Tick()
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Second)
		return ds
	}
	tick()
	if _, err := store.Add(task.AddInput{Title: "t27", Class: "implement", DeclaredFiles: []string{"cmd/main.go"}}, now); err != nil {
		t.Fatal(err)
	}
	dispatched := func(ds []scheduler.Decision) int {
		n := 0
		for _, d := range ds {
			if d.Kind == "dispatched" && d.TaskID == "t1" && d.Seat == "dev" && d.Pool == terra.Key() {
				n++
			}
		}
		return n
	}
	if n := dispatched(tick()); n != 1 {
		t.Fatal("attempt 1 was not dispatched to dev")
	}
	if _, err := store.Claim("t1", "dev", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Done("t1", "dev", "ok", "", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if n := dispatched(tick()); n != 0 {
			t.Fatalf("tick %d dispatched the refused task", i)
		}
	}
	if got, _ := store.Get("t1"); got.State != task.StateBlocked || got.BlockReason != seatcommit.ReasonSizeCap {
		t.Fatalf("t1 %+v", got)
	}
	if _, err := store.Reset("t1", nil, now); err != nil {
		t.Fatal(err)
	}
	if n := dispatched(tick()); n != 1 {
		got, _ := store.Get("t1")
		t.Fatalf("attempt 2 after the reset is not on dev: %+v", got)
	}
	if got, _ := store.Get("t1"); got.Attempt != 2 || got.Lease == nil || *got.Pool != terra {
		t.Fatalf("attempt 2 %+v", got)
	}
}

// The plan reviewer's addition 3: a seat's typed widen request (#844) is a
// block that asks the lead for a decision, not a failure of the work: after
// the lead's widen the task retries on the same tier and entry. Reverted
// (every block a failed attempt), the retry moved a tier up, t27's bug in
// another form.
func TestWidenRequestedBlockIsNotAFailedAttempt(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	first := f.decide(newTask("t28", "implement", "a.go"))
	tk := newTask("t28", "implement", "a.go")
	tk.Attempt = 1
	tk.Attempts = []task.Attempt{{N: 1, Holder: "s", Pool: first.Entry, GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeBlocked, Detail: task.ReasonWidenRequested}}
	d := f.decide(tk)
	if d.UsedTier != first.UsedTier || d.Entry == nil || d.Entry.Key() != first.Entry.Key() {
		t.Fatalf("after a widen_requested block: %+v, want tier %s entry %s", d, first.UsedTier, first.Entry.Key())
	}
	if RefusalWorkFailure[task.ReasonWidenRequested] {
		t.Fatal("widen_requested is classified a work failure")
	}
	// a seat's own block with any other reason still counts
	tk.Attempts[0].Detail = "tests_red"
	if d := f.decide(tk); d.UsedTier != up(first.UsedTier) {
		t.Fatalf("a seat's own block: %+v", d)
	}
}
