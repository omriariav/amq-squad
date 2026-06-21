package cli

import (
	"strings"
	"testing"
)

func withAMQCommandSeams(t *testing.T, env amqEnv, output string) *[]amqCommandRequest {
	t.Helper()
	var calls []amqCommandRequest
	prevEnv := resolveAMQEnvForAMQCommand
	prevRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		got := env
		got.Root = strings.ReplaceAll(got.Root, "{session}", session)
		got.SessionName = session
		got.Me = handle
		if got.BaseRoot == "" {
			got.BaseRoot = ".agent-mail"
		}
		return got, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		calls = append(calls, req)
		return []byte(output), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	return &calls
}

func TestAMQRouteBuildsRouteExplain(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack", "--target-session", "review", "--json"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	if !strings.Contains(stdout, `"routable":true`) {
		t.Fatalf("route output = %q", stdout)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--from-root", ".agent-mail/issue-96", "--me cto", "--to fullstack", "--session review", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestAMQRouteAddsJSONByDefault(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--to fullstack", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestAMQReceiptsWaitBuildsCommand(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "receipt ok\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"receipts", "wait", "--session", "issue-96", "--me", "qa", "--msg-id", "msg_123", "--stage", "dlq", "--timeout", "5s"})
	})
	if err != nil {
		t.Fatalf("amq receipts wait: %v", err)
	}
	if stdout != "receipt ok\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"receipts wait", ".agent-mail/issue-96", "--me qa", "--msg-id msg_123", "--stage dlq", "--timeout 5s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipts wait args missing %q: %s", want, got)
		}
	}
}

func TestAMQDLQRetryPreviewAndYesExecutes(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "retried\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"dlq", "retry", "--session", "issue-96", "--me", "qa", "--id", "dlq_1", "--yes"})
	})
	if err != nil {
		t.Fatalf("amq dlq retry: %v", err)
	}
	for _, want := range []string{"AMQ command preview", "amq dlq retry", "retried"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"dlq retry", ".agent-mail/issue-96", "--me qa", "--id dlq_1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dlq retry args missing %q: %s", want, got)
		}
	}
}

func TestAMQDLQRetryAllUsesRetryAllFlag(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "retried all\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"dlq", "retry-all", "--session", "issue-96", "--me", "qa", "--yes"})
	})
	if err != nil {
		t.Fatalf("amq dlq retry-all: %v", err)
	}
	if !strings.Contains(stdout, "amq dlq retry") || !strings.Contains(stdout, "--all") {
		t.Fatalf("stdout should preview retry --all:\n%s", stdout)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"dlq retry", ".agent-mail/issue-96", "--me qa", "--all"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dlq retry-all args missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "retry-all") {
		t.Fatalf("underlying AMQ command should be retry --all, got: %s", got)
	}
}

func TestAMQCleanupDryRunDoesNotExecute(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "cleaned\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"cleanup", "--session", "issue-96", "--tmp-older-than", "36h", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("amq cleanup dry-run: %v", err)
	}
	for _, want := range []string{"AMQ command preview", "amq cleanup", "--tmp-older-than 36h", "Dry run: command not executed."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run calls = %d, want 0", len(*calls))
	}
}

func TestAMQCleanupRequiresSession(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "cleaned\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"cleanup", "--tmp-older-than", "36h", "--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "amq cleanup requires --session") {
		t.Fatalf("cleanup without session error = %v", err)
	}
}

func TestAMQRejectsUnknownSubcommand(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"frobnicate"})
	})
	if err == nil || !strings.Contains(err.Error(), "unknown amq subcommand") {
		t.Fatalf("unknown subcommand error = %v", err)
	}
}

func TestAMQSendResolvesRootAndForwards(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "sent\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--me", "lead", "--to", "worker", "--kind", "todo", "--subject", "go"})
	})
	if err != nil {
		t.Fatalf("amq send: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	req := (*calls)[0]
	got := strings.Join(req.Arg, " ")
	// send is the verb; the resolved root is injected; the rest is forwarded
	// verbatim. --session/--me are consumed for resolution, NOT forwarded.
	for _, want := range []string{"send", "--root", ".agent-mail/issue-96", "--to worker", "--kind todo", "--subject go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("send args missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "--session issue-96") || strings.Contains(got, "--me lead") {
		t.Fatalf("resolution flags must not be forwarded to amq: %s", got)
	}
	// The acting handle reaches amq via AM_ME, and the root via AM_ROOT.
	if !envHas(req.Env, "AM_ME", "lead") {
		t.Fatalf("AM_ME=lead not injected: %v", req.Env)
	}
	if !envHasPrefix(req.Env, "AM_ROOT", ".agent-mail/issue-96") {
		t.Fatalf("AM_ROOT not injected with resolved root: %v", req.Env)
	}
}

func TestAMQDrainResolvesRootAndForwards(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"drain", "--session", "issue-96", "--me", "lead", "--include-body", "--json"})
	})
	if err != nil {
		t.Fatalf("amq drain: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"drain", "--root", ".agent-mail/issue-96", "--include-body", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("drain args missing %q: %s", want, got)
		}
	}
}

func TestAMQReadVerbsResolveRootAndForward(t *testing.T) {
	// An external lead inspecting the bus must hit the SESSION root, not the
	// default .agent-mail — so the read verbs are root-resolving passthroughs too.
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"thread", []string{"thread", "--session", "issue-96", "--me", "lead", "--id", "p2p/lead__qa", "--include-body"},
			[]string{"thread", "--root", ".agent-mail/issue-96", "--id p2p/lead__qa", "--include-body"}},
		{"list", []string{"list", "--session", "issue-96", "--me", "qa"},
			[]string{"list", "--root", ".agent-mail/issue-96"}},
		{"read", []string{"read", "--session", "issue-96", "--me", "qa", "--id", "msg1"},
			[]string{"read", "--root", ".agent-mail/issue-96", "--id msg1"}},
		{"reply", []string{"reply", "--session", "issue-96", "--me", "lead", "--id", "msg1", "--body", "ok"},
			[]string{"reply", "--root", ".agent-mail/issue-96", "--id msg1", "--body ok"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chdir(t, t.TempDir())
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "{}\n")
			if _, _, err := captureOutput(t, func() error { return runAMQ(tc.args) }); err != nil {
				t.Fatalf("amq %s: %v", tc.name, err)
			}
			got := strings.Join((*calls)[0].Arg, " ")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("amq %s args missing %q: %s", tc.name, want, got)
				}
			}
			if strings.Contains(got, "--session issue-96") || strings.Contains(got, "--me ") {
				t.Fatalf("resolution flags must not be forwarded: %s", got)
			}
		})
	}
}

func TestAMQPassthroughRejectsRoot(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"send", "--session", "issue-96", "--root", ".agent-mail", "--to", "worker"})
	})
	if err == nil || !strings.Contains(err.Error(), "do not pass --root") {
		t.Fatalf("send --root should be rejected, got %v", err)
	}
}

func TestAMQWatchStreams(t *testing.T) {
	chdir(t, t.TempDir())
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "")
	var streamed []string
	prev := runAMQStreaming
	runAMQStreaming = func(ctx amqContext, cmd []string) error {
		streamed = cmd
		return nil
	}
	t.Cleanup(func() { runAMQStreaming = prev })

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"watch", "--session", "issue-96", "--me", "lead", "--poll"})
	})
	if err != nil {
		t.Fatalf("amq watch: %v", err)
	}
	got := strings.Join(streamed, " ")
	for _, want := range []string{"watch", "--root", ".agent-mail/issue-96", "--poll"} {
		if !strings.Contains(got, want) {
			t.Fatalf("watch streamed args missing %q: %s", want, got)
		}
	}
}

func TestSplitAMQPassthroughArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantProject string
		wantSession string
		wantMe      string
		wantSet     bool
		wantPass    []string
		wantErr     string
	}{
		{
			name:        "space form consumed, rest forwarded",
			args:        []string{"--session", "work", "--me", "lead", "--to", "worker", "--kind", "todo"},
			wantSession: "work", wantMe: "lead",
			wantPass: []string{"--to", "worker", "--kind", "todo"},
		},
		{
			name:        "equals form and single dash",
			args:        []string{"-session=work", "--me=lead", "--to=worker"},
			wantSession: "work", wantMe: "lead",
			wantPass: []string{"--to=worker"},
		},
		{
			name:        "project sets flag",
			args:        []string{"--project", "/repo", "--to", "x"},
			wantProject: "/repo", wantSet: true,
			wantPass: []string{"--to", "x"},
		},
		{
			name:        "terminator forwards target flags verbatim",
			args:        []string{"--session", "work", "--", "--session", "target", "--to", "codex"},
			wantSession: "work",
			wantPass:    []string{"--session", "target", "--to", "codex"},
		},
		{
			// A passthrough flag whose VALUE equals a wrapper flag name must NOT be
			// re-read as a wrapper flag: parsing stops at the first non-wrapper
			// token (--to), so --subject's value "--session" is forwarded verbatim.
			name:        "passthrough value equal to a wrapper flag is not consumed",
			args:        []string{"--session", "work", "--to", "qa", "--subject", "--session", "--body", "x"},
			wantSession: "work",
			wantPass:    []string{"--to", "qa", "--subject", "--session", "--body", "x"},
		},
		{
			// Likewise, a --root appearing AFTER the leading wrapper run is a
			// passthrough value/flag, forwarded to amq, never a false rejection.
			name:        "root after the leading run is forwarded, not rejected",
			args:        []string{"--session", "work", "--subject", "--root"},
			wantSession: "work",
			wantPass:    []string{"--subject", "--root"},
		},
		{
			name:    "root rejected in the wrapper position",
			args:    []string{"--session", "work", "--root", ".agent-mail"},
			wantErr: "do not pass --root",
		},
		{
			name:    "dangling wrapper value flag",
			args:    []string{"--me", "lead", "--session"},
			wantErr: "needs a value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, session, me, set, pass, err := splitAMQPassthroughArgs("send", tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if project != tc.wantProject || session != tc.wantSession || me != tc.wantMe || set != tc.wantSet {
				t.Fatalf("got project=%q session=%q me=%q set=%v; want %q/%q/%q/%v",
					project, session, me, set, tc.wantProject, tc.wantSession, tc.wantMe, tc.wantSet)
			}
			if strings.Join(pass, " ") != strings.Join(tc.wantPass, " ") {
				t.Fatalf("passthrough = %v, want %v", pass, tc.wantPass)
			}
		})
	}
}

func envHas(env []string, key, val string) bool {
	return containsString(env, key+"="+val)
}

func envHasPrefix(env []string, key, valSubstr string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") && strings.Contains(e, valSubstr) {
			return true
		}
	}
	return false
}
