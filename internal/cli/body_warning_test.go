package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuspiciousInlineBodyReasons(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unbalanced backtick", body: "run `go test", want: "unbalanced backticks"},
		{name: "unclosed command substitution", body: "inspect $(git status", want: "opening $( without a close"},
		{name: "command not found", body: "zsh: command not found: jq", want: "command-not-found residue"},
		{name: "bare command not found diagnostic", body: "command not found: without", want: "command-not-found residue"},
		{name: "shell not found form", body: "sh: missing-tool: not found", want: "command-not-found residue"},
		{name: "nested unclosed command substitution", body: "inspect $(printf '%s' $(date)", want: "opening $( without a close"},
		{name: "ordinary nested paren unclosed", body: "inspect $(foo (bar)", want: "opening $( without a close"},
		{name: "balanced syntax", body: "run `go test` and report $(date)", want: ""},
		{name: "balanced nested syntax", body: "report $(printf '%s' $(date))", want: ""},
		{name: "arithmetic syntax", body: "total $((1 + 2))", want: ""},
		{name: "prose command not found", body: "document how command not found errors are handled", want: ""},
		{name: "ordinary prose", body: "please review PR #417", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(suspiciousInlineBodyReasons(tt.body), ", ")
			if tt.want == "" && got != "" {
				t.Fatalf("unexpected reasons: %q", got)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Fatalf("reasons = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWarnSuspiciousInlineBodyAdvisory(t *testing.T) {
	var out bytes.Buffer
	warnSuspiciousInlineBody("send", "run `go test", true, &out)
	for _, want := range []string{"warning: amq-squad send", "patterns commonly left by shell mangling", "may have been changed", "unbalanced backticks", "--body-file FILE", "--body-file -", "Shell expansion happens before"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("warning %q missing %q", out.String(), want)
		}
	}

	out.Reset()
	warnSuspiciousInlineBody("send", "run `go test", false, &out)
	if out.Len() != 0 {
		t.Fatalf("non-inline body warned: %q", out.String())
	}
}

func TestRunSendAndDispatchWarnOnlyForSuspiciousInlineBody(t *testing.T) {
	commands := []struct {
		name string
		run  func([]string) error
		base []string
		json bool
	}{
		{name: "send", run: runSend, base: []string{"--role", "qa"}},
		{name: "dispatch", run: runDispatch, base: []string{"--role", "qa", "--subject", "Inspect"}, json: true},
	}
	for _, command := range commands {
		t.Run(command.name+" inline signatures", func(t *testing.T) {
			for _, body := range []string{"run `go test", "inspect $(git status", "zsh: command not found: jq"} {
				args := append(append([]string{}, command.base...), "--body", body)
				if command.json {
					args = append(args, "--json")
				}
				stdout, stderr, _ := captureOutput(t, func() error { return command.run(args) })
				if !strings.Contains(stderr, "warning: amq-squad "+command.name) {
					t.Fatalf("body %q stderr = %q", body, stderr)
				}
				if stdout != "" {
					t.Fatalf("stdout polluted by advisory: %q", stdout)
				}
			}
		})

		t.Run(command.name+" balanced inline control", func(t *testing.T) {
			args := append(append([]string{}, command.base...), "--body", "run `go test` and report $(date)")
			_, stderr, _ := captureOutput(t, func() error { return command.run(args) })
			if strings.Contains(stderr, "patterns commonly left by shell mangling") {
				t.Fatalf("balanced inline body warned: %q", stderr)
			}
		})

		t.Run(command.name+" file silence", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "body.md")
			if err := os.WriteFile(path, []byte("run `go test"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, command.base...), "--body-file", path)
			_, stderr, _ := captureOutput(t, func() error { return command.run(args) })
			if strings.Contains(stderr, "patterns commonly left by shell mangling") {
				t.Fatalf("file body warned: %q", stderr)
			}
		})

		t.Run(command.name+" stdin silence", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stdin.md")
			if err := os.WriteFile(path, []byte("inspect $(git status"), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			oldStdin := os.Stdin
			os.Stdin = stdin
			defer func() { os.Stdin = oldStdin }()

			args := append(append([]string{}, command.base...), "--body-file", "-")
			_, stderr, _ := captureOutput(t, func() error { return command.run(args) })
			if strings.Contains(stderr, "patterns commonly left by shell mangling") {
				t.Fatalf("stdin body warned: %q", stderr)
			}
		})
	}
}

func TestRunDispatchSuspiciousInlineBodyKeepsJSONStdoutPure(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-417 to qa (session: , root: /x/.agent-mail/issue-417)\n")
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)
	body := "run `go test"

	stdout, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-417", "--role", "qa", "--subject", "Inspect", "--body", body, "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "warning: amq-squad dispatch") {
		t.Fatalf("missing stderr warning: %q", stderr)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "dispatch" || env.Data.MessageID != "msg-417" {
		t.Fatalf("bad dispatch envelope: %+v", env)
	}
	if len(*calls) != 1 {
		t.Fatalf("AMQ calls = %d, want 1", len(*calls))
	}
	gotBody := ""
	for i, arg := range (*calls)[0].Arg {
		if arg == "--body" && i+1 < len((*calls)[0].Arg) {
			gotBody = (*calls)[0].Arg[i+1]
		}
	}
	if gotBody != body {
		t.Fatalf("warning changed body bytes: got %q want %q", gotBody, body)
	}
}
