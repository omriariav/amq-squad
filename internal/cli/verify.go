package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/ledger"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/releaseflow"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func runVerify(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad verify - deterministic preflight and evidence checks

Usage:
  amq-squad verify action --project DIR --session S --gate TOPIC --action KIND --target TARGET [--json]
  amq-squad verify authorization --file FILE --action KIND --target TARGET --trust-store FILE [--json]
  amq-squad verify rebind --project DIR --old-head A --new-head B [options]
  amq-squad verify merge --repository OWNER/NAME --pr N [--task ID] [--root DIR] [--json]
  amq-squad verify release --tag TAG [--task ID] [--root DIR] [--json]
  amq-squad verify release-plan --repository OWNER/REPO --branch BRANCH --head SHA --version VERSION [options]

action and authorization check a gate's answer for one action and target.
merge and release read the evidence 3.0 keeps, never a v2 evidence file: the
merge train's audit log (the batch digest, the rules check, the merge under
the owner's identity), the release flow's records and gates, and, with
--task, the task's hook ledger lines (its lease holders' lines inside their
lease windows) and its outcome log rows, redacted. None of these commands
merges, pushes, tags or releases. Failed evidence prints the failed
conditions and exits non-zero.
`)
		if len(args) == 0 {
			return usageErrorf("verify requires a subcommand (action, authorization, rebind, merge, or release)")
		}
		return nil
	}
	switch args[0] {
	case "action":
		return runVerifyAction(args[1:])
	case "authorization":
		return runVerifyAuthorization(args[1:])
	case "rebind":
		return runVerifyRebind(args[1:])
	case "merge", "release":
		return runVerifyEvidence(args[0], args[1:])
	case "release-plan":
		return runVerifyReleasePlan(args[1:])
	default:
		return unknownSubcommandError("verify", args[0], "action", "authorization", "rebind", "merge", "release", "release-plan")
	}
}

type verifyMergeFailure struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// verifyEvidenceResult is the evidence of one merge or one release.
type verifyEvidenceResult struct {
	OK       bool                 `json:"ok"`
	Kind     string               `json:"kind"` // merge | release
	Subject  string               `json:"subject"`
	Audit    []mergetrain.Entry   `json:"audit,omitempty"`
	Release  []releaseflow.Step   `json:"release,omitempty"`
	Ledger   []ledger.TaskLine    `json:"ledger,omitempty"`
	Outcomes []json.RawMessage    `json:"outcomes,omitempty"`
	Failures []verifyMergeFailure `json:"failures,omitempty"`
}

func runVerifyEvidence(kind string, args []string) error {
	fs := flag.NewFlagSet("verify "+kind, flag.ContinueOnError)
	repo := fs.String("repository", "", "owner/name (merge)")
	pr := fs.Int("pr", 0, "PR number (merge)")
	tag := fs.String("tag", "", "release tag (release)")
	taskID := fs.String("task", "", "task whose ledger and outcome rows to include")
	root := fs.String("root", "", "session root (default AM_ROOT)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	r := firstOf(strings.TrimSpace(*root), strings.TrimSpace(os.Getenv("AM_ROOT")))
	if r == "" || !filepath.IsAbs(r) {
		return usageErrorf("verify %s: the session root must be absolute (--root or AM_ROOT)", kind)
	}
	var res verifyEvidenceResult
	var err error
	if kind == "merge" {
		if *repo == "" || *pr < 1 {
			return usageErrorf("verify merge: --repository and --pr are required")
		}
		res, err = mergeEvidence(r, *repo, *pr)
	} else {
		if *tag == "" {
			return usageErrorf("verify release: --tag is required")
		}
		res, err = releaseEvidence(r, *tag)
	}
	if err != nil {
		return fmt.Errorf("verify %s: %w", kind, err)
	}
	if *taskID != "" {
		store, err := task.Open(r)
		if err != nil {
			return err
		}
		t, err := store.Get(*taskID)
		if err != nil {
			return fmt.Errorf("verify %s: task %s: %w", kind, *taskID, err)
		}
		if res.Ledger, err = ledger.ForTask(r, t, hookNow()); err != nil {
			return err
		}
		if res.Outcomes, err = ledger.Outcomes(r, t.ID); err != nil {
			return err
		}
	}
	if *jsonOut {
		if err := printJSONEnvelope("verify_"+kind, res); err != nil {
			return err
		}
	} else {
		fmt.Printf("%s evidence for %s: ok %v (%d audit entries, %d release steps, %d ledger lines, %d outcome rows)\n",
			kind, res.Subject, res.OK, len(res.Audit), len(res.Release), len(res.Ledger), len(res.Outcomes))
		for _, f := range res.Failures {
			fmt.Printf("- %s: %s\n", f.Code, f.Detail)
		}
	}
	if !res.OK {
		return UsageError(kind + " evidence failed")
	}
	return nil
}

// mergeEvidence: the PR merged through the train when its last audit entry
// is a merge whose head is the head of the last batch digest naming it.
func mergeEvidence(root, repo string, pr int) (verifyEvidenceResult, error) {
	res := verifyEvidenceResult{Kind: "merge", Subject: fmt.Sprintf("%s#%d", repo, pr)}
	entries, err := mergetrain.AuditFor(root).Read()
	if err != nil {
		return res, err
	}
	var batchHead string
	for _, e := range entries {
		if e.Repository != repo {
			continue
		}
		if e.Kind == "batch" && !e.DryRun {
			for _, p := range e.Pairs {
				if p.Number == pr {
					batchHead = p.Head
				}
			}
		}
		if e.PR == pr {
			res.Audit = append(res.Audit, e)
		}
	}
	var last *mergetrain.Entry
	for i := range res.Audit {
		if !res.Audit[i].DryRun {
			last = &res.Audit[i]
		}
	}
	switch {
	case last == nil:
		res.Failures = append(res.Failures, verifyMergeFailure{"no_merge", "the audit log has no real-run entry for this PR"})
	case last.Kind != "merge":
		res.Failures = append(res.Failures, verifyMergeFailure{"not_merged", "the last entry is " + last.Kind + ": " + last.Detail})
	case last.Head != batchHead:
		res.Failures = append(res.Failures, verifyMergeFailure{"digest_mismatch", "the merged head " + last.Head + " is not the head of the last batch digest (" + batchHead + ")"})
	}
	res.OK = len(res.Failures) == 0
	return res, nil
}

// releaseEvidence: both children of the tag's release ran, each after its
// gate's accepted decision.
func releaseEvidence(root, tag string) (verifyEvidenceResult, error) {
	res := verifyEvidenceResult{Kind: "release", Subject: tag}
	gates, err := gatestore.Open(root)
	if err != nil {
		return res, err
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "extensions", "amq-squad", "releases", "*"))
	for _, dir := range dirs {
		var steps []releaseflow.Step
		for _, role := range []string{"tag", "github_release"} {
			raw, err := os.ReadFile(filepath.Join(dir, role+".json"))
			if err != nil {
				continue
			}
			var st releaseflow.Step
			if json.Unmarshal(raw, &st) == nil {
				steps = append(steps, st)
			}
		}
		if len(steps) > 0 && strings.HasPrefix(steps[0].Target, "git tag "+tag+" ") {
			res.Release = steps
		}
	}
	if len(res.Release) != 2 {
		res.Failures = append(res.Failures, verifyMergeFailure{"not_released", fmt.Sprintf("%d of 2 release steps ran for %s", len(res.Release), tag)})
	}
	for _, st := range res.Release {
		if d := gates.Decision(st.GateID); d != gatestore.StateAccepted {
			res.Failures = append(res.Failures, verifyMergeFailure{"gate_not_accepted", st.Role + " gate " + st.GateID + " is " + d})
		}
	}
	res.OK = len(res.Failures) == 0
	return res, nil
}
