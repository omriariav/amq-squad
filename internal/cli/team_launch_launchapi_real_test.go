package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestRealPrepareApplyRoundTripAcceptsCanonicalTarget is t8's real-binding
// acceptance test for gh#757 (cto's DIRECTIVE on task/t8, superseding the
// narrower wording of the earlier one): buildIntentInput's Target (this
// file's buildIntentInput, feeding every launchapi Prepare/Apply call on the
// start/plan launchapi path) carried three stacked defects, none reachable
// from the package's own mocked-amq-env unit tests because
// launchapiTestStubAMQEnv's fixture is itself degenerate (BaseRoot==Root, a
// shape openExplicitBaseAuthority can never accept):
//
//  1. SessionRoot was the team's project root instead of BaseRoot's direct
//     child named Session (base_root_relation_invalid, this task's original
//     finding).
//  2. ProjectRoot was not canonicalized, so openPrepareTarget's "project_root
//     must be canonical" (filepath.Abs+EvalSymlinks) check rejects any
//     project path with a symlinked ancestor (found live by fullstack's t10
//     verification of the sibling Inspect call site, e.g. a macOS
//     /var/folders temp dir resolving through /private/var).
//  3. BaseRoot was not canonicalized either, so openExplicitBaseAuthority's
//     plain STRING comparison against the .amqrc-configured root (itself
//     derived from the canonical project) rejects an equivalent but
//     differently-spelled BaseRoot (base_root_unauthorized).
//
// This calls buildIntentInput itself (the actual production method, fed
// with a real `amq env --json` envelope resolved against a real project) and
// passes its output straight to adoptionseam.Prepare -- exercising the exact
// fixed code path, not a hand-rolled Target -- against one genuine, fully-
// authorized project/base-root layout produced by a real `amq init` plus a
// real `.amqrc`: the same authorization openExplicitBaseAuthority itself
// requires, mirroring fullstack's TestRealLaunchapiInspectSessionTargetShape
// (internal/cli/liveness_real_launchapi_target_test.go on t10) but against
// adoptionseam.Prepare instead of launchapi.Inspect.
//
// Apply is deliberately NOT exercised here: a genuine Apply against this
// backend composes and would attempt to bind a real tmux pane running the
// seat's executable, which is not safely dry-runnable in a unit test process
// (no tmux session context here, and no cleanup story for a bound pane) --
// this is the "if safely dry-runnable" case cto's directive allowed for.
// Prepare alone is a read-only, deterministic call
// (docs/amq-0.73.0-adoption-verdict.md) and is sufficient to prove the
// Target shape itself clears authorization: the acceptance criterion is
// Outcome, not any side effect only Apply would produce.
func TestRealPrepareApplyRoundTripAcceptsCanonicalTarget(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run the disposable real-launchapi Prepare round trip")
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is unavailable or not executable: %v", binary, err)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	t.Logf("real AMQ binary=%s version=%s", binary, version)

	project := t.TempDir()
	session := "t8-real-binding"
	root := filepath.Join(project, ".agent-mail", session)
	realAMQInit(t, binary, project, root)
	// This is the exact operator-facing requirement for the launchapi path
	// to reach outcome "ready" against a real project (t14 release-notes
	// note, per cto's directive): a `.amqrc` at the project root naming the
	// base container -- the same authorization `amq init` alone does not
	// write (confirmed directly against the real binary) but
	// openExplicitBaseAuthority unconditionally requires for Prepare/Apply/
	// Inspect alike.
	rc := []byte("{\n  \"root\": \".agent-mail\"\n}\n")
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), rc, 0o600); err != nil {
		t.Fatal(err)
	}

	// Real `amq env --json --session <session>` against the real project:
	// cto asked whether pre.Root/pre.BaseRoot arrive canonical from amq's
	// own resolution rather than assuming either way. They do, in fact --
	// confirmed here directly rather than assumed: the real binary's own
	// `env --json` already resolves symlinks in the Root/BaseRoot it
	// echoes back (this run's Root prints the resolved /private/var/...
	// form, not t.TempDir()'s raw /var/... spelling), so
	// canonicalFilesystemPath is a no-op on pre.Root/pre.BaseRoot in
	// practice. It is ProjectRoot (t.Project) that genuinely needs it:
	// amq-squad computes that value itself and never routes it through
	// `amq env`, so it stays exactly as uncanonicalized as the operator (or
	// t.TempDir(), here) originally spelled it -- which this test's `project`
	// local still is, and is what buildIntentInput's ProjectRoot
	// canonicalization below is actually exercising.
	var envelope amqEnv
	if err := json.Unmarshal([]byte(realAMQCommand(t, binary, project, nil, "env", "--json", "--session", session)), &envelope); err != nil {
		t.Fatalf("real amq env --json: %v", err)
	}
	t.Logf("real amq env: root=%q base_root=%q", envelope.Root, envelope.BaseRoot)
	if project == canonicalFilesystemPath(project) {
		t.Skip("this platform's t.TempDir() is already canonical (no symlinked ancestor); the ProjectRoot canonicalization this test exercises is inert here")
	}

	tm := team.Team{
		Project: project,
		Members: []team.Member{{Role: "worker", Binary: "claude", Handle: "worker", Session: session}},
	}
	preflights := []agentLaunchPreflight{{
		Role: "worker", Handle: "worker", CWD: project,
		Root: envelope.Root, BaseRoot: envelope.BaseRoot, Workstream: session,
	}}
	opts := teamLaunchOptions{Workstream: session, Trust: trustModeApproveForMe}

	in, err := (launchapiTeamLaunchBackend{}).buildIntentInput(tm, opts, preflights, nil)
	if err != nil {
		t.Fatalf("buildIntentInput: %v", err)
	}

	prepared, err := adoptionseam.Prepare(context.Background(), adoptionseam.PrepareInput{Intent: in, Launcher: "tmux"})
	if err != nil {
		t.Fatalf("adoptionseam.Prepare against a real, fully-authorized project/base-root layout: %v", err)
	}
	t.Logf("PrepareResultV1: outcome=%q reason=%q required_actions=%+v subject_digest=%s", prepared.Result.Outcome, prepared.Result.Reason, prepared.Result.RequiredActions, prepared.Result.SubjectDigest)

	if prepared.Result.Reason == "base_root_unauthorized" || prepared.Result.Reason == "base_root_relation_invalid" {
		t.Fatalf("Target shape rejected against a real, fully-authorized layout -- the canonicalization fix did not resolve it: outcome=%q reason=%q", prepared.Result.Outcome, prepared.Result.Reason)
	}
	if prepared.Result.Outcome == "unsupported" {
		t.Fatalf("outcome = %q (reason=%q), want %q or a legitimate %q trust gate -- not a Target/support refusal", prepared.Result.Outcome, prepared.Result.Reason, "ready", "action_required")
	}
	// A brand-new subject digest has never been trusted before, so
	// launchapi's own trust gate legitimately answers "action_required"
	// (RequiredActionTrustConfirmation, reason "untrusted_config_digest")
	// on this very first Prepare against a fresh project -- confirmed live
	// below -- rather than "ready". That is the correct, by-design first-
	// launch answer, not a defect: only "unsupported" (asserted above) or
	// the two base_root reasons (asserted above) would mean the Target
	// itself was rejected.
	if prepared.Result.Outcome == "action_required" {
		if len(prepared.Result.RequiredActions) == 0 {
			t.Fatalf("outcome = action_required but RequiredActions is empty")
		}
		if got := prepared.Result.RequiredActions[0].Kind; got != launchapi.RequiredActionTrustConfirmation {
			t.Fatalf("action_required kind = %q, want %q (any other required-action kind on a fresh, never-launched subject would itself be worth investigating)", got, launchapi.RequiredActionTrustConfirmation)
		}
	}
}
