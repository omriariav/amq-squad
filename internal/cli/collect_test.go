package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunCollectNonEmptyDrainSkipsWatch(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{
		"message one\n",
	})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "cto", "--timeout", "60s", "--include-body"})
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if stdout != "message one\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "drain" {
		t.Fatalf("verbs = %v, want drain only", got)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"drain", ".agent-mail/issue-96", "--me cto", "--include-body"} {
		if !strings.Contains(got, want) {
			t.Fatalf("drain args missing %q: %s", want, got)
		}
	}
}

func TestRunCollectEmptyDrainTimeoutWatchesOnceThenDrains(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{
		"",
		"watch noticed something\n",
		"message after watch\n",
	})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "cto", "--timeout", "30s"})
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if stdout != "message after watch\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "drain,watch,drain" {
		t.Fatalf("verbs = %v, want drain,watch,drain", got)
	}
	watch := strings.Join((*calls)[1].Arg, " ")
	for _, want := range []string{"watch", ".agent-mail/issue-96", "--me cto", "--timeout 30s"} {
		if !strings.Contains(watch, want) {
			t.Fatalf("watch args missing %q: %s", want, watch)
		}
	}
}

func TestRunCollectWatchTimeoutStillDrainsFinal(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	calls := withCollectAMQSeamsFunc(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		switch n {
		case 0:
			return nil, nil
		case 1:
			return nil, errors.New("exit status 4: No new messages (timeout)")
		default:
			return []byte(""), nil
		}
	})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "nobody", "--timeout", "1ms"})
	})
	if err != nil {
		t.Fatalf("collect should tolerate bounded watch timeout: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "drain,watch,drain" {
		t.Fatalf("verbs = %v, want drain,watch,drain", got)
	}
}

func TestRunCollectEmptyDrainZeroTimeoutDoesNotWatch(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{""})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "cto"})
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "drain" {
		t.Fatalf("verbs = %v, want drain only", got)
	}
}

func TestRunCollectBlocksNonOwnerMailboxInProjectTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{"message\n"})

	_, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing collect") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") ||
		!strings.Contains(err.Error(), "--override-boundary --reason") {
		t.Fatalf("collect boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked collect should not call amq, calls = %d", len(*calls))
	}
}

func TestRunCollectBlocksNonOwnerMailboxInNamedProfile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ME", "cto")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{"message\n"})

	_, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--profile", "review", "--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing collect") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") {
		t.Fatalf("named-profile collect boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked named-profile collect should not call amq, calls = %d", len(*calls))
	}
}

func TestRunCollectInfersNamedProfileFromResolvedRoot(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ME", "cto")
	calls := withCollectAMQSeams(t, amqEnv{Root: filepath.Join(".agent-mail", "review", "issue-96"), BaseRoot: ".agent-mail"}, []string{"message\n"})

	_, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil ||
		!strings.Contains(err.Error(), "refusing collect") ||
		!strings.Contains(err.Error(), "lead-owned mailbox") {
		t.Fatalf("root-inferred named-profile collect boundary error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("blocked root-inferred named-profile collect should not call amq, calls = %d", len(*calls))
	}
}

func TestRunCollectOverrideRequiresReason(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{"message\n"})

	_, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--override-boundary"})
	})
	if err == nil || !strings.Contains(err.Error(), "collect --override-boundary requires --reason") {
		t.Fatalf("collect override error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("missing-reason collect should not call amq, calls = %d", len(*calls))
	}
}

func TestRunCollectOverrideWritesAuditAndExecutes(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{"message\n"})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--override-boundary", "--reason", "recover child report", "--include-body"})
	})
	if err != nil {
		t.Fatalf("collect override should pass: %v", err)
	}
	if stdout != "message\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if len(*calls) != 1 {
		t.Fatalf("collect override calls = %d, want 1", len(*calls))
	}
	auditPath := filepath.Join(dir, team.DirName, "boundary-audit", "issue-96.jsonl")
	b, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"subcommand":"collect"`, `"actor":"cto"`, `"target":"qa"`, `"reason":"recover child report"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("audit missing %q:\n%s", want, string(b))
		}
	}
}

func TestRunCollectValidations(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing session", []string{"--me", "cto"}, "requires --session"},
		{"missing me", []string{"--session", "issue-96"}, "requires --me"},
		{"bad timeout", []string{"--session", "issue-96", "--me", "cto", "--timeout", "soon"}, "invalid --timeout"},
		{"negative timeout", []string{"--session", "issue-96", "--me", "cto", "--timeout", "-1s"}, "non-negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error { return runCollect(tc.args) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func withCollectAMQSeams(t *testing.T, env amqEnv, outputs []string) *[]amqCommandRequest {
	t.Helper()
	return withCollectAMQSeamsFunc(t, env, func(req amqCommandRequest, n int) ([]byte, error) {
		if len(outputs) == 0 {
			return nil, nil
		}
		out := outputs[0]
		outputs = outputs[1:]
		return []byte(out), nil
	})
}

func withCollectAMQSeamsFunc(t *testing.T, env amqEnv, run func(amqCommandRequest, int) ([]byte, error)) *[]amqCommandRequest {
	t.Helper()
	var calls []amqCommandRequest
	prevEnv := resolveAMQEnvForAMQCommand
	prevRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		got := env
		if strings.TrimSpace(rootFlag) != "" {
			got.Root = rootFlag
		} else {
			got.Root = strings.ReplaceAll(got.Root, "{session}", session)
		}
		got.SessionName = session
		got.Me = handle
		if got.BaseRoot == "" {
			got.BaseRoot = ".agent-mail"
		}
		return got, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		calls = append(calls, req)
		if run == nil {
			return nil, nil
		}
		return run(req, len(calls)-1)
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	return &calls
}

func collectCallVerbs(calls []amqCommandRequest) []string {
	var verbs []string
	for _, c := range calls {
		if len(c.Arg) > 0 {
			verbs = append(verbs, c.Arg[0])
		}
	}
	return verbs
}
