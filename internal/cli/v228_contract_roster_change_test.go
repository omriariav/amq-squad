package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// AC12: launch is a reconciler, so roster edits are free. On a running 3-agent
// team, adding a role and running `start` spawns exactly the one new agent and
// leaves the three live ones untouched; `down <role>` stops exactly that agent.
// No separate roster-edit machinery, no manifest to invalidate.

func TestV228ContractRosterAddSpawnsOnlyTheNewAgent(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	const session = "ac12"
	original := []string{"cto", "dev", "qa"}
	fixture := v228NewStartFixture(t, session, v228StartMembers(session, original...))
	pids := map[string]int{"cto": 5001, "dev": 5002, "qa": 5003, "docs": 5004}
	pidFor := func(role string) int { return pids[role] }
	alive := map[int]bool{}

	first := v228RunStart(t, fixture, alive, pidFor)
	if first.Err != nil {
		t.Fatalf("initial start: %v\n%s", first.Err, first.Output)
	}
	if len(first.SpawnedRole) != len(original) {
		t.Fatalf("initial start spawned %v, want all of %v", first.SpawnedRole, original)
	}
	// Freeze what the three live records look like, so "untouched" is checkable
	// rather than asserted.
	before := map[string]launch.Record{}
	for _, role := range original {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatal(err)
		}
		before[role] = rec
	}

	// Add a role to the roster. That is the whole edit.
	v228SeedProfile(t, fixture.Project, fixture.Profile, session, v228StartMembers(session, append(append([]string{}, original...), "docs")...))

	second := v228RunStart(t, fixture, alive, pidFor)
	if second.Err != nil {
		t.Fatalf("start after roster add: %v\n%s", second.Err, second.Output)
	}
	if len(second.SpawnedRole) != 1 || second.SpawnedRole[0] != "docs" {
		t.Fatalf("start after roster add spawned %v, want exactly [docs]", second.SpawnedRole)
	}
	// The plan showed the delta rather than the whole roster as pending work.
	if !strings.Contains(second.Output, "docs") {
		t.Errorf("start plan did not name the added role:\n%s", second.Output)
	}

	for _, role := range original {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatalf("live role %s lost its record: %v", role, err)
		}
		if rec.AgentPID != before[role].AgentPID || !rec.StartedAt.Equal(before[role].StartedAt) {
			t.Errorf("%s was disturbed by the roster add: pid %d->%d started %s->%s",
				role, before[role].AgentPID, rec.AgentPID, before[role].StartedAt, rec.StartedAt)
		}
		if before[role].Tmux != nil && (rec.Tmux == nil || rec.Tmux.PaneID != before[role].Tmux.PaneID) {
			t.Errorf("%s pane ownership changed across the roster add", role)
		}
	}
	docs, err := launch.Read(filepath.Join(fixture.Root, "agents", "docs"))
	if err != nil || docs.AgentPID != pids["docs"] {
		t.Fatalf("added role docs = %+v (err %v), want a live record at pid %d", docs, err, pids["docs"])
	}
}

func TestV228ContractDownStopsExactlyTheNamedRole(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	const session = "ac12"
	roles := []string{"cto", "dev", "qa"}
	fixture := v228NewStartFixture(t, session, v228StartMembers(session, roles...))
	pids := map[string]int{"cto": 5011, "dev": 5012, "qa": 5013}
	alive := map[int]bool{}
	if run := v228RunStart(t, fixture, alive, func(role string) int { return pids[role] }); run.Err != nil {
		t.Fatalf("initial start: %v\n%s", run.Err, run.Output)
	}

	terminator := &recordingTerminator{}
	out, err := runDownExec(t, downExecution{
		Verb:             "stop",
		ProjectDir:       fixture.Project,
		Profile:          fixture.Profile,
		RequestedSession: session,
		ExplicitProject:  true,
		ExplicitProfile:  true,
		ExplicitSession:  true,
		Role:             "dev",
		Terminator:       terminator,
		Probe:            v228Probe(pids["cto"], pids["dev"], pids["qa"]),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("down --role dev: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[downEnvelopeData](t, out)
	if len(env.Data.Reports) != 1 || env.Data.Reports[0].Role != "dev" {
		t.Fatalf("down reports = %+v, want exactly the dev actor", env.Data.Reports)
	}
	if got := terminator.calls; len(got) != 1 || got[0] != pids["dev"] {
		t.Fatalf("down signaled %v, want only pid %d", got, pids["dev"])
	}

	// The record is archived (stopped), not deleted, and the siblings are intact.
	stopped, err := launch.Read(filepath.Join(fixture.Root, "agents", "dev"))
	if err != nil {
		t.Fatalf("down deleted the dev record instead of archiving it: %v", err)
	}
	if stopped.StoppedAt == nil || stopped.StoppedAt.IsZero() {
		t.Errorf("dev record has no stopped_at after down: %+v", stopped)
	}
	for _, role := range []string{"cto", "qa"} {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatalf("sibling %s lost its record: %v", role, err)
		}
		if rec.StoppedAt != nil {
			t.Errorf("down --role dev also stopped %s", role)
		}
	}
}
