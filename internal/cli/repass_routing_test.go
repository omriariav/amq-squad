package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 re-pass, P2-8: a merged routing.yaml that does not load stops
// `scheduler run` at its start with routing_invalid (routing.Open), never a
// plain wrapped error; and --lead-pool takes only runtime:model:effort.
func TestRepassSchedulerRunRefusesAnInvalidTableAsRoutingInvalid(t *testing.T) {
	_, root, seatsFile := schedulerFixture(t)
	proj := filepath.Dir(filepath.Dir(root))
	mustWrite(t, filepath.Join(proj, ".amq-squad", "routing.yaml"), "schema: 1\nunknown_key: 1\n")
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead"}, os.Stdout)
	})
	if !routing.IsInvalid(err) || !strings.Contains(err.Error(), routing.ReasonInvalid) {
		t.Fatalf("an invalid routing.yaml: %v", err)
	}
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead", "--lead-pool", "pi"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "--lead-pool") {
		t.Fatalf("a malformed --lead-pool: %v", err)
	}
}

// Narrow pass R2 (b): --lead-pool is refused when the process was not
// started by the lead's team mode (here the parent is the test binary).
func TestRepassLeadPoolOnlyFromTheLeadsTeamMode(t *testing.T) {
	_, _, seatsFile := schedulerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // were the flag admitted, the process would stop at once
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(ctx, []string{"--seats", seatsFile, "--lead", "lead", "--lead-pool", "pi:ollama/qwen3.6:27b:low"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "team mode") {
		t.Fatalf("--lead-pool from a process the lead did not start: %v", err)
	}
}

// Narrow pass, coverage: the lead's team mode forwards one --lead-pool per
// model at its thinking level to the scheduler child it starts.
func TestRepassLeadForwardsItsPools(t *testing.T) {
	pools := leadPools([]string{"ollama/qwen3.6:27b", "ollama/qwen3.6:35b-a3b"}, "high")
	cmd := leadSchedulerCommand("/bin/amq-squad", "/p/seats.json", "lead-1", pools, false)(context.Background())
	want := []string{"/bin/amq-squad", "scheduler", "run", "--seats", "/p/seats.json", "--lead", "lead-1",
		"--lead-pool", "pi:ollama/qwen3.6:27b:high", "--lead-pool", "pi:ollama/qwen3.6:35b-a3b:high"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("child argv %q, want %q", cmd.Args, want)
	}
}

// Narrow pass, coverage: scheduler run's router reads the scheduler's own
// seat view once the scheduler exists, nothing before.
func TestRepassSchedulerRunSeatViewWiring(t *testing.T) {
	var sched *scheduler.Scheduler
	view := schedulerSeatView(&sched)
	if got := view(); got != nil {
		t.Fatalf("before the scheduler: %+v", got)
	}
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sched, err = scheduler.New(scheduler.Options{Store: store, Launcher: &cliFakeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	pool := task.Pool{Runtime: "pi", Model: "ollama/qwen3.6:27b", Effort: "low"}
	sched.AdoptClosing("seat-old", pool, time.Now())
	if got := view(); len(got) != 1 || got[0].Handle != "seat-old" || got[0].Pool != pool || got[0].Closing == "" {
		t.Fatalf("seat view: %+v", got)
	}
}

// Narrow pass, coverage: verify --task reads the whole outcome log; a line
// over the 4 MiB bound makes the result incomplete, with the reason in the
// JSON and in the text.
func TestRepassVerifyTaskEvidenceIncomplete(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(task.AddInput{Title: "x", Class: "plan"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := mergetrain.AuditFor(root).Append(mergetrain.Entry{Kind: "merge", Repository: "o/r", Base: "main", PR: 7, Head: "h1", Merged: "m1"}); err != nil {
		t.Fatal(err)
	}
	long := `{"task":"t1","pad":"` + strings.Repeat("z", 4<<20) + `"}`
	mustWrite(t, filepath.Join(root, "extensions", "amq-squad", "routing", "outcomes.jsonl"), long+"\n"+`{"task":"t1","attempt":1}`+"\n")
	for _, json := range []bool{true, false} {
		args := []string{"merge", "--repository", "o/r", "--pr", "7", "--root", root, "--task", "t1"}
		if json {
			args = append(args, "--json")
		}
		out, _, _ := captureOutput(t, func() error { return runVerify(args) })
		switch {
		case json && (!strings.Contains(out, `"incomplete": true`) || !strings.Contains(out, "outcomes.jsonl line 1 is over")):
			t.Fatalf("JSON: %s", out)
		case !json && !strings.Contains(out, "incomplete evidence: outcomes.jsonl line 1 is over"):
			t.Fatalf("text: %s", out)
		}
	}
}

// Narrow pass, coverage: the stores the CLI opens (the task verbs' store
// and scheduler run's, both through openTaskStore) read the verifier gate
// from the owner grant: with `verifiers: {implement: ...}` an implement done
// report with no verdict blocks with verdict_missing, while a script task,
// which the grant does not verify, completes on its report. No verdict
// helper: the gate is the grant's.
func TestRepassCLIStoresReadTheVerifierGrant(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("schema: 1\nverifiers:\n  implement: go test ./...\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	f := newTaskFlags("done")
	if err := parseFlags(f.fs, []string{"--root", root}); err != nil {
		t.Fatal(err)
	}
	viaVerb, _, release, err := f.store(false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	viaScheduler, err := openTaskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for name, s := range map[string]task.Store{"task verbs": viaVerb, "scheduler run": viaScheduler} {
		for _, tc := range []struct{ class, state string }{{"implement", task.StateBlocked}, {"script", task.StateCompleted}} {
			tk, err := s.Add(task.AddInput{Title: name + " " + tc.class, Class: tc.class, ProjectPath: proj}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(tk.ID, "seat-"+tk.ID, nil, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			got, err := s.Done(tk.ID, "seat-"+tk.ID, "", "", now)
			if err != nil || got.State != tc.state || (tc.state == task.StateBlocked && got.BlockReason != task.BlockVerdictMissing) {
				t.Fatalf("%s, %s: %s %s %v", name, tc.class, got.State, got.BlockReason, err)
			}
		}
	}
}
