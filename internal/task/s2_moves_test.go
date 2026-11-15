package task

import (
	"bytes"
	"encoding/json"
	"errors"
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
// against its row: the state before and after, the block group per
// ClearsBlockGroup, and the stored record re-read from disk. Each row also
// has a refusal row: the row's first method called on a task in a state the
// row does not list leaves the task file byte for byte as it was. Which
// writer ran is TestEveryStateWriteIsDeclared's (contract_test.go), not this
// test's.

// The F5 list is knownDisagreements (contract_test.go), the one list
// TestLeavingBlockedClearsTheBlockGroup reads too: a move the store makes
// otherwise than the plan's rule for its row. Adding an entry is a plan
// ruling, never a seat's own edit.

const (
	s2Holder  = "seat-a"
	s2Lead    = "cto"
	s2Creator = "lead-a"
	s2Reason  = "s21_reason"
)

var s2T0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// s2Setup builds a task in the row's From state that meets the row's Needs,
// and returns its id ("" for the add row, which creates the task).
type s2Setup func(t *testing.T, s Store) string

// s2Call is one store call as one Who. It returns the moved task.
type s2Call func(s Store, id, who string, now time.Time) (Task, error)

// s2Driver is one (row, method): its setup, the call, the Who that call is
// made as (each is run), and the reason a move to blocked records.
type s2Driver struct {
	setup  s2Setup
	call   s2Call
	who    []string
	reason string
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

// s2BlockedForLead is blocked from pending, no report.
func s2BlockedForLead(createdBy string) s2Setup {
	return func(t *testing.T, s Store) string {
		id := s2Add(t, s, AddInput{CreatedBy: createdBy})
		got, err := s.Block(id, s2Lead, s2Reason, "", RouteLead, s2T0)
		s2Must(t, got, err)
		return id
	}
}

// s2BlockedRefused is blocked by a commit refusal, its report kept.
func s2BlockedRefused(t *testing.T, s Store) string {
	id := s2Reported("plan")(t, s)
	got, err := s.RefuseCommitPaths(id, "outside_declared_files", "", "", []string{"b.go"}, s2T0)
	s2Must(t, got, err)
	return id
}

// s2BlockedTyped is a seat's typed widen request: no report, refused paths.
func s2BlockedTyped(t *testing.T, s Store) string {
	id := s2Claimed("plan", []string{"a.go"})(t, s)
	got, err := s.BlockWithPaths(id, s2Holder, ReasonWidenRequested, "", RouteLead, []string{"b.go"}, s2T0)
	s2Must(t, got, err)
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

func s2Drivers() map[string]s2Driver {
	sched, shell := WhoScheduler, WhoHarnessShell
	asserted := WhoAssertedHandle
	addCall := func(waits bool) s2Call {
		return func(s Store, _ string, who string, now time.Time) (Task, error) {
			in := AddInput{Title: "s21", Class: "plan", CreatedBy: s2Actor(who)}
			if waits {
				got, _, err := s.AddWithFileWaits(in, now)
				return got, err
			}
			return s.Add(in, now)
		}
	}
	grantLike := func(f func(Store, string, string, *Pool, time.Duration, time.Time) (Task, error)) s2Call {
		return func(s Store, id, who string, now time.Time) (Task, error) {
			// the holder handle is the seat's whoever calls (the scheduler
			// grants it, the seat or the lead names it)
			return f(s, id, s2Holder, nil, time.Minute, now)
		}
	}
	block := func(paths []string, reason string) s2Call {
		return func(s Store, id, who string, now time.Time) (Task, error) {
			return s.BlockWithPaths(id, s2Actor(who), reason, "", RouteLead, paths, now)
		}
	}
	plainBlock := func(s Store, id, who string, now time.Time) (Task, error) {
		return s.Block(id, s2Actor(who), s2Reason, "", RouteLead, now)
	}
	cancel := func(s Store, id, who string, now time.Time) (Task, error) {
		return s.Cancel(id, s2Actor(who), "s21", now)
	}
	done := func(s Store, id, who string, now time.Time) (Task, error) {
		return s.Done(id, s2Actor(who), "ok", "", now)
	}
	complete := func(s Store, id, _ string, now time.Time) (Task, error) { return s.Complete(id, now) }
	completeBound := func(s Store, id, _ string, now time.Time) (Task, error) {
		return s.CompleteBound(id, "o/r", 0, "", now)
	}
	refuse := func(paths []string) s2Call {
		return func(s Store, id, _ string, now time.Time) (Task, error) {
			return s.RefuseCommitPaths(id, "outside_declared_files", "", "", paths, now)
		}
	}
	widen := func(gated bool) s2Call {
		return func(s Store, id, who string, now time.Time) (Task, error) {
			if gated {
				return s.WidenGated(id, s2Actor(who), "s21", []string{"b.go"}, []string{"gate-target"}, time.Minute, now)
			}
			return s.Widen(id, s2Actor(who), "s21", []string{"b.go"}, time.Minute, now)
		}
	}
	verdictBlocks := s2Claimed("implement", nil) // a mechanical verifier and no verdict
	m := map[string]s2Driver{
		s2Key("", StatePending, "Add"):              {setup: func(*testing.T, Store) string { return "" }, call: addCall(false), who: []string{asserted}},
		s2Key("", StatePending, "AddWithFileWaits"): {setup: func(*testing.T, Store) string { return "" }, call: addCall(true), who: []string{asserted}},

		s2Key(StatePending, StateInProgress, "Grant"):        {setup: s2Pending, call: grantLike(Store.Grant), who: []string{sched}},
		s2Key(StatePending, StateInProgress, "GrantClean"):   {setup: s2Pending, call: grantLike(Store.GrantClean), who: []string{asserted}},
		s2Key(StatePending, StateInProgress, "Claim"):        {setup: s2Pending, call: grantLike(Store.Claim), who: []string{asserted}},
		s2Key(StatePending, StateInProgress, "ClaimServing"): {setup: s2Pending, call: grantLike(Store.ClaimServing), who: []string{asserted}},

		s2Key(StatePending, StateBlocked, "Block"):          {setup: s2Pending, call: plainBlock, who: []string{sched, asserted}, reason: s2Reason},
		s2Key(StatePending, StateBlocked, "BlockWithPaths"): {setup: s2Pending, call: block(nil, s2Reason), who: []string{sched, asserted}, reason: s2Reason},

		s2Key(StatePending, StateCancelled, "Cancel"): {setup: s2Pending, call: cancel, who: []string{asserted}},

		s2Key(StateInProgress, StateCompleted, "Done"):          {setup: s2Claimed("plan", nil), call: done, who: []string{WhoLiveHolder}},
		s2Key(StateInProgress, StateCompleted, "Complete"):      {setup: s2Reported("plan"), call: complete, who: []string{sched, shell}},
		s2Key(StateInProgress, StateCompleted, "CompleteBound"): {setup: s2Reported("plan"), call: completeBound, who: []string{sched, shell}},

		s2Key(StateInProgress, StateBlocked, "Done"):              {setup: verdictBlocks, call: done, who: []string{WhoLiveHolder}, reason: BlockVerdictMissing},
		s2Key(StateInProgress, StateBlocked, "Complete"):          {setup: s2Reported("implement"), call: complete, who: []string{sched, shell}, reason: BlockVerdictMissing},
		s2Key(StateInProgress, StateBlocked, "CompleteBound"):     {setup: s2Reported("implement"), call: completeBound, who: []string{sched, shell}, reason: BlockVerdictMissing},
		s2Key(StateInProgress, StateBlocked, "RefuseCommit"):      {setup: s2Reported("plan"), call: refuse(nil), who: []string{sched, shell}, reason: "outside_declared_files"},
		s2Key(StateInProgress, StateBlocked, "RefuseCommitPaths"): {setup: s2Reported("plan"), call: refuse([]string{"b.go"}), who: []string{sched, shell}, reason: "outside_declared_files"},
		s2Key(StateInProgress, StateBlocked, "Block"):             {setup: s2Claimed("plan", nil), call: plainBlock, who: []string{WhoLiveHolder}, reason: s2Reason},
		s2Key(StateInProgress, StateBlocked, "BlockWithPaths"):    {setup: s2Claimed("plan", []string{"a.go"}), call: block([]string{"b.go"}, ReasonWidenRequested), who: []string{WhoLiveHolder}, reason: ReasonWidenRequested},

		s2Key(StateInProgress, StatePending, "Fail"): {setup: s2Claimed("plan", nil), call: func(s Store, id, who string, now time.Time) (Task, error) {
			return s.Fail(id, s2Actor(who), "s21", now)
		}, who: []string{WhoLiveHolder}},
		s2Key(StateInProgress, StatePending, "Release"): {setup: s2Claimed("plan", nil), call: func(s Store, id, who string, now time.Time) (Task, error) {
			return s.Release(id, s2Actor(who), "s21", now)
		}, who: []string{asserted}},
		s2Key(StateInProgress, StatePending, "ExpireDue"): {setup: s2Claimed("plan", nil), call: func(s Store, id, _ string, now time.Time) (Task, error) {
			got, err := s.ExpireDue(now.Add(2 * time.Minute)) // the one-minute lease is due
			if err != nil || len(got) != 1 || got[0].ID != id {
				return Task{}, errors.Join(err, errors.New("ExpireDue did not return the one due task"))
			}
			return got[0], nil
		}, who: []string{sched}},
		s2Key(StateInProgress, StateCancelled, "Cancel"): {setup: s2Claimed("plan", nil), call: cancel, who: []string{asserted}},

		s2Key(StateBlocked, StatePending, "ResetBy"): {setup: nil, call: func(s Store, id, who string, now time.Time) (Task, error) {
			return s.ResetBy(id, s2Actor(who), nil, now)
		}, who: []string{WhoCreator, asserted}},
		s2Key(StateBlocked, StatePending, "Reset"): {setup: s2BlockedForLead(""), call: func(s Store, id, _ string, now time.Time) (Task, error) {
			return s.Reset(id, nil, now)
		}, who: []string{sched}},
		s2Key(StateBlocked, StatePending, "ResetIfBlocked"): {setup: s2BlockedForLead(""), call: func(s Store, id, _ string, now time.Time) (Task, error) {
			return s.ResetIfBlocked(id, s2Reason, nil, now)
		}, who: []string{sched}},
		s2Key(StateBlocked, StatePending, "Widen"):         {setup: s2BlockedTyped, call: widen(false), who: []string{WhoProvenLead}},
		s2Key(StateBlocked, StatePending, "WidenGated"):    {setup: s2BlockedTyped, call: widen(true), who: []string{WhoProvenLead}},
		s2Key(StateBlocked, StateInProgress, "Widen"):      {setup: s2BlockedRefused, call: widen(false), who: []string{WhoProvenLead}},
		s2Key(StateBlocked, StateInProgress, "WidenGated"): {setup: s2BlockedRefused, call: widen(true), who: []string{WhoProvenLead}},
		s2Key(StateBlocked, StateBlocked, "Escalate"): {setup: s2BlockedRefused, call: func(s Store, id, _ string, now time.Time) (Task, error) {
			return s.Escalate(id, ReasonGrowthGateRequired, "s21", now)
		}, who: []string{WhoProvenLead}, reason: ReasonGrowthGateRequired},
		s2Key(StateBlocked, StateCancelled, "Cancel"): {setup: s2BlockedForLead(""), call: cancel, who: []string{asserted}},
	}
	return m
}

// s2ResetBySetup: the creator Who resets a task whose creator is recorded;
// asserted_handle resets one with no creator recorded (ResetBy's Needs).
func s2ResetBySetup(who string) s2Setup {
	if who == WhoCreator {
		return s2BlockedForLead(s2Creator)
	}
	return s2BlockedForLead("")
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

type s2Group struct {
	Reason, Route, Detail string
	Attempt               int
	Paths                 []string
}

func s2GroupOf(t Task) s2Group {
	return s2Group{t.BlockReason, t.BlockRoute, t.BlockDetail, t.BlockAttempt, t.RefusedPaths}
}

func (g s2Group) empty() bool {
	return g.Reason == "" && g.Route == "" && g.Detail == "" && g.Attempt == 0 && len(g.Paths) == 0
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
					}
					got, err := d.call(s, id, who, s2T0.Add(time.Second))
					if err != nil {
						t.Fatalf("the call was refused: %v", err)
					}
					if id == "" {
						id = got.ID
					}
					disk := s2ReadDisk(t, s, id)
					if got.State != move.To || disk.State != move.To {
						t.Fatalf("state: returned %s, on disk %s, the row says %s", got.State, disk.State, move.To)
					}
					g, was := s2GroupOf(disk), s2GroupOf(before)
					switch {
					case move.To == StateBlocked:
						if g.Reason != d.reason || g.Route == "" {
							t.Fatalf("a move to blocked recorded the block group %+v, want reason %s and a route", g, d.reason)
						}
					case move.ClearsBlockGroup:
						if !g.empty() {
							t.Fatalf("the row clears the block group; on disk %+v", g)
						}
					case move.From == StateBlocked:
						known := s2Known(move, via)
						if len(known) != 1 {
							t.Fatalf("a move out of blocked that does not clear the block group needs exactly one entry on the known list; it has %d", len(known))
						}
						observed[known[0]] = true
						if !reflect.DeepEqual(g, was) || g.empty() {
							t.Fatalf("the known disagreement: the block group should stay as it was (%+v), on disk %+v", was, g)
						}
					default:
						if !g.empty() || !was.empty() {
							t.Fatalf("a move outside blocked changed or kept a block group: before %+v, after %+v", was, g)
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
}

// s2Refusal is one row's refusal: its first method called on a task in a
// state the row does not list.
type s2Refusal struct {
	from  string
	setup s2Setup
	call  s2Call
	who   string
}

func s2Refusals() map[string]s2Refusal {
	d := s2Drivers()
	first := func(from, to, via string) s2Call { return d[s2Key(from, to, via)].call }
	return map[string]s2Refusal{
		s2Key(StatePending, StateInProgress, "Grant"):        {StateBlocked, s2BlockedForLead(""), first(StatePending, StateInProgress, "Grant"), WhoScheduler},
		s2Key(StatePending, StateBlocked, "Block"):           {StateBlocked, s2BlockedForLead(""), first(StatePending, StateBlocked, "Block"), WhoAssertedHandle},
		s2Key(StatePending, StateCancelled, "Cancel"):        {StateCompleted, s2Completed, first(StatePending, StateCancelled, "Cancel"), WhoAssertedHandle},
		s2Key(StateInProgress, StateCompleted, "Done"):       {StatePending, s2Pending, first(StateInProgress, StateCompleted, "Done"), WhoLiveHolder},
		s2Key(StateInProgress, StateCompleted, "Complete"):   {StateBlocked, s2BlockedRefused, first(StateInProgress, StateCompleted, "Complete"), WhoScheduler},
		s2Key(StateInProgress, StateBlocked, "Done"):         {StatePending, s2Pending, first(StateInProgress, StateBlocked, "Done"), WhoLiveHolder},
		s2Key(StateInProgress, StateBlocked, "RefuseCommit"): {StateBlocked, s2BlockedRefused, first(StateInProgress, StateBlocked, "RefuseCommit"), WhoScheduler},
		s2Key(StateInProgress, StateBlocked, "Block"):        {StateCompleted, s2Completed, first(StateInProgress, StateBlocked, "Block"), WhoLiveHolder},
		s2Key(StateInProgress, StatePending, "Fail"):         {StateBlocked, s2BlockedForLead(""), first(StateInProgress, StatePending, "Fail"), WhoLiveHolder},
		s2Key(StateInProgress, StatePending, "Release"):      {StateBlocked, s2BlockedForLead(""), first(StateInProgress, StatePending, "Release"), WhoAssertedHandle},
		s2Key(StateInProgress, StatePending, "ExpireDue"):    {StateBlocked, s2BlockedForLead(""), nil, WhoScheduler},
		s2Key(StateInProgress, StateCancelled, "Cancel"):     {StateCancelled, s2Cancelled, first(StateInProgress, StateCancelled, "Cancel"), WhoAssertedHandle},
		s2Key(StateBlocked, StatePending, "ResetBy"):         {StatePending, s2Pending, first(StateBlocked, StatePending, "ResetBy"), WhoAssertedHandle},
		s2Key(StateBlocked, StatePending, "Reset"):           {StateInProgress, s2Claimed("plan", nil), first(StateBlocked, StatePending, "Reset"), WhoScheduler},
		s2Key(StateBlocked, StatePending, "ResetIfBlocked"):  {StatePending, s2Pending, first(StateBlocked, StatePending, "ResetIfBlocked"), WhoScheduler},
		s2Key(StateBlocked, StatePending, "Widen"):           {StateCompleted, s2Completed, first(StateBlocked, StatePending, "Widen"), WhoProvenLead},
		s2Key(StateBlocked, StateInProgress, "Widen"):        {StateCancelled, s2Cancelled, first(StateBlocked, StateInProgress, "Widen"), WhoProvenLead},
		s2Key(StateBlocked, StateBlocked, "Escalate"):        {StatePending, s2Pending, first(StateBlocked, StateBlocked, "Escalate"), WhoProvenLead},
		s2Key(StateBlocked, StateCancelled, "Cancel"):        {StateCompleted, s2Completed, first(StateBlocked, StateCancelled, "Cancel"), WhoAssertedHandle},
		s2Key("", StatePending, "Add"):                       {"", nil, nil, WhoAssertedHandle},
	}
}

// TestEveryMoveRefusesAnUndeclaredFrom: per row, the first method on a task
// in a state the row does not list is refused, and the task file is byte
// for byte as it was. The add row has no From: its refusal is an add whose
// dependency does not exist, which writes no task file. ExpireDue takes no
// id: on a task it does not list it returns nothing and writes nothing.
func TestEveryMoveRefusesAnUndeclaredFrom(t *testing.T) {
	refusals := s2Refusals()
	for _, move := range Moves() {
		key := s2Key(move.From, move.To, move.Via[0])
		r, ok := refusals[key]
		if !ok {
			t.Errorf("row %s has no refusal row", key)
			continue
		}
		delete(refusals, key)
		if r.from != "" && contains(s2FromsOf(move.Via[0]), r.from) {
			t.Errorf("%s: the refusal's From %s is a From the table lists for %s", key, r.from, move.Via[0])
		}
		t.Run(key, func(t *testing.T) {
			s := s2Store(t)
			switch {
			case move.From == "":
				_, err := s.Add(AddInput{Title: "s21", Class: "plan", DependsOn: []string{"t99"}}, s2T0)
				if err == nil {
					t.Fatal("an add with an unknown dependency was accepted")
				}
				if entries, _ := os.ReadDir(s.Dir); s2CountTaskFiles(entries) != 0 {
					t.Fatalf("a refused add wrote a task file: %v", entries)
				}
				return
			}
			id := r.setup(t, s)
			if got := s2ReadDisk(t, s, id).State; got != r.from {
				t.Fatalf("setup left %s, want %s", got, r.from)
			}
			before, err := os.ReadFile(s2TaskFile(s, id))
			if err != nil {
				t.Fatal(err)
			}
			if r.call == nil { // ExpireDue
				got, err := s.ExpireDue(s2T0.Add(time.Hour))
				if err != nil || len(got) != 0 {
					t.Fatalf("ExpireDue on a %s task: %v, %+v", r.from, err, got)
				}
			} else if _, err := r.call(s, id, r.who, s2T0.Add(time.Second)); err == nil {
				t.Fatalf("%s from %s was accepted", move.Via[0], r.from)
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
	for key := range refusals {
		t.Errorf("refusal row %s matches no row", key)
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
