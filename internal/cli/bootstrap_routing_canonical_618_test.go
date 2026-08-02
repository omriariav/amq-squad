package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// BEHAVIOURAL BISECT FOR THIS PR. Both tests here FAIL at this PR's parent and
// PASS with the fix, reporting the real defect: every peer line reading
// "send: unavailable (AMQ project identity is missing...)" in a squad where all
// members share one checkout.
//
// They assert the routing PROPERTY rather than digest equality, deliberately.
// Two identically-broken prompts would satisfy every digest-equality check in
// this PR while shipping a squad that cannot coordinate, so equality is
// necessary and not sufficient.
//
// HISTORY, because these comments previously said the opposite and a reader
// deserves to know why. The canonicalization these tests cover was briefly
// dropped from this PR on the strength of a bisect that measured the wrong tree
// -- a git stash did not apply as assumed, so the run that "passed at the
// parent" was almost certainly measuring the tree WITH the fix. A full-package
// -race run failed both tests and reversed the decision; the fix is restored.
// The lesson is worth more than the incident: a bisect conclusion is only as
// good as the proof of which tree it measured.
func TestAcceptedPromptNeverRendersUnroutablePeers(t *testing.T) {
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
			"--goal", "Accepted prompts must route to peers sharing the launch cwd",
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

	for _, member := range initial {
		prompt, err := preparedBootstrap(dir, profile, session, binding, tm, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		if err != nil {
			t.Fatalf("accepted render for %s: %v", member.Role, err)
		}
		if !strings.Contains(prompt, "Current team routing:") {
			t.Fatalf("role %s: accepted prompt has no routing block at all", member.Role)
		}
		if strings.Contains(prompt, "send: unavailable") {
			t.Errorf("role %s: ACCEPTED prompt reports peers as unroutable while every member"+
				" shares the launch cwd; the routing comparison is trusting a recorded"+
				" spelling instead of a canonical one (#618)", member.Role)
			for _, line := range strings.Split(prompt, "\n") {
				if strings.Contains(line, "send:") {
					t.Errorf("  %s", strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestAcceptedPromptRoutesPeersWithExplicitlyRecordedCWD is the bisecting
// regression for the canonical-comparison fix, and it exists because the
// ORIGINAL one stopped bisecting.
//
// The first version of this regression used members with no explicit CWD, so
// both operands of the samePath comparison derived from the profile's Project
// path. #617/#621 then canonicalized that path, which made both sides agree and
// fixed the routing collapse from the other side of the same seam. The test kept
// passing at its parent and therefore proved nothing about THIS change.
//
// A member with an EXPLICITLY RECORDED CWD is the case #621 does not reach. The
// recorded spelling comes straight from team.json and is compared against a
// canonicalDir-resolved rec.CWD, so a member whose recorded path is the
// symlinked spelling of the same directory still compares unequal without this
// fix. On macOS t.TempDir() lives under /var -> /private/var, so recording the
// unresolved spelling exercises it without contriving anything.
func TestAcceptedPromptRoutesPeersWithExplicitlyRecordedCWD(t *testing.T) {
	dir := t.TempDir()
	// Record the UNRESOLVED spelling deliberately: this is what an operator's
	// shell hands the tool, and what os.Getwd reports when $PWD carries it.
	if err := team.Write(dir, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		SharedCwdException: "regression fixture: one checkout, recorded unresolved",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "claude", Session: "prepared", CWD: dir},
			{Role: "senior-dev", Handle: "senior-dev", Binary: "claude", Session: "prepared", CWD: dir},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: "prepared", CWD: dir, ToolProfile: team.ToolProfileFull},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Peers sharing an explicitly recorded cwd must stay routable",
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
	for _, member := range initial {
		prompt, err := preparedBootstrap(dir, profile, session, binding, tm, member,
			acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
		if err != nil {
			t.Fatalf("accepted render for %s: %v", member.Role, err)
		}
		if strings.Contains(prompt, "send: unavailable") {
			t.Errorf("role %s: peers reported unroutable although every member records the SAME"+
				" directory; the comparison is trusting the recorded spelling (#618)", member.Role)
			for _, line := range strings.Split(prompt, "\n") {
				if strings.Contains(line, "send:") {
					t.Errorf("  %s", strings.TrimSpace(line))
				}
			}
		}
	}
}
