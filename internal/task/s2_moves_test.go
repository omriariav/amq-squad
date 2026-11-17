package task

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// S2-1 (3.3.0): every move of the declared task machine (Moves) is driven
// through the real store, once per (row, Via method, Who), and checked
// against its row: the state before and after, the five block-group fields
// to exact expected values, and the returned record equal to the record
// re-read from disk. Each (row, method) also has a refusal row: the method on
// a task in a state the table lists for no move of that method leaves the
// task file byte for byte as it was. Which writer ran is
// TestEveryStateWriteIsDeclared's (contract_test.go), not this test's.
//
// The method a row runs is chosen by the test, never by a driver: the test
// calls reflect.ValueOf(store).MethodByName(via) with the driver's
// arguments, so a driver cannot reach another method through a shared
// helper. The expected block groups are test data written from the code (as
// the Who of each method is), named per (row, method).
//
// The F5 list is knownDisagreements (contract_test.go), the one list
// TestLeavingBlockedClearsTheBlockGroup reads too. Adding an entry is a plan
// ruling, never a seat's own edit.

const (
	s2Holder  = "seat-a"
	s2Lead    = "cto"
	s2Creator = "lead-a"
	s2Reason  = "s21_reason"
)

var s2T0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// s2Group is the five fields of the block group (BlockGroup()).
type s2Group struct {
	Reason, Route, Detail string
	Attempt               int
	Paths                 []string
}

func s2GroupOf(t Task) s2Group {
	return s2Group{t.BlockReason, t.BlockRoute, t.BlockDetail, t.BlockAttempt, t.RefusedPaths}
}

func (g s2Group) full() bool {
	return g.Reason != "" && g.Route != "" && g.Detail != "" && g.Attempt != 0 && len(g.Paths) > 0
}

// s2Setup builds a task in the row's From state that meets the row's Needs,
// and returns its id ("" for the add row, which creates the task).
type s2Setup func(t *testing.T, s Store) string

// s2Args is the arguments of one call of the row's method, as one Who.
type s2Args func(id, who string, now time.Time) []any

// s2Driver is one (row, method): its setup, the arguments, the Who the call
// is made as (each is run), and the block group the call must leave, given
// the Who and the group before.
type s2Driver struct {
	setup s2Setup
	args  s2Args
	who   []string
	group func(who string, before s2Group) s2Group
}

func s2Key(from, to, via string) string { return from + ">" + to + "/" + via }

func s2Store(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// s2Invoke calls the store method named via with args and returns the tasks
// it returned (one for a Task result, the list for ExpireDue) and its error.
func s2Invoke(t *testing.T, s Store, via string, args []any) ([]Task, error) {
	t.Helper()
	m := reflect.ValueOf(s).MethodByName(via)
	if !m.IsValid() {
		t.Fatalf("Store has no method %s", via)
	}
	if m.Type().NumIn() != len(args) {
		t.Fatalf("%s takes %d arguments, the driver gives %d", via, m.Type().NumIn(), len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		if a == nil {
			in[i] = reflect.Zero(m.Type().In(i))
		} else {
			in[i] = reflect.ValueOf(a)
		}
	}
	out := m.Call(in)
	err, _ := out[len(out)-1].Interface().(error)
	switch v := out[0].Interface().(type) {
	case Task:
		return []Task{v}, err
	case []Task:
		return v, err
	}
	t.Fatalf("%s returns %s first", via, out[0].Type())
	return nil, nil
}

func s2Add(t *testing.T, s Store, in AddInput) string {
	t.Helper()
	if in.Title == "" {
		in.Title = "s21"
	}
	if in.Class == "" {
		in.Class = "plan" // no verifier: a done report completes
	}
	got, err := s.Add(in, s2T0)
	if err != nil {
		t.Fatal(err)
	}
	return got.ID
}

func s2Must(t *testing.T, _ Task, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The From states.
func s2Pending(t *testing.T, s Store) string { return s2Add(t, s, AddInput{}) }

func s2Claimed(class string, files []string) s2Setup {
	return func(t *testing.T, s Store) string {
		id := s2Add(t, s, AddInput{Class: class, DeclaredFiles: files})
		got, err := s.Claim(id, s2Holder, nil, time.Minute, s2T0)
		s2Must(t, got, err)
		return id
	}
}

// s2Reported is in progress with a done report waiting for the commit step.
func s2Reported(class string) s2Setup {
	return func(t *testing.T, s Store) string {
		id := s2Claimed(class, []string{"a.go"})(t, s)
		got, err := s.Done(id, s2Holder, "ok", "", s2T0)
		s2Must(t, got, err)
		return id
	}
}

// The seed values a blocked fixture's empty block fields are given on disk,
// so every "cleared" and "kept" check starts from five non-empty fields.
const (
	s2SeedDetail  = "seed detail"
	s2SeedAttempt = 7
	s2SeedPath    = "seed.go"
)

// s2Seed writes the seed values into the empty block fields of a blocked
// task's file (a test fixture: the store's own paths leave RefusedPaths and
// BlockAttempt empty on most blocks), then checks all five are non-empty.
func s2Seed(t *testing.T, s Store, id string) {
	t.Helper()
	rec := s2ReadDisk(t, s, id)
	if rec.State != StateBlocked {
		t.Fatalf("seeding a %s task", rec.State)
	}
	if rec.BlockDetail == "" {
		rec.BlockDetail = s2SeedDetail
	}
	if rec.BlockAttempt == 0 {
		rec.BlockAttempt = s2SeedAttempt
	}
	if len(rec.RefusedPaths) == 0 {
		rec.RefusedPaths = []string{s2SeedPath}
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2TaskFile(s, id), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := s2GroupOf(s2ReadDisk(t, s, id)); !g.full() {
		t.Fatalf("the seeded block group is not full: %+v", g)
	}
}

// s2BlockedForLead is blocked from pending, no report, seeded.
func s2BlockedForLead(createdBy string) s2Setup {
	return func(t *testing.T, s Store) string {
		id := s2Add(t, s, AddInput{CreatedBy: createdBy})
		got, err := s.Block(id, s2Lead, s2Reason, "", RouteLead, s2T0)
		s2Must(t, got, err)
		s2Seed(t, s, id)
		return id
	}
}

// s2BlockedRefused is blocked by a commit refusal, its report kept, seeded.
func s2BlockedRefused(t *testing.T, s Store) string {
	id := s2Reported("plan")(t, s)
	got, err := s.RefuseCommitPaths(id, "outside_declared_files", "refused", "", []string{"b.go"}, s2T0)
	s2Must(t, got, err)
	s2Seed(t, s, id)
	return id
}

// s2BlockedTyped is a seat's typed widen request: no report, refused paths,
// its attempt bound (all five fields set by the store itself).
func s2BlockedTyped(t *testing.T, s Store) string {
	id := s2Claimed("plan", []string{"a.go"})(t, s)
	got, err := s.BlockWithPaths(id, s2Holder, ReasonWidenRequested, "", RouteLead, []string{"b.go"}, s2T0)
	s2Must(t, got, err)
	s2Seed(t, s, id)
	return id
}

func s2Completed(t *testing.T, s Store) string {
	id := s2Claimed("plan", nil)(t, s)
	got, err := s.Done(id, s2Holder, "ok", "", s2T0)
	s2Must(t, got, err)
	return id
}

func s2Cancelled(t *testing.T, s Store) string {
	id := s2Pending(t, s)
	got, err := s.Cancel(id, s2Lead, "s21", s2T0)
	s2Must(t, got, err)
	return id
}

// s2Actor is the handle a call passes for a Who: the lease holder for
// live_holder, the recorded creator for creator, the lead for proven_lead,
// and another handle for asserted_handle. The store has no actor argument
// for scheduler and harness_shell calls (their callers prove them).
func s2Actor(who string) string {
	switch who {
	case WhoLiveHolder:
		return s2Holder
	case WhoCreator:
		return s2Creator
	case WhoProvenLead:
		return s2Lead
	case WhoAssertedHandle:
		return "asserted-1"
	}
	return who
}

// The expected groups (test data written from the code).
func s2Empty(string, s2Group) s2Group         { return s2Group{} }
func s2Kept(_ string, before s2Group) s2Group { return before }

// s2ByActor is block's detail for a call with no detail: " (by <actor>)",
// trimmed (lease.go block, byActor).
func s2ByActor(who string) string { return "(by " + s2Actor(who) + ")" }

const s2VerdictMissingDetail = "class implement completes only on a recorded tests verdict from the harness; the done report has none"

func s2Drivers() map[string]s2Driver {
	sched, shell, asserted := WhoScheduler, WhoHarnessShell, WhoAssertedHandle
	noPool := (*Pool)(nil)
	noPaths := []string(nil)
	addArgs := func(id, who string, now time.Time) []any {
		return []any{AddInput{Title: "s21", Class: "plan", CreatedBy: s2Actor(who)}, now}
	}
	grantArgs := func(id, who string, now time.Time) []any {
		// the holder handle is the seat's whoever calls (the scheduler grants
		// it, the seat or the lead names it)
		return []any{id, s2Holder, noPool, time.Minute, now}
	}
	verdictMissing := func(string, s2Group) s2Group {
		return s2Group{Reason: BlockVerdictMissing, Route: RouteLead, Detail: s2VerdictMissingDetail}
	}
	byActorBlock := func(who string, _ s2Group) s2Group {
		return s2Group{Reason: s2Reason, Route: RouteLead, Detail: s2ByActor(who)}
	}
	blockArgs := func(id, who string, now time.Time) []any {
		return []any{id, s2Actor(who), s2Reason, "", RouteLead, now}
	}
	blockPathsArgs := func(id, who string, now time.Time) []any {
		return []any{id, s2Actor(who), s2Reason, "", RouteLead, noPaths, now}
	}
	cancelArgs := func(id, who string, now time.Time) []any { return []any{id, s2Actor(who), "s21", now} }
	doneArgs := func(id, who string, now time.Time) []any { return []any{id, s2Actor(who), "ok", "", now} }
	completeArgs := func(id, _ string, now time.Time) []any { return []any{id, now} }
	completeBoundArgs := func(id, _ string, now time.Time) []any { return []any{id, "o/r", 0, "", now} }
	widenArgs := func(id, who string, now time.Time) []any {
		return []any{id, s2Actor(who), "s21", []string{"b.go"}, time.Minute, now}
	}
	widenGatedArgs := func(id, who string, now time.Time) []any {
		return []any{id, s2Actor(who), "s21", []string{"b.go"}, []string{"gate-target"}, time.Minute, now}
	}
	return map[string]s2Driver{
		s2Key("", StatePending, "Add"):              {setup: func(*testing.T, Store) string { return "" }, args: addArgs, who: []string{asserted}, group: s2Empty},
		s2Key("", StatePending, "AddWithFileWaits"): {setup: func(*testing.T, Store) string { return "" }, args: addArgs, who: []string{asserted}, group: s2Empty},

		s2Key(StatePending, StateInProgress, "Grant"):        {setup: s2Pending, args: grantArgs, who: []string{sched}, group: s2Empty},
		s2Key(StatePending, StateInProgress, "GrantClean"):   {setup: s2Pending, args: grantArgs, who: []string{asserted}, group: s2Empty},
		s2Key(StatePending, StateInProgress, "Claim"):        {setup: s2Pending, args: grantArgs, who: []string{asserted}, group: s2Empty},
		s2Key(StatePending, StateInProgress, "ClaimServing"): {setup: s2Pending, args: grantArgs, who: []string{asserted}, group: s2Empty},

		s2Key(StatePending, StateBlocked, "Block"):          {setup: s2Pending, args: blockArgs, who: []string{sched, asserted}, group: byActorBlock},
		s2Key(StatePending, StateBlocked, "BlockWithPaths"): {setup: s2Pending, args: blockPathsArgs, who: []string{sched, asserted}, group: byActorBlock},

		s2Key(StatePending, StateCancelled, "Cancel"): {setup: s2Pending, args: cancelArgs, who: []string{asserted}, group: s2Empty},

		s2Key(StateInProgress, StateCompleted, "Done"):          {setup: s2Claimed("plan", nil), args: doneArgs, who: []string{WhoLiveHolder}, group: s2Empty},
		s2Key(StateInProgress, StateCompleted, "Complete"):      {setup: s2Reported("plan"), args: completeArgs, who: []string{sched, shell}, group: s2Empty},
		s2Key(StateInProgress, StateCompleted, "CompleteBound"): {setup: s2Reported("plan"), args: completeBoundArgs, who: []string{sched, shell}, group: s2Empty},

		// a mechanical verifier and no verdict: the done report blocks
		s2Key(StateInProgress, StateBlocked, "Done"):          {setup: s2Claimed("implement", nil), args: doneArgs, who: []string{WhoLiveHolder}, group: verdictMissing},
		s2Key(StateInProgress, StateBlocked, "Complete"):      {setup: s2Reported("implement"), args: completeArgs, who: []string{sched, shell}, group: verdictMissing},
		s2Key(StateInProgress, StateBlocked, "CompleteBound"): {setup: s2Reported("implement"), args: completeBoundArgs, who: []string{sched, shell}, group: verdictMissing},
		s2Key(StateInProgress, StateBlocked, "RefuseCommit"): {setup: s2Reported("plan"), args: func(id, _ string, now time.Time) []any {
			return []any{id, "outside_declared_files", "refused", "", now}
		}, who: []string{sched, shell}, group: func(string, s2Group) s2Group {
			return s2Group{Reason: "outside_declared_files", Route: RouteLead, Detail: "refused"}
		}},
		s2Key(StateInProgress, StateBlocked, "RefuseCommitPaths"): {setup: s2Reported("plan"), args: func(id, _ string, now time.Time) []any {
			return []any{id, "outside_declared_files", "refused", RouteOwner, []string{"b.go"}, now}
		}, who: []string{sched, shell}, group: func(string, s2Group) s2Group {
			return s2Group{Reason: "outside_declared_files", Route: RouteOwner, Detail: "refused", Paths: []string{"b.go"}}
		}},
		s2Key(StateInProgress, StateBlocked, "Block"): {setup: s2Claimed("plan", nil), args: blockArgs, who: []string{WhoLiveHolder}, group: byActorBlock},
		s2Key(StateInProgress, StateBlocked, "BlockWithPaths"): {setup: s2Claimed("plan", []string{"a.go"}), args: func(id, who string, now time.Time) []any {
			return []any{id, s2Actor(who), ReasonWidenRequested, "", RouteLead, []string{"b.go"}, now}
		}, who: []string{WhoLiveHolder}, group: func(who string, _ s2Group) s2Group {
			return s2Group{Reason: ReasonWidenRequested, Route: RouteLead, Detail: s2ByActor(who), Attempt: 1, Paths: []string{"b.go"}}
		}},

		s2Key(StateInProgress, StatePending, "Fail"): {setup: s2Claimed("plan", nil), args: func(id, who string, now time.Time) []any {
			return []any{id, s2Actor(who), "s21", now}
		}, who: []string{WhoLiveHolder}, group: s2Empty},
		s2Key(StateInProgress, StatePending, "Release"): {setup: s2Claimed("plan", nil), args: func(id, who string, now time.Time) []any {
			return []any{id, s2Actor(who), "s21", now}
		}, who: []string{asserted}, group: s2Empty},
		s2Key(StateInProgress, StatePending, "ExpireDue"): {setup: s2Claimed("plan", nil), args: func(_, _ string, now time.Time) []any {
			return []any{now.Add(2 * time.Minute)} // the one-minute lease is due
		}, who: []string{sched}, group: s2Empty},
		s2Key(StateInProgress, StateCancelled, "Cancel"): {setup: s2Claimed("plan", nil), args: cancelArgs, who: []string{asserted}, group: s2Empty},

		// ResetBy's setup depends on the Who (s2ResetBySetup)
		s2Key(StateBlocked, StatePending, "ResetBy"): {args: func(id, who string, now time.Time) []any {
			return []any{id, s2Actor(who), noPool, now}
		}, who: []string{WhoCreator, asserted}, group: s2Empty},
		s2Key(StateBlocked, StatePending, "Reset"): {setup: s2BlockedForLead(""), args: func(id, _ string, now time.Time) []any {
			return []any{id, noPool, now}
		}, who: []string{sched}, group: s2Empty},
		s2Key(StateBlocked, StatePending, "ResetIfBlocked"): {setup: s2BlockedForLead(""), args: func(id, _ string, now time.Time) []any {
			return []any{id, s2Reason, (func(Task) bool)(nil), now}
		}, who: []string{sched}, group: s2Empty},
		s2Key(StateBlocked, StatePending, "Widen"):         {setup: s2BlockedTyped, args: widenArgs, who: []string{WhoProvenLead}, group: s2Empty},
		s2Key(StateBlocked, StatePending, "WidenGated"):    {setup: s2BlockedTyped, args: widenGatedArgs, who: []string{WhoProvenLead}, group: s2Empty},
		s2Key(StateBlocked, StateInProgress, "Widen"):      {setup: s2BlockedRefused, args: widenArgs, who: []string{WhoProvenLead}, group: s2Empty},
		s2Key(StateBlocked, StateInProgress, "WidenGated"): {setup: s2BlockedRefused, args: widenGatedArgs, who: []string{WhoProvenLead}, group: s2Empty},
		// a refused commit has a report: Escalate does not keep the paths
		s2Key(StateBlocked, StateBlocked, "Escalate"): {setup: s2BlockedRefused, args: func(id, _ string, now time.Time) []any {
			return []any{id, ReasonGrowthGateRequired, "s21", now}
		}, who: []string{WhoProvenLead}, group: func(string, s2Group) s2Group {
			return s2Group{Reason: ReasonGrowthGateRequired, Route: RouteOwner, Detail: "s21"}
		}},
		// the F5 entry: the group stays as it was
		s2Key(StateBlocked, StateCancelled, "Cancel"): {setup: s2BlockedForLead(""), args: cancelArgs, who: []string{asserted}, group: s2Kept},
	}
}

// s2ResetBySetup: the creator Who resets a task whose creator is recorded;
// asserted_handle resets one with no creator recorded (ResetBy's Needs).
func s2ResetBySetup(who string) s2Setup {
	if who == WhoCreator {
		return s2BlockedForLead(s2Creator)
	}
	return s2BlockedForLead("")
}

// s2ReturnedDiffers names, field by field, where a method's returned record
// differs from the record it wrote. Each entry is listed here and in the note;
// adding one is a plan ruling. These three take their returned copy inside
// the store's update (Claim lease.go:359, ClaimServing :430, ExpireDue
// :1072), before the commit stamps UpdatedAt (task.go:476-480), and do not
// re-stamp it as mutate does (task.go:502): their returned UpdatedAt is the
// value before the move. A persisted field, so a defect named for 3.2.x
// (listed by name, not fixed here), not a computed field.
var s2ReturnedDiffers = map[string][]string{
	"Claim":        {"UpdatedAt"},
	"ClaimServing": {"UpdatedAt"},
	"ExpireDue":    {"UpdatedAt"},
}

func s2TaskFile(s Store, id string) string { return filepath.Join(s.Dir, id+".json") }

func s2ReadDisk(t *testing.T, s Store, id string) Task {
	t.Helper()
	raw, err := os.ReadFile(s2TaskFile(s, id))
	if err != nil {
		t.Fatal(err)
	}
	var got Task
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// s2FieldDiffs is the names of the Task fields in which a and b differ.
func s2FieldDiffs(a, b Task) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var out []string
	for i := 0; i < va.NumField(); i++ {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, va.Type().Field(i).Name)
		}
	}
	return out
}

// s2Known is the indexes of the F5 entries that name this row and method.
func s2Known(move MoveContract, via string) []int {
	var out []int
	for i, k := range knownDisagreements {
		if k.From == move.From && k.To == move.To && k.Via == via {
			out = append(out, i)
		}
	}
	return out
}

// TestEveryMoveIsDrivenThroughTheStore drives each (row, Via method, Who).
func TestEveryMoveIsDrivenThroughTheStore(t *testing.T) {
	drivers := s2Drivers()
	used := map[string]bool{}
	observed := make([]bool, len(knownDisagreements)) // the F5 entries a driven row met
	returned := map[string]bool{}                     // the s2ReturnedDiffers entries a driven row met
	for _, move := range Moves() {
		rowWho := map[string]bool{}
		for _, via := range move.Via {
			key := s2Key(move.From, move.To, via)
			d, ok := drivers[key]
			if !ok {
				t.Errorf("row %s via %s has no driver", key, via)
				continue
			}
			used[key] = true
			for _, who := range d.who {
				rowWho[who] = true
				if !contains(move.Who, who) {
					t.Errorf("%s: the driver calls as %s, which the row does not name (%v)", key, who, move.Who)
				}
				t.Run(key+"/"+who, func(t *testing.T) {
					s := s2Store(t)
					setup := d.setup
					if setup == nil {
						setup = s2ResetBySetup(who)
					}
					id := setup(t, s)
					var before Task
					if id != "" {
						before = s2ReadDisk(t, s, id)
						if before.State != move.From {
							t.Fatalf("setup left %s, the row's From is %s", before.State, move.From)
						}
						if move.From == StateBlocked && !s2GroupOf(before).full() {
							t.Fatalf("a blocked From with an empty block field: %+v", s2GroupOf(before))
						}
					}
					got, err := s2Invoke(t, s, via, d.args(id, who, s2T0.Add(time.Second)))
					if err != nil {
						t.Fatalf("%s was refused: %v", via, err)
					}
					if len(got) != 1 || (id != "" && got[0].ID != id) {
						t.Fatalf("%s returned %d tasks, want the one moved task", via, len(got))
					}
					if id == "" {
						id = got[0].ID
					}
					disk := s2ReadDisk(t, s, id)
					if disk.State != move.To {
						t.Fatalf("on disk %s, the row says %s", disk.State, move.To)
					}
					diffs := s2FieldDiffs(got[0], disk)
					if want := s2ReturnedDiffers[via]; strings.Join(diffs, ",") != strings.Join(want, ",") {
						t.Fatalf("%s returned a record that differs from the disk record in %v; the named differences are %v", via, diffs, want)
					}
					if len(diffs) > 0 {
						returned[via] = true
					}
					want := d.group(who, s2GroupOf(before))
					if g := s2GroupOf(disk); !reflect.DeepEqual(g, want) {
						t.Fatalf("block group on disk %+v, want %+v", g, want)
					}
					// the group the row's own flags allow
					switch {
					case move.To == StateBlocked:
						if want.Reason == "" || want.Route == "" {
							t.Fatalf("a move to blocked expects no reason or route: %+v", want)
						}
					case move.ClearsBlockGroup:
						if !reflect.DeepEqual(want, s2Group{}) {
							t.Fatalf("the row clears the block group; the driver expects %+v", want)
						}
					case move.From == StateBlocked:
						known := s2Known(move, via)
						if len(known) != 1 {
							t.Fatalf("a move out of blocked that does not clear the block group needs exactly one entry on the known list; it has %d", len(known))
						}
						observed[known[0]] = true
						if !reflect.DeepEqual(want, s2GroupOf(before)) {
							t.Fatalf("the known disagreement keeps the group; the driver expects %+v", want)
						}
					default:
						if !reflect.DeepEqual(want, s2Group{}) || !reflect.DeepEqual(s2GroupOf(before), s2Group{}) {
							t.Fatalf("a move outside blocked: before %+v, expected %+v", s2GroupOf(before), want)
						}
					}
				})
			}
		}
		var want, have []string
		want = append(want, move.Who...)
		for w := range rowWho {
			have = append(have, w)
		}
		sort.Strings(want)
		sort.Strings(have)
		if strings.Join(want, ",") != strings.Join(have, ",") {
			t.Errorf("row %s>%s via %v: the drivers call as %v, the row names %v", move.From, move.To, move.Via, have, want)
		}
	}
	for key := range drivers {
		if !used[key] {
			t.Errorf("driver %s matches no row", key)
		}
	}
	for i, k := range knownDisagreements {
		if !observed[i] {
			t.Errorf("known disagreement %s via %s to %s (%s) was met by no driven row", k.From, k.Via, k.To, k.Detail)
		}
	}
	for via := range s2ReturnedDiffers {
		if !returned[via] {
			t.Errorf("the named returned-record difference of %s was met by no driven row", via)
		}
	}
}

// s2RefusalFrom is, per method, a state the table lists for no move of
// that method, and a setup that builds a task in it.
var s2RefusalFrom = map[string]struct {
	from  string
	setup s2Setup
}{
	"Grant": {StateBlocked, s2BlockedForLead("")}, "GrantClean": {StateBlocked, s2BlockedForLead("")},
	"Claim": {StateBlocked, s2BlockedForLead("")}, "ClaimServing": {StateBlocked, s2BlockedForLead("")},
	"Block": {StateBlocked, s2BlockedForLead("")}, "BlockWithPaths": {StateBlocked, s2BlockedForLead("")},
	"Cancel": {StateCompleted, s2Completed},
	"Done":   {StatePending, s2Pending}, "Complete": {StateBlocked, s2BlockedRefused}, "CompleteBound": {StateBlocked, s2BlockedRefused},
	"RefuseCommit": {StateBlocked, s2BlockedRefused}, "RefuseCommitPaths": {StateBlocked, s2BlockedRefused},
	"Fail": {StateBlocked, s2BlockedForLead("")}, "Release": {StateBlocked, s2BlockedForLead("")},
	"ExpireDue": {StateBlocked, s2BlockedForLead("")},
	"ResetBy":   {StatePending, s2Pending}, "Reset": {StateInProgress, s2Claimed("plan", nil)},
	"ResetIfBlocked": {StatePending, s2Pending}, "Escalate": {StatePending, s2Pending},
	"Widen": {StateCompleted, s2Completed}, "WidenGated": {StateCancelled, s2Cancelled},
}

// TestEveryMoveRefusesAnUndeclaredFrom: per (row, method), the method on a
// task in a state the table lists for no move of that method is refused,
// with the row's own arguments, and the task file is byte for byte as it
// was. Two stand-ins, named in the note: the add rows have no From, so an
// add whose dependency does not exist is refused and writes no task file;
// ExpireDue takes no id, so on a task it does not list it is a no-op (no
// error, nothing returned), and the file is byte-equal.
func TestEveryMoveRefusesAnUndeclaredFrom(t *testing.T) {
	drivers := s2Drivers()
	for _, move := range Moves() {
		for _, via := range move.Via {
			key := s2Key(move.From, move.To, via)
			d := drivers[key]
			if move.From == "" {
				t.Run(key, func(t *testing.T) {
					s := s2Store(t)
					args := d.args("", WhoAssertedHandle, s2T0)
					in := args[0].(AddInput)
					in.DependsOn = []string{"t99"}
					args[0] = in
					if _, err := s2Invoke(t, s, via, args); err == nil {
						t.Fatal("an add with an unknown dependency was accepted")
					}
					if entries, _ := os.ReadDir(s.Dir); s2CountTaskFiles(entries) != 0 {
						t.Fatalf("a refused add wrote a task file: %v", entries)
					}
				})
				continue
			}
			r, ok := s2RefusalFrom[via]
			if !ok {
				t.Errorf("%s has no refusal From", via)
				continue
			}
			if contains(s2FromsOf(via), r.from) {
				t.Errorf("%s: the refusal's From %s is a From the table lists for %s", key, r.from, via)
				continue
			}
			t.Run(key, func(t *testing.T) {
				s := s2Store(t)
				id := r.setup(t, s)
				if got := s2ReadDisk(t, s, id).State; got != r.from {
					t.Fatalf("setup left %s, want %s", got, r.from)
				}
				before, err := os.ReadFile(s2TaskFile(s, id))
				if err != nil {
					t.Fatal(err)
				}
				got, err := s2Invoke(t, s, via, d.args(id, d.who[0], s2T0.Add(time.Hour)))
				switch {
				case via == "ExpireDue":
					if err != nil || len(got) != 0 {
						t.Fatalf("ExpireDue on a %s task: %v, %d tasks", r.from, err, len(got))
					}
				case err == nil:
					t.Fatalf("%s from %s was accepted", via, r.from)
				}
				after, err := os.ReadFile(s2TaskFile(s, id))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("a refused call changed the task file:\nbefore %s\nafter  %s", before, after)
				}
			})
		}
	}
	for via := range s2RefusalFrom {
		if len(s2FromsOf(via)) == 0 {
			t.Errorf("refusal From for %s, which no row names", via)
		}
	}
}

// s2FromsOf is every From the table lists for a method.
func s2FromsOf(via string) []string {
	var out []string
	for _, m := range Moves() {
		if contains(m.Via, via) {
			out = append(out, m.From)
		}
	}
	return out
}

func s2CountTaskFiles(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}
