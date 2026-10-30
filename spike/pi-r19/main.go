// Command pi-r19 is the R19 loop test of brief 4-1 (acceptance 8): a pi
// team lead given a three-task brief does not start coding; it decomposes,
// labels classes and converges through the scheduler.
//
// The scheduler and the task store are the real ones: scheduler.New and
// Tick from internal/scheduler (2b-1) over task.Open on the squad's AMQ root.
// The lead and the worker are real embedded-pi sessions started through
// `amq-squad lead --team` and `amq-squad pi run`. Four scheduler hooks are
// stand-ins, because their briefs are not on v3 yet (lead ruling, 4-1):
//
//	Router     2b-4's routing table: every task goes to the one pi pool
//	Launcher   2b-2's launch: the worker seat is announced with SeatUp under the
//	           request id; its process is one `pi run` per turn
//	Doorbell   AMQ's wake: a ring becomes the worker's next pi turn
//	Committer  2b-2 milestone B's harness commit step: git commit of the
//	           changed files when every one is inside the declared set,
//	           else a refusal (outside_declared_files)
//
// Usage: go run ./spike/pi-r19 -dir <empty dir> -bin <amq-squad with pi> -home <HOME> -model ollama/qwen3.6:27b -label run1
// Prints one JSON summary; writes the pi events and the decision log under -dir.
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
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

const brief = `Goal: the Go module in the current directory (package calc) needs three helpers, each in its own file with its own table test:
- Max(a, b int) int returns the larger of a and b; files max.go and max_test.go (TestMax).
- Reverse(s string) string returns s with its runes reversed; files reverse.go and reverse_test.go (TestReverse).
- IsEven(n int) bool reports whether n is even; files even.go and even_test.go (TestIsEven).
Your team has one worker seat, pi-1. Get this done through your team.`

const workerRole = `# Role: worker seat pi-1

You are pi-1, a worker seat of an amq-squad team. Your lead is pi-lead.

When the scheduler leases you a task, claim it with amq_task_claim, do exactly
what it asks in the current directory and touch only the files it declares,
run go test ./... with your bash tool, then finish it with amq_task_done and
the evidence (the command and its last line), and tell your lead in one line
with amq_send (kind status). Never edit a file the task does not declare.
`

var fixture = map[string]string{
	"go.mod":       "module example.com/calc\n\ngo 1.22\n",
	"calc.go":      "package calc\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)\n\t}\n}\n",
}

type harness struct {
	dir, proj, root, bin, home, model, label, teamRole string
	path                                               string
	rings                                              []string
	requests                                           []scheduler.SeatRequest
	log                                                *os.File
	leadCalls                                          []call
	workerTurns, leadTurns                             int
	reported                                           map[string]bool
}

type call struct {
	Turn int    `json:"turn"`
	Tool string `json:"tool"`
	Args string `json:"args"`
}

func main() {
	h := &harness{reported: map[string]bool{}}
	flag.StringVar(&h.dir, "dir", "", "empty run directory")
	flag.StringVar(&h.bin, "bin", "", "amq-squad built with the embedded pi")
	flag.StringVar(&h.home, "home", "", "HOME for the seats")
	flag.StringVar(&h.model, "model", "ollama/qwen3.6:27b", "model for lead and worker")
	flag.StringVar(&h.label, "label", "run", "run label")
	flag.StringVar(&h.teamRole, "team-role", "internal/cli/pi_lead_team.md", "the team lead role text")
	maxRounds := flag.Int("rounds", 24, "maximum scheduler rounds")
	flag.Parse()
	if err := h.run(*maxRounds); err != nil {
		fmt.Fprintln(os.Stderr, "pi-r19:", err)
		os.Exit(1)
	}
}

func (h *harness) run(maxRounds int) error {
	h.proj = filepath.Join(h.dir, "proj")
	if err := os.MkdirAll(h.proj, 0o755); err != nil {
		return err
	}
	for name, body := range fixture {
		if err := os.WriteFile(filepath.Join(h.proj, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	h.path = "/opt/homebrew/bin:/usr/local/go/bin:/usr/bin:/bin"
	if _, err := h.sh(h.proj, "amq", "coop", "init", "--agents", "pi-1,pi-lead"); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=owner", "-c", "user.email=o@local", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		if _, err := h.sh(h.proj, "git", args...); err != nil {
			return err
		}
	}
	h.root = filepath.Join(h.proj, ".agent-mail", "collab")
	team, err := os.ReadFile(h.teamRole)
	if err != nil {
		return err
	}
	for seat, text := range map[string]string{"pi-1": workerRole, "pi-lead": string(team)} {
		p := filepath.Join(h.root, "agents", seat, "extensions", "io.github.omriariav.amq-squad", "role.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			return err
		}
	}
	if h.log, err = os.Create(filepath.Join(h.dir, "decisions.jsonl")); err != nil {
		return err
	}
	defer h.log.Close()

	store, err := task.Open(h.root)
	if err != nil {
		return err
	}
	pool := task.Pool{Runtime: "pi", Model: strings.TrimPrefix(h.model, "ollama/"), Effort: "medium"}
	sch, err := scheduler.New(scheduler.Options{
		Store: store, Launcher: launcher{h}, Doorbell: doorbell{h}, Router: router{pool},
		Committer: committer{h}, Config: scheduler.Config{MaxSeats: 1},
	})
	if err != nil {
		return err
	}

	start := time.Now()
	final, err := h.leadTurn(brief)
	if err != nil {
		return err
	}
	converged := false
	for round := 0; round < maxRounds && !converged; round++ {
		if err := h.tick(sch); err != nil {
			return err
		}
		for _, r := range h.requests {
			sch.Post(scheduler.Event{Kind: scheduler.SeatUp, At: time.Now(), Handle: "pi-1", Pool: r.Pool, RequestID: r.ID})
		}
		h.requests = nil
		if len(h.rings) > 0 {
			h.rings = h.rings[1:]
			if err := h.workerTurn("AMQ doorbell: the scheduler leased you a task. Act on it."); err != nil {
				return err
			}
			sch.Post(scheduler.Event{Kind: scheduler.SeatActivity, At: time.Now(), Handle: "pi-1"})
			continue
		}
		list, err := store.List()
		if err != nil {
			return err
		}
		open, blocked := 0, []task.Task{}
		for _, t := range list {
			switch {
			case t.State == task.StateBlocked && !h.reported[t.ID]:
				blocked = append(blocked, t)
			case t.State == task.StatePending || t.State == task.StateInProgress:
				open++
			}
		}
		if len(blocked) > 0 {
			// The commit step's refusal is routed to the lead (amendment 21).
			var why []string
			for _, t := range blocked {
				h.reported[t.ID] = true
				why = append(why, fmt.Sprintf("task %s %q was refused by the harness commit step: %s (%s)", t.ID, t.Title, t.BlockReason, t.BlockDetail))
			}
			if final, err = h.leadTurn("AMQ doorbell: " + strings.Join(why, "; ") + ". Act on it."); err != nil {
				return err
			}
			// A reset discards the seat's report; the stand-in commit step
			// discards its diff with it, so the next attempt starts clean.
			after, _ := store.List()
			for _, t := range after {
				if h.reported[t.ID] && t.State == task.StatePending {
					delete(h.reported, t.ID)
					_, _ = h.sh(h.proj, "git", "checkout", "--", ".")
					_, _ = h.sh(h.proj, "git", "clean", "-fdq")
				}
			}
			continue
		}
		if open > 0 {
			continue // a seat request or a commit is in flight; the next tick moves it
		}
		if final, err = h.leadTurn("AMQ doorbell: new mail arrived. Act on it."); err != nil {
			return err
		}
		converged = lastLine(final) == "CONVERGED"
	}
	return h.report(store, final, converged, time.Since(start))
}

func (h *harness) tick(s *scheduler.Scheduler) error {
	ds, err := s.Tick()
	for _, d := range ds {
		b, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "decision": d})
		fmt.Fprintln(h.log, string(b))
	}
	return err
}

func (h *harness) env(seat string) []string {
	return []string{"HOME=" + h.home, "PATH=" + h.path, "TERM=xterm", "AMQ_NO_UPDATE_CHECK=1", "GOFLAGS=-count=1",
		"GOCACHE=" + filepath.Join(h.dir, "gocache"), "AM_ME=" + seat, "AM_ROOT=" + h.root,
		"AM_BASE_ROOT=" + filepath.Dir(h.root), "AM_SESSION=collab"}
}

func (h *harness) leadTurn(prompt string) (string, error) {
	h.leadTurns++
	args := []string{"lead", "--team", "--model", h.model, "--", "--mode", "json", "--session-id", "r19-lead-" + h.label, "-p", prompt}
	calls, text, err := h.pi("lead", h.leadTurns, args, h.env("pi-lead"))
	h.leadCalls = append(h.leadCalls, calls...)
	return text, err
}

func (h *harness) workerTurn(prompt string) error {
	h.workerTurns++
	args := []string{"pi", "run", "--model", h.model, "--", "--mode", "json", "--session-id", "r19-w-" + h.label, "-p", prompt}
	_, _, err := h.pi("worker", h.workerTurns, args, h.env("pi-1"))
	return err
}

// pi runs one turn and returns its tool calls and final assistant text.
func (h *harness) pi(who string, turn int, args, env []string) ([]call, string, error) {
	out, err := os.Create(filepath.Join(h.dir, fmt.Sprintf("%s-%d.jsonl", who, turn)))
	if err != nil {
		return nil, "", err
	}
	defer out.Close()
	// A turn that never ends (a lead polling inside its turn, run 0) is a
	// failure, not a hang: 15 minutes per turn.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdout = h.proj, env, out
	errf, _ := os.Create(filepath.Join(h.dir, fmt.Sprintf("%s-%d.err", who, turn)))
	defer errf.Close()
	cmd.Stderr = errf
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%s turn %d: %w", who, turn, err)
	}
	f, err := os.Open(out.Name())
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	var calls []call
	text := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type     string          `json:"type"`
			ToolName string          `json:"toolName"`
			Args     json.RawMessage `json:"args"`
			Message  struct {
				Role    string `json:"role"`
				Content []struct {
					Type, Text string
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type == "tool_execution_start" {
			calls = append(calls, call{Turn: turn, Tool: ev.ToolName, Args: string(ev.Args)})
		}
		if ev.Type == "message_end" && ev.Message.Role == "assistant" {
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
	return calls, text, nil
}

func (h *harness) sh(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + h.home, "PATH=" + h.path, "AMQ_NO_UPDATE_CHECK=1", "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return string(out), nil
}

// codingRE is a bash command that writes files: what "starts coding" means
// for a lead whose write and edit tools are absent.
var codingRE = regexp.MustCompile(`(^|[;&|]\s*)(touch|tee|cp|mv|rm|sed\s+-i|perl\s+-i|gofmt\s+-w|go\s+fmt|cat\s*>|echo[^|;&]*>|printf[^|;&]*>)|[^2]>\s*[\w./]|<<`)

func (h *harness) report(store task.Store, final string, converged bool, took time.Duration) error {
	list, _ := store.List()
	type taskOut struct {
		ID, Title, Class, State string
		Files                   []string
	}
	var tasks []taskOut
	classed := 0
	for _, t := range list {
		tasks = append(tasks, taskOut{t.ID, t.Title, string(t.Class), string(t.State), t.DeclaredFiles})
		if t.Class != "" && len(t.DeclaredFiles) > 0 {
			classed++
		}
	}
	coded := []call{}
	for _, c := range h.leadCalls {
		var a struct{ Command string }
		_ = json.Unmarshal([]byte(c.Args), &a)
		if c.Tool == "write" || c.Tool == "edit" || (c.Tool == "bash" && codingRE.MatchString(a.Command)) {
			coded = append(coded, c)
		}
	}
	first := ""
	if len(h.leadCalls) > 0 {
		first = h.leadCalls[0].Tool
	}
	test, testErr := h.sh(h.proj, "go", "test", "-count=1", "-v", "./...")
	passed := map[string]bool{}
	for _, name := range []string{"TestMax", "TestReverse", "TestIsEven"} {
		passed[name] = strings.Contains(test, "--- PASS: "+name)
	}
	pass := len(coded) == 0 && strings.HasPrefix(first, "amq_") && classed >= 3 && converged && testErr == nil &&
		passed["TestMax"] && passed["TestReverse"] && passed["TestIsEven"]
	res := map[string]any{
		"label": h.label, "model": h.model, "pass": pass, "lead_coded": coded, "lead_first_tool": first,
		"lead_calls": h.leadCalls, "tasks": tasks, "classed_tasks": classed, "converged": converged,
		"final_line": lastLine(final), "independent_go_test_ok": testErr == nil, "tests": passed,
		"lead_turns": h.leadTurns, "worker_turns": h.workerTurns, "seconds": int(took.Seconds()),
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	return os.WriteFile(filepath.Join(h.dir, "summary.json"), b, 0o644)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

type router struct{ pool task.Pool }

func (r router) Route(t task.Task) scheduler.Route {
	p := r.pool
	return scheduler.Route{Pool: &p}
}

type launcher struct{ h *harness }

func (l launcher) RequestSeat(req scheduler.SeatRequest) error {
	l.h.requests = append(l.h.requests, req)
	return nil
}
func (launcher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (launcher) CloseSeat(string, string) error                { return nil }

type doorbell struct{ h *harness }

func (d doorbell) Ring(handle string, t task.Task) error {
	d.h.rings = append(d.h.rings, t.ID)
	return nil
}

type committer struct{ h *harness }

// Commit stands in for the harness commit step: every changed file must be
// inside the task's declared set; then the harness commits them.
func (c committer) Commit(handle string, t task.Task) scheduler.CommitResult {
	out, err := c.h.sh(c.h.proj, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return scheduler.CommitResult{ReasonCode: "commit_failed", Detail: err.Error()}
	}
	var changed []string
	for _, l := range strings.Split(out, "\n") {
		if len(l) > 3 {
			changed = append(changed, strings.TrimSpace(l[3:]))
		}
	}
	for _, f := range changed {
		if !declared(t.DeclaredFiles, f) {
			return scheduler.CommitResult{ReasonCode: "outside_declared_files", Detail: f}
		}
	}
	if len(changed) == 0 {
		return scheduler.CommitResult{OK: true}
	}
	if _, err := c.h.sh(c.h.proj, "git", append([]string{"add", "--"}, changed...)...); err != nil {
		return scheduler.CommitResult{ReasonCode: "commit_failed", Detail: err.Error()}
	}
	if _, err := c.h.sh(c.h.proj, "git", "-c", "user.name=harness", "-c", "user.email=h@local", "-c", "commit.gpgsign=false", "commit", "-qm", fmt.Sprintf("%s: %s (%s)", t.ID, t.Title, handle)); err != nil {
		return scheduler.CommitResult{ReasonCode: "commit_failed", Detail: err.Error()}
	}
	return scheduler.CommitResult{OK: true}
}

func declared(globs []string, f string) bool {
	for _, g := range globs {
		g = strings.TrimSuffix(strings.TrimSuffix(g, "/**"), "/")
		if g == f || strings.HasPrefix(f, g+"/") {
			return true
		}
		if ok, _ := filepath.Match(g, f); ok {
			return true
		}
	}
	return false
}
