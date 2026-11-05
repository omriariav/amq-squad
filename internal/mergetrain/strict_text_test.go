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
			{Number: 2, Head: head, Status: "refused", Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleStrictUpToDate, Detail: "the branch is behind and the train cannot bring it up to date: the PR conflicts with the current base"}}},
		},
	}
	text := cli.FormatMergeTrain(result)
	for _, want := range []string{
		"GitHub requires each branch to be up to date, so every PR after the first is brought up to date and waits for its checks (1 more check run)",
		"GitHub requires the branch to be up to date before merging; the train brings it up to date and waits for CI",
		"refused: the branch is behind and the train cannot bring it up to date: the PR conflicts with the current base",
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
		Refusals: []mergetrain.Refusal{{Rule: mergetrain.RuleRebasePatch, Detail: "the branch update changed the seat's work"}},
	}}}
	text := cli.FormatMergeTrain(result)
	want := "refused: the branch update changed the seat's work\n\trule: rebase_patch_changed\n\told head: " + proof.OldHead + "\n\tnew head: " + proof.NewHead + "\n\told patch-id: " + proof.OldID + "\n\tnew patch-id: " + proof.NewID
	if !strings.Contains(text, want) {
		t.Fatalf("patch proof is not words-first and below its rule:\n%s", text)
	}
}
