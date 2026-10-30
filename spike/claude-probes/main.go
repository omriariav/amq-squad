// Command claude-probes runs the live Claude Code probes of brief 2b-2
// milestone B against the installed claude, each in a scratch project and a
// scratch CLAUDE_CONFIG_DIR written by internal/claudeseat (the seat layout
// of commit 4); the owner's ~/.claude is never written.
//
//	auth        a fresh seat config dir: does the login come from the Keychain?
//	plugin      the plugin layout loads: a stub hook writes a marker
//	a-seat      (a) the seat's own dir, no allow rule, the hook absent, dontAsk: does echo run?
//	a-allow     (a) control: an allow rule Bash(echo:*) in the config dir: echo runs
//	a-settings  (a) a --settings file with an empty allow over a config dir allow: merged?
//	b           (b) a worktree deny of Bash(echo:*) against the hook's explicit allow
//	fault-deny, fault-timeout, fault-exit1, fault-missing   the S4 fault table under dontAsk
//
// Usage: go run ./spike/claude-probes -out DIR [-model haiku] [probe ...]
// Writes one JSON line per probe to DIR/probes.jsonl and the raw stream of
// each run to DIR/<probe>.jsonl.
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
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

type probe struct {
	name     string
	hook     string // allow | deny | sleep | exit1 | missing | "" (no plugin enabled)
	dirAllow []string
	settings string // a --settings file body
	project  string // project .claude/settings.json body
}

var probes = []probe{
	{name: "auth", hook: "allow"},
	{name: "plugin", hook: "allow"},
	{name: "a-seat", hook: ""},
	{name: "a-allow", hook: "", dirAllow: []string{"Bash(echo:*)"}},
	{name: "a-settings", hook: "", dirAllow: []string{"Bash(echo:*)"}, settings: `{"permissions":{"allow":[]}}`},
	{name: "b", hook: "allow", project: `{"permissions":{"deny":["Bash(echo:*)"]}}`},
	{name: "fault-deny", hook: "deny"},
	{name: "fault-timeout", hook: "sleep"},
	{name: "fault-exit1", hook: "exit1"},
	{name: "fault-missing", hook: "missing"},
}

type result struct {
	Probe         string   `json:"probe"`
	ClaudeVersion string   `json:"claude_version"`
	Hook          string   `json:"hook"`
	EchoRan       bool     `json:"echo_ran"`
	ToolCalled    bool     `json:"tool_called"`
	ToolError     string   `json:"tool_error,omitempty"`
	HookCalls     int      `json:"hook_calls"`
	AuthError     string   `json:"auth_error,omitempty"`
	Seconds       float64  `json:"seconds"`
	ToolSeconds   float64  `json:"tool_seconds,omitempty"`
	Exit          int      `json:"exit"`
	FinalText     string   `json:"final_text,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

func main() {
	out := flag.String("out", "", "evidence directory")
	model := flag.String("model", "haiku", "model")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "claude-probes: -out DIR is required")
		os.Exit(2)
	}
	want := map[string]bool{}
	for _, a := range flag.Args() {
		want[a] = true
	}
	os.MkdirAll(*out, 0o700)
	ver, _ := exec.Command("claude", "--version").Output()
	log, _ := os.OpenFile(filepath.Join(*out, "probes.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	defer log.Close()
	for _, p := range probes {
		if len(want) > 0 && !want[p.name] {
			continue
		}
		r := run(*out, *model, p)
		r.ClaudeVersion = strings.TrimSpace(string(ver))
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
		fmt.Fprintln(log, string(b))
	}
}

func run(out, model string, p probe) result {
	r := result{Probe: p.name, Hook: p.hook}
	scratch, _ := os.MkdirTemp(out, p.name+"-")
	proj := filepath.Join(scratch, "project")
	os.MkdirAll(proj, 0o755)
	if p.project != "" {
		os.MkdirAll(filepath.Join(proj, ".claude"), 0o700)
		os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(p.project), 0o600)
	}
	dir := filepath.Join(scratch, "claude-config")
	if _, err := claudeseat.Write(dir, "dontAsk", proj, time.Now()); err != nil {
		r.Notes = append(r.Notes, "write config dir: "+err.Error())
		return r
	}
	marker := filepath.Join(scratch, "hook-calls")
	stubHook(dir, p.hook, marker)
	if len(p.dirAllow) > 0 || p.hook == "" {
		var s map[string]any
		raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
		json.Unmarshal(raw, &s)
		s["permissions"] = map[string]any{"defaultMode": "dontAsk", "allow": p.dirAllow}
		if p.hook == "" {
			s["enabledPlugins"] = map[string]bool{}
		}
		raw, _ = json.MarshalIndent(s, "", "  ")
		os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600)
	}
	token := "probe-" + p.name
	args := []string{"-p", "Use the Bash tool to run exactly this command and nothing else: echo " + token + " . Then reply with one line: RAN followed by the output, or DENIED followed by the error.",
		"--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk", "--model", model, "--max-turns", "3"}
	if p.settings != "" {
		sp := filepath.Join(scratch, "launch-settings.json")
		os.WriteFile(sp, []byte(p.settings), 0o600)
		args = append(args, "--settings", sp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = proj
	cmd.Env = append(scrub(os.Environ()), "CLAUDE_CONFIG_DIR="+dir, "PROBE_MARKER="+marker)
	stream, _ := os.Create(filepath.Join(out, p.name+".jsonl"))
	defer stream.Close()
	pipe, _ := cmd.StdoutPipe()
	cmd.Stderr = stream
	began := time.Now()
	var toolAt time.Time
	if err := cmd.Start(); err != nil {
		r.Notes = append(r.Notes, err.Error())
		return r
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		line := sc.Bytes()
		stream.Write(append(append([]byte(nil), line...), '\n'))
		var ev struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
			Message struct {
				Content []struct {
					Type    string          `json:"type"`
					Name    string          `json:"name"`
					Content json.RawMessage `json:"content"`
					IsError bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		for _, c := range ev.Message.Content {
			switch {
			case c.Type == "tool_use" && c.Name == "Bash":
				r.ToolCalled, toolAt = true, time.Now()
			case c.Type == "tool_result":
				text := string(c.Content)
				if !toolAt.IsZero() {
					r.ToolSeconds = time.Since(toolAt).Seconds()
				}
				if strings.Contains(text, token) && !c.IsError {
					r.EchoRan = true
				} else if c.IsError || text != "" {
					r.ToolError = trim(text)
				}
			}
		}
		if ev.Type == "result" {
			r.FinalText = trim(ev.Result)
			if ev.IsError && strings.Contains(strings.ToLower(ev.Result), "log") {
				r.AuthError = trim(ev.Result)
			}
		}
	}
	err := cmd.Wait()
	r.Seconds = time.Since(began).Seconds()
	if ee, ok := err.(*exec.ExitError); ok {
		r.Exit = ee.ExitCode()
	}
	if raw, err := os.ReadFile(marker); err == nil {
		r.HookCalls = strings.Count(string(raw), "\n")
	}
	if r.AuthError == "" && !r.ToolCalled && (strings.Contains(r.FinalText, "/login") || strings.Contains(strings.ToLower(r.FinalText), "api key")) {
		r.AuthError = r.FinalText
	}
	return r
}

// stubHook replaces the installed plugin's hook with a probe stub that
// records each call and answers by kind.
func stubHook(dir, kind, marker string) {
	if kind == "" {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", "*", "hooks"))
	for _, h := range matches {
		var body string
		switch kind {
		case "allow":
			body = `echo call >> "$PROBE_MARKER"; cat >/dev/null; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"probe allow"}}'`
		case "deny":
			body = `echo call >> "$PROBE_MARKER"; cat >/dev/null; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"probe deny"}}'`
		case "sleep":
			body = `echo call >> "$PROBE_MARKER"; cat >/dev/null; sleep 20`
		case "exit1":
			body = `echo call >> "$PROBE_MARKER"; cat >/dev/null; exit 1`
		}
		script := filepath.Join(h, "amq-squad-hook.sh")
		os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o700)
		cmd := `sh \"${CLAUDE_PLUGIN_ROOT}/hooks/amq-squad-hook.sh\" pre-tool`
		if kind == "missing" {
			cmd = `/nonexistent/amq-squad-hook pre-tool`
		}
		hooks := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"` + cmd + `","timeout":5}]}]}}`
		os.WriteFile(filepath.Join(h, "hooks.json"), []byte(hooks), 0o600)
	}
}

// scrub drops the caller's Claude session variables (the probe is a fresh
// seat, not a child of this session).
func scrub(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") || name == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(name, "AM_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
