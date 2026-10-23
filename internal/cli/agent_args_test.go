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
