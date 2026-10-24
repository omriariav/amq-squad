package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Tests of the 2b-2 milestone B fix commit 1 (the first pass on e2a59176
// and the briefs ruling 18 and 19 items) at the verbs.

// scopedSelection resolves a store selection the way the verbs do.
func scopedSelection(t *testing.T, args []string) storeSelection {
	t.Helper()
	f := newStoreFlags("test")
	if err := parseFlags(f.fs, args); err != nil {
		t.Fatal(err)
	}
	sel, err := f.selection(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sel.release)
	return sel
}

type sentReply struct{ to, subject, body string }

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// leadDecision delivers one widening message from `from` to the scheduler's
// request handler (the scheduler process's per-tick drain) and returns the
// scheduler's replies.
func leadDecision(t *testing.T, sel storeSelection, project, from string, req schedproc.WidenRequest) []sentReply {
	t.Helper()
	body, _ := json.Marshal(req)
	msgs := []schedproc.Message{{ID: "m-" + from, From: from, To: []string{schedproc.Handle}, Subject: "widen " + req.Task, Kind: "todo", Body: string(body)}}
	drained := false
	var sent []sentReply
	sender := schedproc.Sender{AMQ: "amq", Root: sel.root, Run: func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		switch args[0] {
		case "drain":
			if drained {
				return nil, nil
			}
			drained = true
			return json.Marshal(map[string]any{"drained": msgs, "count": len(msgs)})
		case "send":
			sent = append(sent, sentReply{to: argAfter(args, "--to"), subject: argAfter(args, "--subject"), body: argAfter(args, "--body")})
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected amq %s", args[0])
	}}
	handleSchedulerRequests(sel, sender, project, t.Logf)
	return sent
}

// replyData is the decision JSON of a scheduler reply.
func replyData(t *testing.T, r sentReply) taskWidenData {
	t.Helper()
	var d taskWidenData
	i := strings.IndexByte(r.body, '{')
	if i < 0 || json.Unmarshal([]byte(r.body[i:]), &d) != nil {
		t.Fatalf("reply %q carries no decision", r.subject)
	}
	return d
}

// R1 (task_widen.go:62): the widening was authorized on the caller's --me,
// so a holder ran it with env -u AM_ME and --me LEAD. Now the verb takes no
// --me and only sends the lead's AMQ message; the scheduler applies it only
// when the message is from the lead of its launch record and the task is
// its squad's.
func TestTaskWidenIsAcceptedOnlyFromTheSchedulersLead(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"a.go"}, ProjectPath: proj}, time.Now())
	var sentArgs []string
	prev := taskWidenSend
	taskWidenSend = func(_ string, args []string) ([]byte, error) { sentArgs = args; return nil, nil }
	t.Cleanup(func() { taskWidenSend = prev })
	if _, _, err := captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--me", "lead", "--files", "b.go", "--reason", "r"}, os.Stdout)
	}); err == nil || !strings.Contains(err.Error(), "takes no --me") {
		t.Fatalf("the verb took --me: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--files", "b.go", "--reason", "r"}, os.Stdout)
	}); err != nil {
		t.Fatal(err)
	}
	req, ok := schedproc.ParseWidenRequest(argAfter(sentArgs, "--body"))
	if argAfter(sentArgs, "--to") != schedproc.Handle || !ok || req.Task != tk.ID {
		t.Fatalf("the verb sent %q", sentArgs)
	}
	if got, _ := s.Get(tk.ID); len(got.DeclaredFiles) != 1 {
		t.Fatalf("the verb widened the task itself: %+v", got.DeclaredFiles)
	}
	sel := storeSelection{store: s, root: root, release: func() {}}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, ProjectRoot: proj}); err != nil {
		t.Fatal(err)
	}
	// A seat's message naming the lead in nothing but its claim.
	replies := leadDecision(t, sel, proj, "s1", req)
	if len(replies) != 1 || replies[0].to != "s1" || !strings.Contains(replies[0].subject, "not_lead") {
		t.Fatalf("a seat's widening: %+v", replies)
	}
	if got, _ := s.Get(tk.ID); len(got.DeclaredFiles) != 1 {
		t.Fatalf("a seat widened the task: %+v", got.DeclaredFiles)
	}
	// Another squad's task, from the lead.
	tk2, _ := s.Add(task.AddInput{Title: "y", Class: "implement", DeclaredFiles: []string{"c.go"}, ProjectPath: other}, time.Now())
	req2 := req
	req2.Task, req2.Files = tk2.ID, []string{"d.go"}
	if replies := leadDecision(t, sel, proj, "cto", req2); len(replies) != 1 || !strings.Contains(replies[0].subject, "not_squad") {
		t.Fatalf("another squad's task: %+v", replies)
	}
	// The lead's message.
	if replies := leadDecision(t, sel, proj, "cto", req); len(replies) != 1 || replies[0].to != "cto" || !strings.HasSuffix(replies[0].subject, ": applied") {
		t.Fatalf("the lead's widening: %+v", replies)
	}
	if got, _ := s.Get(tk.ID); len(got.DeclaredFiles) != 2 || got.ScopeChanges[0].Actor != "cto" {
		t.Fatalf("after the lead's widening: %+v", got)
	}
}

// writeOwnerKeys writes the core's issuer key and the owner's trust store
// under HOME/.amq-squad (what amq-squad remote enrolls).
func writeOwnerKeys(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".amq-squad")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o700)
	pub, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	if err := os.WriteFile(filepath.Join(dir, "issuer-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	trust, _ := json.Marshal(operatorauth.AuthorizationTrustStore{SchemaVersion: 1, Keys: []operatorauth.AuthorizationTrustKey{{
		KeyID: operatorauth.KeyIDForEd25519(pub), Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}}})
	if err := os.WriteFile(filepath.Join(dir, "authorization-trust.json"), trust, 0o600); err != nil {
		t.Fatal(err)
	}
}

// ownerProcess sets the seams of the owner proof: this process is pid 100,
// its chain 100 -> 90 -> 80 -> 1 (stamps "s<pid>"), and seatPanes the seat
// panes' shells; chainBroken makes the hop at 90 unreadable.
func ownerProcess(t *testing.T, seatPanesBy map[int]string, chainBroken bool) {
	t.Helper()
	prevPanes, prevReach, prevSelf, prevStart := seatPanes, chainReaches, selfPID, selfStart
	t.Cleanup(func() { seatPanes, chainReaches, selfPID, selfStart = prevPanes, prevReach, prevSelf, prevStart })
	seatPanes = func() ([]panechain.Pane, error) {
		var out []panechain.Pane
		for pid, agent := range seatPanesBy {
			out = append(out, panechain.Pane{Shell: pid, Start: fmt.Sprintf("s%d", pid), Agent: agent})
		}
		return out, nil
	}
	selfPID = func() int { return 100 }
	selfStart = func(pid int) (string, bool) { return fmt.Sprintf("s%d", pid), true }
	chainReaches = func(pid int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) {
		for p := pid; p > 1; p = map[int]int{100: 90, 90: 80, 80: 1}[p] {
			for i := range panes {
				if panes[i].Shell == p {
					return &panes[i], true
				}
			}
			if chainBroken && p == 90 {
				return nil, false
			}
		}
		return nil, true
	}
	for _, name := range seatMarkers {
		t.Setenv(name, "")
	}
}

// quarantinedSeat sets up a seat whose global config change quarantined it
// at its done report (the tree kept on the ledger), and returns the store.
func quarantinedSeat(t *testing.T) (string, task.Store, task.Task) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeOwnerKeys(t, home)
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := seatRepo(t, root)
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	s.Claim(tk.ID, "s1", nil, time.Hour, time.Now())
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// w\n")
	s.Done(tk.ID, "s1", "ok", "", time.Now())
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1,"hooks":{"PreToolUse":[]}}`)
	if _, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", root}, os.Stdout) }); ExitCode(err) != ExitCommitRefused {
		t.Fatalf("seat commit: %v", err)
	}
	return root, s, tk
}

// R1 (seat_commit.go:100) and the reviewer's pre-clearance: seat clear let
// seat B clear seat A's quarantine. It runs only from the owner's terminal:
// it takes no --me, refuses when AM_ME or a seat or lead marker is set or
// when the process descends from a seat pane, and writes a clear record
// signed with the core's issuer key, the only clear the commit step accepts.
func TestSeatClearRunsOnlyFromTheOwnersTerminal(t *testing.T) {
	root, s, tk := quarantinedSeat(t)
	clear := func(args ...string) error {
		_, _, err := captureOutput(t, func() error {
			return runSeatClear(append([]string{"--seat", "s1", "--root", root}, args...), os.Stdout)
		})
		return err
	}
	ownerProcess(t, map[int]string{80: "lead"}, false)
	if err := clear(); !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "seat lead's pane") {
		t.Fatalf("a process under the lead's pane cleared: %v", err)
	}
	ownerProcess(t, nil, false)
	for _, marker := range []string{"AM_ME", "AMQ_SQUAD_SEAT", "AMQ_SQUAD_LEAD"} {
		t.Setenv(marker, "s2")
		if err := clear(); !errors.Is(err, errNotOwner) {
			t.Fatalf("seat clear ran with %s set: %v", marker, err)
		}
		t.Setenv(marker, "")
	}
	if err := clear("--me", "user"); err == nil || !strings.Contains(err.Error(), "takes no --me") {
		t.Fatalf("seat clear took --me: %v", err)
	}
	ownerProcess(t, map[int]string{70: "lead"}, true)
	if err := clear(); !errors.Is(err, errNotOwner) {
		t.Fatalf("an unreadable process chain passed: %v", err)
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateBlocked {
		t.Fatalf("a refused clear changed the task: %+v", got)
	}
	// The owner's terminal: not under any seat pane.
	ownerProcess(t, map[int]string{70: "lead"}, false)
	if err := clear(); err != nil {
		t.Fatalf("the owner's clear: %v", err)
	}
	l, _ := seatcommit.ReadLedger(root, "s1")
	if l.Quarantine == nil || l.Quarantine.Clear == nil || !l.Quarantine.Clear.Verified("s1", l.Quarantine, ownerTrust{}) {
		t.Fatalf("no signed clear record: %+v", l.Quarantine)
	}
	if out, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", root}, os.Stdout) }); err != nil {
		t.Fatalf("seat commit after the owner's clear: %v\n%s", err, out)
	}
}

// A clear written into the ledger without the owner's signature (a seat
// editing the core's file) never lifts the quarantine.
func TestUnsignedClearKeepsTheSeatQuarantined(t *testing.T) {
	root, s, tk := quarantinedSeat(t)
	// The config change is undone, so only the forged clear stands between
	// the kept tree and the push.
	home, _ := os.UserHomeDir()
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`)
	p := filepath.Join(seatcommit.LedgerDir(root), "s1.json")
	raw, _ := os.ReadFile(p)
	var l map[string]any
	json.Unmarshal(raw, &l)
	q := l["quarantine"].(map[string]any)
	q["cleared_at"], q["cleared_by"] = time.Now().UTC().Format(time.RFC3339Nano), "owner"
	raw, _ = json.Marshal(l)
	os.WriteFile(p, raw, 0o600)
	s.ClearQuarantine("s1")
	s.Rearm(tk.ID, time.Hour, time.Now())
	_, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", root}, os.Stdout) })
	if got, _ := s.Get(tk.ID); ExitCode(err) != ExitCommitRefused || got.BlockReason != seatcommit.ReasonClearUnsigned {
		t.Fatalf("an unsigned clear was accepted: %v %+v", err, got)
	}
}

// P1 (seatexec_unix.go:209): the hook's context call during the run checks
// the seat's Claude config dir against the launch digest in the core's
// staged record; a change is claude_config_changed and quarantines the seat.
func TestContextCallQuarantinesAChangedClaudeConfigDir(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	wt, _ := filepath.EvalSymlinks(t.TempDir())
	dir := claudeseat.Dir(root, "s1", "launch-1")
	w, err := claudeseat.Write(dir, "dontAsk", wt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, "s1")
	os.MkdirAll(staged, 0o700)
	rec := launch.Record{Handle: "s1", Root: root, Session: "s", Runtime: "claude", Worktree: wt, Branch: "seat/s1", LaunchID: "launch-1", ClaudeConfig: w.Digest, PermissionMode: "dontAsk"}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	id := hookIdentity{Handle: "s1", Root: root, AgentDir: filepath.Join(root, "agents", "s1")}
	if d := buildHookContext(id, "", time.Now()); d.ConfigChanged != nil {
		t.Fatalf("an unchanged dir reported: %+v", d.ConfigChanged)
	}
	mustWrite(t, filepath.Join(dir, "settings.json"), `{"permissions":{"defaultMode":"dontAsk","allow":["Bash(*)"]}}`)
	d := buildHookContext(id, "", time.Now())
	if d.ConfigChanged == nil || d.ConfigChanged.Code != claudeseat.ConfigChanged || !d.ConfigChanged.Quarantined {
		t.Fatalf("the change was not reported and quarantined: %+v", d.ConfigChanged)
	}
	qs, _ := task.Quarantined(task.DirFor(root))
	if len(qs) != 1 || qs[0].Handle != "s1" || qs[0].Reason != claudeseat.ConfigChanged {
		t.Fatalf("store quarantine: %+v", qs)
	}
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "y", Class: "implement"}, time.Now())
	if _, err := s.Grant(tk.ID, "s1", nil, time.Minute, time.Now()); !errors.Is(err, task.ErrQuarantined) {
		t.Fatalf("the seat got a lease: %v", err)
	}
}

// schedulerFixture is a squad for runSchedulerRun with a fake launcher.
func schedulerFixture(t *testing.T) (home, root, seatsFile string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(proj, ".agent-mail", "s1")
	os.MkdirAll(filepath.Join(root, "agents"), 0o700)
	mustWrite(t, filepath.Join(proj, "roles", "impl.md"), "# Role: implementer\n")
	raw, _ := json.Marshal(map[string]any{"project_root": proj, "session": "s1", "seats": []map[string]any{
		{"handle": "impl-1", "runtime": "claude", "model": "sonnet", "effort": "medium", "role": "implementer", "role_file": filepath.Join(proj, "roles", "impl.md"), "worktree": filepath.Join(proj, "wt-impl-1")}}})
	seatsFile = filepath.Join(proj, "seats.json")
	mustWrite(t, seatsFile, string(raw))
	prevL, prevS, prevStarted := schedulerAMQLauncher, schedulerSendRun, schedulerStarted
	t.Cleanup(func() { schedulerAMQLauncher, schedulerSendRun, schedulerStarted = prevL, prevS, prevStarted })
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		return &cliFakeLauncher{sl: sl}
	}
	schedulerSendRun = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	return home, root, seatsFile
}

// P1 (scheduler_run.go:139 before process.go:81): the preflight baseline
// was refreshed before the duplicate-process check, so a rejected second
// scheduler erased the evidence of a config change. Admission comes first,
// and an admitted process fills a missing copy but never refreshes one: the
// baseline persists across restarts.
func TestRejectedSchedulerNeverTouchesThePreflightBaseline(t *testing.T) {
	home, root, seatsFile := schedulerFixture(t)
	copyPath := filepath.Join(seatexec.PreflightDir(home), "claude-settings.json")
	mustWrite(t, copyPath, `{"baseline":true}`)
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"changed":true}`)
	os.MkdirAll(schedproc.Dir(root), 0o700)
	lock, ok, err := flock.TryExclusive(filepath.Join(schedproc.Dir(root), "run.lock"), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead"}, os.Stdout)
	})
	lock.Close()
	if !errors.Is(err, schedproc.ErrRunning) {
		t.Fatalf("the duplicate ran: %v", err)
	}
	if raw, _ := os.ReadFile(copyPath); string(raw) != `{"baseline":true}` {
		t.Fatalf("a rejected scheduler refreshed the baseline to %s", raw)
	}
	// An admitted restart keeps it too.
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	prev := schedulerStarted
	schedulerStarted = func(*scheduler.Scheduler) { close(started) }
	t.Cleanup(func() { schedulerStarted = prev })
	done := make(chan error, 1)
	go func() {
		_, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(copyPath); string(raw) != `{"baseline":true}` {
		t.Fatalf("a restart refreshed the baseline to %s", raw)
	}
	if rec, err := schedproc.ReadLaunchRecord(root); err != nil || rec.Lead != "lead" {
		t.Fatalf("the admitted process's launch record: %+v %v", rec, err)
	}
}

// The reviewer's pre-clearance through the verb: the PR gh answers for the
// seat branch is never bound when the store binds that number to another
// task (pr_bound_elsewhere), and the task is not completed.
func TestSeatCommitRefusesAPRBoundToAnotherTask(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := seatRepo(t, root)
	gh := filepath.Join(t.TempDir(), "gh")
	mustWrite(t, gh, "#!/bin/sh\nif [ \"$2\" = list ]; then echo '[]'; exit 0; fi\necho https://github.com/o/r/pull/7\n")
	os.Chmod(gh, 0o755)
	prev := seatCommitGh
	seatCommitGh = gh
	t.Cleanup(func() { seatCommitGh = prev })
	s, _ := task.Open(root)
	old, _ := s.Add(task.AddInput{Title: "old", Class: "implement", DeclaredFiles: []string{"z/"}}, time.Now())
	s.Claim(old.ID, "s9", nil, time.Hour, time.Now())
	s.Done(old.ID, "s9", "ok", "", time.Now())
	if _, err := s.CompleteBound(old.ID, 7, "abc", time.Now()); err != nil {
		t.Fatal(err)
	}
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	s.Claim(tk.ID, "s1", nil, time.Hour, time.Now())
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// pr\n")
	s.Done(tk.ID, "s1", "ok", "", time.Now())
	_, _, err := captureOutput(t, func() error {
		return runSeatCommit([]string{"--seat", "s1", "--root", root, "--open-pr"}, os.Stdout)
	})
	got, _ := s.Get(tk.ID)
	if ExitCode(err) != ExitCommitRefused || got.BlockReason != seatcommit.ReasonPRBoundElsewhere || got.PRNumber != 0 {
		t.Fatalf("a PR bound to %s was reused: %v %+v", old.ID, err, got)
	}
}

// writeTestRegistry writes the owner's registry with the verified entry the
// scheduler tests route to (2b-4's router reads it).
func writeTestRegistry(t *testing.T, home string) {
	t.Helper()
	e := registry.Entry{Runtime: "claude", Provider: "anthropic", Model: "sonnet", Effort: "medium", Verified: true,
		State: registry.StateReady, PrivacyClass: "vendor", ConcurrencyPool: "claude-subscription", ConcurrencyCap: 6, CostTier: "low"}
	if err := registry.Write(registry.Path(home), registry.Registry{Schema: 1, Entries: []registry.Entry{e}}); err != nil {
		t.Fatal(err)
	}
}
