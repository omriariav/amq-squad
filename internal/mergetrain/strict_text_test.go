package mergetrain_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/cli"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

func TestStrictPlanOwnerTextIsWordsFirst(t *testing.T) {
	head := strings.Repeat("a", 40)
	result := mergetrain.Result{
		Run: "run", Repository: "owner/repo", Base: "main", BaseSHA: strings.Repeat("b", 40), DryRun: true,
		Protection: mergetrain.Protection{Strict: true},
		PRs: []mergetrain.PlanPR{
			{Number: 1, Head: head, Status: "planned", RebaseForCI: true, RebaseRule: mergetrain.RuleStrictUpToDate, RebaseReason: "GitHub requires the branch to be up to date before merging"},
			{Number: 2, Head: head, Status: "refused", Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleStrictUpToDate, Detail: "GitHub requires the branch to be up to date before merging, and the train could not bring it up to date. No gate overrides GitHub's rule. Run the train again; if it repeats, the lead closes the PR and redoes the task on the current base. (the PR conflicts with the current base)"}}},
		},
	}
	text := cli.FormatMergeTrain(result)
	for _, want := range []string{
		"GitHub requires each branch to be up to date, so every PR after the first is brought up to date and waits for its checks (1 more check run)",
		"GitHub requires the branch to be up to date before merging; the train brings it up to date and waits for CI",
		"refused: GitHub requires the branch to be up to date before merging, and the train could not bring it up to date. No gate overrides GitHub's rule. Run the train again; if it repeats, the lead closes the PR and redoes the task on the current base. (the PR conflicts with the current base)",
		"rule: " + mergetrain.RuleStrictUpToDate,
		"head: " + head,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("strict plan text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "no required check ran at this head") {
		t.Fatalf("strict rebase used the absent-CI text:\n%s", text)
	}
	code := regexp.MustCompile(`[0-9a-f]{8,}|strict_required_status_checks_policy`)
	for _, line := range strings.Split(text, "\n") {
		if (strings.Contains(line, "GitHub requires") || strings.Contains(line, "refused: the branch")) && code.MatchString(line) {
			t.Fatalf("owner line puts code above its rule: %q", line)
		}
	}
	raw, err := json.Marshal(result.PRs)
	if err != nil || !strings.Contains(string(raw), `"rebase_rule":"strict_required_status_checks_policy"`) || !strings.Contains(string(raw), `"rule":"strict_required_status_checks_policy"`) {
		t.Fatalf("strict JSON lost its machine code: %v %s", err, raw)
	}
}

func TestChangedPatchRefusalTextPutsProofBelowRule(t *testing.T) {
	proof := &mergetrain.PatchProof{Required: true, OldHead: strings.Repeat("a", 40), NewHead: strings.Repeat("b", 40), OldID: strings.Repeat("c", 40), NewID: strings.Repeat("d", 40)}
	result := mergetrain.Result{Run: "run", Repository: "o/r", Base: "main", PRs: []mergetrain.PlanPR{{
		Number: 1, Head: proof.NewHead, Status: "refused", PatchProof: proof,
		Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleRebasePatch, Detail: "its change moved during the update"}},
	}}}
	text := cli.FormatMergeTrain(result)
	want := "#1 was not merged: its change moved during the update\n\trule: rebase_patch_changed\n\told head: " + proof.OldHead + "\n\tnew head: " + proof.NewHead + "\n\told patch-id: " + proof.OldID + "\n\tnew patch-id: " + proof.NewID
	if text != "merge train run (run) on o/r main at \nbranch protection met: false (required checks [], direct push refused false) \n"+want+"\nbatch digest  over 0 PR(s); 1 refused; wall 0 ms\n" {
		t.Fatalf("patch proof is not words-first and below its rule:\n%s", text)
	}
	above, _, _ := strings.Cut(text, "\n\trule:")
	if regexp.MustCompile(`[0-9a-f]{8,}|rebase_patch_changed`).MatchString(strings.Split(above, "\n")[2]) {
		t.Fatalf("patch refusal summary carries a code or head: %q", strings.Split(above, "\n")[2])
	}
}

func TestUnliftableRefusalsPrintTheActionableWordsWithoutAGate(t *testing.T) {
	strict := "GitHub requires the branch to be up to date before merging, and the train could not bring it up to date. No gate overrides GitHub's rule. Run the train again; if it repeats, the lead closes the PR and redoes the task on the current base."
	conflict := "The branch conflicts with the base, so the train cannot update it. No gate overrides GitHub's rule. The lead closes the PR and redoes the task on the current base (a seat never merges the base by hand)."
	merged := "PR #3 was already merged at another head (merge abc123, bound head def456); nothing to land; the lead checks task t3's record against what merged."
	result := mergetrain.Result{Run: "run", Repository: "o/r", Base: "main", PRs: []mergetrain.PlanPR{
		{Number: 1, Head: "h1", Status: "refused", Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleStrictUpToDate, Detail: strict}}},
		{Number: 2, Head: "h2", Status: "refused", Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleUpdateConflict, Detail: conflict}}},
		{Number: 3, Task: "t3", Head: "h3", Status: "refused", Refusals: []mergetrain.Refusal{{Rule: mergetrain.RulePRState, Detail: merged}}},
	}}
	text := cli.FormatMergeTrain(result)
	for _, want := range []string{strict, conflict, merged} {
		if !strings.Contains(text, want) {
			t.Fatalf("owner text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\n\tgate ") {
		t.Fatalf("unliftable refusal printed a gate:\n%s", text)
	}
}
