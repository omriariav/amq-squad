package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

func TestWizardCommandIsDiscoverable(t *testing.T) {
	if _, ok := lookupCommand("wizard", "test"); !ok {
		t.Fatal("wizard is not registered")
	}
	if !containsString(completionTopCommands, "wizard") {
		t.Fatal("wizard is not included in completion")
	}
	var out, errOut bytes.Buffer
	if err := runWizardWithDependencies([]string{"--help"}, simpleWizardTestDependencies(t, t.TempDir()), strings.NewReader(""), &out, &errOut); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("wizard --help: %v", err)
	}
	if !strings.Contains(errOut.String(), "literal composition") || !strings.Contains(errOut.String(), "final confirmation") || !strings.Contains(errOut.String(), "defaults to No") {
		t.Fatalf("wizard help missing composition/default-No contract:\n%s", errOut.String())
	}
}

// TestWizardIsCompositionOfBriefInitPlanStart is gh#759/t13 commit 4's named
// acceptance test (cto's ruling): wizard's plan-building must go through
// init's own real computeInitPlan and brief's own real draftSimpleStartBrief,
// not a wizard-local reimplementation -- proven here by checking that a
// direct, standalone 'init' invocation against the identical flags produces
// the byte-identical planned team.Team/rules content wizard's own preview
// captured, and that applying the wizard plan leaves the profile/rules/
// brief in exactly the state a direct init+brief run would.
func TestWizardIsCompositionOfBriefInitPlanStart(t *testing.T) {
	project := t.TempDir()
	members := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex"},
		{Role: "fullstack", Handle: "fullstack", Binary: "claude"},
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, members)

	directInitArgs := []string{"--project", project, "--profile", "review", "--roles", "cto,fullstack", "--session", "issue-709", "--lead", "cto", "--lead-mode", "planner"}
	directInit, err := computeInitPlan(directInitArgs)
	if err != nil {
		t.Fatalf("computeInitPlan (direct): %v", err)
	}

	var out, errOut bytes.Buffer
	err = runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack", "--json"}, deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard preview: %v\nstderr:\n%s", err, errOut.String())
	}
	var envelope struct {
		Data struct {
			InitDigest string `json:"init_digest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode wizard JSON: %v\n%s", err, out.String())
	}
	if envelope.Data.InitDigest != directInit.Digest {
		t.Fatalf("wizard's init_digest %s does not match a direct 'init' invocation's digest %s -- wizard is not composing init's own real plan", envelope.Data.InitDigest, directInit.Digest)
	}

	// Now actually apply (--yes) and confirm the on-disk state matches what
	// a direct init --apply + brief --goal would have produced.
	deps.Start = func([]string, simpleStartDependencies, io.Reader, io.Writer) error { return nil }
	if err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack", "--yes"}, deps, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("wizard apply: %v", err)
	}
	stored, err := team.ReadProfile(project, "review")
	if err != nil {
		t.Fatalf("read applied profile: %v", err)
	}
	if len(stored.Members) != 2 || stored.Lead != "cto" {
		t.Fatalf("applied profile = %+v, want the 2-member cto-led roster init would have written", stored)
	}
	if _, err := os.Stat(briefPathForProfile(project, "review", "issue-709")); err != nil {
		t.Fatalf("brief was not written: %v", err)
	}
	if _, err := os.Stat(rules.Path(project)); err != nil {
		t.Fatalf("team-rules.md was not written by the composed init: %v", err)
	}
}

// TestWizardRequiresRolesForNewProfile is cto's "no silent degradation"
// ruling on task/t13: wizard no longer infers a roster from the goal text,
// so a new profile with no --roles must refuse closed naming the fix,
// rather than either guessing a roster or falling through to init's own
// interactive stdin prompt.
func TestWizardRequiresRolesForNewProfile(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		t.Fatal("wizard must refuse before ever reaching the drafter when --roles is missing for a new profile")
		return drafter.Result{}, nil
	}
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709"}, deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wizard no longer infers a roster from the goal text") || !strings.Contains(err.Error(), "--roles") {
		t.Fatalf("missing-roles error = %v, want the no-silent-degradation refusal naming --roles", err)
	}
	if team.ExistsProfile(project, "review") {
		t.Fatal("wizard wrote a profile despite refusing for missing --roles")
	}
}

func TestWizardDefaultNoLeavesEveryArtifactAbsent(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture"}, deps, strings.NewReader("\n"), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard preview: %v\nstderr:\n%s", err, errOut.String())
	}
	for _, path := range []string{
		team.ProfilePath(project, "review"),
		rules.Path(project),
		briefPathForProfile(project, "review", "issue-709"),
		filepath.Join(project, rules.ClaudeFile),
		filepath.Join(project, rules.AgentsFile),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("default-No wizard mutated %s (stat err %v)", path, statErr)
		}
	}
	text := out.String()
	for _, want := range []string{"Stage 1/4 readiness", "Stage 2/4 profile & rules", "Stage 3/4 brief", "Stage 4/4 approved execution", "Apply these exact artifacts and start the squad? [y/N]", "wizard cancelled; nothing changed"} {
		if !strings.Contains(text, want) {
			t.Errorf("wizard output missing %q:\n%s", want, text)
		}
	}
}

func TestWizardYesWritesReviewedArtifactsThenDelegatesToStart(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	simpleStartStubLaunchapiAMQEnv(t, squadnamespace.AMQRoot(project, "review", "issue-709"), "issue-709")
	startCalled := false
	deps.Start = func(args []string, _ simpleStartDependencies, _ io.Reader, _ io.Writer) error {
		startCalled = true
		// gh#757: wizard no longer hands start a bare --yes on the
		// launchapi default path (that silently no-ops there) -- it runs
		// the same probe start's own launch would and binds to that exact
		// subject_digest via --apply instead.
		if got := strings.Join(args, " "); !strings.Contains(got, "--profile review") || !strings.Contains(got, "--session issue-709") || !strings.Contains(got, "--apply ") || strings.Contains(got, "--yes") {
			t.Fatalf("wizard start args = %q", got)
		}
		if _, err := team.ReadProfile(project, "review"); err != nil {
			t.Fatalf("start called before profile was readable: %v", err)
		}
		for _, path := range []string{rules.Path(project), briefPathForProfile(project, "review", "issue-709"), filepath.Join(project, rules.ClaudeFile), filepath.Join(project, rules.AgentsFile)} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("start called before reviewed artifact %s existed: %v", path, err)
			}
		}
		return nil
	}
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture", "--yes"}, deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard approved run: %v\nstderr:\n%s", err, errOut.String())
	}
	if !startCalled {
		t.Fatal("wizard did not delegate the accepted plan to start")
	}
	stored, err := team.ReadProfile(project, "review")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Lead != "cto" || team.EffectiveLeadMode(stored) != team.LeadModePlanner {
		t.Fatalf("stored lead contract = lead %q mode %q", stored.Lead, team.EffectiveLeadMode(stored))
	}
	implementation := 0
	for _, member := range stored.Members {
		if team.EffectiveActorMode(stored, member) == team.ActorModeImplementation {
			implementation++
		}
	}
	// init's own default actor-mode policy (unlike the old wizard-local
	// roster builder) assigns every non-lead member "implementation";
	// only the lead is forced to "review" under --lead-mode planner.
	if implementation != 2 {
		t.Fatalf("wizard default roster has %d implementation actors, want 2 non-lead members", implementation)
	}
}

// TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch is gh#757's
// named acceptance test for cto's ruling on task/t8: wizard's handoff to
// start must go through the REAL runStartWithDependencies (not a stubbed
// deps.Start), on the launchapi default path, with an empty/non-interactive
// reader, and still reach Launch. Before this fix, wizard passed a bare
// --yes, which silently no-ops on the launchapi path (fullstack's finding):
// start's digest gate reads only a --apply <subject_digest> match or an
// interactive confirmation, neither of which --yes satisfies, so an
// automated (non-interactive) wizard run read as "cancelled" with nothing
// launched and no error. wizardStartArgs's fix computes the digest itself
// and passes --apply, so the same empty reader now reaches Launch instead
// of silently no-op'ing.
func TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	installFakeClaudeBinary(t)
	project := t.TempDir()
	// All-claude roster (see binaryOverride below): the goal-draft brief
	// fixture must describe the same roster the --binary override actually
	// produces, or buildSimpleWizardPlan's brief validation refuses with a
	// roster-mismatch error.
	allClaudeMembers := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "claude"},
		{Role: "fullstack", Handle: "fullstack", Binary: "claude"},
		{Role: "senior-dev", Handle: "senior-dev", Binary: "claude"},
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, allClaudeMembers)
	profile, session := "review", "issue-709"
	root := squadnamespace.AMQRoot(project, profile, session)
	simpleStartStubLaunchapiAMQEnv(t, root, session)
	// adoptionseam.Prepare's own openExplicitBaseAuthority requires a real
	// .amqrc at the project root (gh#757 finding); resolveTeamLaunchAMQEnv
	// being stubbed above only fakes what `amq env` would return, not this
	// filesystem-level authorization check.
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"root": ".agent-mail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second, non-obvious real requirement (confirmed directly against
	// openExplicitBaseAuthority, internal/launch/base_root.go): for a NAMED
	// profile, BaseRoot is one directory below .amqrc's configured root, and
	// that configured root must already EXIST on disk before a profile-child
	// BaseRoot is authorized at all -- otherwise it refuses closed with
	// base_root_unauthorized ("configured root must exist before creating a
	// profile child"), regardless of whether the relation itself is correct.
	// The default profile has no such requirement (BaseRoot == configured
	// root, created fresh). This is exactly the kind of operator-facing gap
	// t14's release notes need to name for real non-default-profile teams.
	if err := os.MkdirAll(filepath.Join(project, ".agent-mail"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A never-before-seen subject legitimately answers action_required/
	// untrusted_config_digest on its very first Prepare (t8's real
	// round-trip test proves this is the correct, by-design first-launch
	// answer, not a defect). wizardStartArgs correctly never auto-decides
	// this -- so reaching Launch here requires trust to already be
	// established for this exact subject, the same way an operator would
	// have answered it once interactively.
	//
	// Establishing it is not one Prepare+Apply pair: confirmed live that
	// launchapi.Apply's own create_base_root planned write (the base
	// container not existing yet) changes what the NEXT Prepare observes
	// (roster flips from all-missing to all-present), which changes the
	// computed subject_digest again -- so a decision trusted against the
	// PRE-Apply digest no longer matches the POST-Apply digest. This
	// converges: base_root creation is a one-time transition, so looping
	// Prepare-then-trust-Apply stabilizes once nothing observable is left
	// to change between consecutive Prepare calls.
	preTrust := simpleWizardTestDependenciesForMembers(t, project, allClaudeMembers)
	preTrust.StartDeps = deps.StartDeps
	var preOut, preErrOut bytes.Buffer
	// All-claude roster: codex/cursor adapters pin an exact live-probed
	// binary version for their capture mechanism (internal/launch's
	// codexCaptureVersion/cursorCaptureVersion), which drifts with the
	// locally installed tool and is not hermetic; claude's adapter has no
	// such pin. --binary keeps the same built-in role names (no custom-role
	// drafting triggered).
	binaryOverride := "--binary=cto=claude,senior-dev=claude"
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", profile, "--session", session, "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture", "--yes", binaryOverride}, preTrust, strings.NewReader(""), &preOut, &preErrOut)
	if err == nil || !strings.Contains(err.Error(), "operator decision(s) required") {
		t.Fatalf("expected the first wizard run to refuse on the untrusted-subject gate, got: %v\n%s", err, preOut.String())
	}
	probeReq, err := parseSimpleStartRequest([]string{"--project", project, "--profile", profile, "--session", session, "--goal", "Ship the reviewed change"})
	if err != nil {
		t.Fatalf("parseSimpleStartRequest: %v", err)
	}
	var trustedDigest string
	const maxTrustConvergenceAttempts = 5
	for attempt := 0; ; attempt++ {
		if attempt >= maxTrustConvergenceAttempts {
			t.Fatalf("trust establishment did not converge after %d attempts (last trusted digest %s)", attempt, trustedDigest)
		}
		probeAccepted, err := buildSimpleStartPlan(probeReq, deps.StartDeps)
		if err != nil {
			t.Fatalf("buildSimpleStartPlan: %v", err)
		}
		prepared, _, err := (launchapiTeamLaunchBackend{}).prepare(probeAccepted.SpawnTeam, probeAccepted.LaunchOptions)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		t.Logf("trust convergence attempt=%d outcome=%s reason=%s subject_digest=%s", attempt, prepared.Result.Outcome, prepared.Result.Reason, prepared.Result.SubjectDigest)
		// "ready" means no operator decision stands in the way at all,
		// regardless of whether this exact digest was the one just
		// trusted -- Apply's own create_base_root write (the base
		// container not existing yet) legitimately shifts the NEXT
		// Prepare's observed roster/subject once, independent of trust.
		if prepared.Result.Outcome != "action_required" {
			break
		}
		if len(prepared.Result.RequiredActions) != 1 || prepared.Result.RequiredActions[0].Kind != launchapi.RequiredActionTrustConfirmation {
			t.Fatalf("required_actions = %+v, want exactly one trust_confirmation", prepared.Result.RequiredActions)
		}
		trustedDigest = prepared.Result.SubjectDigest
		applyResult, err := adoptionseam.Apply(context.Background(), prepared, []launchapi.DecisionV1{{ActionID: prepared.Result.RequiredActions[0].ActionID, Choice: launchapi.DecisionTrustExactSubject}})
		if err != nil {
			t.Fatalf("establish trust via adoptionseam.Apply (attempt %d): %v", attempt, err)
		}
		t.Logf("apply result attempt=%d outcome=%s reason_code=%s disposition=%+v", attempt, applyResult.Outcome, applyResult.ReasonCode, applyResult.Disposition)
	}

	deps.Start = runStartWithDependencies
	deps.StartDeps.Sleep = func(time.Duration) {}
	deps.StartDeps.DeliverGoal = func(simpleStartPlan, string) error { return nil }
	alive := map[int]bool{}
	started := time.Unix(1_000, 0).UTC()
	deps.StartDeps.DuplicateProbe.PIDAlive = func(pid int) bool { return alive[pid] }
	deps.StartDeps.RuntimeProbe.PIDAlive = func(pid int) bool { return alive[pid] }
	deps.StartDeps.DuplicateProbe.ProcessStartTime = func(pid int) (time.Time, bool) { return started, alive[pid] }
	deps.StartDeps.RuntimeProbe.ProcessStartTime = func(pid int) (time.Time, bool) { return started, alive[pid] }

	launchCalled := false
	nextPID := 41000
	deps.StartDeps.Launch = func(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		var result teamLaunchResult
		for _, member := range spawn.Members {
			handle := memberHandle(member)
			pid := nextPID
			nextPID++
			paneID := fmt.Sprintf("%%%d", pid)
			rec := launch.Record{
				Schema: launch.SchemaVersion, CWD: project, TeamHome: project, TeamProfile: profile,
				Root: root, BaseRoot: filepath.Dir(root), Session: session,
				Role: member.Role, Handle: handle, Binary: member.Binary, Trust: trustModeSandboxed,
				ToolProfile: team.ToolProfileFull, AgentPID: pid, AgentTTY: "/dev/ttys-test", StartedAt: started,
				Tmux: &launch.TmuxInfo{Session: "test", WindowID: "@1", PaneID: paneID, Target: "new-window"},
			}
			if err := launch.Write(filepath.Join(root, "agents", handle), rec); err != nil {
				t.Fatal(err)
			}
			alive[pid] = true
			result.Panes = append(result.Panes, teamLaunchResultPane{Role: member.Role, PaneID: paneID, WindowID: "@1"})
		}
		return result, nil
	}

	var out, errOut bytes.Buffer
	// The profile already exists (created by the pre-trust run above), so
	// this reuses it via the existing-profile flow -- --roles/--binary/
	// --shared-cwd-exception are new-profile-only options and must not be
	// repeated here.
	err = runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", profile, "--session", session, "--yes"}, deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("wizard real handoff: %v\nstderr:\n%s\nout:\n%s", err, errOut.String(), out.String())
	}
	if !launchCalled {
		t.Fatal("wizard's real handoff to start never invoked Launch -- the --yes no-op bug is back")
	}
	if !strings.Contains(out.String(), "Launch subject_digest:") || !strings.Contains(out.String(), "outcome:") {
		t.Fatalf("wizard did not surface its computed subject_digest/plan preview before handoff:\n%s", out.String())
	}
}

// TestStartCancelsOnEmptyReaderWithoutApplyOnLaunchapiPath is the other half
// of gh#757's acceptance criteria: an empty/non-interactive reader on the
// launchapi path, with no --apply supplied, must still cancel rather than
// launch -- proving TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch
// above reaches Launch only because wizardStartArgs supplies --apply, not
// because start's own cancel-on-empty-reader behavior silently changed.
func TestStartCancelsOnEmptyReaderWithoutApplyOnLaunchapiPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	simpleStartStubLaunchapiAMQEnv(t, f.root, f.session)
	launchCalled := false
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, fmt.Errorf("start must not call Launch without --apply or an interactive yes")
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runStartWithDependencies: %v\n%s", err, out.String())
	}
	if launchCalled {
		t.Fatal("start called Launch on an empty reader without --apply")
	}
	if !strings.Contains(out.String(), "start cancelled") {
		t.Fatalf("empty-reader launchapi start without --apply did not cancel:\n%s", out.String())
	}
}

func TestWizardRefusesPlanChangedAtConfirmation(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	startCalled := false
	deps.Start = func([]string, simpleStartDependencies, io.Reader, io.Writer) error { startCalled = true; return nil }
	reader := &wizardMutationReader{mutate: func() {
		if err := os.MkdirAll(filepath.Dir(briefPathForProfile(project, "review", "issue-709")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(briefPathForProfile(project, "review", "issue-709"), []byte("changed during review\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture"}, deps, reader, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "plan changed while awaiting approval") {
		t.Fatalf("wizard changed-plan error = %v", err)
	}
	if startCalled {
		t.Fatal("wizard called start after accepted inputs changed")
	}
	if team.ExistsProfile(project, "review") {
		t.Fatal("wizard wrote the profile after snapshot rejection")
	}
}

// A companion ABA-safety test for the profile/rules/pointer-stub half
// (guarded by recomputing computeInitPlan fresh at apply time, see
// applySimpleWizardPlan's doc comment) was deliberately not written: unlike
// the brief path, computeInitPlan for a NEW profile is a pure function of
// its own argv (--roles/--binary/--lead/...), not of whatever a concurrent
// writer put at the profile path -- init's own --force apply always
// overwrites deterministically from those same args regardless. The digest
// recompute here protects against the args' own external inputs changing
// (e.g. a --role-file's content), not against a third party writing a
// different team.json to the same path in the meantime.

func TestWizardReadinessFailsBeforeDrafterRuns(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	drafterCalled := false
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		drafterCalled = true
		return drafter.Result{}, nil
	}
	deps.LookPath = func(name string) (string, error) {
		if name == "amq" {
			return "", errors.New("not found")
		}
		return "/test/bin/" + name, nil
	}
	err := runWizardWithDependencies([]string{"Ship it", "--project", project, "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture"}, deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `required executable "amq"`) {
		t.Fatalf("wizard readiness error = %v", err)
	}
	if drafterCalled {
		t.Fatal("wizard invoked the drafter before core readiness passed")
	}
}

// TestWizardReadinessFailsOnYoetzWithoutModel proves wizard readiness
// refuses a global yoetz-preset drafter config with no model up front
// (gh#760), before the drafter ever runs, instead of only surfacing yoetz's
// own opaque "provider is required" failure at invocation time.
func TestWizardReadinessFailsOnYoetzWithoutModel(t *testing.T) {
	project := t.TempDir()
	deps := simpleWizardTestDependencies(t, project)
	deps.ReadConfig = func() (userconfig.Config, error) {
		return userconfig.Config{Drafter: &drafter.Config{Backend: drafter.BackendYoetz}}, nil
	}
	drafterCalled := false
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		drafterCalled = true
		return drafter.Result{}, nil
	}
	err := runWizardWithDependencies([]string{"Ship it", "--project", project, "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture"}, deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "model: required for the yoetz preset backend") {
		t.Fatalf("wizard readiness error = %v, want yoetz-without-model refusal", err)
	}
	if drafterCalled {
		t.Fatal("wizard invoked the drafter before core readiness passed")
	}
}

func TestWizardImplicitProfileCollisionUsesExistingFlow(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	for _, profile := range []string{"ship-it", "other"} {
		tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
		if err := team.WriteProfile(project, profile, tm); err != nil {
			t.Fatal(err)
		}
	}
	if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
		t.Fatal(err)
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}})
	deps.RunGoalDraft = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: validSimpleStartBriefDraft("fresh", "Ship it", team.Member{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}), Evidence: drafter.Evidence{Backend: drafter.BackendCodex}}, nil
	}
	var out bytes.Buffer
	if err := runWizardWithDependencies([]string{"Ship it", "--project", project, "--session", "fresh", "--json"}, deps, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"flow": "existing_profile_session"`) || !strings.Contains(out.String(), `"profile": "ship-it"`) {
		t.Fatalf("implicit colliding profile did not use existing flow:\n%s", out.String())
	}
}

func TestWizardExistingProfileRejectsExplicitDefaultValuedNewProfileFlag(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
	if err := team.WriteProfile(project, "reusable", tm); err != nil {
		t.Fatal(err)
	}
	deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
	err := runWizardWithDependencies([]string{"Fresh", "--project", project, "--profile", "reusable", "--session", "fresh", "--lead", "cto", "--json"}, deps, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "new-profile options") {
		t.Fatalf("explicit --lead cto on existing profile error = %v", err)
	}
}

func TestWizardExistingReusableProfileUsesFlowBWithoutRewritingProfileOrRules(t *testing.T) {
	project := t.TempDir()
	op := team.DefaultOperator()
	tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
	if err := team.WriteProfile(project, "reusable", tm); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
		t.Fatal(err)
	}
	profileBefore, _ := os.ReadFile(team.ProfilePath(project, "reusable"))
	rulesBefore, _ := os.ReadFile(rules.Path(project))
	deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
	var out, errOut bytes.Buffer
	err := runWizardWithDependencies([]string{"A fresh workstream", "--project", project, "--profile", "reusable", "--session", "fresh", "--json"}, deps, strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("existing-profile wizard: %v", err)
	}
	var envelope struct {
		Kind string `json:"kind"`
		Data struct {
			Flow            string               `json:"flow"`
			ProfileArtifact simpleWizardArtifact `json:"profile_artifact"`
			Rules           simpleWizardArtifact `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode wizard JSON: %v\n%s", err, out.String())
	}
	if envelope.Kind != "wizard_plan" || envelope.Data.Flow != "existing_profile_session" || envelope.Data.ProfileArtifact.Action != "reuse" || envelope.Data.Rules.Action != "reuse" {
		t.Fatalf("existing wizard plan = %+v", envelope)
	}
	profileAfter, _ := os.ReadFile(team.ProfilePath(project, "reusable"))
	rulesAfter, _ := os.ReadFile(rules.Path(project))
	if !bytes.Equal(profileBefore, profileAfter) || !bytes.Equal(rulesBefore, rulesAfter) {
		t.Fatal("preview-only existing-profile wizard rewrote profile or rules")
	}
}

// TestInSessionFallthroughIncludesAttemptEvidence proves the wizard's
// stopped-before-mutation error (gh#760) surfaces the per-attempt drafter
// evidence -- backend, exact command, and fall-through reason -- for both
// the new-profile brief draft path and the existing-profile brief draft
// path, both of which now share the one buildSimpleWizardBrief helper.
func TestInSessionFallthroughIncludesAttemptEvidence(t *testing.T) {
	fallThroughDraft := func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			UseInSession: true,
			Attempts: []drafter.Evidence{
				{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask --prompt-file *** --format text", Failure: "exit 17: missing provider API key"},
			},
		}, nil
	}

	t.Run("new profile", func(t *testing.T) {
		project := t.TempDir()
		deps := simpleWizardTestDependencies(t, project)
		deps.RunGoalDraft = fallThroughDraft
		err := runWizardWithDependencies([]string{"Ship the reviewed change", "--project", project, "--profile", "review", "--session", "issue-709", "--roles", "cto,fullstack,senior-dev", "--shared-cwd-exception", "test fixture", "--yes"}, deps, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected the in-session fallthrough error")
		}
		if !strings.Contains(err.Error(), "attempt[1] backend=yoetz") || !strings.Contains(err.Error(), "fall-through=") {
			t.Fatalf("new-profile fallthrough error dropped attempt evidence: %v", err)
		}
	})

	t.Run("existing profile", func(t *testing.T) {
		project := t.TempDir()
		op := team.DefaultOperator()
		tm := team.Team{Operator: &op, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", ActorMode: team.ActorModeReview}}}
		if err := team.WriteProfile(project, "reusable", tm); err != nil {
			t.Fatal(err)
		}
		if err := rules.Write(project, "# Existing reviewed rules\n"); err != nil {
			t.Fatal(err)
		}
		deps := simpleWizardTestDependenciesForMembers(t, project, tm.Members)
		deps.RunGoalDraft = fallThroughDraft
		err := runWizardWithDependencies([]string{"A fresh workstream", "--project", project, "--profile", "reusable", "--session", "fresh", "--yes"}, deps, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected the in-session fallthrough error")
		}
		if !strings.Contains(err.Error(), "attempt[1] backend=yoetz") || !strings.Contains(err.Error(), "fall-through=") {
			t.Fatalf("existing-profile fallthrough error dropped attempt evidence: %v", err)
		}
	})
}

type wizardMutationReader struct {
	mutate func()
	done   bool
}

func (r *wizardMutationReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.mutate()
	return copy(p, "yes\n"), nil
}

func simpleWizardTestDependencies(t *testing.T, project string) simpleWizardDependencies {
	members := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex"},
		{Role: "fullstack", Handle: "fullstack", Binary: "claude"},
		{Role: "senior-dev", Handle: "senior-dev", Binary: "codex"},
	}
	return simpleWizardTestDependenciesForMembers(t, project, members)
}

func simpleWizardTestDependenciesForMembers(t *testing.T, project string, members []team.Member) simpleWizardDependencies {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("AMQ_SQUAD_CONFIG", configPath)
	cfg := userconfig.Config{Drafter: &drafter.Config{Chain: []string{drafter.BackendCodex}, OnFailure: drafter.FailureError}}
	if _, err := userconfig.Write(cfg); err != nil {
		t.Fatal(err)
	}
	goal := "Ship the reviewed change"
	if len(members) == 1 {
		goal = "A fresh workstream"
	}
	return simpleWizardDependencies{
		Now:        func() time.Time { return time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC) },
		LookPath:   func(name string) (string, error) { return "/test/bin/" + name, nil },
		ReadConfig: func() (userconfig.Config, error) { return cfg, nil },
		ConfigPath: func() (string, error) { return configPath, nil },
		RunGoalDraft: func(_ context.Context, _ *drafter.Config, _ drafter.Request) (drafter.Result, error) {
			return drafter.Result{Text: validSimpleStartBriefDraft(map[bool]string{true: "fresh", false: "issue-709"}[len(members) == 1], goal, members...), Evidence: drafter.Evidence{Backend: drafter.BackendCodex, ExitCode: 0}}, nil
		},
		Start: func([]string, simpleStartDependencies, io.Reader, io.Writer) error {
			t.Fatal("preview must not call start")
			return nil
		},
		StartDeps: hermeticSimpleStartDepsForWizardTest(t),
	}
}

// installFakeClaudeBinary puts a fake "claude" executable ahead of PATH so a
// real adoptionseam.Prepare call resolves and validates it successfully on
// any machine, CI included. This is required, not optional, once a test
// actually reaches launchapi's real Prepare/participant validation (as
// opposed to unit tests that only construct a launchintent.Input and never
// call Prepare): the pinned module's internal/launch adapter.go
// validateKnownExecutable unconditionally resolves each seat's Executable
// via exec.LookPath and requires it to exist, be outside the project
// directory, and match the expected provider by basename -- confirmed live
// on CI (make ci failure on aff9e53): a developer machine with a real
// claude on PATH masks this, CI does not have one.
//
// The fake also answers ClaudeAdapter.Capabilities' --version/--help probe
// (internal/launch/adapter_claude.go) so capability negotiation succeeds
// too, though that alone is a soft signal (a failed probe degrades to
// below-floor, not a hard refusal) -- only validateKnownExecutable's check
// is unconditional.
func installFakeClaudeBinary(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version) echo 1.0.0; exit 0 ;;\n" +
		"  --help) printf -- '--session-id <uuid>\\n--resume [value]\\n'; exit 0 ;;\n" +
		"  *) exit 0 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// hermeticSimpleStartDepsForWizardTest builds a simpleStartDependencies that
// lets wizardStartArgs's probe (parseSimpleStartRequest -> buildSimpleStartPlan
// -> (launchapiTeamLaunchBackend{}).prepare) run for real against the team
// wizard just wrote, without touching a real tmux/amq binary or spawning
// anything. Launch is deliberately left failing loudly: wizardStartArgs never
// calls it (it only previews), so any test that reaches it either wired its
// own deps.Start stub incorrectly or is exercising a real runStartWithDependencies
// handoff and must override Launch itself.
func hermeticSimpleStartDepsForWizardTest(t *testing.T) simpleStartDependencies {
	t.Helper()
	previousBackend, hadBackend := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = &fakeBackend{}
	t.Cleanup(func() {
		if hadBackend {
			teamLaunchBackends["tmux"] = previousBackend
			return
		}
		delete(teamLaunchBackends, "tmux")
	})
	return simpleStartDependencies{
		LookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		ResolveAMQEnv: func(project, root, session, handle string) (amqEnv, error) {
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: root, BaseRoot: filepath.Dir(root), SessionName: session, Me: handle}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:         func(int) bool { return false },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(int) (string, bool) { return "", false },
			ProcessStartTime: func(int) (time.Time, bool) { return time.Time{}, false },
			Now:              func() time.Time { return time.Unix(1_000, 0).UTC() },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:         func(int) bool { return false },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(int) (string, bool) { return "", false },
			ProcessStartTime: func(int) (time.Time, bool) { return time.Time{}, false },
			PaneTitle:        func(string) (string, bool) { return "", false },
		},
		ListPanes:    func() ([]tmuxpane.TmuxPane, error) { return nil, nil },
		StartWatcher: func(team.Team, string, string, string) error { return nil },
		Launch: func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
			t.Fatal("wizard's own preview must never reach Launch; only a real runStartWithDependencies handoff should, and that test must stub Launch itself")
			return teamLaunchResult{}, nil
		},
	}
}
