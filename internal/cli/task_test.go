package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func withFixedTaskNow(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prev := taskNow
	taskNow = func() time.Time { return now }
	t.Cleanup(func() { taskNow = prev })
	return now
}

func taskCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := captureOutput(t, func() error { return runTask(args) })
	return out, err
}

func writeTaskCLISeatPool(t *testing.T, root, handle string, pool task.Pool) {
	t.Helper()
	start, ok := procinfo.StartToken(os.Getpid())
	if !ok {
		t.Fatal("current process start stamp is unavailable")
	}
	writeTaskCLISeatRecord(t, root, handle, pool, &launch.Exec{Outcome: "exec", PID: os.Getpid(), ProcessStart: start})
}

func writeTaskCLISeatRecord(t *testing.T, root, handle string, pool task.Pool, exec *launch.Exec) {
	t.Helper()
	agentDir := filepath.Join(root, "agents", handle)
	if err := launch.WithRecordLock(agentDir, func() error {
		return launch.WriteUnderRecordLock(agentDir, launch.Record{Handle: handle, Root: root, Runtime: pool.Runtime, Model: pool.Model, Effort: pool.Effort, StartedAt: time.Now().UTC(), Exec: exec})
	}); err != nil {
		t.Fatal(err)
	}
}

func writeTaskCLIScheduler(t *testing.T, root, processStart string) {
	t.Helper()
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: os.Getpid(), ProcessStart: processStart, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskCLIFlowAtTheSessionRoot(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "Fix the parser", "--class", "implement", "--files", "internal/parse/", "--pool", "claude:sonnet:medium", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "add", "--title", "Review it", "--class", "review", "--depends-on", "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "extensions", "amq-squad", "tasks", "t1.json")); err != nil {
		t.Fatalf("the store is not under the session root: %v", err)
	}
	out, err := taskCLI(t, "claim", "--me", "impl-1", "--pool", "claude:sonnet:medium", "--json")
	if err != nil {
		t.Fatal(err)
	}
	claimed := decodeJSONEnvelope[taskEnvelope](t, out).Data.Task
	if claimed.ID != "t1" || claimed.Lease == nil || claimed.Lease.Holder != "impl-1" || claimed.Lease.ClaimedAt == nil {
		t.Fatalf("claim = %+v", claimed)
	}
	if _, err := taskCLI(t, "claim", "--me", "impl-2"); err == nil {
		t.Fatal("impl-2 pulled t2 before its dependency completed")
	}
	if _, err := taskCLI(t, "done", "t1", "--me", "impl-1", "--evidence", "go test ok"); err != nil {
		t.Fatal(err)
	}
	shown, err := taskCLI(t, "show", "t1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeJSONEnvelope[taskEnvelope](t, shown).Data.Task; got.State != task.StateInProgress || got.Report == nil {
		t.Fatalf("a task with declared files must wait for the commit step: %+v", got)
	}
	list, err := taskCLI(t, "list")
	if err != nil || !strings.Contains(list, "t1") || !strings.Contains(list, "claude:sonnet:medium") {
		t.Fatalf("list (%v):\n%s", err, list)
	}
	raw, err := taskCLI(t, "list", "--json", "--state", "pending")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Kind string           `json:"kind"`
		Data taskListEnvelope `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Kind != "tasks" || len(env.Data.Tasks) != 1 || env.Data.Tasks[0].ID != "t2" {
		t.Fatalf("list --json --state pending = %s (%v)", raw, err)
	}
}

func TestTaskCLIClaimThroughLiveSchedulerRequiresAndAcceptsOffer(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	writeTaskCLISeatPool(t, root, "seat-a", sol)
	start, ok := procinfo.StartToken(os.Getpid())
	if !ok {
		t.Fatal("current process start stamp is unavailable")
	}
	writeTaskCLIScheduler(t, root, start)
	if _, err := taskCLI(t, "add", "--title", "offered", "--class", "script", "--pool", sol.Key()); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat-a"); err == nil || !strings.Contains(err.Error(), "t1 is handed out by the scheduler") || !strings.Contains(err.Error(), "refused (claim_offer_required)") {
		t.Fatalf("live scheduler accepted an unoffered claim: %v", err)
	}
	s, _ := task.Open(root)
	if _, err := s.Grant("t1", "seat-a", &sol, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	out, err := taskCLI(t, "claim", "t1", "--me", "seat-a", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSONEnvelope[taskEnvelope](t, out).Data.Task
	if got.Lease == nil || got.Lease.ClaimedAt == nil || got.Lease.Holder != "seat-a" {
		t.Fatalf("offered claim = %+v", got)
	}
}

func TestTaskCLIClaimTreatsAStaleSchedulerAsTheCompatibilityPath(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	writeTaskCLISeatPool(t, root, "seat-a", sol)
	start, ok := procinfo.StartToken(os.Getpid())
	if !ok {
		t.Fatal("current process start stamp is unavailable")
	}
	writeTaskCLIScheduler(t, root, start+"-reused")
	if _, err := taskCLI(t, "add", "--title", "stale scheduler", "--class", "script", "--pool", sol.Key()); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat-a"); err != nil {
		t.Fatalf("stale scheduler record did not take the compatibility path: %v", err)
	}
}

func TestTaskCLIClaimWithoutSchedulerFailsClosedOnlyForAnUnknownPinnedPool(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "pinned", "--class", "script", "--pool", "codex:gpt-5.6-sol:high"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat-a"); err == nil || !strings.Contains(err.Error(), "t1 is pinned to codex:gpt-5.6-sol:high; claim it with --pool, or let the scheduler hand it out") || !strings.Contains(err.Error(), "refused (claim_pool_unknown)") {
		t.Fatalf("unknown pool claim of pinned task: %v", err)
	}
	if _, err := taskCLI(t, "add", "--title", "unpinned", "--class", "script"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t2", "--me", "seat-a"); err != nil {
		t.Fatalf("unknown pool could not claim unpinned task: %v", err)
	}
}

func TestTaskCLIPullRefusesALiveSchedulerAndOtherwiseFiltersByServingPool(t *testing.T) {
	t.Run("live scheduler", func(t *testing.T) {
		root := t.TempDir()
		chdir(t, root)
		withFixedTaskNow(t)
		t.Setenv("AM_ROOT", root)
		t.Setenv("AM_ME", "")
		start, ok := procinfo.StartToken(os.Getpid())
		if !ok {
			t.Fatal("current process start stamp is unavailable")
		}
		writeTaskCLIScheduler(t, root, start)
		if _, err := taskCLI(t, "add", "--title", "pending", "--class", "script"); err != nil {
			t.Fatal(err)
		}
		if _, err := taskCLI(t, "claim", "--me", "seat-a"); err == nil || !strings.Contains(err.Error(), "the scheduler hands out work; wait for its offer") || !strings.Contains(err.Error(), "refused (claim_offer_required)") {
			t.Fatalf("pull under live scheduler: %v", err)
		}
	})

	t.Run("no scheduler", func(t *testing.T) {
		root := t.TempDir()
		chdir(t, root)
		withFixedTaskNow(t)
		t.Setenv("AM_ROOT", root)
		t.Setenv("AM_ME", "")
		sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
		writeTaskCLISeatPool(t, root, "seat-a", sol)
		if _, err := taskCLI(t, "add", "--title", "other", "--class", "script", "--priority", "2", "--pool", "codex:gpt-5.6-terra:medium"); err != nil {
			t.Fatal(err)
		}
		if _, err := taskCLI(t, "add", "--title", "served", "--class", "script", "--priority", "1", "--pool", sol.Key()); err != nil {
			t.Fatal(err)
		}
		out, err := taskCLI(t, "claim", "--me", "seat-a", "--json")
		if err != nil {
			t.Fatal(err)
		}
		got := decodeJSONEnvelope[taskEnvelope](t, out).Data.Task
		if got.ID != "t2" || got.Lease == nil || got.Lease.ClaimedAt == nil {
			t.Fatalf("pool-filtered pull = %+v", got)
		}
	})
}

func TestTaskCLIClaimIgnoresNonLiveSeatRecordsAndUsesExplicitPool(t *testing.T) {
	sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := task.Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	tests := []struct {
		name string
		seed func(*testing.T, string)
	}{
		{"no record", func(*testing.T, string) {}},
		{"gone pid", func(t *testing.T, root string) {
			writeTaskCLISeatRecord(t, root, "seat-a", terra, &launch.Exec{Outcome: "exec", PID: 999999, ProcessStart: "gone"})
		}},
		{"pid with no stamp", func(t *testing.T, root string) {
			writeTaskCLISeatRecord(t, root, "seat-a", terra, &launch.Exec{Outcome: "exec", PID: os.Getpid()})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			chdir(t, root)
			withFixedTaskNow(t)
			t.Setenv("AM_ROOT", root)
			t.Setenv("AM_ME", "")
			if _, err := taskCLI(t, "add", "--title", "pinned", "--class", "script", "--pool", sol.Key()); err != nil {
				t.Fatal(err)
			}
			tt.seed(t, root)
			if _, err := taskCLI(t, "claim", "t1", "--me", "seat-a", "--pool", sol.Key()); err != nil {
				t.Fatalf("explicit pool was overridden by a non-live record: %v", err)
			}
		})
	}
}

func TestTaskCLIClaimRefusesExplicitPoolAgainstALiveSeatRecord(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := task.Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	writeTaskCLISeatPool(t, root, "seat-a", sol)
	if _, err := taskCLI(t, "add", "--title", "pinned", "--class", "script", "--pool", sol.Key()); err != nil {
		t.Fatal(err)
	}
	_, err := taskCLI(t, "claim", "t1", "--me", "seat-a", "--pool", terra.Key())
	if err == nil || !strings.Contains(err.Error(), "seat-a is live on "+sol.Key()+"; --pool names "+terra.Key()) || !strings.Contains(err.Error(), "refused (claim_pool_mismatch)") {
		t.Fatalf("live record and explicit pool mismatch: %v", err)
	}
}

func TestTaskCLIClaimRefusalWordsAreAboveTheRule(t *testing.T) {
	type setup func(*testing.T, string, time.Time) error
	sol := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	terra := task.Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	tests := []struct {
		code  string
		plain string
		setup setup
	}{
		{task.RefusalClaimOfferRequired, "is handed out by the scheduler", func(t *testing.T, root string, _ time.Time) error {
			writeTaskCLISeatPool(t, root, "seat-a", sol)
			start, _ := procinfo.StartToken(os.Getpid())
			writeTaskCLIScheduler(t, root, start)
			_, err := taskCLI(t, "claim", "t1", "--me", "seat-a")
			return err
		}},
		{task.RefusalClaimOfferExpired, "offer to seat-a has expired", func(t *testing.T, root string, now time.Time) error {
			writeTaskCLISeatPool(t, root, "seat-a", sol)
			start, _ := procinfo.StartToken(os.Getpid())
			writeTaskCLIScheduler(t, root, start)
			s, _ := task.Open(root)
			if _, err := s.Grant("t1", "seat-a", &sol, time.Minute, now.Add(-2*time.Minute)); err != nil {
				t.Fatal(err)
			}
			_, err := taskCLI(t, "claim", "t1", "--me", "seat-a")
			return err
		}},
		{task.RefusalClaimPoolMismatch, "is handed out by the scheduler", func(t *testing.T, root string, now time.Time) error {
			writeTaskCLISeatPool(t, root, "seat-a", terra)
			start, _ := procinfo.StartToken(os.Getpid())
			writeTaskCLIScheduler(t, root, start)
			s, _ := task.Open(root)
			if _, err := s.Grant("t1", "seat-a", &sol, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			_, err := taskCLI(t, "claim", "t1", "--me", "seat-a")
			return err
		}},
		{task.RefusalClaimPoolUnknown, "is pinned to " + sol.Key(), func(t *testing.T, _ string, _ time.Time) error {
			_, err := taskCLI(t, "claim", "t1", "--me", "seat-a")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			root := t.TempDir()
			chdir(t, root)
			now := withFixedTaskNow(t)
			t.Setenv("AM_ROOT", root)
			t.Setenv("AM_ME", "")
			if _, err := taskCLI(t, "add", "--title", "pinned", "--class", "script", "--pool", sol.Key()); err != nil {
				t.Fatal(err)
			}
			err := tt.setup(t, root, now)
			if err == nil {
				t.Fatal("claim succeeded")
			}
			plain, record, ok := strings.Cut(err.Error(), "\n\n----------\n")
			if !ok || !strings.Contains(plain, tt.plain) || strings.Contains(plain, tt.code) || !strings.Contains(record, "refused ("+tt.code+")") {
				t.Fatalf("refusal words = %q", err)
			}
		})
	}
}

func TestTaskCLIRefusals(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "x", "--class", "script"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"add", "--title", "no class"}, "class"},
		{[]string{"add", "--title", "x", "--class", "deploy"}, "closed list"},
		{[]string{"claim", "t1", "--me", "user"}, "non-runnable mailbox participant"},
		{[]string{"done", "t1"}, "requires --me"},
		{[]string{"done", "t1", "--me", "h", "--reason", "x"}, "does not take --reason"},
		{[]string{"fail", "t1", "--me", "h"}, "requires --reason"},
		{[]string{"done", "--me", "h"}, "requires a task id"},
		{[]string{"set", "t1", "--me", "lead"}, "needs --priority or --pool"},
		{[]string{"claim", "t1", "--me", "h", "--pool", "nocolons"}, "runtime:model:effort"},
		{[]string{"bogus"}, "bogus"},
	}
	for _, c := range cases {
		if _, err := taskCLI(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("task %v: err %v, want %q", c.args, err, c.want)
		}
	}
	t.Setenv("AM_ROOT", "")
	if _, err := taskCLI(t, "list"); err == nil || !strings.Contains(err.Error(), "no store root") {
		t.Errorf("list without a root: %v", err)
	}
}

func TestTaskCLIPersistsAndExtendsTheAttemptBudget(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "bounded", "--class", "implement", "--budget", "45m", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "dev"); err != nil {
		t.Fatal(err)
	}
	shown, err := taskCLI(t, "show", "t1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSONEnvelope[taskEnvelope](t, shown).Data.Task
	if got.Budget != 45*time.Minute || got.Lease == nil || !got.Lease.BudgetEndsAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("bounded task = %+v", got)
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reply := extendBudgetDecision(storeSelection{store: store, root: root}, "lead", schedproc.WidenRequest{Kind: schedproc.WidenRequestKind, Task: "t1", Reason: taskExtendDecisionPrefix + "30m"})
	if !reply.OK {
		t.Fatalf("extend decision: %+v", reply)
	}
	shown, err = taskCLI(t, "show", "t1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got = decodeJSONEnvelope[taskEnvelope](t, shown).Data.Task
	if got.Budget != 75*time.Minute || got.Lease == nil || !got.Lease.BudgetEndsAt.Equal(now.Add(75*time.Minute)) || len(got.BudgetExtensions) != 1 {
		t.Fatalf("extended task = %+v", got)
	}
}

func TestManualRenewNormalizesAndBindsTheLegacyAttempt(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	if _, err := taskCLI(t, "add", "--title", "legacy", "--class", "script"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "dev"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(task.DirFor(root), "t1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy task.Task
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Budget = 0
	legacy.Lease.Attempt = 0
	legacy.Lease.BudgetEndsAt = time.Time{}
	raw, _ = json.Marshal(legacy)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "renew", "--me", "dev"); err != nil {
		t.Fatal(err)
	}
	got, _ := task.Open(root)
	current, _ := got.Get("t1")
	if current.Lease.Attempt != current.Attempt || current.Lease.BudgetEndsAt.IsZero() || current.Lease.ExpiresAt.After(current.Lease.BudgetEndsAt) {
		t.Fatalf("manual renew bypassed normalization: %+v", current.Lease)
	}
	lines, err := schedproc.ReadDecisionsTail(root)
	if err != nil || len(lines) != 1 || lines[0].Reason != "lease of t1 normalised: attempt 1, budget ends "+now.Add(task.DefaultTaskBudget).Format(time.RFC3339) {
		t.Fatalf("normalization decisions = %+v, err %v", lines, err)
	}
}

func TestTaskCLIBlockCarriesTypedWidenPaths(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	if _, err := taskCLI(t, "add", "--title", "x", "--class", "implement"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "block", "t1", "--me", "seat", "--reason", task.ReasonWidenRequested,
		"--path", "src/[name].go", "--path", "docs/a, b.md", "--paths", "./src/new.go,docs/z.md"); err != nil {
		t.Fatal(err)
	}
	shown, err := taskCLI(t, "show", "t1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSONEnvelope[taskEnvelope](t, shown).Data.Task
	if strings.Join(got.RefusedPaths, "|") != "docs/a, b.md|docs/z.md|src/[name].go|src/new.go" {
		t.Fatalf("refused paths = %q", got.RefusedPaths)
	}

	if _, err := taskCLI(t, "add", "--title", "y", "--class", "implement"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "block", "t2", "--me", "lead", "--reason", task.ReasonWidenRequested); err == nil || !strings.Contains(err.Error(), "at least one path") {
		t.Fatalf("pathless widen request: %v", err)
	}
	if _, err := taskCLI(t, "block", "t2", "--me", "lead", "--reason", "decision", "--path", "x.go"); err == nil || !strings.Contains(err.Error(), "only valid") {
		t.Fatalf("paths with another reason: %v", err)
	}
}

func TestTypedWidenResolvesASymlinkIntoAProtectedPath(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	project, _ := seatRepo(t, root)
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), "{}\n")
	if err := os.Symlink(filepath.Join(".claude", "settings.json"), filepath.Join(project, "settings-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s, _ := task.Open(root)
	tk, err := s.Add(task.AddInput{Title: "protected alias", Class: "implement", ProjectPath: project}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blockAndArchive := func(tk task.Task, path string) {
		t.Helper()
		if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, time.Now()); err != nil {
			t.Fatal(err)
		}
		blocked, err := s.BlockWithPaths(tk.ID, "s1", task.ReasonWidenRequested, "", task.RouteLead, []string{path}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if archived, err := seatcommit.ArchiveAttempt(root, "s1", "", nil, tk.ID, blocked.BlockAttempt, time.Now()); err != nil || !archived {
			t.Fatalf("archive stopped attempt: archived=%v err=%v", archived, err)
		}
	}
	blockAndArchive(tk, "settings-link")
	literal, _ := task.LiteralGlob("settings-link")
	data, err := applyLeadWiden(storeSelection{store: s, root: root}, "lead", schedproc.WidenRequest{Task: tk.ID, Files: []string{literal}, Reason: "needed"})
	resolved, _ := task.LiteralGlob(".claude/settings.json")
	want := seatcommit.ProtectedTarget(root, tk.ID, []string{resolved})
	if err == nil || data.Decision.ReasonCode != seatcommit.ReasonGrowthGate || len(data.Decision.Crossings) != 1 || data.Decision.Crossings[0].Target != want || !strings.Contains(data.Decision.Detail, "settings-link") {
		t.Fatalf("symlinked protected widen = %+v, %v; want target %q", data, err, want)
	}

	mustWrite(t, filepath.Join(project, "src", "real.go"), "package src\n")
	if err := os.Symlink(filepath.Join("src", "real.go"), filepath.Join(project, "source-link")); err != nil {
		t.Fatal(err)
	}
	tk, _ = s.Add(task.AddInput{Title: "ordinary alias", Class: "implement", ProjectPath: project}, time.Now())
	blockAndArchive(tk, "source-link")
	literal, _ = task.LiteralGlob("source-link")
	data, err = applyLeadWiden(storeSelection{store: s, root: root}, "lead", schedproc.WidenRequest{Task: tk.ID, Files: []string{literal}, Reason: "needed"})
	if err != nil || data.Task == nil || strings.Join(data.Task.DeclaredFiles, ",") != "src/real.go" || len(data.Task.ScopeChanges) != 1 || !strings.Contains(data.Task.ScopeChanges[0].Reason, "source-link") {
		t.Fatalf("ordinary alias widen = %+v, %v", data, err)
	}

	if err := os.Symlink("CLAUDE.md", filepath.Join(project, "managed-link")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "managed\n")
	tk, _ = s.Add(task.AddInput{Title: "managed alias", Class: "implement", ProjectPath: project}, time.Now())
	blockAndArchive(tk, "managed-link")
	literal, _ = task.LiteralGlob("managed-link")
	data, err = applyLeadWiden(storeSelection{store: s, root: root}, "lead", schedproc.WidenRequest{Task: tk.ID, Files: []string{literal}, Reason: "needed"})
	want = seatcommit.ProtectedTarget(root, tk.ID, []string{"CLAUDE.md"})
	if err == nil || data.Decision.ReasonCode != seatcommit.ReasonGrowthGate || data.Decision.Crossings[0].Target != want {
		t.Fatalf("managed alias widen = %+v, %v; want target %q", data, err, want)
	}
}

func TestEveryWidenBranchRoutesLiteralPathsThroughResolution(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, task.Store, task.Task, time.Time){
		"nonblocked": func(*testing.T, task.Store, task.Task, time.Time) {},
		"protected report": func(t *testing.T, s task.Store, tk task.Task, now time.Time) {
			if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Done(tk.ID, "s1", "ok", "", now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RefuseCommit(tk.ID, seatcommit.ReasonProtected, "protected", task.RouteLead, now); err != nil {
				t.Fatal(err)
			}
		},
		"growth-gate report": func(t *testing.T, s task.Store, tk task.Task, now time.Time) {
			if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Done(tk.ID, "s1", "ok", "", now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RefuseCommit(tk.ID, seatcommit.ReasonGrowthGate, "growth", task.RouteLead, now); err != nil {
				t.Fatal(err)
			}
		},
		"typed widen_requested": func(t *testing.T, s task.Store, tk task.Task, now time.Time) {
			if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BlockWithPaths(tk.ID, "s1", task.ReasonWidenRequested, "", task.RouteLead, []string{"source-link"}, now); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			repo, _ := seatRepo(t, root)
			mustWrite(t, filepath.Join(repo, "real", "file.go"), "package real\n")
			if err := os.Symlink(filepath.Join("real", "file.go"), filepath.Join(repo, "source-link")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			s, err := task.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			tk, err := s.Add(task.AddInput{Title: name, Class: "implement", ProjectPath: repo, DeclaredFiles: []string{"seed.go"}}, now)
			if err != nil {
				t.Fatal(err)
			}
			prepare(t, s, tk, now)
			current, err := s.Get(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.BlockAttempt > 0 {
				if archived, err := seatcommit.ArchiveAttempt(root, "s1", "", nil, tk.ID, current.BlockAttempt, now); err != nil || !archived {
					t.Fatalf("archive stopped attempt: archived=%v err=%v", archived, err)
				}
			}
			data, err := applyLeadWiden(storeSelection{store: s, root: root}, "lead", schedproc.WidenRequest{
				Task: tk.ID, Files: []string{"./source-link"}, Reason: "needed",
			})
			if err != nil || data.Task == nil || strings.Join(data.Task.DeclaredFiles, ",") != "real/file.go,seed.go" || !strings.Contains(data.Task.ScopeChanges[0].Reason, "source-link") {
				t.Fatalf("production widen branch = %+v, %v", data, err)
			}
		})
	}

	t.Run("real glob stays unresolved", func(t *testing.T) {
		home, root, repo := t.TempDir(), t.TempDir(), t.TempDir()
		t.Setenv("HOME", home)
		ownerGrant(t, filepath.Join(home, "origin.git"))
		s, err := task.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		tk, err := s.Add(task.AddInput{Title: "glob", Class: "implement", ProjectPath: repo}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		data, err := applyLeadWiden(storeSelection{store: s, root: root}, "lead", schedproc.WidenRequest{
			Task: tk.ID, Files: []string{"[s]ource-link"}, Reason: "needed",
		})
		if err != nil || data.Task == nil || strings.Join(data.Task.DeclaredFiles, ",") != "[s]ource-link" || len(data.Task.ScopeChanges) != 1 || strings.Contains(data.Task.ScopeChanges[0].Reason, "exact-path resolution") {
			t.Fatalf("glob widen = %+v, %v", data, err)
		}
	})
}

// task set re-pins a task until its first attempt is granted; after the
// grant it prints the refusal code and ends with the way forward, which works.
func TestTaskCLISetRepinBoundary(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	if _, err := taskCLI(t, "add", "--title", "x", "--class", "script", "--pool", "claude:haiku:low", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "set", "t1", "--pool", "codex:gpt-5.6-terra:medium", "--me", "lead"); err != nil {
		t.Fatalf("re-pin before the first grant: %v", err)
	}
	if _, err := taskCLI(t, "claim", "t1", "--me", "seat", "--pool", "codex:gpt-5.6-terra:medium"); err != nil {
		t.Fatal(err)
	}
	_, err := taskCLI(t, "set", "t1", "--pool", "claude:sonnet:medium", "--me", "lead")
	if !errors.Is(err, task.ErrRePinAfterAttempt) || !strings.Contains(err.Error(), task.ReasonRePinAfterAttempt) ||
		!strings.HasSuffix(err.Error(), "use task reset --pool") {
		t.Fatalf("re-pin after the first grant: %v", err)
	}
	if _, err := taskCLI(t, "release", "t1", "--reason", "re-pin", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "block", "t1", "--reason", "re-pin", "--me", "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskCLI(t, "reset", "t1", "--pool", "claude:sonnet:medium", "--me", "lead"); err != nil {
		t.Fatalf("the hint's reset: %v", err)
	}
}

func TestTaskCLIReconcileListsExpiredLeases(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := withFixedTaskNow(t)
	t.Setenv("AM_ROOT", root)
	s, _ := task.Open(root)
	if _, err := s.Add(task.AddInput{Title: "a", Class: "script"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant("t1", "ghost", nil, time.Minute, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := taskCLI(t, "reconcile")
	if err != nil || !strings.Contains(out, "1 expired leases") || !strings.Contains(out, "t1 held by ghost") {
		t.Fatalf("reconcile (%v):\n%s", err, out)
	}
}
