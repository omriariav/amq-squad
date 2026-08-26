package adoptionseam

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
