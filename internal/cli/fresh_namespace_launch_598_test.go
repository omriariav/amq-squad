package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// materializeSpawnState reproduces what spawning actually writes into a fresh
// namespace: the agent mailbox directory, the extension layer, role.md, and
// launch.json. Readiness is calculated before and after so any field whose
// value depends on that state shows up as drift.
func materializeSpawnState(t *testing.T, project, profile, session, handle, roleID string) string {
	t.Helper()
	agentDir := filepath.Join(squadnamespace.AMQRoot(project, profile, session), "agents", handle)
	if err := os.MkdirAll(role.ExtensionDir(agentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role.Path(agentDir), []byte("---\nid: "+roleID+"\n---\n\n# Role\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{
		Role: roleID, Handle: handle, Session: session,
		CWD: project, TeamHome: project, TeamProfile: profile,
		StartedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launch.Path(agentDir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

func readinessRowsByArtifact(res runReadinessResult) map[string]runReadinessRow {
	out := make(map[string]runReadinessRow, len(res.Rows))
	for _, row := range res.Rows {
		out[row.Artifact] = row
	}
	return out
}

// TestPreparedBootstrapRenderIsDeterministicWithoutStateChange settles whether
// the accept-time stamp inside preparedBootstrap (bootstrapack.NewExpectation,
// which calls time.Now()) reaches the digested prompt text. If it does, EVERY
// re-render drifts and #598 root cause 1 is a far broader bug than the issue
// describes. This test changes nothing between the two renders, so a mismatch
// here can only come from a non-deterministic input.
func TestPreparedBootstrapRenderIsDeterministicWithoutStateChange(t *testing.T) {
	project := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	profile, session := team.DefaultProfile, "prepared"

	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	accepted := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}

	for _, member := range tm.Members {
		first, err := preparedBootstrap(project, profile, session, binding, tm, member, accepted)
		if err != nil {
			t.Fatalf("first render for %s: %v", member.Role, err)
		}
		time.Sleep(1100 * time.Millisecond) // cross a whole-second boundary
		second, err := preparedBootstrap(project, profile, session, binding, tm, member, accepted)
		if err != nil {
			t.Fatalf("second render for %s: %v", member.Role, err)
		}
		if digestRunArtifactBytes([]byte(first)) != digestRunArtifactBytes([]byte(second)) {
			t.Errorf("preparedBootstrap for %s is NOT deterministic across renders with zero state change; a timestamp or other non-deterministic input reaches the digested prompt", member.Role)
			for i, line := range diffPromptLines(first, second) {
				if i > 20 {
					t.Errorf("  ... further differences truncated")
					break
				}
				t.Errorf("  %s", line)
			}
			continue
		}
		if digestRunArtifactBytes([]byte(first)) != manifest.BootstrapDigests[member.Role] {
			t.Errorf("preparedBootstrap for %s is deterministic but does NOT match the accepted digest recorded at preparation time; the accepted preview was captured from a different render path", member.Role)
			continue
		}
		t.Logf("preparedBootstrap for %s is deterministic and matches the accepted digest", member.Role)
	}
}

// diffPromptLines reports the first differing lines between two renders so a
// drift names the field that moved instead of only asserting inequality. This
// is the surface #598 root cause 4 says the CLI is missing.
func diffPromptLines(a, b string) []string {
	linesA, linesB := strings.Split(a, "\n"), strings.Split(b, "\n")
	out := []string{}
	max := len(linesA)
	if len(linesB) > max {
		max = len(linesB)
	}
	for i := 0; i < max; i++ {
		var la, lb string
		if i < len(linesA) {
			la = linesA[i]
		}
		if i < len(linesB) {
			lb = linesB[i]
		}
		if la != lb {
			out = append(out, "line "+strconv.Itoa(i+1)+":\n    accepted:  "+la+"\n    generated: "+lb)
		}
	}
	return out
}

// TestFreshNamespaceReadinessSurvivesSpawnMaterialization is the #598 root
// cause 1 repro. run start --go validates the bootstrap rows as ready and
// spawns; each agent up then re-validates the SAME rows against a world that
// spawning itself just changed. If the two disagree, a fresh namespace is
// bricked on first launch: the panes die on drift and the accepted preview can
// never be satisfied again.
func TestFreshNamespaceReadinessSurvivesSpawnMaterialization(t *testing.T) {
	project := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	profile, session := team.DefaultProfile, "prepared"

	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	accepted := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}

	before := calculateRunReadinessWithContext(project, profile, session, accepted)
	if !before.Ready {
		for _, row := range before.Rows {
			if row.Status != "ready" {
				t.Logf("pre-spawn row %s/%s: %s", row.Artifact, row.Status, row.Evidence)
			}
		}
		t.Fatalf("fixture must be ready before spawn; readiness=%v", before.Ready)
	}

	for _, roleID := range manifest.InitialRoster {
		materializeSpawnState(t, project, profile, session, roleID, roleID)
	}

	after := calculateRunReadinessWithContext(project, profile, session, accepted)
	if after.Ready {
		return
	}
	beforeRows := readinessRowsByArtifact(before)
	for _, row := range after.Rows {
		if row.Status == "ready" {
			continue
		}
		t.Errorf("row %q flipped %q -> %q after spawn materialized state it validates against\n  evidence: %s\n  fix text: %s",
			row.Artifact, beforeRows[row.Artifact].Status, row.Status, row.Evidence, row.Fix)
	}
	t.Fatalf("fresh namespace bricks: readiness passed pre-spawn and failed post-spawn (#598 RC1)")
}
