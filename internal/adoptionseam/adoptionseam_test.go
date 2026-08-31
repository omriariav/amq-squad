package adoptionseam

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v2/internal/launchintent"
)

func baseIntentInput(t *testing.T, projectRoot string) launchintent.Input {
	t.Helper()
	return launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: "user"},
		Seats: []launchintent.SeatFacts{
			{
				Handle:      "fullstack",
				Executable:  "/usr/bin/claude",
				Args:        []string{"--permission-mode", "auto"},
				Cwd:         launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: projectRoot},
				RequireWake: true,
			},
		},
		Target: launchintent.TargetFacts{
			ProjectRoot: projectRoot,
			BaseRoot:    filepath.Join(projectRoot, ".agent-mail", "squad-v2-30-0", "v2-30-0"),
			SessionRoot: projectRoot,
			Session:     "v2-30-0",
		},
	}
}

// TestAdoptionSeamRefusesEmptyBaseRoot proves Prepare fails closed with
// ErrEmptyBaseRoot, and does so before touching internal/launchintent or
// launchapi at all: no AMQ call, no compile, no network, no filesystem I/O.
func TestAdoptionSeamRefusesEmptyBaseRoot(t *testing.T) {
	projectRoot := t.TempDir()
	intent := baseIntentInput(t, projectRoot)
	intent.Target.BaseRoot = ""

	_, err := Prepare(context.Background(), PrepareInput{Intent: intent, Launcher: "tmux"})
	if err == nil {
		t.Fatal("expected Prepare to refuse an empty base root")
	}
	if !errors.Is(err, ErrEmptyBaseRoot) {
		t.Fatalf("err = %v, want ErrEmptyBaseRoot", err)
	}
}

// TestAdoptionSeamNeverInvokesLaunchCLI is a source-level guard: the seam's
// own .go files must never import os/exec or reference the amq CLI by
// exec-style string, so a future change cannot silently reintroduce a shell
// dependency on the CLI whose --plan path cannot express an explicit
// base_root at all.
func TestAdoptionSeamNeverInvokesLaunchCLI(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || hasTestSuffix(name) {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(data)
		if containsImport(content, `"os/exec"`) {
			t.Fatalf("%s imports os/exec; the adoption seam must call launchapi exclusively", name)
		}
		for _, needle := range []string{`"amq"`, "exec.Command", "exec.CommandContext"} {
			if contains(content, needle) {
				t.Fatalf("%s references %q; the adoption seam must never shell out to the amq CLI", name, needle)
			}
		}
	}
}

func hasTestSuffix(name string) bool {
	return len(name) > len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go"
}

func containsImport(content, importPath string) bool {
	return contains(content, importPath)
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestAdoptionSeamStripsInheritedAMQIdentity locks the five variables a live
// parent AMQ seat injects into its own environment (gh#735): none of them
// may reach a child this seam launches.
func TestAdoptionSeamStripsInheritedAMQIdentity(t *testing.T) {
	cases := []string{"AM_ROOT", "AM_BASE_ROOT", "AM_ROOT_ID", "AM_BASE_ROOT_ID", "AM_SESSION"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			env := []string{key + "=inherited-value", "PATH=/usr/bin"}
			got := SanitizeEnv(env)
			for _, entry := range got {
				if entry == key || len(entry) > len(key) && entry[:len(key)+1] == key+"=" {
					t.Fatalf("SanitizeEnv did not strip %s: %v", key, got)
				}
			}
		})
	}
}

// TestAdoptionSeamPreservesNonAMQEnv proves the strip is targeted: unrelated
// environment entries, including ones with an AM_/AMQ_ prefix that are not
// one of the five identity variables, pass through untouched.
func TestAdoptionSeamPreservesNonAMQEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/senior-dev",
		"AM_ME=senior-dev",          // not one of the five, must survive
		"AMQ_NO_UPDATE_CHECK=1",     // not one of the five, must survive
		"AMQSESSION_UNRELATED=keep", // prefix collision, must survive
	}
	got := SanitizeEnv(env)
	want := append([]string(nil), env...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SanitizeEnv(%v) = %v, want unchanged %v", env, got, want)
	}
}

// TestApplyForwardsSubjectSchemaFromPrepareResult is gh#757's regression
// test for a real bug this seam's Apply had never surfaced before: it built
// ApplyRequestV1 without setting SubjectSchema, so launchapi.Apply's own
// zero-value default (SubjectSchemaV1, launchapi/apply.go) disagreed with
// launchapi.Prepare's own hardcoded default (SubjectSchemaV2,
// launchapi/prepare.go) -- the two public entry points do not share a
// default. Since every real caller here always sets
// PrepareRequestV1.CallerContext (team_launch_launchapi.go's callPrepare),
// an Apply that left SubjectSchema unset re-validated its embedded Prepare
// request at V1 and refused closed with "caller_context requires subject
// schema 2" on every real call that had any required action to decide --
// confirmed live via a real amq v0.75.0 binding
// (TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch,
// internal/cli/simple_wizard_test.go): no existing test had ever exercised
// a real (non-stubbed) Apply call with a non-empty Decisions list before,
// because every prior test replaces deps.Launch (and therefore this whole
// call) with a stub.
//
// This proves the fix directly against a real, fully-authorized project
// layout: Prepare surfaces a trust_confirmation required action (a fresh
// subject digest never seen before), and Apply -- given CallerContext and a
// decision for that action -- must not refuse with the schema-mismatch
// error.
func TestApplyForwardsSubjectSchemaFromPrepareResult(t *testing.T) {
	binary := os.Getenv("AMQ_SQUAD_REAL_AMQ")
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run this real-binding regression test")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skipf("AMQ_SQUAD_REAL_AMQ %q is unavailable or not executable", binary)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"root": ".agent-mail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	intent := launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: "user"},
		Seats: []launchintent.SeatFacts{{
			Handle: "worker", Executable: "claude",
			Cwd:          launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: project},
			ResumePolicy: launchapi.ResumePolicyFresh, RequireWake: true,
		}},
		Target: launchintent.TargetFacts{
			ProjectRoot: project,
			BaseRoot:    filepath.Join(project, ".agent-mail"),
			SessionRoot: filepath.Join(project, ".agent-mail", "s"),
			Session:     "s",
		},
	}

	prepared, err := Prepare(context.Background(), PrepareInput{
		Intent: intent, Launcher: "tmux",
		Caller: map[string]string{"profile": "default", "workstream": "s"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Logf("outcome=%s reason=%s required_actions=%+v", prepared.Result.Outcome, prepared.Result.Reason, prepared.Result.RequiredActions)
	if len(prepared.Result.RequiredActions) != 1 || prepared.Result.RequiredActions[0].Kind != launchapi.RequiredActionTrustConfirmation {
		t.Fatalf("required_actions = %+v, want exactly one trust_confirmation (a fresh, never-before-seen subject)", prepared.Result.RequiredActions)
	}
	if prepared.Result.SubjectSchema == 0 {
		t.Fatalf("PrepareResultV1.SubjectSchema = 0, want a real schema version for this test to exercise the forwarding fix")
	}

	// Apply's own tmux composition needs a real pane/session context this
	// standalone package test does not set up (verified separately in
	// TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch,
	// internal/cli/simple_wizard_test.go, where it succeeds end to end with
	// outcome "applied") -- so this test only isolates the ONE thing it
	// exists to prove: Apply must not refuse on the caller_context/
	// subject_schema mismatch the fix addresses, regardless of what a
	// downstream tmux-composition step separately requires.
	result, err := Apply(context.Background(), prepared, []launchapi.DecisionV1{
		{ActionID: prepared.Result.RequiredActions[0].ActionID, Choice: launchapi.DecisionTrustExactSubject},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Logf("apply outcome=%s reason_code=%s failure_detail=%s follow_ups=%+v", result.Outcome, result.ReasonCode, result.FailureDetail, result.FollowUps)
	if strings.Contains(strings.ToLower(result.ReasonCode), "caller_context") || strings.Contains(strings.ToLower(result.FailureDetail), "caller_context requires subject schema") {
		t.Fatalf("Apply refused on the caller_context/subject_schema mismatch the fix addresses: outcome=%q reason_code=%q detail=%q", result.Outcome, result.ReasonCode, result.FailureDetail)
	}
}
