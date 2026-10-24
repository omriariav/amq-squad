package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

const mergeTrainUsage = `amq-squad merge-train - land the lead's ordered PR set under the merge grant

Usage:
  amq-squad merge-train --request FILE|- [--dry-run] [--manual] [--repo-root DIR] [--gh PATH]
                        [--project DIR] [--profile P] [--session S] [--me HANDLE] [--json]
  amq-squad merge-train --request FILE --dry-run --root DIR [--grants FILE] [--json]

The request is the lead's ordered set (schema 1):
  {"schema":1,"repository":"owner/name","base":"main",
   "prs":[{"number":12,"task":"t-3","declared_files":["cmd/x.go"]}],
   "required_checks":["ci"]}
In a dry run a PR with a task and no declared_files takes them from the session's
task store. A real run's request names PR numbers only: each PR's task, declared
files and pushed head come from the task the harness commit step bound to it
(pr_number, pr_head); a PR with no bound task is refused (task_unbound).

Per PR the Go core checks the merge grant in ~/.amq-squad/grants.yaml
(repository, base branch, CI green, size cap, no force push, no protected-path
change) and the task scope. A refused PR merges nothing and raises an
owner-signed gate (gate status shows it). The batch digest over the ordered
(PR, head SHA) pairs plus the base SHA goes to the session's audit log
(extensions/amq-squad/mergetrain/audit.jsonl); nobody signs it. A real run
needs server branch protection that requires CI and refuses direct push, and
runs under the owner's gh outside any seat. --dry-run plans, orders and
checks, and merges and raises nothing. Manual mode (--manual, or mode manual
in the grant) stops before every merge.

Exit codes: 0 everything planned or landed, 1 a refusal, a stop or an error.
`

func runMergeTrain(args []string) error {
	g := newGateStoreFlags("merge-train", mergeTrainUsage)
	reqPath := g.fs.String("request", "", "the lead's ordered PR set (file or -)")
	dry := g.fs.Bool("dry-run", false, "plan, order and check; merge and raise nothing")
	manual := g.fs.Bool("manual", false, "stop before every merge (manual mode)")
	repoRoot := g.fs.String("repo-root", "", "repository root whose squad.yaml narrows the grant")
	ghBin := g.fs.String("gh", "gh", "gh executable (the owner's identity)")
	auditRoot := g.fs.String("root", "", "dry run only: the session root for the audit log, without resolving a session")
	grantsFile := g.fs.String("grants", "", "dry run only: a proposed owner grants file in place of ~/.amq-squad/grants.yaml")
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, mergeTrainUsage)
		return nil
	}
	var sel contextResolution
	if err := parseFlags(g.fs, args); err != nil {
		return err
	}
	req, err := readMergeTrainRequest(*reqPath, os.Stdin)
	if err != nil {
		return usageErrorf("merge-train: %v", err)
	}
	if !*dry {
		if err := realRunScope(req); err != nil {
			return err
		}
	}
	if *auditRoot != "" {
		if !*dry {
			return usageErrorf("merge-train: --root is for a dry run; a real run resolves its session (gates bind its namespace)")
		}
		sel = contextResolution{Root: *auditRoot, Handle: firstOf(*g.me, "lead")}
	} else {
		resolved, err := resolveGateRaiseContext(*g.project, *g.profile, *g.session, *g.me, g.fs)
		if err != nil {
			return err
		}
		emitContextDiagnostics(resolved)
		sel = resolved
	}
	if *dry {
		if err := fillDeclaredFiles(sel.Root, &req); err != nil {
			return err
		}
	} else if err := mergetrain.BindTasks(task.DirFor(sel.Root), &req); err != nil {
		return fmt.Errorf("merge-train: %w", err)
	}
	if *grantsFile != "" && !*dry {
		return usageErrorf("merge-train: --grants is for a dry run; a real run reads the owner's grants file only")
	}
	ownerFile := grants.OwnerPath()
	if *grantsFile != "" {
		ownerFile = *grantsFile
	}
	resolved, err := grants.LoadFrom(ownerFile, *repoRoot)
	if err != nil {
		return fmt.Errorf("merge-train: grants_invalid: %w", err)
	}
	gh := &mergetrain.GH{Bin: *ghBin}
	t := &mergetrain.Train{Forge: gh, Grants: resolved, Audit: mergetrain.AuditFor(sel.Root), Lead: firstOf(sel.Handle, "lead"),
		DryRun: *dry, Manual: *manual || resolved.Mode == grants.ModeManual, InSeat: os.Getenv("AMQ_SQUAD_SEAT") != ""}
	t.Confirm = func(s mergetrain.Step) bool { return confirmMerge(s, os.Stdin, os.Stderr) }
	if gs, err := gatestore.Open(sel.Root); err == nil {
		t.ReleaseGates, t.Root = &gs, sel.Root // the tag and release drift check, every run
	}
	ctx := context.Background()
	if !*dry {
		attachMergeGates(ctx, t, sel)
	}
	res, runErr := t.Run(ctx, req)
	if *g.jsonOut {
		if err := printJSONEnvelope("merge_train", mergeTrainData{Result: res, GHCalls: gh.Calls, Error: errString(runErr)}); err != nil {
			return err
		}
	} else {
		printMergeTrain(os.Stdout, res)
	}
	switch {
	case runErr != nil:
		return fmt.Errorf("merge-train: %w", runErr)
	case len(res.Refused()) > 0 && !*dry:
		return fmt.Errorf("merge-train: %d PR(s) refused: %v (gates raised; the lead decides)", len(res.Refused()), res.Refused())
	case res.Stopped != "":
		return fmt.Errorf("merge-train: %s", res.Stopped)
	}
	return nil
}

type mergeTrainData struct {
	mergetrain.Result
	GHCalls int    `json:"gh_calls"`
	Error   string `json:"error,omitempty"`
}

func readMergeTrainRequest(path string, stdin io.Reader) (mergetrain.Request, error) {
	var raw []byte
	var err error
	switch path {
	case "":
		return mergetrain.Request{}, fmt.Errorf("--request is required")
	case "-":
		raw, err = io.ReadAll(io.LimitReader(stdin, 1<<20))
	default:
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return mergetrain.Request{}, err
	}
	var req mergetrain.Request
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("request: %w", err)
	}
	return req, req.Validate()
}

// realRunScope (P1-4): a real run's request names PR numbers only. The task,
// its declared files and its pushed head come from the task bound to the PR
// by the harness commit step (mergetrain.BindTasks); a request-supplied task
// or file set is a dry-run feature (the S6 dry run reads S6-tasklist.md).
func realRunScope(req mergetrain.Request) error {
	for _, it := range req.PRs {
		switch {
		case len(it.DeclaredFiles) > 0:
			return usageErrorf("merge-train: PR #%d carries declared_files; a real run takes them only from the task bound to the PR (declared_files in a request is for --dry-run)", it.Number)
		case strings.TrimSpace(it.Task) != "":
			return usageErrorf("merge-train: PR #%d names task %s; a real run reads the task bound to the PR by the harness, the request names PR numbers only", it.Number, it.Task)
		}
	}
	return nil
}

// fillDeclaredFiles takes a PR's declared files from the session's task
// store when the request names a task and no files (the commit step's set).
func fillDeclaredFiles(root string, req *mergetrain.Request) error {
	var store *task.Store
	for i := range req.PRs {
		it := &req.PRs[i]
		if it.Task == "" || len(it.DeclaredFiles) > 0 {
			continue
		}
		if store == nil {
			s, err := task.Open(root)
			if err != nil {
				return fmt.Errorf("merge-train: task store: %w", err)
			}
			store = &s
		}
		tk, err := store.Get(it.Task)
		if err != nil {
			return fmt.Errorf("merge-train: PR #%d task %s: %w", it.Number, it.Task, err)
		}
		it.DeclaredFiles = tk.DeclaredFiles
	}
	return nil
}

// attachMergeGates wires the refusal escalation through 3-1's core (record,
// then question) and the accepted-gate lookup through the full owner check.
// Without a binding the refusals are still refused, with gate_error set.
func attachMergeGates(ctx context.Context, t *mergetrain.Train, sel contextResolution) {
	core, err := gateCore(ctx, sel, true)
	if core == nil {
		return
	}
	esc := mergetrain.StoreEscalator{Store: core.Store, Identity: core.Identity(), Now: hookNow, Squad: mergetrain.SquadOf(sel.Root)}
	if err == nil {
		esc.Send = core.QuestionSender(ctx)
	}
	t.Escalate = esc
	trust, terr := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if terr != nil {
		return
	}
	v := gateverify.Verifier{Trust: trust, Pin: core.Pin, Namespace: core.Namespace}
	t.Gates = mergetrain.InSquad(mergetrain.SquadOf(sel.Root), func(action, target string) (gatestore.Record, bool) {
		return v.AcceptedFor(core.Store, action, target)
	})
}

// confirmMerge is manual mode's per-merge stop: one "y" line on a terminal
// merges the next PR; anything else, or no terminal, stops the train.
func confirmMerge(s mergetrain.Step, in *os.File, out io.Writer) bool {
	if st, err := in.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Fprintf(out, "merge #%d at %s (batch %s)? [y/N] ", s.Number, s.Head, s.Digest)
	var answer string
	_, _ = fmt.Fscanln(in, &answer)
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}

func printMergeTrain(w io.Writer, r mergetrain.Result) {
	mode := "run"
	if r.DryRun {
		mode = "dry run"
	}
	fmt.Fprintf(w, "merge train %s (%s) on %s %s at %s\n", r.Run, mode, r.Repository, r.Base, r.BaseSHA)
	fmt.Fprintf(w, "branch protection met: %v (required checks %v, direct push refused %v) %s\n",
		r.ProtectionMet, r.Protection.Checks, r.Protection.DirectPushRefused, strings.Join(r.Protection.Notes, "; "))
	for _, p := range r.PRs {
		line := fmt.Sprintf("#%d\t%s\t%s\t%d lines\t%s", p.Number, orDash(p.Task), shortSHA(p.Head), p.Changed, p.Status)
		if p.RebaseForCI {
			line += "\trebase for CI (no required check ran at this head; its base is behind)"
		}
		if len(p.After) > 0 {
			line += fmt.Sprintf("\tafter %v (%s)", p.After, strings.Join(p.Overlap, ","))
		}
		for _, f := range p.Refusals {
			line += "\n\trefused " + f.Rule + ": " + f.Detail
		}
		if p.GateID != "" || p.GateError != "" {
			line += "\n\tgate " + orDash(p.GateID) + " " + p.GateError
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "batch digest %s over %d PR(s); %d refused; wall %d ms\n", r.Digest, len(r.Pairs), len(r.Refused()), r.WallMS)
	if r.Stopped != "" {
		fmt.Fprintln(w, "stopped: "+r.Stopped)
	}
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
