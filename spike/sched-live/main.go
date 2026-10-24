// Command sched-live runs brief 2b-2 milestone B's live loop tests through
// the real scheduler process (`amq-squad scheduler run`), replacing the
// stand-ins of 4-1's spike/pi-r19 harness except where AMQ 0.107.0 cannot do
// the step yet:
//
//	real        the scheduler process (scheduler run: the task store, leases,
//	            DefaultRouter with --default-pool, the usage-limit detector),
//	            the doorbell and the lead's refusal notice as AMQ messages from
//	            the scheduler mailbox, the harness commit step (seat commit),
//	            the seat's worktree, branch, staged record and commit base
//	            (the External launcher's core half), the lead (`amq-squad lead
//	            --team`, the embedded pi, the package) and the worker
//	            (`amq-squad pi run`) as real sessions with their hooks, tools
//	            and task verbs
//	stand-in    the pi launch (AMQ 0.107.0 has no pi adapter): this harness
//	            is the External runner, it reads the request file, reports
//	            seat_up with `scheduler post` and runs one `pi run` turn per
//	            doorbell on a persistent session id; the wake (AMQ's wake
//	            injects into a terminal): the harness reads the AMQ message from
//	            the seat's inbox and starts the turn with it; the merge train
//	            (3-2): after a completed task the harness fast-forwards main to
//	            the seat branch so the lead's read-only repo view sees it
//
// Modes:
//
//	r19   4-1 acceptance 8: a three-task brief to the team lead; pass when the
//	      lead made no write (it has no write tool), classed and declared
//	      three tasks, the tasks completed through the commit step, the lead
//	      ends with CONVERGED and an independent go test passes
//	r12   the R12 probe (spike/piprobe predicate v2) as a task through the
//	      scheduler: read calc.go, one one-line edit, go test, final line
//	      RESULT: PASS; the worktree check is the harness commit's numstat
//
// Usage: go run ./spike/sched-live -mode r19|r12 -dir EMPTY -bin AMQ_SQUAD -home SCRATCH [-model ollama/qwen3.6:27b] [-label run1]
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

const r19Brief = `Goal: the Go module in the current directory (package calc) needs three helpers, each in its own file with its own table test:
- Max(a, b int) int returns the larger of a and b; files max.go and max_test.go (TestMax).
- Reverse(s string) string returns s with its runes reversed; files reverse.go and reverse_test.go (TestReverse).
- IsEven(n int) bool reports whether n is even; files even.go and even_test.go (TestIsEven).
Your team has one worker seat, pi-1. Get this done through your team.`

const workerRole = `# Role: worker seat pi-1

You are pi-1, a worker seat of an amq-squad team. Your lead is pi-lead.

When the scheduler leases you a task (a message from the scheduler says so),
claim it with amq_task_claim, do exactly what the task's description asks in
the current directory and touch only the files it declares, run go test ./...
with your bash tool, then finish it with amq_task_done and the evidence (the
command and its last line), and tell your lead in one line with amq_send
(kind status). Never edit a file the task does not declare. Never commit:
the harness commits for you.
`

// probeRole is the worker role of the R12 probe: the probe's own last line
// (RESULT: PASS) must be the reply's last line, so no closing status message.
const probeRole = `# Role: probe seat pi-1

You are pi-1, a seat of an amq-squad team. When the scheduler leases you a
task (a message from the scheduler says so), claim it with amq_task_claim and
follow its description exactly, step by step, in the current directory,
touching only the files it declares. Never commit: the harness commits for
you. End your reply exactly as the task says.
`

const r12Task = `You are working in a Go module in the current directory.
1. Read the file calc.go.
2. Make exactly one one-line edit: change the return value of Greeting from "hello" to "hello, world". Change nothing else.
3. Run the command ` + "`go test ./...`" + ` with your bash tool.
4. Finish the task with amq_task_done, the last line of the test output as the evidence.
5. Report the last line of the test output, then end your reply with a line that is exactly RESULT: PASS if the tests passed, or RESULT: FAIL otherwise.`

var r19Fixture = map[string]string{
	"go.mod":       "module example.com/calc\n\ngo 1.22\n",
	"calc.go":      "package calc\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)\n\t}\n}\n",
}

var r12Fixture = map[string]string{
	"go.mod":       "module example.com/probecalc\n\ngo 1.22\n",
	"calc.go":      "package calc\n\n// Greeting returns the greeting text.\nfunc Greeting() string {\n\treturn \"hello\"\n}\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestGreeting(t *testing.T) {\n\tif got := Greeting(); got != \"hello, world\" {\n\t\tt.Fatalf(\"Greeting() = %q, want %q\", got, \"hello, world\")\n\t}\n}\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)\n\t}\n}\n",
}

type call struct {
	Turn int    `json:"turn"`
	Tool string `json:"tool"`
	Args string `json:"args"`
	Out  string `json:"out,omitempty"`
	Err  bool   `json:"is_error,omitempty"`
}

type harness struct {
	mode, dir, proj, root, bin, home, model, label, path string
	pool                                                 task.Pool
	sched                                                *exec.Cmd
	leadCalls, workerCalls                               []call
	leadTurns, workerTurns                               int
	seenMail                                             map[string]bool
	posted                                               map[string]bool
	worktree                                             string
	workerText                                           string
	stoodIn                                              []string
	probeRole                                            bool
}

func main() {
	h := &harness{seenMail: map[string]bool{}, posted: map[string]bool{}}
	flag.StringVar(&h.mode, "mode", "r19", "r19 or r12")
	flag.StringVar(&h.dir, "dir", "", "empty run directory")
	flag.StringVar(&h.bin, "bin", "", "amq-squad built with the embedded pi")
	flag.StringVar(&h.home, "home", "", "scratch HOME for the pi seats")
	flag.StringVar(&h.model, "model", "ollama/qwen3.6:27b", "model")
	flag.StringVar(&h.label, "label", "run", "run label")
	teamRole := flag.String("team-role", "internal/cli/pi_lead_team.md", "the team lead role text")
	rounds := flag.Int("rounds", 40, "maximum rounds")
	flag.BoolVar(&h.probeRole, "probe-role", false, "r12: the probe seat role (no closing status message)")
	flag.Parse()
	if err := h.run(*teamRole, *rounds); err != nil {
		fmt.Fprintln(os.Stderr, "sched-live:", err)
		h.stop()
		os.Exit(1)
	}
}

func (h *harness) run(teamRole string, rounds int) error {
	h.pool = task.Pool{Runtime: "pi", Model: strings.TrimPrefix(h.model, "ollama/"), Effort: "medium"}
	h.path = "/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	h.proj = filepath.Join(h.dir, "proj")
	if err := os.MkdirAll(h.proj, 0o755); err != nil {
		return err
	}
	fixture := r19Fixture
	if h.mode == "r12" {
		fixture = r12Fixture
	}
	for name, body := range fixture {
		if err := os.WriteFile(filepath.Join(h.proj, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	os.WriteFile(filepath.Join(h.proj, ".gitignore"), []byte(".agent-mail/\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=owner", "-c", "user.email=o@local", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		if _, err := h.sh(h.proj, nil, "git", args...); err != nil {
			return err
		}
	}
	if _, err := h.sh(h.proj, nil, "amq", "coop", "init", "--agents", "pi-1,pi-lead,scheduler"); err != nil {
		return err
	}
	h.root = filepath.Join(h.proj, ".agent-mail", "collab")
	team, err := os.ReadFile(teamRole)
	if err != nil {
		return err
	}
	role := workerRole
	if h.mode == "r12" && h.probeRole {
		role = probeRole
	}
	for seat, text := range map[string]string{"pi-1": role, "pi-lead": string(team)} {
		p := filepath.Join(h.root, "agents", seat, "extensions", "io.github.omriariav.amq-squad", "role.md")
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			return err
		}
	}
	roleFile := filepath.Join(h.dir, "worker-role.md")
	os.WriteFile(roleFile, []byte(role), 0o600)
	seats := map[string]any{"project_root": h.proj, "session": "collab", "seats": []map[string]any{
		{"handle": "pi-1", "runtime": "pi", "role": "implementer", "role_file": roleFile, "worktree": filepath.Join(h.dir, "wt-pi-1"), "branch": "seat/pi-1"},
	}}
	raw, _ := json.MarshalIndent(seats, "", "  ")
	seatsFile := filepath.Join(h.dir, "seats.json")
	os.WriteFile(seatsFile, raw, 0o600)
	// The real scheduler process.
	logf, _ := os.Create(filepath.Join(h.dir, "scheduler-run.log"))
	h.sched = exec.Command(h.bin, "scheduler", "run", "--seats", seatsFile, "--lead", "pi-lead", "--default-pool", h.pool.Key(), "--tick", "1s")
	h.sched.Dir, h.sched.Env, h.sched.Stdout, h.sched.Stderr = h.proj, h.env(""), logf, logf
	if err := h.sched.Start(); err != nil {
		return err
	}
	defer h.stop()
	h.stoodIn = []string{"pi launch: External request file read by this harness, seat_up by scheduler post, one pi run turn per doorbell",
		"wake: the harness reads the AMQ doorbell from the seat inbox and starts the turn with it",
		"merge train: the harness fast-forwards main to seat/pi-1 after each completed task"}

	start := time.Now()
	final := ""
	if h.mode == "r19" {
		if final, err = h.leadTurn(r19Brief); err != nil {
			return err
		}
	} else {
		if _, err := h.sh(h.proj, h.env("pi-lead"), h.bin, "task", "add", "--title", "R12 probe: Greeting", "--class", "implement", "--files", "calc.go", "--desc", r12Task); err != nil {
			return err
		}
	}
	converged := false
	idle := 0
	for round := 0; round < rounds && !converged; round++ {
		time.Sleep(2 * time.Second)
		h.runner()
		if msgs := h.inbox("pi-1"); len(msgs) > 0 {
			idle = 0
			if err := h.workerTurn(fmt.Sprintf("AMQ message from %s: %s: %s", msgs[0].From, msgs[0].Subject, msgs[0].Body)); err != nil {
				return err
			}
			h.sh(h.proj, nil, h.bin, "scheduler", "post", "--root", h.root, "--kind", "seat_activity", "--handle", "pi-1")
			continue
		}
		list, _ := task.Snapshot(task.DirFor(h.root))
		open := 0
		for _, t := range list {
			if t.State == task.StatePending || t.State == task.StateInProgress {
				open++
			}
		}
		h.mergeTrain()
		if h.mode == "r12" {
			if open == 0 && len(list) > 0 {
				converged = true
			}
			continue
		}
		if msgs := h.inbox("pi-lead"); len(msgs) > 0 || (open == 0 && idle > 1) {
			idle = 0
			prompt := "AMQ doorbell: new mail arrived. Act on it."
			if len(msgs) > 0 {
				var parts []string
				for _, m := range msgs {
					parts = append(parts, fmt.Sprintf("from %s: %s: %s", m.From, m.Subject, m.Body))
				}
				prompt = "AMQ doorbell: " + strings.Join(parts, " | ") + ". Act on it."
			}
			if final, err = h.leadTurn(prompt); err != nil {
				return err
			}
			converged = lastLine(final) == "CONVERGED"
			continue
		}
		idle++
	}
	return h.report(final, converged, time.Since(start))
}

func (h *harness) stop() {
	if h.sched != nil && h.sched.Process != nil {
		h.sched.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { h.sched.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			h.sched.Process.Kill()
		}
		h.sched = nil
	}
}

// runner is the stand-in External runner: a new request file becomes a
// seat_up for its request id.
func (h *harness) runner() {
	entries, _ := os.ReadDir(schedproc.RequestsDir(h.root))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), "close-") || h.posted[e.Name()] {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(schedproc.RequestsDir(h.root), e.Name()))
		var r schedproc.Request
		if err != nil || json.Unmarshal(raw, &r) != nil {
			continue
		}
		h.posted[e.Name()] = true
		h.worktree = r.Worktree
		h.sh(h.proj, nil, h.bin, "scheduler", "post", "--root", h.root, "--kind", "seat_up", "--handle", r.Handle, "--request-id", r.ID, "--pool", r.Pool.Key())
	}
}

// mergeTrain fast-forwards main to the seat branch (the stand-in for 3-2).
func (h *harness) mergeTrain() {
	if h.worktree == "" {
		return
	}
	h.sh(h.proj, nil, "git", "-c", "core.hooksPath=/dev/null", "merge", "-q", "--ff-only", "seat/pi-1")
}

type mail struct {
	ID, From, Subject, Body string
}

// inbox returns the seat's unseen messages (the stand-in wake reads them).
func (h *harness) inbox(seat string) []mail {
	out, err := h.sh(h.proj, h.envFor(seat), "amq", "list", "--me", seat, "--new", "--json")
	if err != nil {
		return nil
	}
	var list []struct {
		ID      string `json:"id"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if i := strings.Index(out, "["); i >= 0 {
		json.Unmarshal([]byte(out[i:]), &list)
	}
	var fresh []mail
	for _, m := range list {
		if h.seenMail[m.ID] {
			continue
		}
		h.seenMail[m.ID] = true
		body := m.Body
		if body == "" {
			if b, err := h.sh(h.proj, h.envFor(seat), "amq", "read", "--me", seat, "--id", m.ID, "--json"); err == nil {
				var r struct {
					Body string `json:"body"`
				}
				if i := strings.Index(b, "{"); i >= 0 {
					json.Unmarshal([]byte(b[i:]), &r)
				}
				body = r.Body
			}
		}
		fresh = append(fresh, mail{ID: m.ID, From: m.From, Subject: m.Subject, Body: body})
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })
	return fresh
}

func (h *harness) env(seat string) []string {
	e := []string{"HOME=" + h.home, "PATH=" + h.path, "TERM=xterm", "AMQ_NO_UPDATE_CHECK=1", "GOFLAGS=-count=1",
		"GOCACHE=" + filepath.Join(h.dir, "gocache"), "GOTOOLCHAIN=local"}
	if seat != "" {
		e = append(e, "AM_ME="+seat, "AM_ROOT="+h.root, "AM_BASE_ROOT="+filepath.Dir(h.root), "AM_SESSION=collab")
	}
	return e
}

func (h *harness) envFor(seat string) []string { return h.env(seat) }

func (h *harness) leadTurn(prompt string) (string, error) {
	h.leadTurns++
	args := []string{"lead", "--team", "--model", h.model, "--", "--mode", "json", "--session-id", "sched-lead-" + h.label, "-p", prompt}
	calls, text, err := h.pi("lead", h.leadTurns, h.proj, args, h.env("pi-lead"))
	h.leadCalls = append(h.leadCalls, calls...)
	return text, err
}

func (h *harness) workerTurn(prompt string) error {
	h.workerTurns++
	cwd := h.worktree
	if cwd == "" {
		cwd = h.proj
	}
	args := []string{"pi", "run", "--seat", "pi-1", "--model", h.model, "--", "--mode", "json", "--session-id", "sched-w-" + h.label, "-p", prompt}
	calls, text, err := h.pi("worker", h.workerTurns, cwd, args, h.env("pi-1"))
	h.workerCalls = append(h.workerCalls, calls...)
	h.workerText = text
	return err
}

func (h *harness) pi(who string, turn int, cwd string, args, env []string) ([]call, string, error) {
	out, err := os.Create(filepath.Join(h.dir, fmt.Sprintf("%s-%d.jsonl", who, turn)))
	if err != nil {
		return nil, "", err
	}
	defer out.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdout = cwd, env, out
	errf, _ := os.Create(filepath.Join(h.dir, fmt.Sprintf("%s-%d.err", who, turn)))
	defer errf.Close()
	cmd.Stderr = errf
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%s turn %d: %w", who, turn, err)
	}
	f, _ := os.Open(out.Name())
	defer f.Close()
	byID := map[string]*call{}
	var calls []*call
	text := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type       string          `json:"type"`
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Args       json.RawMessage `json:"args"`
			IsError    bool            `json:"isError"`
			Result     struct {
				Content []struct{ Type, Text string } `json:"content"`
			} `json:"result"`
			Message struct {
				Role    string                        `json:"role"`
				Content []struct{ Type, Text string } `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "tool_execution_start":
			c := &call{Turn: turn, Tool: ev.ToolName, Args: string(ev.Args)}
			byID[ev.ToolCallID] = c
			calls = append(calls, c)
		case "tool_execution_end":
			if c := byID[ev.ToolCallID]; c != nil {
				var b strings.Builder
				for _, x := range ev.Result.Content {
					b.WriteString(x.Text)
				}
				c.Out, c.Err = trim(b.String(), 400), ev.IsError
			}
		case "message_end":
			if ev.Message.Role == "assistant" {
				var b strings.Builder
				for _, c := range ev.Message.Content {
					if c.Type == "text" {
						b.WriteString(c.Text)
					}
				}
				if strings.TrimSpace(b.String()) != "" {
					text = b.String()
				}
			}
		}
	}
	var flat []call
	for _, c := range calls {
		flat = append(flat, *c)
	}
	return flat, text, nil
}

func (h *harness) sh(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env == nil {
		env = h.env("")
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return string(out), nil
}

var codingRE = regexp.MustCompile(`(^|[;&|]\s*)(touch|tee|cp|mv|rm|sed\s+-i|perl\s+-i|gofmt\s+-w|go\s+fmt|cat\s*>|echo[^|;&]*>|printf[^|;&]*>)|[^2]>\s*[\w./]|<<`)

func (h *harness) report(final string, converged bool, took time.Duration) error {
	h.stop()
	list, _ := task.Snapshot(task.DirFor(h.root))
	type taskOut struct {
		ID, Title, Class, State, BlockReason string
		Files                                []string
	}
	var tasks []taskOut
	classed, completed := 0, 0
	for _, t := range list {
		tasks = append(tasks, taskOut{t.ID, t.Title, t.Class, t.State, t.BlockReason, t.DeclaredFiles})
		if t.Class != "" && len(t.DeclaredFiles) > 0 {
			classed++
		}
		if t.State == task.StateCompleted {
			completed++
		}
	}
	decisions, _ := os.ReadFile(filepath.Join(schedproc.Dir(h.root), "decisions.jsonl"))
	os.WriteFile(filepath.Join(h.dir, "decisions.jsonl"), decisions, 0o600)
	harnessLog, _ := h.sh(h.worktreeOr(), nil, "git", "log", "--format=%H %an|%s|%(trailers:key=Amq-Squad-Task,valueonly,separator=)", "seat/pi-1")
	os.WriteFile(filepath.Join(h.dir, "git-log.txt"), []byte(harnessLog), 0o600)
	test, testErr := h.sh(h.worktreeOr(), nil, "go", "test", "-count=1", "-v", "./...")
	res := map[string]any{"mode": h.mode, "label": h.label, "model": h.model, "tasks": tasks, "classed_tasks": classed, "completed_tasks": completed,
		"lead_turns": h.leadTurns, "worker_turns": h.workerTurns, "seconds": int(took.Seconds()), "stood_in": h.stoodIn,
		"independent_go_test_ok": testErr == nil, "decision_kinds": decisionKinds(string(decisions)), "harness_commits": strings.Count(harnessLog, "amq-squad harness")}
	pass := false
	switch h.mode {
	case "r19":
		coded := []call{}
		for _, c := range h.leadCalls {
			var a struct{ Command string }
			json.Unmarshal([]byte(c.Args), &a)
			if c.Tool == "write" || c.Tool == "edit" || c.Tool == "bash" || codingRE.MatchString(a.Command) {
				coded = append(coded, c)
			}
		}
		first := ""
		if len(h.leadCalls) > 0 {
			first = h.leadCalls[0].Tool
		}
		passed := map[string]bool{}
		for _, n := range []string{"TestMax", "TestReverse", "TestIsEven"} {
			passed[n] = strings.Contains(test, "--- PASS: "+n)
		}
		pass = len(coded) == 0 && strings.HasPrefix(first, "amq_") && classed >= 3 && completed >= 3 && converged && testErr == nil && passed["TestMax"] && passed["TestReverse"] && passed["TestIsEven"]
		res["lead_coded"], res["lead_first_tool"], res["lead_calls"], res["converged"], res["final_line"], res["tests"] = coded, first, h.leadCalls, converged, lastLine(final), passed
	case "r12":
		readOK, testOK := false, false
		for _, c := range h.workerCalls {
			var a struct{ Path, Command string }
			json.Unmarshal([]byte(c.Args), &a)
			if !c.Err && ((c.Tool == "read" && strings.HasSuffix(a.Path, "calc.go")) || (c.Tool == "bash" && regexp.MustCompile(`\b(cat|head|sed|less)\b[^|;&]*calc\.go`).MatchString(a.Command))) {
				readOK = true
			}
			if c.Tool == "bash" && regexp.MustCompile(`\bgo\s+test\b`).MatchString(a.Command) && !c.Err && regexp.MustCompile(`(?m)^ok\s`).MatchString(c.Out) {
				testOK = true
			}
		}
		numstat, _ := h.sh(h.worktreeOr(), nil, "git", "diff", "--numstat", "main~0", "seat/pi-1")
		first, _ := h.sh(h.proj, nil, "git", "rev-list", "--max-parents=0", "main")
		numstat, _ = h.sh(h.worktreeOr(), nil, "git", "diff", "--numstat", strings.TrimSpace(first), "seat/pi-1")
		exact := strings.TrimSpace(numstat) == "1\t1\tcalc.go"
		finalExact := lastLine(h.workerText) == "RESULT: PASS"
		pass = readOK && testOK && exact && testErr == nil && finalExact && completed == 1
		res["checks"] = map[string]bool{"read_confirmed": readOK, "go_test_confirmed": testOK, "worktree_exact_harness_commit": exact, "independent_go_test": testErr == nil, "final_line_exact": finalExact, "completed_through_commit_step": completed == 1}
		res["false_success"] = finalExact && !(testErr == nil && exact)
		res["worker_calls"], res["numstat"] = h.workerCalls, strings.TrimSpace(numstat)
	}
	res["pass"] = pass
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	return os.WriteFile(filepath.Join(h.dir, "summary.json"), b, 0o644)
}

func (h *harness) worktreeOr() string {
	if h.worktree != "" {
		return h.worktree
	}
	return h.proj
}

func decisionKinds(log string) map[string]int {
	out := map[string]int{}
	for _, line := range strings.Split(log, "\n") {
		var d struct{ Kind string }
		if json.Unmarshal([]byte(line), &d) == nil && d.Kind != "" {
			out[d.Kind]++
		}
	}
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
