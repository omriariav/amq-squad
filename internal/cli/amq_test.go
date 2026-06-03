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
		return runAMQ([]string{"send"})
	})
	if err == nil || !strings.Contains(err.Error(), "unknown amq subcommand") {
		t.Fatalf("unknown subcommand error = %v", err)
	}
}
