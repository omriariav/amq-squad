package cli

import (
	"reflect"
	"testing"
)

func TestComposeBinaryArgsClaudePrecedenceAndRepeatables(t *testing.T) {
	got := composeBinaryArgs("claude",
		[]string{"--effort", "low", "--plugin-dir", "a", "--plugin-dir", "a", "--model=old"},
		[]string{"--effort", "high", "--permission-mode", "plan"},
		[]string{"--effort", "max", "--model", "new", "--permission-mode=auto"},
	)
	want := []string{"--plugin-dir", "a", "--plugin-dir", "a", "--effort", "max", "--model", "new", "--permission-mode=auto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestComposeBinaryArgsCodexConfigByKeyAndNativeSingletons(t *testing.T) {
	got := composeBinaryArgs("codex",
		[]string{"-c", "model_reasoning_effort=low", "-c", "model=old", "--model", "old-native", "--enable", "goals"},
		[]string{"-c", "model_reasoning_effort=high", "--profile", "launch", "--enable", "goals"},
		[]string{"-c", "model_reasoning_effort=max", "-c", "model=new", "--model", "new-native", "--profile=member", "--sandbox", "workspace-write", "--ask-for-approval=never"},
	)
	want := []string{"--enable", "goals", "--enable", "goals", "-c", "model_reasoning_effort=max", "-c", "model=new", "--model", "new-native", "--profile=member", "--sandbox", "workspace-write", "--ask-for-approval=never"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestComposeBinaryArgsCodexConfigMixedAliasesByKey(t *testing.T) {
	got := composeBinaryArgs("codex",
		[]string{"-c", "model_reasoning_effort=low", "--config", "model=old", "-c=approval_policy=untrusted"},
		[]string{"--config=model_reasoning_effort=high", "--config", "approval_policy=on-request", "-cmodel=middle"},
		[]string{"-c=model_reasoning_effort=max", "--config=model=new", "-capproval_policy=never"},
	)
	want := []string{"-c=model_reasoning_effort=max", "--config=model=new", "-capproval_policy=never"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestComposeBinaryArgsPreservesLiteralDashDashTail(t *testing.T) {
	in := []string{"--effort", "high", "--", "--effort", "low", "same", "same"}
	if got := composeBinaryArgs("claude", in); !reflect.DeepEqual(got, in) {
		t.Fatalf("got %#v want %#v", got, in)
	}
}

func TestComposeBinaryArgsLowerLayerDashDashMakesLaterLayersPositional(t *testing.T) {
	got := composeBinaryArgs("claude", []string{"--effort", "high", "--", "prompt"}, []string{"--effort", "low"})
	want := []string{"--effort", "high", "--", "prompt", "--effort", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestComposeBinaryArgsAliasesEqualsAndDanglingFlag(t *testing.T) {
	got := composeBinaryArgs("codex", []string{"-m", "old", "--model=new", "-p", "old-profile", "--profile=new-profile"})
	want := []string{"--model=new", "--profile=new-profile"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	// A dangling recognized flag remains one token. The generated bootstrap is
	// appended only after composition and can therefore never become its value.
	if got := composeBinaryArgs("claude", []string{"--effort"}); !reflect.DeepEqual(got, []string{"--effort"}) {
		t.Fatalf("dangling=%#v", got)
	}
	got = composeBinaryArgs("claude", []string{"--effort", "--plugin-dir", "x"}, []string{"--effort", "high"})
	want = []string{"--plugin-dir", "x", "--effort", "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dangling before unknown got %#v want %#v", got, want)
	}
}

func TestMergeBinaryArgsNormalizesTeamAndLaunchLayers(t *testing.T) {
	got := mergeBinaryArgs(map[string][]string{"claude": {"--effort", "low"}}, map[string][]string{"claude": {"--effort", "high"}})
	want := []string{"--effort", "high"}
	if !reflect.DeepEqual(got["claude"], want) {
		t.Fatalf("got %#v want %#v", got["claude"], want)
	}
}

func TestValidateNativePromptBoundaryRejectsDanglingKnownValues(t *testing.T) {
	for _, tc := range []struct {
		binary string
		args   []string
	}{
		{"claude", []string{"--settings"}}, {"claude", []string{"--settings", "--chrome"}},
		{"codex", []string{"--profile"}}, {"codex", []string{"-C", "--search"}}, {"codex", []string{"-c"}},
	} {
		if err := validateNativePromptBoundary(tc.binary, tc.args); err == nil {
			t.Errorf("%s %#v should fail", tc.binary, tc.args)
		}
	}
}

func TestValidateNativePromptBoundaryCardinalityAliasesAndDelimiter(t *testing.T) {
	valid := []struct {
		binary string
		args   []string
	}{
		{"claude", []string{"--settings=config.json", "--allowed-tools", "Read", "Edit"}},
		{"claude", []string{"-d", "--chrome"}}, // optional value omitted
		{"codex", []string{"-C", "/repo", "--image=a.png", "--enable", "goals"}},
		{"codex", []string{"-c=features.shell_tool=true", "-cmodel_reasoning_effort=high"}},
		{"codex", []string{"--profile", "p", "--"}},
	}
	for _, tc := range []struct {
		binary string
		args   []string
	}{
		{"codex", []string{"--profile", "p", "--", "existing-prompt"}},
		{"codex", []string{"--profile", "p", "existing-prompt"}},
		{"codex", []string{"--mystery", "value"}},
	} {
		got, err := assessNativePromptBoundary(tc.binary, tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if got.Safe || got.Reason == "" {
			t.Errorf("unsafe boundary accepted: %s %#v => %#v", tc.binary, tc.args, got)
		}
	}
	for _, tc := range valid {
		if err := validateNativePromptBoundary(tc.binary, tc.args); err != nil {
			t.Errorf("%s %#v: %v", tc.binary, tc.args, err)
		}
	}
	for _, tc := range []struct {
		binary string
		args   []string
	}{{"claude", []string{"--allowedTools"}}, {"codex", []string{"-i"}}} {
		if err := validateNativePromptBoundary(tc.binary, tc.args); err == nil {
			t.Errorf("variadic minimum not enforced: %s %#v", tc.binary, tc.args)
		}
	}
}
