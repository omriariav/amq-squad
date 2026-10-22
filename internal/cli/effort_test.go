package cli

import (
	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestTeamInitEffortPersistsOnlyExistingMemberArgs(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--project", dir, "--session", "sess", "--roles", "cto,qa", "--binary", "cto=codex,qa=claude", "--effort", "cto=high,qa=medium"})
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
				return runTeamInit([]string{"--project", dir, "--session", "sess", "--roles", "qa", "--binary", "qa=claude", "--effort", "qa=" + tc.effort})
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
			args := append([]string{"--project", dir, "--session", "sess"}, tc.args...)
			_, _, err := captureOutput(t, func() error { return runTeamInit(args) })
			if err == nil || !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("error = %v, want %q", err, tc.needle)
			}
		})
	}
}

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
		got, _, err := effortArgsForBinaryCatalog(tc.binary, tc.effort, agentcatalog.Builtins())
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
		return applyMemberEffortCatalog(&member, "  FutureTier  ", agentcatalog.Builtins())
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
	if _, _, err := effortArgsForBinaryCatalog("other", "FutureTier", agentcatalog.Builtins()); err == nil || !strings.Contains(err.Error(), "choose codex or claude") {
		t.Fatalf("unsupported binary error = %v", err)
	}
	if _, _, err := effortArgsForBinaryCatalog("claude", "  ", agentcatalog.Builtins()); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("empty effort error = %v", err)
	}
}
