package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

const mergeTrainRebindUsage = `amq-squad merge-train rebind - the lead's decision to re-bind a task to the head the train's unrecorded update produced

Usage:
  amq-squad merge-train rebind --pr N --from SHA --to SHA [--root DIR | --project P --session S] [--json]

A recorded lead decision (brief 3.2.0-3a item 6), in the family of task
widen: it takes no --me and goes over the scheduler's decision socket, which
proves the sender's process descends from the lead's pane; from a seat's
process it is refused (not_lead). The scheduler applies it only on the
train's own proof: exactly one force push on the PR from the bound head
(--from) to the named head (--to); the named head's first parent is a base
tip the train rebased onto (a journaled update request's base, a real run's
batch base that listed the PR at the bound head, or the base's current tip);
the patch-id of the bound head against that base equals the named head's.
Then the audit log gets the rebase and rebind entries and the task's binding
is written; the decision log carries one line. Any failing part refuses the
re-bind and nothing is written. Refusals:
  not_lead              the sender does not descend from the lead's pane
  origin_unknown        the sender's origin cannot be read
  rebind_invalid        not a PR number and two full head SHAs
  rebind_unbound        no one task is bound to the PR at --from
  rebind_head_mismatch  the PR's head is not --to
  rebind_unproved       a part of the proof failed (the detail names it)
  binding_failed        the proof held and the binding write failed
Exit 15: refused.
`

// Seams for tests: the socket send, the forge the scheduler proves with
// and the train's re-bind.
var (
	mergeTrainRebindSend  = schedproc.SendRebind
	mergeTrainRebindForge = func() mergetrain.Forge { return &mergetrain.GH{} }
	mergeTrainRebind      = mergetrain.Rebind
)

func runMergeTrainRebind(args []string, out io.Writer) error {
	f := newStoreFlags("merge-train rebind")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, mergeTrainRebindUsage) }
	pr := f.fs.Int("pr", 0, "the PR whose task binding moves")
	from := f.fs.String("from", "", "the bound head (pr_head of the task)")
	to := f.fs.String("to", "", "the head the train's unrecorded update produced (the PR's head)")
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, mergeTrainRebindUsage)
		return nil
	}
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("merge-train rebind: takes no arguments")
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("merge-train rebind: takes no --me; the scheduler proves the lead from the process, never from a flag")
	}
	if *pr < 1 || strings.TrimSpace(*from) == "" || strings.TrimSpace(*to) == "" {
		return usageErrorf("merge-train rebind: --pr, --from and --to are required")
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil {
		return fmt.Errorf("merge-train rebind: no running scheduler for %s (the re-bind is the scheduler's to apply): %v", root, err)
	}
	req := schedproc.RebindRequest{PR: *pr, From: strings.ToLower(strings.TrimSpace(*from)), To: strings.ToLower(strings.TrimSpace(*to))}
	var reply schedproc.DecideReply
	if rec.DecisionSocket == "" {
		reply = schedproc.DecideReply{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits ledger is loaded and every seat of the previous incarnation is closed and confirmed gone"}
	} else if reply, err = mergeTrainRebindSend(root, rec.DecisionSocket, req); err != nil {
		return fmt.Errorf("merge-train rebind: the scheduler's decision socket: %w", err)
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "merge_train_rebind", reply); err != nil {
			return err
		}
	} else if reply.OK {
		var res mergetrain.RebindResult
		_ = json.Unmarshal(reply.Data, &res)
		fmt.Fprintf(out, "merge-train rebind: task %s re-bound to PR #%d at %s (from %s)\n\tfirst parent %s is %s\n\tpatch-id %s at both heads\n\tforce push %s\n\taudit run %s\n",
			res.Task, res.PR, shortSHA(res.To), shortSHA(res.From), shortSHA(res.Parent), res.ParentIs, res.PatchID, res.Push, res.Run)
	}
	if !reply.OK {
		return &codedExitError{code: ExitCommitRefused, msg: rebindReplyRefusal(*pr, reply)}
	}
	return nil
}

// rebindReplyRefusal is the verb's refusal: the plain line above the rule,
// the code and the detail below it (brief 3.1.1-3's form).
func rebindReplyRefusal(pr int, reply schedproc.DecideReply) string {
	plain := mergetrain.RebindPlain(reply.Code)
	switch reply.Code {
	case schedproc.RefusedNotLead, schedproc.RefusedOriginUnknown, schedproc.RefusedStarting, schedproc.RefusedBusy:
		plain = widenReplyPlain(reply.Code)
	case schedproc.RefusedRebindUnsupported:
		plain = "this scheduler applies no merge-train re-bind"
	}
	return fmt.Sprintf("merge-train rebind #%d was refused: %s\n\n%s\nrefused (%s): %s", pr, plain, gatestore.Rule, reply.Code, reply.Detail)
}

// rebindDecision is the scheduler's apply step for a re-bind its socket
// proved to come from the lead's pane: the train's proof and writes
// (mergetrain.Rebind) over the owner's gh, then one decision line either
// way.
func rebindDecision(sel storeSelection, lead string, req schedproc.RebindRequest) schedproc.DecideReply {
	now := taskNow()
	res, err := mergeTrainRebind(context.Background(), mergetrain.RebindInput{Forge: mergeTrainRebindForge(), Audit: mergetrain.AuditFor(sel.root),
		Tasks: sel.store, Binder: mergetrain.StoreBinder{Store: sel.store, Now: taskNow}, PR: req.PR, From: req.From, To: req.To, Lead: lead, Now: taskNow})
	if err != nil {
		code := mergetrain.RefusedRebindFailed
		var refusal *mergetrain.RebindRefusal
		if errors.As(err, &refusal) {
			code = refusal.Code
		}
		_ = schedproc.AppendDecision(sel.root, now, scheduler.Decision{Kind: "merge_train_rebind_refused", Seat: lead, ReasonCode: code,
			Reason: fmt.Sprintf("PR #%d from %s to %s: %v", req.PR, shortSHA(req.From), shortSHA(req.To), err)})
		return schedproc.DecideReply{Code: code, Detail: err.Error()}
	}
	raw, _ := json.Marshal(res)
	if err := schedproc.AppendDecision(sel.root, now, scheduler.Decision{Kind: "merge_train_rebound", TaskID: res.Task, Seat: lead,
		Reason: fmt.Sprintf("PR #%d re-bound from %s to %s by the lead's decision; first parent %s is %s; patch-id %s; force push %s; audit run %s", res.PR, shortSHA(res.From), shortSHA(res.To), shortSHA(res.Parent), res.ParentIs, res.PatchID, res.Push, res.Run)}); err != nil {
		return schedproc.DecideReply{Code: "rebind_record_failed", Detail: "the binding is written; the decision line was not: " + err.Error(), Data: raw}
	}
	return schedproc.DecideReply{OK: true, Data: raw}
}
