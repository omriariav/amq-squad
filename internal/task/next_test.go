package task

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// S3-1 (3.3.0): Next's rows. The expected lists are test data written from
// the declared moves and the store's checks.

var nextNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

const nextDispatch = "dispatch checks more than the task record shows: its dependencies completed with their PRs merged at the bound head, no active task holding its files, and the named seat free, cleaned, allowed to run the task's class, and given the owner grant's verifier command for a class in the verifier gate"
const nextWiden = "the owner's grant must be readable and allow the added paths, which must be refused paths of the task, valid, and not overlap another active task's declared files; a widening past a grant limit needs the owner's growth gate (the command escalates the task to the owner); a re-armed commit must pass the commit step's check again"
const nextSelf = "; the widening handle must not hold or have reported the task"

func nextLive(holder string) *Lease {
	c := nextNow.Add(-time.Minute)
	return &Lease{Holder: holder, ExpiresAt: nextNow.Add(10 * time.Minute), ClaimedAt: &c}
}

func TestNextRows(t *testing.T) {
	holder, lead := NextCaller{Roles: []string{NextHolder}, Handle: "seat-a"}, NextCaller{Roles: []string{NextLead}, Handle: "cto"}
	expired := nextLive("seat-a")
	expired.ExpiresAt = nextNow.Add(-time.Second)
	cases := []struct {
		name   string
		task   Task
		caller NextCaller
		want   []NextEntry
	}{
		{"pending, every role", Task{State: StatePending}, NextCaller{}, []NextEntry{
			{Verb: "dispatch --task", Who: NextLead, MayRefuse: nextDispatch}, {Verb: "task block", Who: NextLead}, {Verb: "task cancel", Who: NextLead}}},
		{"pending, the holder role has nothing", Task{State: StatePending}, holder, []NextEntry{}},
		{"in progress, every role", Task{State: StateInProgress, Lease: nextLive("seat-a")}, NextCaller{}, []NextEntry{
			{Verb: "task block", Who: NextHolder}, {Verb: "task done", Who: NextHolder}, {Verb: "task fail", Who: NextHolder}, {Verb: "task release", Who: NextHolder},
			{Verb: "task cancel", Who: NextLead}, {Verb: "task release", Who: NextLead}}},
		{"in progress, the holder", Task{State: StateInProgress, Lease: nextLive("seat-a")}, holder, []NextEntry{
			{Verb: "task block", Who: NextHolder}, {Verb: "task done", Who: NextHolder}, {Verb: "task fail", Who: NextHolder}, {Verb: "task release", Who: NextHolder}}},
		{"in progress, a handle that does not hold it", Task{State: StateInProgress, Lease: nextLive("seat-b")}, holder, []NextEntry{}},
		{"in progress, the lead", Task{State: StateInProgress, Lease: nextLive("seat-a")}, lead, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task release", Who: NextLead}}},
		{"in progress, a done report filed: no done", Task{State: StateInProgress, Lease: nextLive("seat-a"), Report: &Report{By: "seat-a"}}, holder, []NextEntry{
			{Verb: "task block", Who: NextHolder}, {Verb: "task fail", Who: NextHolder}, {Verb: "task release", Who: NextHolder}}},
		{"in progress, the lease expired: the holder can only release", Task{State: StateInProgress, Lease: expired}, holder, []NextEntry{
			{Verb: "task release", Who: NextHolder}}},
		{"blocked for the lead, created by cto, every role", Task{State: StateBlocked, BlockReason: "x", BlockRoute: RouteLead, CreatedBy: "cto"}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead, MayRefuse: "only its creator cto resets it"}}},
		{"blocked for the lead, its creator", Task{State: StateBlocked, BlockReason: "x", BlockRoute: RouteLead, CreatedBy: "cto"}, lead, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"blocked for the lead, another lead handle", Task{State: StateBlocked, BlockReason: "x", BlockRoute: RouteLead, CreatedBy: "lead-b"}, lead, []NextEntry{
			{Verb: "task cancel", Who: NextLead}}},
		{"blocked by a commit refusal (a report)", Task{State: StateBlocked, BlockReason: "outside_declared_files", BlockRoute: RouteLead, Report: &Report{By: "seat-a"}, RefusedPaths: []string{"b.go"}}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}, {Verb: "task widen", Who: NextLead, MayRefuse: nextWiden + nextSelf}}},
		{"blocked by a commit refusal, the lead named", Task{State: StateBlocked, BlockReason: "outside_declared_files", BlockRoute: RouteLead, Report: &Report{By: "seat-a"}, RefusedPaths: []string{"b.go"}}, lead, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}, {Verb: "task widen", Who: NextLead, MayRefuse: nextWiden}}},
		{"blocked by a commit refusal the caller reported: no widen (self_widen)", Task{State: StateBlocked, BlockReason: "outside_declared_files", BlockRoute: RouteLead, Report: &Report{By: "cto"}, RefusedPaths: []string{"b.go"}}, lead, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"blocked on verdict_missing with a report: no widen (widen_not_requested)", Task{State: StateBlocked, BlockReason: BlockVerdictMissing, BlockRoute: RouteLead, Report: &Report{By: "seat-a"}}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"blocked on a protected path with its report", Task{State: StateBlocked, BlockReason: "protected_path", BlockRoute: RouteLead, Report: &Report{By: "seat-a"}}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}, {Verb: "task widen", Who: NextLead, MayRefuse: nextWiden + nextSelf}}},
		{"outside_declared_files with no refused paths: no widen (widen_paths_missing)", Task{State: StateBlocked, BlockReason: "outside_declared_files", BlockRoute: RouteLead, Report: &Report{By: "seat-a"}}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"a typed widen request", Task{State: StateBlocked, BlockReason: ReasonWidenRequested, BlockRoute: RouteLead, RefusedPaths: []string{"b.go"}, BlockAttempt: 1}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}, {Verb: "task widen", Who: NextLead, MayRefuse: nextWiden + nextSelf}}},
		{"a typed request with no bound attempt: no widen", Task{State: StateBlocked, BlockReason: ReasonWidenRequested, BlockRoute: RouteLead, RefusedPaths: []string{"b.go"}}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"blocked for the owner's growth gate", Task{State: StateBlocked, BlockReason: ReasonGrowthGateRequired, BlockRoute: RouteOwner, RefusedPaths: []string{"b.go"}, BlockAttempt: 1}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task widen", Who: NextLead, MayRefuse: nextWiden + nextSelf + "; the owner's growth gate must be accepted first"},
			{Verb: "answer through Buzz", Who: NextOwner}}},
		{"blocked for the lead with no report and no typed request: no widen", Task{State: StateBlocked, BlockReason: "x", BlockRoute: RouteLead}, NextCaller{}, []NextEntry{
			{Verb: "task cancel", Who: NextLead}, {Verb: "task reset", Who: NextLead}}},
		{"completed", Task{State: StateCompleted}, NextCaller{}, []NextEntry{}},
		{"cancelled", Task{State: StateCancelled}, NextCaller{}, []NextEntry{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := json.Marshal(c.task)
			got := Next(c.task, nextNow, c.caller)
			if got == nil {
				t.Fatal("Next returned nil, want a list")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Next = %+v\nwant   %+v", got, c.want)
			}
			if after, _ := json.Marshal(c.task); string(after) != string(before) {
				t.Fatal("Next changed the task")
			}
		})
	}
}

// Every method of nextMethods is a Via of a declared move, and no method a
// row lists only for the scheduler or the harness is in it: such a method
// never yields an entry.
func TestNextNamesOnlyMethodsALeadHolderOrOwnerRuns(t *testing.T) {
	via := map[string]bool{}
	onlyScheduler := map[string]bool{}
	for _, move := range Moves() {
		for _, method := range move.Via {
			via[method] = true
			machine := true
			for _, who := range move.Who {
				if who != WhoScheduler && who != WhoHarnessShell {
					machine = false
				}
			}
			if machine {
				onlyScheduler[method] = true
			}
		}
	}
	for method, m := range nextMethods {
		if !via[method] {
			t.Errorf("nextMethods names %s, which is the Via of no declared move", method)
		}
		if onlyScheduler[method] {
			t.Errorf("nextMethods names %s, which only the scheduler or the harness runs", method)
		}
		for _, role := range m.asserted {
			if role != NextLead && role != NextHolder {
				t.Errorf("%s: asserted_handle stands for %q", method, role)
			}
		}
		if !strings.HasPrefix(m.verb, "task ") && m.verb != "dispatch --task" {
			t.Errorf("%s: verb %q is no command", method, m.verb)
		}
	}
	// a method some row lists only for the scheduler or the harness is
	// theirs where a row shares its Who with others (Complete and
	// CompleteBound beside Done)
	for _, move := range Moves() {
		for _, method := range move.Via {
			if _, ok := nextMethods[method]; ok || onlyScheduler[method] {
				continue
			}
			for _, who := range move.Who {
				if who == WhoLiveHolder || who == WhoProvenLead || who == WhoCreator {
					t.Errorf("%s (%s to %s) is run by %s and has no next entry", method, move.From, move.To, who)
				}
			}
		}
	}
}

// next is beside the task, never in it: the stored record has no next.
func TestTheTaskRecordCarriesNoNext(t *testing.T) {
	raw, err := json.Marshal(Task{State: StatePending})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"next"`) {
		t.Fatalf("the task record carries next: %s", raw)
	}
	if _, ok := reflect.TypeOf(Task{}).FieldByName("Next"); ok {
		t.Fatal("task.Task has a Next field")
	}
}
