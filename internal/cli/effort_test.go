package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestEffortArgsForBinaryIncludesCurrentClaudeTiers(t *testing.T) {
	for _, tc := range []struct {
		binary string
		effort string
		want   []string
	}{
		{binary: "codex", effort: "high", want: []string{"-c", "model_reasoning_effort=high"}},
		{binary: "claude", effort: "medium", want: []string{"--effort", "medium"}},
		{binary: "claude", effort: "xhigh", want: []string{"--effort", "xhigh"}},
		{binary: "claude", effort: "max", want: []string{"--effort", "max"}},
		{binary: "codex", effort: "automatic", want: nil},
	} {
		got, err := effortArgsForBinary(tc.binary, tc.effort)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.binary, tc.effort, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s/%s = %#v, want %#v", tc.binary, tc.effort, got, tc.want)
		}
	}
}

func TestUnknownSupportedEffortWarnsAndPreservesExactSpelling(t *testing.T) {
	member := team.Member{Role: "qa", Binary: "claude"}
	_, stderr, err := captureOutput(t, func() error {
		return applyMemberEffort(&member, "  FutureTier  ")
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--effort", "FutureTier"}; !reflect.DeepEqual(member.ClaudeArgs, want) {
		t.Fatalf("claude args = %#v, want %#v", member.ClaudeArgs, want)
	}
	if strings.Count(stderr, "not in the merged catalog") != 1 || !strings.Contains(stderr, "FutureTier") {
		t.Fatalf("stderr = %q", stderr)
	}
	if _, err := effortArgsForBinary("other", "FutureTier"); err == nil || !strings.Contains(err.Error(), "choose codex or claude") {
		t.Fatalf("unsupported binary error = %v", err)
	}
	if _, err := effortArgsForBinary("claude", "  "); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("empty effort error = %v", err)
	}
}

func TestApplyLaunchEffortOverridesReplacesNativeArgsWithoutMutatingProfile(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Binary: "codex", CodexArgs: []string{"--profile", "fast", "-c", "model_reasoning_effort=low", "--config=model_reasoning_effort=medium", "-cmodel_reasoning_effort=max", "-c", "model=preserved", "--search"}},
		{Role: "qa", Binary: "claude", ClaudeArgs: []string{"--chrome", "--effort=medium", "--debug"}},
	}
	got, err := applyLaunchEffortOverrides(members, map[string]string{"cto": "xhigh", "qa": "automatic"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--profile", "fast", "-c", "model=preserved", "--search", "-c", "model_reasoning_effort=xhigh"}; !reflect.DeepEqual(got[0].CodexArgs, want) {
		t.Fatalf("cto args = %#v, want %#v", got[0].CodexArgs, want)
	}
	if want := []string{"--chrome", "--debug"}; !reflect.DeepEqual(got[1].ClaudeArgs, want) {
		t.Fatalf("qa args = %#v, want %#v", got[1].ClaudeArgs, want)
	}
	if want := []string{"--profile", "fast", "-c", "model_reasoning_effort=low", "--config=model_reasoning_effort=medium", "-cmodel_reasoning_effort=max", "-c", "model=preserved", "--search"}; !reflect.DeepEqual(members[0].CodexArgs, want) {
		t.Fatalf("stored cto args mutated: %#v", members[0].CodexArgs)
	}
	if want := []string{"--chrome", "--effort=medium", "--debug"}; !reflect.DeepEqual(members[1].ClaudeArgs, want) {
		t.Fatalf("stored qa args mutated: %#v", members[1].ClaudeArgs)
	}
}

func TestApplyLaunchEffortOverridesRejectsUnknownProfileRole(t *testing.T) {
	_, err := applyLaunchEffortOverrides([]team.Member{{Role: "cto", Binary: "codex"}}, map[string]string{"qa": "high"})
	if err == nil || !strings.Contains(err.Error(), "not present in the selected profile") {
		t.Fatalf("error = %v", err)
	}
}

func TestTeamShowEffortUsesProjectCatalogWithoutPersisting(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	home := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
	if err := team.Write(dir, team.Team{
		Workstream: "sess",
		Members: []team.Member{{
			Role: "qa", Binary: "claude", Handle: "qa", Session: "sess",
			ClaudeArgs: []string{"--chrome", "--effort", "low"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	writeCatalogFixture(t, filepath.Join(dir, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"claude": {"efforts": [{"value":"ProjectTier","label":"Project tier"}]}}
}`)

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--json", "--no-bootstrap", "--effort", "qa=projecttier"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(stdout)) || !strings.Contains(stdout, "--effort ProjectTier") {
		t.Fatalf("team show JSON did not use the project catalog's canonical effort:\n%s", stdout)
	}
	if strings.Contains(stderr, "not in the merged catalog") {
		t.Fatalf("known project effort warned as custom: %q", stderr)
	}
	stored, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stored.Members[0].ClaudeArgs, []string{"--chrome", "--effort", "low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("team show mutated stored args: got %#v want %#v", got, want)
	}
}
