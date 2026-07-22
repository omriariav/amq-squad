package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func stagedProjectionRecord(t *testing.T, project string, token preparedRunToken, claim preparedRunStagedClaim) launch.Record {
	t.Helper()
	runtimeCWD, err := canonicalDir(project)
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: runtimeCWD, TeamHome: project, TeamProfile: team.DefaultProfile,
		Session: "prepared", SharedWorkstream: true, Role: claim.Role, Handle: claim.Handle,
		Root: filepath.Join(".agent-mail", "prepared"), StartedAt: time.Now().UTC(),
		Tmux:                 &launch.TmuxInfo{Session: "fixture", WindowID: "@2", PaneID: "%2"},
		BootstrapExpectation: &bootstrapack.Expectation{Required: true},
	}
	applyPreparedRunStagedEffectiveIdentity(&rec, claim.Effective)
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	applyPreparedRunTokenToRecord(&rec, launchToken)
	return rec
}

func preparedStagedProjectionFixture(t *testing.T, binary string) (string, preparedRunManifest, preparedRunToken, preparedRunStagedClaim) {
	t.Helper()
	project := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "prepared", ActorMode: team.ActorModeImplementation},
			{Role: "qa", Handle: "qa", Binary: binary, Session: "prepared", ActorMode: team.ActorModeImplementation, ToolProfile: team.ToolProfileFull},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", project, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeLeadOnlyStaged, "--staged-roles", "qa",
			"--goal", "Review the accepted staged fixture", "--visibility", "detached", "--prepare",
		}, "test")
	}); err != nil {
		t.Fatal(err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	attempt, err := reservePreparedRunLaunch(project, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = attempt
	if err := consumePreparedRunMember(project, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunGoal(project, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := completePreparedRunLaunch(project, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatal(err)
	}
	token = token.generationRef()
	seedPreparedStagedAuthorizer(t, project, token)
	claim, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	return project, manifest, token, claim
}

func TestPreparedStagedReviewProjectionControlsBootstrapAndDispatchCodexClaude(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			project, manifest, token, claim := preparedStagedProjectionFixture(t, binary)
			rec := stagedProjectionRecord(t, project, token, claim)
			tm, err := team.ReadProfile(project, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := projectPreparedRunStagedTeamForRecord(tm, rec)
			if err != nil {
				t.Fatal(err)
			}
			member, ok := teamMemberByRole(projected, "qa")
			if !ok || team.EffectiveActorMode(projected, member) != team.ActorModeReview {
				t.Fatalf("projected staged member=%+v ok=%t", member, ok)
			}
			contract := executionContractForTeam(projected, team.DefaultProfile, "prepared", "amq_task_brief", "", "dev")
			actor := actorExecutionContractForTeam(projected, "qa", "qa", contract)
			if actor.ImplementationAllowedForYou {
				t.Fatalf("review-only staged actor can dispatch implementation: %+v", actor)
			}

			agentDir := filepath.Join(rec.Root, "agents", rec.Handle)
			prompt, err := buildBootstrapPrompt(bootstrapContextFor(rec, agentDir, project))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Implementation allowed for you: false", "Read-only actor posture:", "Do not edit implementation files"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s review bootstrap missing %q:\n%s", binary, want, prompt)
				}
			}
			context, err := preparedContextForLaunchRecord(rec)
			if err != nil || context == nil || context.Manifest.Generation != manifest.Generation {
				t.Fatalf("prepared staged context=%+v err=%v", context, err)
			}
			if err := validatePreparedBootstrapPromptAgainstContext(rec, prompt, context); err != nil {
				t.Fatalf("review-only prepared bootstrap validation: %v", err)
			}
		})
	}
}

func TestPreparedStagedProjectionFailsClosedAcrossBootstrapAndDispatch(t *testing.T) {
	t.Run("missing claim token", func(t *testing.T) {
		project, _, token, claim := preparedStagedProjectionFixture(t, "codex")
		rec := stagedProjectionRecord(t, project, token, claim)
		rec.PreparedRunGeneration = ""
		tm, err := team.ReadProfile(project, team.DefaultProfile)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projectPreparedRunStagedTeamForRecord(tm, rec); err == nil || !strings.Contains(err.Error(), "complete claim-bound") {
			t.Fatalf("bootstrap projection missing-token error=%v", err)
		}
		projected, err := projectPreparedRunStagedTeamForTarget(project, team.DefaultProfile, "prepared", "qa", tm)
		if err != nil {
			t.Fatalf("dispatch projection should resolve the authoritative active claim without a caller token: %v", err)
		}
		member, ok := teamMemberByRole(projected, "qa")
		if !ok || team.EffectiveActorMode(projected, member) != team.ActorModeReview {
			t.Fatalf("dispatch projection recovered implementation authority: member=%+v ok=%t", member, ok)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string, preparedRunToken)
	}{
		{name: "corrupt snapshot", mutate: func(t *testing.T, project string, _ preparedRunToken) {
			if err := os.WriteFile(preparedRunPath(project, team.DefaultProfile, "prepared"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing active pointer", mutate: func(t *testing.T, project string, token preparedRunToken) {
			if err := os.Remove(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stale active claim", mutate: func(t *testing.T, project string, token preparedRunToken) {
			path := preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa")
			pointer, err := readPreparedRunStagedClaimPointer(path)
			if err != nil {
				t.Fatal(err)
			}
			pointer.ClaimID = strings.Repeat("f", 32)
			data, err := json.Marshal(pointer)
			if err != nil {
				t.Fatal(err)
			}
			if err := durableReplace(path, data); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, token, claim := preparedStagedProjectionFixture(t, "codex")
			rec := stagedProjectionRecord(t, project, token, claim)
			tm, err := team.ReadProfile(project, team.DefaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, project, token)
			if _, err := projectPreparedRunStagedTeamForRecord(tm, rec); err == nil {
				t.Fatal("bootstrap projection recovered implementation authority from damaged staged state")
			}
			if _, err := projectPreparedRunStagedTeamForTarget(project, team.DefaultProfile, "prepared", "qa", tm); err == nil {
				t.Fatal("dispatch projection recovered implementation authority from damaged staged state")
			}
		})
	}
}
