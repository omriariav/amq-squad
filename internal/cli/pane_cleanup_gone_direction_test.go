package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// #577 r10 steps 3-4, CONSUMER SIDE, on the REVALIDATION path.
//
// dev-2's G2, and they were right in a way I should have caught from the artifact itself. My first
// version faked Gone on the FIRST Inspect, which is PreparePaneCleanup's branch. The observed
// failure is not there. The wake-lane log says:
//
//	pane mismatch revalidation_inspection expected="found" actual="unavailable"
//
// "revalidation_inspection" NAMES THE BRANCH. The real sequence is: prepare while the agent is
// still live and the pane is FOUND, signal the agent, the pane root dies, tmux tears down the
// pane/window/session/server, and then ClosePreparedPane's revalidation inspects a pane that is
// now gone. Two separate switches map Gone in this file (PreparePaneCleanup and ClosePreparedPane);
// I pinned the one the bug is not on, and the artifact had already told me which was which.
//
// THE CHAIN, stated so neither half is mistaken for end-to-end coverage:
//
//	internal/tmuxpane  "no server running" stderr -> PaneInspectionGone. Exercises the MARKER.
//	HERE               prepare(Found) -> signal -> revalidate(Gone) -> already_gone, zero closes.
//	                   Exercises the MAPPING on the path that actually failed.
//	wake lanes         the only end-to-end evidence, and the acceptance criterion.
//
// WHY ZERO CLOSES IS LOAD-BEARING: "already_gone" asserts the pane is already gone. If cleanup also
// tried to CLOSE it, the outcome would be right and the behaviour wrong -- signalling a pane just
// declared absent races a reused pane id and can close a pane this launcher does not own. The
// outcome name and the absence of action have to be proven together.
func TestRevalidationGoneBecomesAlreadyGoneWithoutClosing(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	closes := 0

	// FOUND first, GONE on revalidation. That ordering IS the bug's shape: the pane is real when
	// we prepare and gone by the time we revalidate, because our own signal removed it.
	//
	// Built through the package's canonical cleanupDeps, which supplies the valid 100 -> 200 -> 300
	// ancestry graph. My first version hand-rolled a ChildrenIndex returning nil for every PID, so
	// strictDescendant(100, 300) was false, prepared.Ready was false, and BOTH tests died at the
	// Ready assertion before reaching the revalidation branch they exist to prove -- the
	// two-inspection assertion could never have become meaningful. dev-2 found that by reading
	// bytes I had not compiled. Reusing the canonical helper instead of a parallel graph is also
	// the fix for the underlying mistake: a second fixture is a second notion of a valid request.
	deps, inspections := countingCleanupDeps(
		[]tmuxpane.PaneInspection{
			foundPane(fx.pane),
			{
				State: tmuxpane.PaneInspectionGone,
				// The verbatim observed wake-lane detail, so this row traces to the artifact.
				Detail: "no server running on /private/tmp/amq-wake-tmux-b15ec719086b153b4c9c3a7b/tmux-501/default",
			},
		}, &closes)

	prepared := PreparePaneCleanup(fx.req, deps)
	if !prepared.Ready {
		t.Fatalf("preparation must be READY -- this test is about the revalidation branch, and an "+
			"unready preparation would never reach it: %+v", prepared.Result)
	}

	result := ClosePreparedPane(prepared, deps)

	if result.Outcome != PaneCleanupAlreadyGone {
		t.Errorf("revalidation outcome = %q, want %q.\nAfter #577 r10 a stop that tore down the "+
			"session revalidates as proven-Gone, and a gone pane is the SUCCESS case for cleanup. "+
			"Reporting inspection_unavailable here is exactly what produced \"1 pane cleanup(s) were "+
			"not completed\" on all three wake lanes.", result.Outcome, PaneCleanupAlreadyGone)
	}
	if closes != 0 {
		t.Errorf("cleanup issued %d close call(s) for a pane it declared already gone", closes)
	}
	if result.Detail == "" {
		t.Error("the already_gone result must carry the inspection detail: that string is the " +
			"operator's only evidence for WHY the pane was considered gone, and r10 exists because " +
			"it used to say no more than \"exit status 1\"")
	}
	if *inspections < 2 {
		t.Errorf("only %d inspection(s) occurred; this test is void unless BOTH the preparation and "+
			"the revalidation reads happened", *inspections)
	}
}

// The companion direction on the SAME path. Without it, a change mapping every non-Found
// revalidation state to already_gone would satisfy the test above while silently reporting success
// for panes nobody could observe -- the fail-open this whole round exists to avoid widening into.
func TestRevalidationUnavailableIsNotAlreadyGone(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	closes := 0

	deps, inspections := countingCleanupDeps(
		[]tmuxpane.PaneInspection{
			foundPane(fx.pane),
			{
				State:  tmuxpane.PaneInspectionUnavailable,
				Detail: "error connecting to /tmp/tmux-501/default (Operation not permitted)",
			},
		}, &closes)

	prepared := PreparePaneCleanup(fx.req, deps)
	if !prepared.Ready {
		t.Fatalf("preparation must be READY: %+v", prepared.Result)
	}

	result := ClosePreparedPane(prepared, deps)

	if result.Outcome == PaneCleanupAlreadyGone {
		t.Error("an UNAVAILABLE revalidation must never be reported already_gone: a pane we cannot " +
			"observe is unproven, not proven-absent, and reporting success for it is the fail-open " +
			"that r10's narrow marker must not widen into")
	}
	if result.Outcome != PaneCleanupInspectionUnavailable {
		t.Errorf("outcome = %q, want %q", result.Outcome, PaneCleanupInspectionUnavailable)
	}
	if closes != 0 {
		t.Errorf("an unproven pane must not be closed; got %d close call(s)", closes)
	}
	if *inspections < 2 {
		t.Errorf("only %d inspection(s); the revalidation read must have happened", *inspections)
	}
}

// countingCleanupDeps wraps the package's canonical cleanupDeps so a test can assert HOW MANY
// inspections happened, while keeping cleanupDeps as the single source of the valid ancestry graph
// and the sequenced inspection list.
//
// The count matters: without it, a change that skipped revalidation entirely would leave these
// tests passing on the preparation result alone -- green, and proving nothing about the branch they
// are named for.
func countingCleanupDeps(inspections []tmuxpane.PaneInspection, closes *int) (PaneCleanupDependencies, *int) {
	deps := cleanupDeps(inspections, nil, closes)
	calls := 0
	inner := deps.Inspect
	deps.Inspect = func(paneID string) tmuxpane.PaneInspection {
		calls++
		return inner(paneID)
	}
	return deps, &calls
}
