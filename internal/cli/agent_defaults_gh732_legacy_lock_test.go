package cli

import (
	"reflect"
	"strings"
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

// TestClaudePreauthChildArgsStillEmitsAllowedTools locks the legacy half of
// gh#732's --allowedTools decision: the new-path intent compiler emits no
// --allowedTools token on any seat (see TestCompileIntentEmitsNoAllowedTools
// in internal/launchintent), and that omission is only "new path only" if
// this legacy composer keeps producing the equals-joined --allowedTools=
// flag for the same in-scope preauth allowlist it always has.
func TestClaudePreauthChildArgsStillEmitsAllowedTools(t *testing.T) {
	allow := claudeInScopePreauthAllowlist("v2-30-0")
	args := claudePreauthChildArgs(allow)
	if len(args) != 1 {
		t.Fatalf("claudePreauthChildArgs(%v) = %v, want exactly one arg", allow, args)
	}
	if !strings.HasPrefix(args[0], "--allowedTools=") {
		t.Fatalf("claudePreauthChildArgs(%v) = %q, want it to start with --allowedTools=", allow, args[0])
	}
	if args[0] != "--allowedTools=Bash(gh pr create:*)" {
		t.Fatalf("claudePreauthChildArgs(%v) = %q, want byte-identical legacy flag", allow, args[0])
	}
}
