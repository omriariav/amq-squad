package cli

import (
	"reflect"
	"testing"
)

// TestCodexApproveForMeArgsStillEmitsApprovalsReviewer locks the legacy half
// of gh#732's approvals_reviewer decision: internal/launchintent's compiler
// drops approvals_reviewer from the NEW path only (see
// TestCompileIntentDropsApprovalsReviewerOnNewPathOnly in
// internal/launchintent), and that is only a meaningful "only" if this
// legacy composer keeps emitting the byte-identical literal it always has.
// A change here without an equal-and-opposite change in internal/launchintent
// is exactly the drift this test exists to catch.
func TestCodexApproveForMeArgsStillEmitsApprovalsReviewer(t *testing.T) {
	want := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	if !reflect.DeepEqual(codexApproveForMeArgs, want) {
		t.Fatalf("codexApproveForMeArgs = %v, want byte-identical legacy literal %v", codexApproveForMeArgs, want)
	}
}
