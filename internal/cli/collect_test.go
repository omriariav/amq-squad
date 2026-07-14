package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunCollectNonEmptyDrainSkipsWatch(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	root := filepath.Join(".agent-mail", "issue-96")
	seedCollectMessage(t, root, "cto", "m1", "message one")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "cto", "--timeout", "60s", "--include-body"})
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, want := range []string{"[AMQ] 1 new message(s) for cto", "ID: m1", "Body:\nmessage one"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "watch noticed") {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "read" {
		t.Fatalf("verbs = %v, want read only", got)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"read", ".agent-mail/issue-96", "--me cto", "--id m1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("read args missing %q: %s", want, got)
		}
	}
}

func TestRunCollectEmptyDrainTimeoutWatchesOnceThenDrains(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	root := filepath.Join(".agent-mail", "issue-96")
	calls := withCollectAMQSeamsFunc(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		if len(req.Arg) > 0 && req.Arg[0] == "watch" {
			seedCollectMessage(t, root, "cto", "after-watch", "message after watch")
			return []byte("watch noticed something\n"), nil
		}
		return nil, nil
	})

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "cto", "--timeout", "30s"})
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !strings.Contains(stdout, "ID: after-watch") || strings.Contains(stdout, "Body:") {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "watch,read" {
		t.Fatalf("verbs = %v, want watch,read", got)
	}
	watch := strings.Join((*calls)[0].Arg, " ")
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
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "watch" {
		t.Fatalf("verbs = %v, want watch", got)
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
	if got := collectCallVerbs(*calls); strings.Join(got, ",") != "" {
		t.Fatalf("verbs = %v, want none", got)
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
	root := filepath.Join(dir, ".agent-mail", "review", "issue-96")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", root)
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
	root := filepath.Join(".agent-mail", "issue-96")
	seedCollectMessage(t, root, "qa", "override-msg", "message")
	calls := withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--override-boundary", "--reason", "recover child report", "--include-body"})
	})
	if err != nil {
		t.Fatalf("collect override should pass: %v", err)
	}
	if !strings.Contains(stdout, "ID: override-msg") || !strings.Contains(stdout, "Body:\nmessage") {
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

func TestCollectReplaysJournaledMessageAfterInterruptedBeforeAck(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	root := filepath.Join(".agent-mail", "issue-96")
	seedCollectMessage(t, root, "cto", "journal-before-ack", "body survives before ack")
	ctx := collectTestContext(dir, root, "cto")
	calls := withCollectAMQSeamsFunc(t, amqEnv{Root: root, BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		return nil, errors.New("simulated interruption before ack journal-before-ack")
	})

	var first bytes.Buffer
	if err := executeCollect(&first, ctx, 0, true); err == nil {
		t.Fatal("first collect should fail before ack")
	}
	if first.Len() != 0 {
		t.Fatalf("interrupted collect should not output before ack, got %q", first.String())
	}
	journal := newCollectJournal(ctx)
	if _, err := os.Stat(journal.pendingPath("journal-before-ack")); err != nil {
		t.Fatalf("pending journal should exist before ack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "cto", "inbox", "new", "journal-before-ack.md")); err != nil {
		t.Fatalf("message should still be unread after before-ack interruption: %v", err)
	}

	*calls = nil
	withCollectAMQSeamsFunc(t, amqEnv{Root: root, BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		moveCollectMessageToCur(t, root, "cto", "journal-before-ack")
		return nil, nil
	})
	var replay bytes.Buffer
	if err := executeCollect(&replay, ctx, 0, true); err != nil {
		t.Fatalf("replay collect: %v", err)
	}
	if !strings.Contains(replay.String(), "body survives before ack") {
		t.Fatalf("replay lost body:\n%s", replay.String())
	}
	if _, err := os.Stat(journal.pendingPath("journal-before-ack")); !os.IsNotExist(err) {
		t.Fatalf("pending journal should be cleared after replay, err=%v", err)
	}
	if _, err := os.Stat(journal.deliveredPath("journal-before-ack")); err != nil {
		t.Fatalf("delivered journal should exist after replay: %v", err)
	}
}

func TestCollectReplaysJournaledMessageAfterInterruptedBetweenAckAndOutput(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	root := filepath.Join(".agent-mail", "issue-96")
	seedCollectMessage(t, root, "cto", "ack-before-output", "body survives after ack")
	ctx := collectTestContext(dir, root, "cto")
	withCollectAMQSeamsFunc(t, amqEnv{Root: root, BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		moveCollectMessageToCur(t, root, "cto", "ack-before-output")
		return nil, nil
	})

	err := executeCollect(errorWriter{}, ctx, 0, true)
	if err == nil {
		t.Fatal("first collect should fail while writing output")
	}
	journal := newCollectJournal(ctx)
	if _, err := os.Stat(journal.pendingPath("ack-before-output")); err != nil {
		t.Fatalf("pending journal should remain after output interruption: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "cto", "inbox", "cur", "ack-before-output.md")); err != nil {
		t.Fatalf("message should have been acked to cur: %v", err)
	}

	withCollectAMQSeamsFunc(t, amqEnv{Root: root, BaseRoot: ".agent-mail"}, func(req amqCommandRequest, n int) ([]byte, error) {
		return nil, fmt.Errorf("message not found: ack-before-output")
	})
	var replay bytes.Buffer
	if err := executeCollect(&replay, ctx, 0, true); err != nil {
		t.Fatalf("replay collect should tolerate already-acked message: %v", err)
	}
	if !strings.Contains(replay.String(), "body survives after ack") {
		t.Fatalf("replay lost body:\n%s", replay.String())
	}
	if _, err := os.Stat(journal.pendingPath("ack-before-output")); !os.IsNotExist(err) {
		t.Fatalf("pending journal should be cleared after replay, err=%v", err)
	}
}

func TestCollectJournalScopesByProfileSessionAndRecipient(t *testing.T) {
	dir := t.TempDir()
	defaultCtx := collectTestContext(dir, filepath.Join(".agent-mail", "issue-96"), "cto")
	reviewCtx := collectTestContext(dir, filepath.Join(".agent-mail", "review", "issue-96"), "cto")
	reviewCtx.Profile = "review"
	otherRecipient := collectTestContext(dir, filepath.Join(".agent-mail", "issue-96"), "qa")

	paths := []string{
		newCollectJournal(defaultCtx).Root,
		newCollectJournal(reviewCtx).Root,
		newCollectJournal(otherRecipient).Root,
	}
	if paths[0] == paths[1] || paths[0] == paths[2] || paths[1] == paths[2] {
		t.Fatalf("journal paths must be profile/session/recipient scoped: %+v", paths)
	}
	for _, want := range []string{
		filepath.Join(".amq-squad", "collect-journal", "default", "issue-96", "cto"),
		filepath.Join(".amq-squad", "collect-journal", "review", "issue-96", "cto"),
		filepath.Join(".amq-squad", "collect-journal", "default", "issue-96", "qa"),
	} {
		found := false
		for _, got := range paths {
			if strings.HasSuffix(got, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("journal paths %+v missing suffix %s", paths, want)
		}
	}
}

func TestCollectJournalCleansOldDeliveredEntries(t *testing.T) {
	dir := t.TempDir()
	ctx := collectTestContext(dir, filepath.Join(".agent-mail", "issue-96"), "cto")
	journal := newCollectJournal(ctx)
	if err := journal.ensure(); err != nil {
		t.Fatalf("ensure journal: %v", err)
	}
	now := time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC)
	old := collectJournalEntry{ID: "old", Body: "old", Created: now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano), JournaledAt: now.Add(-8 * 24 * time.Hour), DeliveredAt: now.Add(-8 * 24 * time.Hour)}
	fresh := collectJournalEntry{ID: "fresh", Body: "fresh", Created: now.Format(time.RFC3339Nano), JournaledAt: now, DeliveredAt: now}
	if err := writeCollectJournalEntryAtomic(journal.deliveredPath(old.ID), old); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := writeCollectJournalEntryAtomic(journal.deliveredPath(fresh.ID), fresh); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	if err := journal.cleanupDelivered(now); err != nil {
		t.Fatalf("cleanup delivered: %v", err)
	}
	if _, err := os.Stat(journal.deliveredPath(old.ID)); !os.IsNotExist(err) {
		t.Fatalf("old delivered entry should be removed, err=%v", err)
	}
	if _, err := os.Stat(journal.deliveredPath(fresh.ID)); err != nil {
		t.Fatalf("fresh delivered entry should remain: %v", err)
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

func seedCollectMessage(t *testing.T, root, owner, id, body string) {
	t.Helper()
	dir := filepath.Join(root, "agents", owner, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := fmt.Sprintf(`---json
{"schema":1,"id":%q,"from":"worker","to":[%q],"thread":"p2p/cto__worker","subject":"hello","created":"2026-07-06T10:00:00Z","priority":"normal","kind":"status"}
---
%s
`, id, owner, body)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func moveCollectMessageToCur(t *testing.T, root, owner, id string) {
	t.Helper()
	newPath := filepath.Join(root, "agents", owner, "inbox", "new", id+".md")
	curDir := filepath.Join(root, "agents", owner, "inbox", "cur")
	if err := os.MkdirAll(curDir, 0o755); err != nil {
		t.Fatal(err)
	}
	curPath := filepath.Join(curDir, id+".md")
	if err := os.Rename(newPath, curPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func collectTestContext(projectDir, root, me string) amqContext {
	return amqContext{
		ProjectDir: projectDir,
		Profile:    team.DefaultProfile,
		Env: amqEnv{
			Root:        root,
			BaseRoot:    ".agent-mail",
			SessionName: "issue-96",
			Me:          me,
		},
		Root: root,
		Me:   me,
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
