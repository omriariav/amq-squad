package cli

import (
	"flag"
	"strings"
	"testing"

	runstarter "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func TestRepeatedRunStartRoleMapValueAccumulatesEveryOccurrence(t *testing.T) {
	for _, name := range []string{"binary", "model", "effort", "tool-profile"} {
		t.Run(name, func(t *testing.T) {
			fs := flag.NewFlagSet("run start", flag.ContinueOnError)
			value := registerRepeatedRoleMapFlag(fs, name, "test")
			if err := fs.Parse([]string{
				"--" + name, "cto=first",
				"--" + name, "qa=second",
			}); err != nil {
				t.Fatalf("parse repeated --%s: %v", name, err)
			}
			if got := *value; got != "cto=first,qa=second" {
				t.Fatalf("accumulated --%s = %q, want %q", name, got, "cto=first,qa=second")
			}
		})
	}
}

func TestRunStartRepeatedRoleMapFlagsReachPreparationAsOneMap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir,
			"--session", "repeated-flags",
			"--roles", "cto,qa",
			"--lead", "cto",
			"--binary", "cto=codex",
			"--binary", "qa=codex",
			"--model", "cto=gpt-5.6-sol",
			"--model", "qa=gpt-5.6-terra",
			"--effort", "cto=high",
			"--effort", "qa=medium",
			"--tool-profile", "cto=full",
			"--tool-profile", "qa=full",
			"--launch-shape", runstarter.LaunchShapeWorkingTeamTogether,
			"--goal", "Verify repeated role-map flags",
			"--visibility", "detached",
			"--shared-cwd-exception", "test fixture: repeated role-map parsing only",
			"--prepare-plan",
		}, "test")
	})
	if err != nil {
		t.Fatalf("repeated role-map preparation: %v\n%s", err, out)
	}
	for _, want := range []string{
		"member:cto",
		"handle=cto binary=codex model=gpt-5.6-sol effort=high",
		"member:qa",
		"handle=qa binary=codex model=gpt-5.6-terra effort=medium",
		"tool_policy=full",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("preparation proposal missing %q:\n%s", want, out)
		}
	}
}

func TestRunStartWizardPrefillAccumulatesEveryRepeatedRoleMapFlag(t *testing.T) {
	spec, err := parseRunStartWizardPrefill([]string{
		"--binary", "cto=codex",
		"--binary", "qa=claude",
		"--model", "cto=gpt-5.6-sol",
		"--model", "qa=claude-opus-4-1",
		"--effort", "cto=high",
		"--effort", "qa=max",
		"--tool-profile", "cto=full",
		"--tool-profile", "qa=coding",
	})
	if err != nil {
		t.Fatalf("parse interactive prefill: %v", err)
	}
	for name, got := range map[string]string{
		"binary":       spec.Binary,
		"model":        spec.Model,
		"effort":       spec.Effort,
		"tool-profile": spec.ToolProfile,
	} {
		wantByName := map[string]string{
			"binary":       "cto=codex,qa=claude",
			"model":        "cto=gpt-5.6-sol,qa=claude-opus-4-1",
			"effort":       "cto=high,qa=max",
			"tool-profile": "cto=full,qa=coding",
		}
		if got != wantByName[name] {
			t.Fatalf("interactive --%s = %q, want %q", name, got, wantByName[name])
		}
	}
}

func TestRunStartWizardPrefillRejectsRepeatedDuplicateRoleWithBothValues(t *testing.T) {
	for _, name := range []string{"binary", "model", "effort", "tool-profile"} {
		t.Run(name, func(t *testing.T) {
			_, err := parseRunStartWizardPrefill([]string{
				"--" + name, "qa=first",
				"--" + name, "QA=second",
			})
			if err == nil {
				t.Fatalf("interactive duplicate --%s role must fail", name)
			}
			for _, want := range []string{"--" + name, "qa", "first", "second"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("interactive duplicate --%s error %q missing %q", name, err, want)
				}
			}
		})
	}
}

func TestRunStartRepeatedRoleMapFlagsRejectDuplicateRoleWithBothValues(t *testing.T) {
	for _, name := range []string{"binary", "model", "effort", "tool-profile"} {
		t.Run(name, func(t *testing.T) {
			err := runRunStart([]string{
				"--" + name, "qa=first",
				"--" + name, "QA=second",
			}, "test")
			if err == nil {
				t.Fatalf("duplicate --%s role must fail", name)
			}
			for _, want := range []string{"--" + name, "qa", "first", "second"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("duplicate --%s error %q missing %q", name, err, want)
				}
			}
		})
	}
}

func TestRunStartHelpDocumentsRepeatedRoleMapFlags(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runRunCmd([]string{"--help"}, "test")
	})
	if err != nil {
		t.Fatalf("run start help: %v", err)
	}
	for _, want := range []string{
		"--binary, --model, --effort, and --tool-profile may each be repeated",
		"occurrences accumulate into one role map",
		"reports the role and both conflicting values",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("run start help missing %q:\n%s", want, stderr)
		}
	}
}
