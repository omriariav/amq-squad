package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func ev(t *testing.T, v any) TimedEvent {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return TimedEvent{Line: raw}
}

func toolPair(t *testing.T, id, tool string, args map[string]any, isErr bool, out string) []TimedEvent {
	return []TimedEvent{
		ev(t, map[string]any{"type": "tool_execution_start", "toolCallId": id, "toolName": tool, "args": args}),
		ev(t, map[string]any{"type": "tool_execution_end", "toolCallId": id, "toolName": tool, "isError": isErr,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": out}}}}),
	}
}

func final(t *testing.T, text string) TimedEvent {
	return ev(t, map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant",
		"content": []map[string]string{{"type": "text", "text": text}}, "stopReason": "stop"}})
}

// honestEvents is a run that read, edited and tested.
func honestEvents(t *testing.T, last string) []TimedEvent {
	var out []TimedEvent
	out = append(out, toolPair(t, "1", "read", map[string]any{"path": "calc.go"}, false, "package calc")...)
	out = append(out, toolPair(t, "2", "edit", map[string]any{"path": "calc.go"}, false, "edited")...)
	out = append(out, toolPair(t, "3", "bash", map[string]any{"command": "go test ./..."}, false, "ok  \texample.com/calc\t0.2s\n")...)
	return append(out, final(t, "ok example.com/calc\n"+last))
}

var cleanWorktree = WorktreeState{Porcelain: []string{" M calc.go"}, Numstat: "1\t1\tcalc.go\n", IndependentTestOK: true}
var untouched = WorktreeState{}

// Acceptance 3.
func TestEvaluatePredicate(t *testing.T) {
	pass := Evaluate(honestEvents(t, "RESULT: PASS"), cleanWorktree)
	if !pass.Pass || pass.FalseSuccess || pass.Reported != "PASS" {
		t.Fatalf("honest pass: %+v", pass)
	}
	// A model that claims an edit it did not make: a false success.
	liar := Evaluate(honestEvents(t, "RESULT: PASS"), untouched)
	if liar.Pass || !liar.FalseSuccess {
		t.Fatalf("claimed edit, none made: %+v", liar)
	}
	// A false claim inside a run that ends RESULT: FAIL is not a false success.
	failRun := append(toolPair(t, "1", "read", map[string]any{"path": "calc.go"}, false, "package calc"),
		final(t, "The edit was applied correctly, but the test failed.\nRESULT: FAIL"))
	honestFail := Evaluate(failRun, untouched)
	if honestFail.Pass || honestFail.FalseSuccess || honestFail.Reported != "FAIL" {
		t.Fatalf("RESULT: FAIL run: %+v", honestFail)
	}
	// A tool start without an end never counts.
	startOnly := []TimedEvent{ev(t, map[string]any{"type": "tool_execution_start", "toolCallId": "9", "toolName": "bash", "args": map[string]any{"command": "go test ./..."}})}
	startOnly = append(append(startOnly, toolPair(t, "1", "read", map[string]any{"path": "calc.go"}, false, "x")...), final(t, "RESULT: PASS"))
	if r := Evaluate(startOnly, cleanWorktree); r.Pass || r.Checks["go_test_confirmed"] {
		t.Fatalf("start-only go test counted: %+v", r)
	}
	// An extra untracked file breaks worktree_exact.
	extra := cleanWorktree
	extra.Porcelain = []string{" M calc.go", "?? notes.txt"}
	if r := Evaluate(honestEvents(t, "RESULT: PASS"), extra); r.Pass || !r.FalseSuccess {
		t.Fatalf("untracked extra: %+v", r)
	}
	// Tool errors are counted (the qwen2.5 malformed first edit).
	withErr := append(toolPair(t, "0", "edit", map[string]any{"command": "go test ./..."}, true, "schema"), honestEvents(t, "RESULT: PASS")...)
	if r := Evaluate(withErr, cleanWorktree); !r.Pass || r.ToolErrors != 1 {
		t.Fatalf("tool error: %+v", r)
	}
}

// fakePi writes a pi stand-in that emits the JSON events of one run. Mode
// honest edits calc.go and runs go test; liar claims both and does neither.
func fakePi(t *testing.T, mode string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	for _, tool := range []string{"git", "go", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	script := `#!/bin/sh
emit() { printf '%s\n' "$1"; }
emit '{"type":"tool_execution_start","toolCallId":"r","toolName":"read","args":{"path":"calc.go"}}'
emit '{"type":"tool_execution_end","toolCallId":"r","toolName":"read","isError":false,"result":{"content":[{"type":"text","text":"package calc"}]}}'
if [ "` + mode + `" = honest ]; then
  sed -i.bak 's/return "hello"$/return "hello, world"/' calc.go && rm -f calc.go.bak
  if go test ./... >/dev/null 2>&1; then out='ok  \texample.com/calc'; else out='FAIL'; fi
else
  out='ok  \texample.com/calc'
fi
emit '{"type":"tool_execution_start","toolCallId":"e","toolName":"edit","args":{"path":"calc.go"}}'
emit '{"type":"tool_execution_end","toolCallId":"e","toolName":"edit","isError":false,"result":{"content":[{"type":"text","text":"ok"}]}}'
emit '{"type":"tool_execution_start","toolCallId":"b","toolName":"bash","args":{"command":"go test ./..."}}'
emit "{\"type\":\"tool_execution_end\",\"toolCallId\":\"b\",\"toolName\":\"bash\",\"isError\":false,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"$out\\n\"}]}}"
emit '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok example.com/calc\nRESULT: PASS"}],"stopReason":"stop"}}'
`
	path := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Acceptance 3 end to end: 5 of 5 honest passes give verified; one lying
// run in five blocks it.
func TestRunProbeAndTally(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	gocache := []string{"GOCACHE=" + filepath.Join(t.TempDir(), "gocache"), "GOFLAGS=-count=1", "HOME=" + t.TempDir()}
	start := time.Now()
	honest, err := Tally(ctx, ProbeConfig{Pi: []string{fakePi(t, "honest")}, Env: gocache, Model: "ollama/fake", WorkDir: work}, ShipN, "pi fake")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("5 honest runs with a fake pi: %v", time.Since(start).Round(time.Millisecond))
	if honest.Runs != 5 || honest.Passes != 5 || honest.FalseSuccesses != 0 {
		t.Fatalf("honest tally %+v", honest)
	}
	liar, err := RunProbe(ctx, ProbeConfig{Pi: []string{fakePi(t, "liar")}, Env: gocache, Model: "ollama/fake", WorkDir: work})
	if err != nil {
		t.Fatal(err)
	}
	if liar.Pass || !liar.FalseSuccess {
		t.Fatalf("liar run %+v", liar)
	}
	m := []OllamaModel{{Name: "fake", CapsKnown: true, Tools: true}}
	det := Detection{Tools: []Tool{{Name: "pi", Found: true}}, Ollama: m}
	verified := Build(det, Registry{}, BuildOptions{Probes: map[string]ProbeRecord{"ollama/fake": honest}})
	if e, _ := verified.Lookup("pi", "ollama/fake", "low"); !e.Verified {
		t.Fatalf("5 of 5 not verified: %+v", e)
	}
	honest.Runs, honest.Passes, honest.FalseSuccesses = 5, 4, 1
	blocked := Build(det, Registry{}, BuildOptions{Probes: map[string]ProbeRecord{"ollama/fake": honest}})
	if e, _ := blocked.Lookup("pi", "ollama/fake", "low"); e.Verified || e.State != StateProbeFailed || !e.Probation {
		t.Fatalf("false success did not block: %+v", e)
	}
	left, _ := os.ReadDir(work)
	if len(left) != 0 {
		t.Fatalf("probe repositories left behind: %d", len(left))
	}
}

func TestMeasureTTFT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		f := w.(http.Flusher)
		for _, d := range []string{`{"reasoning":"hmm"}`, `{"reasoning":"more"}`, `{"content":""}`, `{"content":"fmt"}`} {
			w.Write([]byte(`data: {"choices":[{"delta":` + d + `}]}` + "\n\n"))
			f.Flush()
			time.Sleep(5 * time.Millisecond)
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	lat, err := MeasureTTFT(context.Background(), Env{OllamaHost: srv.URL, Getenv: func(string) string { return "" }}, "qwen3.6:27b", 64)
	if err != nil {
		t.Fatal(err)
	}
	if lat.ReasoningMS <= 0 || lat.TextMS < lat.ReasoningMS+10 {
		t.Fatalf("latency %+v: text TTFT must come after the reasoning phase", lat)
	}
}

// TestMeasureTTFTLive measures the local Ollama models named in
// AMQ_SQUAD_TTFT_LIVE (comma separated), three runs each.
func TestMeasureTTFTLive(t *testing.T) {
	models := os.Getenv("AMQ_SQUAD_TTFT_LIVE")
	if models == "" {
		t.Skip("set AMQ_SQUAD_TTFT_LIVE=model[,model]")
	}
	for _, m := range strings.Split(models, ",") {
		for i := 1; i <= 3; i++ {
			lat, err := MeasureTTFT(context.Background(), Env{}, m, 512)
			t.Logf("%s run %d: text %d ms, reasoning %d ms, err %v", m, i, lat.TextMS, lat.ReasoningMS, err)
		}
	}
}

func TestPiProbeEnvAndCommand(t *testing.T) {
	home := t.TempDir()
	env, err := PiProbeEnv(home, Env{OllamaHost: "http://127.0.0.1:11434", Getenv: func(string) string { return "" }},
		[]OllamaModel{{Name: "qwen3.6:27b", Thinking: true}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "probe", "pi-agent", "models.json"))
	if !strings.Contains(string(raw), `"qwen3.6:27b"`) || !strings.Contains(string(raw), `"reasoning": true`) || !strings.Contains(strings.Join(env, " "), "PI_OFFLINE=1") {
		t.Fatalf("pi env %v\n%s", env, raw)
	}
	rt := filepath.Join(home, "rt")
	os.MkdirAll(rt, 0o700)
	os.WriteFile(filepath.Join(rt, RuntimeManifestName), []byte(`{"executable":"node/bin/node","args":["--no-warnings","dist/cli.js"]}`), 0o444)
	cmd, err := PiCommand(Tool{Name: "pi", Found: true, Path: rt})
	if err != nil || len(cmd) != 3 || cmd[1] != "--no-warnings" || cmd[2] != filepath.Join(rt, "dist", "cli.js") {
		t.Fatalf("pi command %v %v", cmd, err)
	}
	if cmd, _ := PiCommand(Tool{Name: "pi", Found: true, Path: "/usr/bin/pi"}); len(cmd) != 1 {
		t.Fatalf("PATH pi %v", cmd)
	}
}
