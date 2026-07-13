package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

func TestTeamInitEffortPersistsOnlyExistingMemberArgs(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--binary", "cto=codex,qa=claude", "--effort", "cto=high,qa=medium"})
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Members) != 2 {
		t.Fatalf("members = %+v", cfg.Members)
	}
	if !reflect.DeepEqual(cfg.Members[0].CodexArgs, []string{"-c", "model_reasoning_effort=high"}) {
		t.Fatalf("cto codex_args = %#v", cfg.Members[0].CodexArgs)
	}
	if !reflect.DeepEqual(cfg.Members[1].ClaudeArgs, []string{"--effort", "medium"}) {
		t.Fatalf("qa claude_args = %#v", cfg.Members[1].ClaudeArgs)
	}
}

func TestTeamInitEffortKnownAndCustomWarningContract(t *testing.T) {
	for _, tc := range []struct {
		name        string
		effort      string
		want        string
		wantWarning bool
	}{
		{name: "claude xhigh", effort: "xhigh", want: "xhigh"},
		{name: "claude max canonical", effort: "MAX", want: "max"},
		{name: "future exact", effort: "FutureTier", want: "FutureTier", wantWarning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, stderr, err := captureOutput(t, func() error {
				return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "qa", "--binary", "qa=claude", "--effort", "qa=" + tc.effort})
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(stderr, "not in the merged catalog"); got != tc.wantWarning {
				t.Fatalf("warning=%t stderr=%q", got, stderr)
			}
			cfg, err := team.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"--effort", tc.want}; !reflect.DeepEqual(cfg.Members[0].ClaudeArgs, want) {
				t.Fatalf("args=%#v want=%#v", cfg.Members[0].ClaudeArgs, want)
			}
		})
	}
}

func TestTeamInitEffortRejectsUnknownRole(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		needle string
	}{
		{name: "unknown role", args: []string{"--roles", "cto", "--effort", "qa=high"}, needle: "not selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{"team", "--project", dir, "--session", "sess"}, tc.args...)
			_, _, err := captureOutput(t, func() error { return runNew(args) })
			if err == nil || !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("error = %v, want %q", err, tc.needle)
			}
		})
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

func TestTeamMemberAddEffortDryRunJSONAndLiveParity(t *testing.T) {
	dir := t.TempDir()
	if err := team.Write(dir, team.Team{
		Workstream: "sess",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"}},
	}); err != nil {
		t.Fatal(err)
	}
	args := []string{"add", "qa", "--project", dir, "--binary", "claude", "--effort", "FutureTier", "--dry-run", "--json"}
	stdout, stderr, err := captureOutput(t, func() error { return runTeamMember(args) })
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(stdout)) || !strings.Contains(stdout, `"preview"`) {
		t.Fatalf("dry-run JSON = %q", stdout)
	}
	if strings.Count(stderr, "not in the merged catalog") != 1 {
		t.Fatalf("dry-run stderr = %q", stderr)
	}
	before, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Members) != 1 {
		t.Fatalf("dry-run wrote profile: %+v", before.Members)
	}

	liveArgs := []string{"add", "qa", "--project", dir, "--binary", "claude", "--effort", "FutureTier", "--json"}
	stdout, stderr, err = captureOutput(t, func() error { return runTeamMember(liveArgs) })
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(stdout)) || strings.Count(stderr, "not in the merged catalog") != 1 {
		t.Fatalf("live JSON/stdout=%q stderr=%q", stdout, stderr)
	}
	after, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Members) != 2 || !reflect.DeepEqual(after.Members[1].ClaudeArgs, []string{"--effort", "FutureTier"}) {
		t.Fatalf("live member = %+v", after.Members)
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

func TestTeamMemberAddEffortKnownAutomaticAndAtomicFailure(t *testing.T) {
	t.Run("known project tier is canonicalized", func(t *testing.T) {
		dir := t.TempDir()
		home := t.TempDir()
		oldHome := agentCatalogUserHomeDir
		agentCatalogUserHomeDir = func() (string, error) { return home, nil }
		t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
		if err := team.Write(dir, team.Team{Workstream: "sess", Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"}}}); err != nil {
			t.Fatal(err)
		}
		writeCatalogFixture(t, filepath.Join(dir, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"claude": {"efforts": [{"value":"ProjectTier"}]}}
}`)
		_, stderr, err := captureOutput(t, func() error {
			return runTeamMember([]string{"add", "qa", "--project", dir, "--binary", "claude", "--effort", "projecttier"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr, "not in the merged catalog") {
			t.Fatalf("known effort warning = %q", stderr)
		}
		cfg, err := team.Read(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := cfg.Members[1].ClaudeArgs, []string{"--effort", "ProjectTier"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("known effort args = %#v, want %#v", got, want)
		}
	})

	t.Run("automatic strips supplied native effort", func(t *testing.T) {
		dir := t.TempDir()
		if err := team.Write(dir, team.Team{Workstream: "sess", Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"}}}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := captureOutput(t, func() error {
			return runTeamMember([]string{"add", "qa", "--project", dir, "--binary", "claude", "--claude-args", "--chrome --effort low", "--effort", "automatic"})
		}); err != nil {
			t.Fatal(err)
		}
		cfg, err := team.Read(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := cfg.Members[1].ClaudeArgs, []string{"--chrome"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("automatic args = %#v, want %#v", got, want)
		}
	})

	t.Run("structural failure leaves profile byte exact", func(t *testing.T) {
		dir := t.TempDir()
		if err := team.Write(dir, team.Team{Workstream: "sess", Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"}}}); err != nil {
			t.Fatal(err)
		}
		path := team.ProfilePath(dir, team.DefaultProfile)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = captureOutput(t, func() error {
			return runTeamMember([]string{"add", "qa", "--project", dir, "--binary", "claude", "--handle", "cto", "--effort", "FutureTier"})
		})
		if err == nil || !strings.Contains(err.Error(), "handle") {
			t.Fatalf("structural failure = %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatal("failed member add changed the profile")
		}
	})
}
