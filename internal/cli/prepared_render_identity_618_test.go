package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// TestPrepareAndPaneRenderTheSameRoster is the #618 staged-roster regression.
//
// THE ORIGINAL DEFECT. The routing block was produced by two call sites that each
// chose the roster themselves, with two different functions:
//
//	prepare-time  run_start_readiness.go  bootstrapCurrentTeamForTeam(rec, tm)
//	              where tm was narrowed by partitionPreparedRunMembers
//	              = filterMembersBySession MINUS staged roles
//	pane-time     launch.go               bootstrapCurrentTeamWithRoster(rec, teamHome, true)
//	              = team.ReadProfile + filterMembersBySession, staged roles NOT removed
//
// With a non-empty staged roster the pane render carried members the accepted
// render did not, so every bootstrap row drifted at once.
//
// WHY THIS TEST ASSERTS AT THE RENDERER, NOT AT THOSE TWO FUNCTIONS. It first
// compared bootstrapCurrentTeamForTeam against bootstrapCurrentTeamWithRoster
// directly. After the fix, the pane no longer CALLS the second one for a prepared
// launch, so that comparison would have kept failing while the shipped behaviour
// was correct -- a test asserting a proxy for the outcome instead of the outcome.
// It now drives launchBootstrapPrompt, the renderer the pane actually uses, and
// asserts the property that matters: with staged members present, the pane prompt
// must digest to exactly what preparation accepted.
func TestPrepareAndPaneRenderTheSameRoster(t *testing.T) {
	for _, shape := range []string{runwizard.LaunchShapeWorkingTeamTogether, runwizard.LaunchShapeLeadOnlyStaged} {
		t.Run(shape, func(t *testing.T) { assertPreparedRenderIdentity618(t, shape) })
	}
}

func assertPreparedRenderIdentity618(t *testing.T, shape string) {
	project := prepareStagedRosterFixture618Shape(t, shape)
	profile, session := team.DefaultProfile, "prepared"

	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.StagedRoster) == 0 {
		t.Fatalf("fixture cannot express the defect: staged roster is empty, so the two"+
			" selectors agree trivially and a pass here would prove nothing"+
			" (initial=%v staged=%v)", manifest.InitialRoster, manifest.StagedRoster)
	}

	full, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, staged, err := partitionPreparedRunMembers(full.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatalf("partition accepted roster: %v", err)
	}
	if len(staged) == 0 {
		t.Fatalf("fixture partitioned no staged members; initial=%d", len(initial))
	}
	acceptedRoster := full
	acceptedRoster.Members = initial

	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	root := squadnamespace.AMQRoot(project, profile, session)

	for _, member := range initial {
		handle := memberHandle(member)
		agentDir := filepath.Join(root, "agents", handle)
		rec := launch.Record{
			Role: member.Role, Handle: handle, Binary: member.Binary,
			ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig,
			Session: session, CWD: project, Root: root,
			TeamHome: project, TeamProfile: profile, SharedWorkstream: true,
		}
		// Feed the roster the LAUNCH CONTEXT actually carries for an initial
		// member -- session-filtered with staged roles still present -- not a
		// roster this test narrowed itself. Narrowing here would test the helper
		// with inputs production never supplies, which is how the staged-spawn
		// regression reached a full-suite run instead of this one.
		sessionFiltered := full
		sessionFiltered.Members, _ = filterMembersBySession(full.Members, session)
		panePrompt, err := launchBootstrapPrompt(rec, agentDir, project, &preparedLaunchRecordContext{
			Manifest: manifest, Team: sessionFiltered, Member: member, Binding: binding,
		})
		if err != nil {
			t.Fatalf("pane render for %s: %v", member.Role, err)
		}
		got := digestRunArtifactBytes([]byte(panePrompt))
		want := manifest.BootstrapDigests[member.Role]
		if got == want {
			t.Logf("staged roster present, role %s: pane render matches the accepted digest", member.Role)
			continue
		}
		t.Errorf("#618 STAGED-ROSTER DIVERGENCE: role %s pane render does not match the accepted digest", member.Role)
		t.Errorf("  accepted=%s pane=%s", want, got)
		t.Errorf("  accepted initial roster: %v", manifest.InitialRoster)
		t.Errorf("  accepted staged roster:  %v", manifest.StagedRoster)
		acceptedPrompt, renderErr := preparedBootstrap(project, profile, session, binding, acceptedRoster, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		if renderErr != nil {
			continue
		}
		for i, line := range diffPromptLines(acceptedPrompt, panePrompt) {
			if i > 15 {
				t.Errorf("  ... truncated")
				break
			}
			t.Errorf("  %s", line)
		}
	}

	// STAGED MEMBERS ARE OUT OF SCOPE FOR THIS PR AND DELIBERATELY NOT ASSERTED.
	// launchBootstrapPrompt forks them back to the original bootstrapContextFor
	// path (see stagedRenderIsForked), because rendering a staged member from
	// accepted state alone does not reproduce its accepted digest: the accepted
	// preview pins ActorMode to review while the on-disk profile carries none.
	// Asserting byte-identity here would fail against behaviour this PR
	// intentionally leaves untouched. Forked to its own task with the findings.
}

// prepareStagedRosterFixture618 prepares a run whose accepted manifest carries a
// non-empty staged roster. The staged split is what makes the two selectors
// disagree, so a fixture without one cannot express the defect at all.
func prepareStagedRosterFixture618(t *testing.T) string {
	t.Helper()
	return prepareStagedRosterFixture618Shape(t, runwizard.LaunchShapeWorkingTeamTogether)
}

// prepareStagedRosterFixture618Shape parameterizes over launch shape because the
// shape changes the accepted TOPOLOGY, and topology feeds the execution contract
// that decides "Implementation allowed for you". A staged member under
// lead-only-staged is not the same subject as a staged member under
// working-team-together, and testing only the latter is how the staged-spawn
// regression survived a suite that already had staged coverage.
func prepareStagedRosterFixture618Shape(t *testing.T, shape string) string {
	t.Helper()
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "codex", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "claude", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			// lead-only-staged requires the initial roster to be exactly the lead,
			// so every non-lead member must be staged under that shape. Under
			// working-team-together only qa is staged, which leaves a mixed
			// initial/staged roster -- the two shapes exercise genuinely different
			// partitions and that is the point of running both.
			"--launch-shape", shape, "--staged-roles", stagedRolesForShape618(shape),
			"--goal", "Reproduce #618 render-path roster divergence",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare staged fixture: %v", err)
	}
	return dir
}

// routingHandles618 renders the routing roster as a stable comparable string so
// a failure names WHICH members differ rather than only that a count moved.
func routingHandles618(members []bootstrapTeamMember) string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Role+"/"+m.Handle)
	}
	return strings.Join(out, ",")
}

// TestFieldShapeRosterAgreesWhenNothingIsStaged is probe (b): does the
// CONFIRMED staged-roster divergence explain the 2026-08-02 field hit?
//
// The field-hit prepared tree is gone (#598's teardown fix correctly deleted it
// on reset) and the team profile has been edited since, so neither settles it.
// The issue transcript does: profile squad-v2-27-0 had three members and
// --readiness-json reported "all three bootstrap rows drifted". Three members,
// three initial-roster bootstrap rows, therefore nothing was staged.
//
// This reproduces that SHAPE -- three members, all session-matched, no staged
// roles -- and asks whether the two render paths still disagree.
//
//	They AGREE  -> mechanism 1 cannot explain the field hit. A second
//	               contributor exists and #618 must stay open past the
//	               staged-roster fix.
//	They DIFFER -> mechanism 1 explains it after all and my reading of the
//	               transcript is wrong.
//
// Either answer is worth having before a PR body is written, which is why this
// runs before the fix rather than after it.
func TestFieldShapeRosterAgreesWhenNothingIsStaged(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			// Built-in roles, deliberately. The field hit used custom roles
			// (amq-dev-1/amq-dev-2) which require .amq-squad/roles/<role>.md to
			// exist; staging those files would add a variable this probe is not
			// testing. What must match the field hit is the SHAPE -- three
			// members, all session-matched, nothing staged -- not the names.
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Reproduce the #618 field-hit roster shape",
			"--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare field-shape fixture: %v", err)
	}

	profile, session := team.DefaultProfile, "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.StagedRoster) != 0 {
		t.Fatalf("fixture does not match the field shape: staged roster is %v, want empty", manifest.StagedRoster)
	}
	if len(manifest.InitialRoster) != 3 {
		t.Fatalf("fixture does not match the field shape: initial roster %v, want 3 members", manifest.InitialRoster)
	}

	full, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(full.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatalf("partition accepted roster: %v", err)
	}
	prepareRoster := full
	prepareRoster.Members = initial

	member := initial[0]
	rec := launch.Record{
		Role: member.Role, Handle: memberHandle(member), Binary: member.Binary,
		Session: session, CWD: dir, TeamHome: dir, TeamProfile: profile,
		SharedWorkstream: true,
	}
	prepareTeam, _ := bootstrapCurrentTeamForTeam(rec, prepareRoster)
	paneTeam, _ := bootstrapCurrentTeamWithRoster(rec, dir, true)

	prepareHandles := routingHandles618(prepareTeam)
	paneHandles := routingHandles618(paneTeam)
	if prepareHandles != paneHandles {
		t.Errorf("UNEXPECTED: the paths diverge even with nothing staged, so mechanism 1 may explain the field hit after all")
		t.Errorf("  prepare-time: %s", prepareHandles)
		t.Errorf("  pane-time:    %s", paneHandles)
		return
	}
	t.Logf("field shape: both render paths agree on the routing roster (%s)", prepareHandles)
	t.Logf("CONCLUSION: the confirmed staged-roster divergence does NOT explain the 2026-08-02 field hit;" +
		" a second contributor exists and #618 must stay open past that fix")
}

// TestPromptTextAcrossNamespaceCreation is the mechanism-2 hunt.
//
// WHAT EVERY PRIOR PROBE EXCLUDED, so this one is aimed rather than hopeful:
// the routing roster (proved identical in the field shape), bootstrapContextFor's
// exactSessionRoster stat (dead on all three prepared paths), role/launch
// ExistingPath (same string in both reachable cases), timestamps (#598's
// determinism test passes and a clock input would drift EVERY render), the
// amq env root_id/base_root_id delta (real, but discarded by the amqEnv
// unmarshal), and the staged projection plus execution-contract roster width
// (both no-ops when nothing is staged).
//
// WHAT SURVIVES: routeExplainCommand (bootstrap.go:595-603) shells out to
// `amq route explain --json` ONCE PER PEER and splices the result verbatim into
// that peer's send: line. Its answer depends on the AMQ root and the peer
// mailboxes actually existing. Preparation runs before they do; `agent up`
// re-renders after. That is the last live-state input inside the render.
//
// WHY THIS IS NOT rc1_bisect's EXCLUDED DELTA 3: that fixture materializes the
// agent extension directory, role.md and launch.json, but never initializes AMQ
// MAILBOXES. `amq route explain` does not care about role.md; it cares about
// mailbox structure under the root. So delta 3 crossed a different boundary than
// the one the send: lines actually depend on, and its exclusion does not cover
// this. Stated explicitly so this is not read as re-deriving settled evidence.
//
// Reports the diff rather than asserting a shape: if the differing lines are
// send: lines, mechanism 2 is found. If the diff is EMPTY, mechanism 2 is not a
// render input at all and the fix design has to look at what "accepted" bytes
// were persisted versus re-derived, which is decisive either way.
func TestPromptTextAcrossNamespaceCreation(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Hunt #618 mechanism 2 across namespace creation",
			"--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare fixture: %v", err)
	}

	profile, session := team.DefaultProfile, "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = initial
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	accepted := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}

	member := initial[0]
	before, err := preparedBootstrap(dir, profile, session, binding, tm, member, accepted)
	if err != nil {
		t.Fatalf("render before namespace creation: %v", err)
	}

	// Create what a real launch creates and the existing fixtures do not: the AMQ
	// root with a real mailbox for every peer. This is the state `amq route
	// explain` answers against.
	root := squadnamespace.AMQRoot(dir, profile, session)
	for _, m := range tm.Members {
		handle := memberHandle(m)
		for _, sub := range []string{"inbox/cur", "inbox/new", "inbox/tmp", "outbox"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", handle, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	after, err := preparedBootstrap(dir, profile, session, binding, tm, member, accepted)
	if err != nil {
		t.Fatalf("render after namespace creation: %v", err)
	}

	if digestRunArtifactBytes([]byte(before)) == digestRunArtifactBytes([]byte(after)) {
		t.Logf("render is STABLE across namespace creation for role %s", member.Role)
		t.Logf("mechanism 2 is NOT a render input across this boundary; look at persisted-vs-rederived accepted bytes")
		return
	}

	lines := diffPromptLines(before, after)
	sendLines := 0
	for _, line := range lines {
		if strings.Contains(line, "send:") || strings.Contains(line, "amq send") {
			sendLines++
		}
	}
	t.Errorf("MECHANISM 2 CANDIDATE: prompt text for role %s CHANGES across namespace creation", member.Role)
	t.Errorf("  before sha256=%s", digestRunArtifactBytes([]byte(before)))
	t.Errorf("  after  sha256=%s", digestRunArtifactBytes([]byte(after)))
	t.Errorf("  %d differing line(s), %d of them routing send: lines", len(lines), sendLines)
	for i, line := range lines {
		if i > 25 {
			t.Errorf("  ... further differences truncated")
			break
		}
		t.Errorf("  %s", line)
	}
}

// TestNamedProfileRenderAcrossNamespaceCreation is the one follow-up iteration
// the timebox allows, aimed at a variable every previous probe held constant.
//
// ALL of my probes so far used team.DefaultProfile. The 2026-08-02 field hit used
// the NAMED profile squad-v2-27-0. That is not a cosmetic difference:
// resolveAMQEnvForTeamLaunchProfile (amq_env.go:49-87) BRANCHES on it. The default
// profile asks real `amq env` and can fall back to a synthesized
// "fresh_project_default" envelope when AMQ cannot determine a root; a named
// profile takes resolveAMQEnvForTeamProfile and a deterministic
// squadnamespace.AMQRoot instead. Two different resolvers feed the root that is
// embedded throughout the rendered prompt.
//
// I noticed this branch in my first hour and never came back to it, which is
// why it is worth one run: a variable held constant across every probe is
// exactly where a surviving mechanism hides.
func TestNamedProfileRenderAcrossNamespaceCreation(t *testing.T) {
	const profile = "squad-probe-618"
	dir := t.TempDir()
	if err := team.WriteProfile(dir, profile, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		// The shared-cwd exception is required, not incidental: three
		// mutation-capable members in one directory is otherwise a readiness
		// BLOCKER, and the field-hit profile squad-v2-27-0 carried exactly this
		// exception. Without it the fixture fails on worktree_isolation before it
		// can say anything about bootstrap drift.
		SharedCwdException: "probe fixture: single-directory squad matching the #618 field-hit profile",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	}); err != nil {
		t.Fatalf("seed named profile: %v", err)
	}
	prepStdout, prepStderr, prepErr := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", profile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Hunt #618 mechanism 2 under a NAMED profile",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if prepErr != nil {
		// Print the blocked rows rather than only the summary. "artifact readiness
		// failed after preparation" on a fresh NAMED-profile namespace is itself
		// #618's shape, so the rows are the evidence, not noise to be swallowed.
		t.Errorf("prepare named-profile fixture: %v", prepErr)
		t.Errorf("--- stdout ---\n%s", prepStdout)
		t.Errorf("--- stderr ---\n%s", prepStderr)
		return
	}

	session := "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = initial
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	accepted := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}

	for _, member := range initial {
		before, err := preparedBootstrap(dir, profile, session, binding, tm, member, accepted)
		if err != nil {
			t.Fatalf("render %s before namespace creation: %v", member.Role, err)
		}
		acceptedDigest := manifest.BootstrapDigests[member.Role]
		if got := digestRunArtifactBytes([]byte(before)); got != acceptedDigest {
			t.Errorf("MECHANISM 2 FOUND (named profile, pre-creation): role %s re-render already differs"+
				" from the digest preparation accepted", member.Role)
			t.Errorf("  accepted=%s regenerated=%s", acceptedDigest, got)
			return
		}
	}

	root := squadnamespace.AMQRoot(dir, profile, session)
	for _, m := range tm.Members {
		handle := memberHandle(m)
		for _, sub := range []string{"inbox/cur", "inbox/new", "inbox/tmp", "outbox"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", handle, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, member := range initial {
		after, err := preparedBootstrap(dir, profile, session, binding, tm, member, accepted)
		if err != nil {
			t.Fatalf("render %s after namespace creation: %v", member.Role, err)
		}
		acceptedDigest := manifest.BootstrapDigests[member.Role]
		got := digestRunArtifactBytes([]byte(after))
		if got == acceptedDigest {
			t.Logf("named profile, role %s: render still matches the accepted digest after namespace creation", member.Role)
			continue
		}
		t.Errorf("MECHANISM 2 FOUND (named profile): role %s drifts from the accepted digest"+
			" once the namespace exists -- the field-hit signature", member.Role)
		t.Errorf("  accepted=%s regenerated=%s", acceptedDigest, got)
		before, _ := preparedBootstrap(dir, profile, session, binding, tm, member, accepted)
		for i, line := range diffPromptLines(before, after) {
			if i > 25 {
				t.Errorf("  ... truncated")
				break
			}
			t.Errorf("  %s", line)
		}
	}
}

// TestPanePathRenderMatchesAcceptedDigest renders the PANE path, which no probe
// and no existing fixture has ever rendered.
//
// Everything so far compared preparedBootstrap against preparedBootstrap and
// found the render deterministic across every boundary I could build: default
// profile, named profile, spawn materialization, real AMQ mailboxes. That
// establishes preparedBootstrap is stable. It says NOTHING about the path that
// actually runs in the pane, because the pane does not call preparedBootstrap.
//
// launch.go:697-701 builds the context with bootstrapContextFor and then
// overrides exactly ONE thing (CurrentTeam+Warnings). preparedBootstrap
// overrides FIVE (CurrentTeam+Warnings, Execution, ActorExecution, PlannerLead,
// MutationCapable). So four fields reach the pane prompt straight from
// bootstrapContextFor, which reads live state, while the ACCEPTED digest was
// computed with team-derived replacements for those same four fields.
//
// If mechanism 2 is anywhere, it is in those four fields, and this is the first
// comparison that can see them.
//
// WHAT IN THIS TEST'S DIFF IS THE TEST'S OWN FAULT. Recorded so a reader does
// not attribute fixture noise to the product. The launch.Record below is
// hand-built to mirror preparedBootstrap's construction, and it does not mirror
// it exactly, so three diff lines are MINE:
//   - the CWD spelling (/var vs /private/var): preparedBootstrap runs
//     canonicalDir on the member cwd; this rec does not.
//   - relative vs absolute AMQ root in the role/launch paths: preparedBootstrap
//     takes root from resolveAMQEnvForTeamLaunchProfile; this rec sets it directly.
//   - "native_goal" vs "native_goal_missing": preparedBootstrap sets
//     rec.GoalBinding for the lead; this rec leaves it empty.
//
// NOT the test's fault, and the reason this test exists: the routing block
// collapsing to "send: unavailable" in the ACCEPTED prompt. That comes from
// bootstrapCurrentTeamForTeam comparing samePath(rec.CWD, m.EffectiveCWD(...))
// where run_start_readiness.go:1103 resolved one side through canonicalDir and
// bootstrap.go:493 left the other raw. That asymmetry is real code and holds
// however the record was built.
func TestPanePathRenderMatchesAcceptedDigest(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		SharedCwdException: "probe fixture: single-directory squad matching the #618 field-hit profile",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Render the pane path for #618 mechanism 2",
			"--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare fixture: %v", err)
	}

	profile, session := team.DefaultProfile, "prepared"
	manifest, err := readPreparedRunManifest(dir, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = initial

	// Materialize what spawn writes, so the pane render sees the world the pane
	// actually sees.
	root := squadnamespace.AMQRoot(dir, profile, session)
	for _, m := range tm.Members {
		handle := memberHandle(m)
		materializeSpawnState(t, dir, profile, session, handle, m.Role)
		for _, sub := range []string{"inbox/cur", "inbox/new", "inbox/tmp", "outbox"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", handle, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, member := range initial {
		handle := memberHandle(member)
		agentDir := filepath.Join(root, "agents", handle)
		// rec mirrors preparedBootstrap's construction (run_start_readiness.go:1129-1136)
		// so the ONLY difference between the two renders is which fields get
		// overridden, not the record they are derived from.
		rec := launch.Record{
			Role: member.Role, Handle: handle, Binary: member.Binary,
			ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig,
			Session: session, CWD: dir, Root: root,
			TeamHome: dir, TeamProfile: profile, SharedWorkstream: true,
		}
		// Drive the REAL pane renderer, not a hand-assembled imitation of it.
		// The first version of this test rebuilt launch.go's context inline, which
		// meant it could pass while the shipped path still diverged -- a test of my
		// reconstruction rather than of the product. launchBootstrapPrompt IS what
		// the pane calls.
		panePrompt, err := launchBootstrapPrompt(rec, agentDir, dir, &preparedLaunchRecordContext{
			Manifest: manifest,
			Team:     tm,
			Member:   member,
			Binding: acceptedGoalBinding{
				Text: manifest.GoalText, Source: manifest.GoalSource,
				Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
			},
		})
		if err != nil {
			t.Fatalf("pane render for %s: %v", member.Role, err)
		}
		paneDigest := digestRunArtifactBytes([]byte(panePrompt))
		acceptedDigest := manifest.BootstrapDigests[member.Role]
		if paneDigest == acceptedDigest {
			t.Logf("role %s: PANE render matches the accepted digest", member.Role)
			continue
		}

		binding := acceptedGoalBinding{
			Text: manifest.GoalText, Source: manifest.GoalSource,
			Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
		}
		acceptedPrompt, renderErr := preparedBootstrap(dir, profile, session, binding, tm, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		t.Errorf("MECHANISM 2: role %s PANE render does NOT match the accepted digest", member.Role)
		t.Errorf("  accepted(prepare path)=%s", acceptedDigest)
		t.Errorf("  generated(pane path)  =%s", paneDigest)
		if renderErr != nil {
			t.Errorf("  (could not re-render accepted prompt for a diff: %v)", renderErr)
			continue
		}
		for i, line := range diffPromptLines(acceptedPrompt, panePrompt) {
			if i > 30 {
				t.Errorf("  ... truncated")
				break
			}
			t.Errorf("  %s", line)
		}
	}
}

// stagedRolesForShape618 returns the staged roles a shape's proposal will accept.
func stagedRolesForShape618(shape string) string {
	if shape == runwizard.LaunchShapeLeadOnlyStaged {
		return "senior-dev,qa"
	}
	return "qa"
}

// TestCrossSessionStagedRoleDoesNotBlockInitialRender is amq-dev-2's review
// finding, turned into a regression.
//
// A profile may legitimately pin a staged role to a DIFFERENT session than the
// one being launched; preparation accepts that shape deliberately (see
// TestPreparedRunMixedSessionRosterIsExactAcrossDefaultAndNamedProfiles), and
// preparedContextForLaunchRecord then hands the render a roster containing only
// the ACTIVE session's members.
//
// An earlier version of acceptedRenderRoster re-derived the initial roster by
// re-running partitionPreparedRunMembers over that already-filtered slice while
// supplying the complete manifest.StagedRoster. The helper requires every staged
// role to be present in the members it is given, so the cross-session staged
// role -- correctly absent -- made it refuse, and a valid prepared launch failed
// with "staged role %q has no complete profile member definition".
//
// Worth recording precisely because the intermediate state was WORSE than the
// original: before that, the error was swallowed and the un-narrowed roster
// returned (wrong roster, launch proceeds); propagating it turned a silent wrong
// answer into a refused launch. The real fix was to stop re-deriving and read
// manifest.InitialRoster, which preparation already recorded.
func TestCrossSessionStagedRoleDoesNotBlockInitialRender(t *testing.T) {
	const activeSession = "focus"
	prepared := &preparedLaunchRecordContext{
		Manifest: preparedRunManifest{
			InitialRoster: []string{"cto"},
			StagedRoster:  []string{"qa"},
		},
		// As preparedContextForLaunchRecord builds it: the cross-session staged
		// member is already gone.
		Team: team.Team{
			Lead: "cto",
			Members: []team.Member{
				{Role: "cto", Handle: "cto", Binary: "claude", Session: activeSession},
			},
		},
	}

	roster, err := acceptedRenderRoster(prepared)
	if err != nil {
		t.Fatalf("cross-session staged role must not block an initial render: %v", err)
	}

	if got := len(roster.Members); got != 1 {
		t.Fatalf("accepted render roster has %d members, want exactly the accepted initial roster [cto]", got)
	}
	if roster.Members[0].Role != "cto" {
		t.Fatalf("accepted render roster = %q, want cto", roster.Members[0].Role)
	}

	// And the staged role must not be reintroduced: reproducing InitialRoster
	// means the render sees what preparation accepted, no more and no less.
	for _, member := range roster.Members {
		if member.Role == "qa" {
			t.Fatalf("staged role qa leaked into the initial render roster")
		}
	}
}

// TestAcceptedInitialRoleMissingFromContextRefuses pins the OTHER half of the
// distinction. A staged role absent from the narrowed Team is normal -- it
// belongs to another session. An INITIAL role absent is corruption: the accepted
// manifest names a member the launch context cannot supply, so the accepted
// digest could not be reproduced even in principle. Refusing loudly beats
// rendering a quietly short roster, because a prompt missing a peer looks
// perfectly well-formed and routes to nobody.
func TestAcceptedInitialRoleMissingFromContextRefuses(t *testing.T) {
	prepared := &preparedLaunchRecordContext{
		Manifest: preparedRunManifest{
			InitialRoster: []string{"cto", "senior-dev"},
			StagedRoster:  []string{"qa"},
		},
		Team: team.Team{
			Lead: "cto",
			Members: []team.Member{
				{Role: "cto", Handle: "cto", Binary: "claude", Session: "focus"},
			},
		},
	}
	if _, err := acceptedRenderRoster(prepared); err == nil {
		t.Fatal("an accepted initial role absent from the launch context must refuse, not render short")
	} else if !strings.Contains(err.Error(), "senior-dev") {
		t.Fatalf("refusal must name the missing role, got: %v", err)
	}
}
