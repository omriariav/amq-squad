package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestResumeExecRoleFilterDigestIgnoresLivenessOutsideSubset is cto's named
// requirement (task/t11, slice B commit 2 ruling): resume --exec --role's
// subject_digest binds the FILTERED roster, not the whole team -- a
// liveness change to a role OUTSIDE the --role subset must never change
// the computed subject_digest, since that role was never part of what got
// hashed (RoleFilter narrows SpawnTeam before prepare() ever sees it). The
// complementary claim -- a liveness change INSIDE the (unfiltered) roster
// DOES change the digest and refuses closed under a real re-Prepare -- is
// already covered by TestStartApplyRefusesWhenLivenessChangedSinceDigest;
// --role filtering is what changes which roles count as "inside."
func TestResumeExecRoleFilterDigestIgnoresLivenessOutsideSubset(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	chdir(t, project)
	const session = "work"
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	members := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: session},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: session},
	}
	if err := team.Write(project, team.Team{Project: project, SharedCwdException: "role-filter digest fixture", Members: members}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amq-squad", "team-rules.md"), []byte("test rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedBriefAt(t, project, team.DefaultProfile, session)
	simpleStartStubLaunchapiAMQEnv(t, root, session)

	const ctoPID = 5300
	alive := map[int]bool{ctoPID: false}
	agentDir := filepath.Join(root, "agents", "cto")
	// "cto" (outside the --role qa filter) has a prior record so it can
	// flip live/dead -- the same kind of race
	// TestStartApplyRefusesWhenLivenessChangedSinceDigest exercises for the
	// unfiltered roster, but here the flipping role must not be part of
	// the filtered subject_digest at all.
	if err := launch.Write(agentDir, launch.Record{
		Schema: launch.SchemaVersion, CWD: project, TeamHome: project, TeamProfile: team.DefaultProfile,
		Root: root, BaseRoot: filepath.Dir(root), Session: session,
		Role: "cto", Handle: "cto", Binary: "codex", Trust: trustModeSandboxed,
		ToolProfile: team.ToolProfileFull, AgentPID: ctoPID, AgentTTY: "/dev/ttys-test", StartedAt: time.Unix(1000, 0).UTC(),
		Tmux: &launch.TmuxInfo{Session: "test", WindowID: "@1", PaneID: "%51", Target: "new-window"},
	}); err != nil {
		t.Fatal(err)
	}

	deps := simpleStartDependencies{
		LookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		ResolveAMQEnv: func(proj, r, sess, handle string) (amqEnv, error) {
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: r, BaseRoot: filepath.Dir(r), SessionName: sess, Me: handle}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:         func(pid int) bool { return alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { return "/dev/ttys-test", alive[pid] },
			ProcessStartTime: func(pid int) (time.Time, bool) { return time.Unix(1000, 0).UTC(), alive[pid] },
			Now:              func() time.Time { return time.Unix(1000, 0).UTC() },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:         func(pid int) bool { return alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { return "/dev/ttys-test", alive[pid] },
			ProcessStartTime: func(pid int) (time.Time, bool) { return time.Unix(1000, 0).UTC(), alive[pid] },
			PaneTitle:        func(string) (string, bool) { return "", false },
		},
	}
	deps = normalizeSimpleStartDependencies(deps)

	computeDigest := func() string {
		req := simpleStartRequest{
			Project: project, Profile: team.DefaultProfile, Session: session, SessionExplicit: true,
			Options: teamLaunchOptions{
				Terminal: "tmux", Target: "new-window", Layout: "vertical",
				Profile: team.DefaultProfile, NoBootstrap: true, SimpleStart: true, AllowExistingSession: true,
			},
			LaunchapiPath: true, RoleFilter: []string{"qa"},
		}
		plan, err := buildSimpleStartPlan(req, deps)
		if err != nil {
			t.Fatalf("buildSimpleStartPlan: %v", err)
		}
		if len(plan.SpawnTeam.Members) != 1 || plan.SpawnTeam.Members[0].Role != "qa" {
			t.Fatalf("SpawnTeam = %+v, want exactly the --role-filtered qa member", plan.SpawnTeam.Members)
		}
		prepared, _, err := (launchapiTeamLaunchBackend{}).prepare(plan.SpawnTeam, plan.LaunchOptions)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		return prepared.Result.SubjectDigest
	}

	alive[ctoPID] = false
	digestBefore := computeDigest()
	alive[ctoPID] = true
	digestAfter := computeDigest()
	if digestBefore != digestAfter {
		t.Fatalf("subject_digest changed when only the out-of-filter role cto's liveness changed: before=%s after=%s -- the digest must bind only the --role-filtered roster", digestBefore, digestAfter)
	}
}

// TestStartRestoresLaunchapiMintedConversationNatively is gh#758/t11 slice
// C's named positive-path acceptance test for work item 2 (cto's ruling,
// task/t11): when amq's own session Inspect reports a minted conversation
// for a seat about to be (re)launched on the launchapi path, that seat's
// compiled ResumePolicy is Resume -- proven through the real, shared
// buildSimpleStartPlan -> launchapiTeamLaunchBackend.prepare seam
// (Prepare is documented zero-write, so this is safe against a real
// launchintent.Compile without any Apply). A sibling seat Inspect has no
// evidence for gets an ordinary fresh mint. Named "Start", not "Resume":
// the mechanism lives in buildIntentInput, shared by both verbs -- this
// test exercises it through start's own request shape.
func TestStartRestoresLaunchapiMintedConversationNatively(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	chdir(t, project)
	const session = "work"
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	members := []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: session},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: session},
	}
	if err := team.Write(project, team.Team{Project: project, SharedCwdException: "native restore fixture", Members: members}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amq-squad", "team-rules.md"), []byte("test rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedBriefAt(t, project, team.DefaultProfile, session)
	simpleStartStubLaunchapiAMQEnv(t, root, session)
	stubLaunchapiInspect(t, func(context.Context, launchapi.InspectRequestV1) (launchapi.InspectResultV1, error) {
		return launchapi.InspectResultV1{State: "present", Observations: []launchapi.ParticipantObservationV1{
			{Handle: "cto", Runnable: true, Conversation: "cto-thread"},
			{Handle: "qa", Runnable: true},
		}}, nil
	})

	deps := simpleStartDependencies{
		LookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		ResolveAMQEnv: func(proj, r, sess, handle string) (amqEnv, error) {
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: r, BaseRoot: filepath.Dir(r), SessionName: sess, Me: handle}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now},
		RuntimeProbe:   launchRuntimeProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, PaneTitle: func(string) (string, bool) { return "", false }},
	}
	deps = normalizeSimpleStartDependencies(deps)

	req := simpleStartRequest{
		Project: project, Profile: team.DefaultProfile, Session: session, SessionExplicit: true,
		Options: teamLaunchOptions{
			Terminal: "tmux", Target: "new-window", Layout: "vertical",
			Profile: team.DefaultProfile, NoBootstrap: true, SimpleStart: true, AllowExistingSession: true,
		},
		LaunchapiPath: true,
	}
	plan, err := buildSimpleStartPlan(req, deps)
	if err != nil {
		t.Fatalf("buildSimpleStartPlan: %v", err)
	}
	if len(plan.SpawnTeam.Members) != 2 {
		t.Fatalf("SpawnTeam = %+v, want both fresh/unmanaged members", plan.SpawnTeam.Members)
	}
	prepared, _, err := (launchapiTeamLaunchBackend{}).prepare(plan.SpawnTeam, plan.LaunchOptions)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	var checkedCTO, checkedQA bool
	for _, p := range prepared.Result.Preview.Participants {
		switch p.Handle {
		case "cto":
			checkedCTO = true
			if p.ResumePolicy != launchapi.ResumePolicyResume {
				t.Errorf("cto ResumePolicy = %q, want resume (Inspect reported a minted conversation)", p.ResumePolicy)
			}
		case "qa":
			checkedQA = true
			if p.ResumePolicy == launchapi.ResumePolicyResume {
				t.Errorf("qa has no Inspect-reported conversation but got ResumePolicy=resume")
			}
		}
	}
	if !checkedCTO || !checkedQA {
		t.Fatalf("did not observe both participants: cto=%t qa=%t", checkedCTO, checkedQA)
	}
}
