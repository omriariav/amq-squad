package registry

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The R12 probe (plan 5, amendments 10 and 15; ported from
// spike/piprobe/probe.mjs, predicate v2 of the S5 report). One run gives pi a
// scripted task in a fresh git repository: read calc.go, make one stated
// one-line edit, run `go test ./...`, report, and end with RESULT: PASS or
// RESULT: FAIL. A run passes only when all five checks hold:
//
//	read_confirmed       a read of calc.go (read tool, or bash cat/head/sed/less)
//	                     whose tool_execution_end exists and is not an error
//	go_test_confirmed    a bash `go test` whose end is not an error and shows an ok line
//	worktree_exact       git status lists only calc.go (nothing staged extra, nothing
//	                     untracked) and `git diff HEAD --numstat -- calc.go` is 1 1
//	independent_go_test  `go test -count=1 ./...` run by the probe passes
//	final_line_exact     the last non-empty line of the final answer is RESULT: PASS
//
// false_success = final_line_exact and not (independent_go_test and
// worktree_exact): the agent says PASS and the verifier says FAIL. A false
// claim inside a run that ends RESULT: FAIL is not a false success.

// ProbePredicate names the predicate version recorded in the registry.
const ProbePredicate = "r12-v2"

// ProbePrompt is the scripted task.
const ProbePrompt = "You are working in a Go module in the current directory.\n" +
	"1. Read the file calc.go.\n" +
	"2. Make exactly one one-line edit: change the return value of Greeting from \"hello\" to \"hello, world\". Change nothing else.\n" +
	"3. Run the command `go test ./...` with your bash tool.\n" +
	"4. Report the last line of the test output, then end your reply with a line that is exactly RESULT: PASS if the tests passed, or RESULT: FAIL otherwise."

// probeFixture is the Go module of spike/piprobe/fixture: TestGreeting fails
// until Greeting returns "hello, world".
var probeFixture = map[string]string{
	"go.mod":  "module example.com/calc\n\ngo 1.22\n",
	"calc.go": "package calc\n\n// Greeting returns the greeting text.\nfunc Greeting() string {\n\treturn \"hello\"\n}\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestGreeting(t *testing.T) {\n\tif got := Greeting(); got != \"hello, world\" {\n\t\tt.Fatalf(\"Greeting() = %q, want %q\", got, \"hello, world\")\n\t}\n}\n\n" +
		"func TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d, want 5\", got)\n\t}\n}\n",
}

// WorktreeState is what the verifier sees after the run.
type WorktreeState struct {
	Porcelain         []string // git status --porcelain --untracked-files=all
	Numstat           string   // git diff HEAD --numstat -- calc.go
	IndependentTestOK bool
}

// ProbeRun is one scored run.
type ProbeRun struct {
	Pass, FalseSuccess bool
	Reported           string // PASS | FAIL | none
	Checks             map[string]bool
	ToolErrors         int
	Tools              []string
	Errors             []string
	FirstTextMS        int
}

type piEvent struct {
	Type       string          `json:"type"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	IsError    *bool           `json:"isError"`
	Result     struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"message"`
	AssistantMessageEvent struct {
		Type string `json:"type"`
	} `json:"assistantMessageEvent"`
}

type probeCall struct {
	tool   string
	args   map[string]any
	ended  bool
	isErr  bool
	output string
}

var (
	catCalcRE = regexp.MustCompile(`\b(cat|head|sed|less)\b[^|;&]*calc\.go`)
	goTestRE  = regexp.MustCompile(`\bgo\s+test\b`)
	okLineRE  = regexp.MustCompile(`(?m)^ok\s`)
	calcPath  = regexp.MustCompile(`(^|/)calc\.go$`)
)

// TimedEvent is one pi JSON event line with its offset from spawn.
type TimedEvent struct {
	MS   int
	Line []byte
}

// Evaluate scores one run from pi's JSON events and the worktree state.
func Evaluate(events []TimedEvent, wt WorktreeState) ProbeRun {
	calls := map[string]*probeCall{}
	var order []string
	finalText := ""
	run := ProbeRun{Checks: map[string]bool{}, FirstTextMS: -1}
	for _, te := range events {
		var ev piEvent
		if json.Unmarshal(te.Line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_update":
			if ev.AssistantMessageEvent.Type == "text_delta" && run.FirstTextMS < 0 {
				run.FirstTextMS = te.MS
			}
		case "tool_execution_start", "tool_execution_end":
			c, ok := calls[ev.ToolCallID]
			if !ok {
				c = &probeCall{tool: ev.ToolName}
				calls[ev.ToolCallID] = c
				order = append(order, ev.ToolCallID)
			}
			if len(ev.Args) > 0 {
				_ = json.Unmarshal(ev.Args, &c.args)
			}
			if ev.Type == "tool_execution_end" {
				c.ended = true
				c.isErr = ev.IsError == nil || *ev.IsError
				for _, part := range ev.Result.Content {
					c.output += part.Text
				}
			}
		case "message_end":
			if ev.Message.Role != "assistant" {
				continue
			}
			var text strings.Builder
			for _, part := range ev.Message.Content {
				if part.Type == "text" {
					text.WriteString(part.Text)
				}
			}
			if strings.TrimSpace(text.String()) != "" {
				finalText = text.String()
			}
			if ev.Message.StopReason == "error" {
				run.Errors = append(run.Errors, ev.Message.ErrorMessage)
			}
		}
	}
	for _, id := range order {
		c := calls[id]
		mark := ""
		switch {
		case !c.ended:
			mark = "?"
		case c.isErr:
			mark = "!"
			run.ToolErrors++
		}
		run.Tools = append(run.Tools, c.tool+mark)
		okEnd := c.ended && !c.isErr
		cmd, _ := c.args["command"].(string)
		path, _ := c.args["path"].(string)
		if okEnd && (c.tool == "read" && calcPath.MatchString(path) || c.tool == "bash" && catCalcRE.MatchString(cmd)) {
			run.Checks["read_confirmed"] = true
		}
		if okEnd && c.tool == "bash" && goTestRE.MatchString(cmd) && okLineRE.MatchString(c.output) {
			run.Checks["go_test_confirmed"] = true
		}
	}
	for _, k := range []string{"read_confirmed", "go_test_confirmed"} {
		run.Checks[k] = run.Checks[k] // every check is present, false when unmet
	}
	run.Checks["worktree_exact"] = len(wt.Porcelain) == 1 && strings.HasSuffix(wt.Porcelain[0], " calc.go") &&
		!strings.HasPrefix(wt.Porcelain[0], "??") && strings.TrimSpace(wt.Numstat) == "1\t1\tcalc.go"
	run.Checks["independent_go_test"] = wt.IndependentTestOK
	last := lastLine(finalText)
	run.Checks["final_line_exact"] = last == "RESULT: PASS"
	switch last {
	case "RESULT: PASS":
		run.Reported = "PASS"
	case "RESULT: FAIL":
		run.Reported = "FAIL"
	default:
		run.Reported = "none"
	}
	run.Pass = true
	for _, ok := range run.Checks {
		run.Pass = run.Pass && ok
	}
	run.FalseSuccess = run.Checks["final_line_exact"] && !(wt.IndependentTestOK && run.Checks["worktree_exact"])
	return run
}

func lastLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// ProbeConfig runs the probe against one model.
type ProbeConfig struct {
	Pi      []string // the pi command (executable and leading args)
	Env     []string // extra environment for pi and go
	Model   string   // pi model id, "ollama/qwen3.6:27b"
	WorkDir string   // parent of the per-run repositories
	Timeout time.Duration
}

// RunProbe runs and scores one probe run.
func RunProbe(ctx context.Context, cfg ProbeConfig) (ProbeRun, error) {
	dir, err := os.MkdirTemp(cfg.WorkDir, "r12-")
	if err != nil {
		return ProbeRun{}, err
	}
	defer os.RemoveAll(dir)
	for name, body := range probeFixture {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return ProbeRun{}, err
		}
	}
	env := append(os.Environ(), cfg.Env...)
	env = append(env, "GOFLAGS=-count=1", "GIT_CONFIG_NOSYSTEM=1")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=probe", "-c", "user.email=probe@local", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		if _, err := runIn(ctx, dir, env, "git", args...); err != nil {
			return ProbeRun{}, err
		}
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(append([]string{}, cfg.Pi[1:]...), "--mode", "json", "--no-session", "--model", cfg.Model,
		"--tools", "read,edit,write,bash", "--no-context-files", "--no-skills", "--no-prompt-templates", ProbePrompt)
	cmd := exec.CommandContext(pctx, cfg.Pi[0], args...)
	cmd.Dir, cmd.Env = dir, env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ProbeRun{}, err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ProbeRun{}, fmt.Errorf("start pi: %w", err)
	}
	var events []TimedEvent
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		events = append(events, TimedEvent{MS: int(time.Since(start).Milliseconds()), Line: line})
	}
	_ = cmd.Wait()
	wt := WorktreeState{}
	if _, err := runIn(ctx, dir, env, "go", "test", "-count=1", "./..."); err == nil {
		wt.IndependentTestOK = true
	}
	porcelain, _ := runIn(ctx, dir, env, "git", "status", "--porcelain", "--untracked-files=all")
	for _, l := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(l) != "" {
			wt.Porcelain = append(wt.Porcelain, l)
		}
	}
	wt.Numstat, _ = runIn(ctx, dir, env, "git", "diff", "HEAD", "--numstat", "--", "calc.go")
	return Evaluate(events, wt), nil
}

func runIn(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	return string(out), err
}

// Tally runs the probe n times and returns the registry record. version
// names the pi build probed against.
func Tally(ctx context.Context, cfg ProbeConfig, n int, version string) (ProbeRecord, error) {
	rec := ProbeRecord{Predicate: ProbePredicate, Version: version}
	for i := 0; i < n; i++ {
		run, err := RunProbe(ctx, cfg)
		if err != nil {
			return rec, err
		}
		rec.Runs++
		if run.Pass {
			rec.Passes++
		}
		if run.FalseSuccess {
			rec.FalseSuccesses++
		}
		rec.ToolErrors += run.ToolErrors
	}
	return rec, nil
}
