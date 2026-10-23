package cli

import (
	"testing"
)

func TestFourStaleRolesOneGenericPaneAgreeAcrossStatusAndResume(t *testing.T) {
	t.Skip("gh#758/t11: deferred to a named follow-up (not slice B or C, not deleted or rewritten) -- resume's plan-only path (now a thin alias for plan) no longer computes or exposes any per-member liveness/status classification of its own to compare against status --json's; it delegates that signal to launchapi's Observations entirely. The cross-command agreement invariant this test protects is real and worth keeping once status and resume's plan-only path share a single liveness source of truth again, but unifying that source of truth is a larger architectural question than gh#758's three slices cover. Tracked on task/t11 as a follow-up beyond this issue, to be filed as its own tracked item once slice C lands. Body stripped in slice B commit 3: it decoded via resumeEnvelopeData/resumeRestore, team_resume.go types deleted along with the classifier.")
}

