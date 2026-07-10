package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestEffortArgsForBinaryNormalizesWithoutNewSemantics(t *testing.T) {
	for _, tc := range []struct {
		binary string
		effort string
		want   []string
	}{
		{binary: "codex", effort: "high", want: []string{"-c", "model_reasoning_effort=high"}},
		{binary: "claude", effort: "medium", want: []string{"--effort", "medium"}},
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

func TestTeamInitEffortRejectsRoleAndBinaryMismatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		needle string
	}{
		{name: "unknown role", args: []string{"--roles", "cto", "--effort", "qa=high"}, needle: "not selected"},
		{name: "claude xhigh", args: []string{"--roles", "qa", "--binary", "qa=claude", "--effort", "qa=xhigh"}, needle: "unsupported claude effort"},
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
