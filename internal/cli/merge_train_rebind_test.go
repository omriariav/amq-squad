package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// rebindRig is the verb's rig: a fresh store, the scheduler's decision
// socket serving rebindDecision (the production apply step) with the sender
// proved as the lead's pane or not, and the train's re-bind replaced by a
// stub that records what it was asked (the proof itself has its rows in
// internal/mergetrain). It returns the root and the stub's calls.
func rebindRig(t *testing.T, fromLead bool, outcome func(mergetrain.RebindInput) (mergetrain.RebindResult, error)) (string, *[]mergetrain.RebindInput) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	sel := storeSelection{store: s, root: root, release: func() {}}
	var calls []mergetrain.RebindInput
	prev, prevForge := mergeTrainRebind, mergeTrainRebindForge
	mergeTrainRebind = func(_ context.Context, in mergetrain.RebindInput) (mergetrain.RebindResult, error) {
		calls = append(calls, in)
		return outcome(in)
	}
	mergeTrainRebindForge = func() mergetrain.Forge { return nil }
	t.Cleanup(func() { mergeTrainRebind, mergeTrainRebindForge = prev, prevForge })
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	srv, err := schedproc.ServeDecisions(schedproc.DecideOptions{Root: root, Lead: "lead",
		Apply: func(string, schedproc.WidenRequest) schedproc.DecideReply {
			t.Fatal("a re-bind reached the widening")
			return schedproc.DecideReply{}
		},
		Rebind:   func(l string, req schedproc.RebindRequest) schedproc.DecideReply { return rebindDecision(sel, l, req) },
		LeadPane: func(string, string) (panechain.Pane, error) { return lead, nil },
		Reaches: func(int, string, []panechain.Pane) (*panechain.Pane, bool) {
			if fromLead {
				return &lead, true
			}
			return nil, true
		},
		StartToken: func(int) (string, bool) { return "s", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, DecisionSocket: srv.Path()}); err != nil {
		t.Fatal(err)
	}
	return root, &calls
}

const (
	rebindFrom = "68792071d942003613da13562ed2c7cb1dd34529"
	rebindTo   = "2555d34aa77084145bd7b3ca58f252c5178ac55a"
)

func rebindDecisions(t *testing.T, root string) []schedproc.DecisionLine {
	t.Helper()
	lines, err := schedproc.ReadDecisionsTail(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []schedproc.DecisionLine
	for _, l := range lines {
		if strings.HasPrefix(l.Kind, "merge_train_rebind") || l.Kind == "merge_train_rebound" {
			out = append(out, l)
		}
	}
	return out
}

// Brief 3.2.0-3a item 6: from a seat's process (the sender does not descend
// from the lead's pane) the re-bind is refused not_lead by the socket before
// the train is asked anything: the refusal's plain line above the rule, the
// code below, exit 15, no decision line.
func TestMergeTrainRebindFromASeatIsNotLead(t *testing.T) {
	root, calls := rebindRig(t, false, func(mergetrain.RebindInput) (mergetrain.RebindResult, error) {
		t.Fatal("the train was asked")
		return mergetrain.RebindResult{}, nil
	})
	_, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"rebind", "--root", root, "--pr", "894", "--from", rebindFrom, "--to", rebindTo})
	})
	var coded *codedExitError
	if !errors.As(err, &coded) || coded.code != ExitCommitRefused {
		t.Fatalf("not a coded refusal: %v", err)
	}
	plain, record := splitWidenRule(t, coded.msg)
	if plain != "merge-train rebind #894 was refused: "+widenReplyPlain(schedproc.RefusedNotLead) || widenLineCode(plain) != "" {
		t.Fatalf("the plain line: %q", plain)
	}
	if !strings.HasPrefix(record, "refused ("+schedproc.RefusedNotLead+"): ") || len(*calls) != 0 || len(rebindDecisions(t, root)) != 0 {
		t.Fatalf("record %q, train asked %d times, decisions %d", record, len(*calls), len(rebindDecisions(t, root)))
	}
}

// From the lead's pane the scheduler asks the train for its proof and the
// writes; the verb prints the proof and the decision log carries one line
// with the task, the heads and the parent.
func TestMergeTrainRebindFromTheLeadIsAppliedAndRecorded(t *testing.T) {
	root, calls := rebindRig(t, true, func(in mergetrain.RebindInput) (mergetrain.RebindResult, error) {
		return mergetrain.RebindResult{Run: "mt-rebind-x", Task: "t88", Repository: "owner/name", Base: "v3", PR: in.PR, From: in.From, To: in.To,
			Parent: "87fb71e9b21b5192385edf39543c8545ef5a278d", ParentIs: "the base tip of run mt-1's batch", PatchID: "p1", Push: in.From + " -> " + in.To + " by owner"}, nil
	})
	out, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"rebind", "--root", root, "--pr", "894", "--from", rebindFrom, "--to", rebindTo})
	})
	if err != nil {
		t.Fatalf("the lead's re-bind was refused: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].PR != 894 || (*calls)[0].From != rebindFrom || (*calls)[0].To != rebindTo || (*calls)[0].Lead != "lead" || (*calls)[0].Audit.Path != mergetrain.AuditFor(root).Path {
		t.Fatalf("the train was asked %+v", *calls)
	}
	for _, want := range []string{"task t88 re-bound to PR #894 at 2555d34aa770 (from 68792071d942)", "first parent 87fb71e9b21b is the base tip of run mt-1's batch", "patch-id p1 at both heads", "audit run mt-rebind-x"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the verb's words lack %q:\n%s", want, out)
		}
	}
	lines := rebindDecisions(t, root)
	if len(lines) != 1 || lines[0].Kind != "merge_train_rebound" || lines[0].TaskID != "t88" || lines[0].Seat != "lead" ||
		!strings.Contains(lines[0].Reason, "PR #894 re-bound from 68792071d942 to 2555d34aa770") || !strings.Contains(lines[0].Reason, "first parent 87fb71e9b21b") {
		t.Fatalf("the decision line: %+v", lines)
	}
}

// A part of the proof that fails is the train's refusal, carried through the
// socket: the verb's plain line names the proof, the code and the detail
// (the failing part) sit below the rule, and the decision log carries one
// refused line with the code.
func TestMergeTrainRebindRefusedOnTheProofNamesThePart(t *testing.T) {
	root, _ := rebindRig(t, true, func(mergetrain.RebindInput) (mergetrain.RebindResult, error) {
		return mergetrain.RebindResult{}, &mergetrain.RebindRefusal{Code: mergetrain.RefusedRebindUnproved, Detail: "the current head's first parent 1111 is not a base tip the update could have landed on (the base's current tip 2222)"}
	})
	_, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"rebind", "--root", root, "--pr", "894", "--from", rebindFrom, "--to", rebindTo})
	})
	var coded *codedExitError
	if !errors.As(err, &coded) || coded.code != ExitCommitRefused {
		t.Fatalf("not a coded refusal: %v", err)
	}
	plain, record := splitWidenRule(t, coded.msg)
	if plain != "merge-train rebind #894 was refused: "+mergetrain.RebindPlain(mergetrain.RefusedRebindUnproved) || widenLineCode(plain) != "" {
		t.Fatalf("the plain line: %q", plain)
	}
	if !strings.HasPrefix(record, "refused ("+mergetrain.RefusedRebindUnproved+"): rebind refused (rebind_unproved): the current head's first parent 1111") {
		t.Fatalf("the record: %q", record)
	}
	lines := rebindDecisions(t, root)
	if len(lines) != 1 || lines[0].Kind != "merge_train_rebind_refused" || lines[0].ReasonCode != mergetrain.RefusedRebindUnproved || !strings.Contains(lines[0].Reason, "first parent 1111") {
		t.Fatalf("the decision line: %+v", lines)
	}
}

// The verb's shape: --me is refused (the scheduler proves the lead from the
// process), the three flags are required, and no scheduler is a plain error.
func TestMergeTrainRebindVerbShape(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"rebind", "--root", root, "--pr", "1", "--from", rebindFrom, "--to", rebindTo, "--me", "lead"},
		{"rebind", "--root", root, "--pr", "1", "--from", rebindFrom},
		{"rebind", "--root", root, "--from", rebindFrom, "--to", rebindTo},
	} {
		var usage UsageError
		if _, _, err := captureOutput(t, func() error { return runMergeTrain(args) }); !errors.As(err, &usage) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	_, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"rebind", "--root", root, "--pr", "1", "--from", rebindFrom, "--to", rebindTo})
	})
	if err == nil || !strings.Contains(err.Error(), "no running scheduler") {
		t.Fatalf("no scheduler: %v", err)
	}
}

// R2's words through the command: a PR refused because GitHub never
// performed the update prints what happened first and the code below the
// rule line.
func TestMergeTrainPrintsTheUnconfirmedUpdateInWords(t *testing.T) {
	res := mergetrain.Result{Run: "mt-x", Repository: "owner/name", Base: "v3", BaseSHA: "87fb71e9", PRs: []mergetrain.PlanPR{{Number: 894, Task: "t88", Head: rebindFrom, Status: "refused",
		Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleUpdateUnconfirmed, Detail: "GitHub did not update the branch of PR #894 within 2m0s (the head did not move); nothing is bound and nothing is merged"}}}}}
	out := FormatMergeTrain(res)
	i, j := strings.Index(out, "#894 was not merged: GitHub did not update the branch of PR #894 within 2m0s"), strings.Index(out, "\trule: "+mergetrain.RuleUpdateUnconfirmed)
	if i < 0 || j < i {
		t.Fatalf("the refusal is not in words with the code below the rule:\n%s", out)
	}
	if first := strings.SplitN(out[i:], "\n", 2)[0]; widenLineCode(strings.TrimPrefix(first, "#894 was not merged: ")) != "" {
		t.Fatalf("a code above the rule: %q", first)
	}
}

// Codex on #903 (5): a refusal whose decision line cannot be written names
// that failure in its detail; the refusal stands.
func TestMergeTrainRebindNamesAnUnwrittenDecisionLine(t *testing.T) {
	root, _ := rebindRig(t, true, func(mergetrain.RebindInput) (mergetrain.RebindResult, error) {
		return mergetrain.RebindResult{}, &mergetrain.RebindRefusal{Code: mergetrain.RefusedRebindUnproved, Detail: "no force push; the bound head was not moved by an update"}
	})
	if err := os.MkdirAll(schedproc.DecisionsPath(root), 0o700); err != nil { // the log's path taken by a directory
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"rebind", "--root", root, "--pr", "894", "--from", rebindFrom, "--to", rebindTo})
	})
	var coded *codedExitError
	if !errors.As(err, &coded) || !strings.Contains(coded.msg, "refused ("+mergetrain.RefusedRebindUnproved+"): ") || !strings.Contains(coded.msg, "the decision line was not written: ") {
		t.Fatalf("the refusal does not name the unwritten decision line: %v", err)
	}
}

// Codex's re-pass on #903 (3): the command runs the train under its bound,
// so a gh that never returns ends the run at the bound, with the words
// above the rule and the code below. A gh stand-in that sleeps, the bound
// at 300 ms, a dry run under a temp root with the default grants.
func TestMergeTrainRunEndsAtItsBoundInWords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0o755); err != nil { // a child holds the pipes past the kill, as a helper of gh's would
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "req.json")
	if err := os.WriteFile(reqPath, []byte(`{"schema":1,"repository":"owner/name","base":"main","prs":[{"number":1,"task":"t-1","declared_files":["a.go"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := mergeTrainRunBound
	mergeTrainRunBound = func(int) time.Duration { return 300 * time.Millisecond }
	t.Cleanup(func() { mergeTrainRunBound = prev })
	start := time.Now()
	_, _, err := captureOutput(t, func() error {
		return runMergeTrain([]string{"--request", reqPath, "--dry-run", "--root", filepath.Join(dir, "root"), "--grants", filepath.Join(dir, "no-grants.yaml"), "--gh", gh})
	})
	if elapsed := time.Since(start); err == nil || elapsed > 5*time.Second {
		t.Fatalf("the run did not end at its bound: %v after %s", err, elapsed)
	}
	plain, record := splitWidenRule(t, err.Error())
	if plain != "merge-train: the run timed out after 300ms, a call to GitHub did not return; nothing more was merged, and the next run starts from the journal" || widenLineCode(plain) != "" {
		t.Fatalf("the plain line: %q", plain)
	}
	if !strings.HasPrefix(record, "refused (run_timeout): read branch protection: ") {
		t.Fatalf("the record: %q", record)
	}
}
