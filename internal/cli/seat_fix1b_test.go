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
	"io"
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

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// leadServer starts the scheduler's decision socket for sel with the pane
// seams: the sender (this test process) reaches the lead's pane when
// fromLead, no pane otherwise. It records the socket in the launch record.
func leadServer(t *testing.T, sel storeSelection, project string, fromLead bool) *schedproc.DecisionServer {
	t.Helper()
	lead := []panechain.Pane{{Shell: 80, Start: "s80", Agent: "lead"}}
	s, err := schedproc.ServeDecisions(schedproc.DecideOptions{Root: sel.root, Lead: "lead",
		Apply: func(l string, req schedproc.WidenRequest) schedproc.DecideReply {
			return widenDecision(sel, project, l, req)
		},
		LeadPane: func(string, string) (panechain.Pane, error) { return lead[0], nil },
		Reaches: func(int, string, []panechain.Pane) (*panechain.Pane, bool) {
			if fromLead {
				return &lead[0], true
			}
			return nil, true
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := schedproc.WriteLaunchRecord(sel.root, schedproc.LaunchRecord{Lead: "lead", Root: sel.root, ProjectRoot: project, DecisionSocket: s.Path()}); err != nil {
		t.Fatal(err)
	}
	return s
}

// leadDecision sends one widening over the decision socket as the lead.
func leadDecision(t *testing.T, sel storeSelection, project string, req schedproc.WidenRequest) schedproc.DecideReply {
	t.Helper()
	s := leadServer(t, sel, project, true)
	reply, err := schedproc.SendDecision(s.Path(), req)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return reply
}

// replyData is the decision JSON of a scheduler reply.
func replyData(t *testing.T, r schedproc.DecideReply) taskWidenData {
	t.Helper()
	var d taskWidenData
	if json.Unmarshal(r.Data, &d) != nil {
		t.Fatalf("reply %s carries no decision", r.Code)
	}
	return d
}

// R4 (task_widen.go:154, the second pass): the scheduler trusted the AMQ
// message's sender, which AMQ derives from the caller's AM_ME in a
// same-UID writable mailbox. The verb takes no --me, sends the decision over
// the scheduler's socket, and the scheduler applies it only when the
// sending process descends from the lead's pane and the task is its squad's;
// the AMQ message is the record only.
func TestTaskWidenIsAcceptedOnlyFromTheLeadsPane(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"a.go"}, ProjectPath: proj}, time.Now())
	var sentArgs []string
	prev := taskWidenSend
	taskWidenSend = func(_ string, args []string) ([]byte, error) { sentArgs = args; return nil, nil }
	t.Cleanup(func() { taskWidenSend = prev })
	widen := func(args ...string) error {
		_, _, err := captureOutput(t, func() error {
			return runTaskWiden(append([]string{tk.ID, "--root", root, "--files", "b.go", "--reason", "r"}, args...), os.Stdout)
		})
		return err
	}
	if err := widen("--me", "lead"); err == nil || !strings.Contains(err.Error(), "takes no --me") {
		t.Fatalf("the verb took --me: %v", err)
	}
	if err := widen(); err == nil || !strings.Contains(err.Error(), "no running scheduler") {
		t.Fatalf("a widen without the scheduler: %v", err)
	}
	sel := storeSelection{store: s, root: root, release: func() {}}
	// A seat's process, claiming to be the lead in its environment.
	srv := leadServer(t, sel, proj, false)
	t.Setenv("AM_ME", "lead")
	if err := widen(); ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedNotLead) {
		t.Fatalf("a seat's widening: %v", err)
	}
	if got, _ := s.Get(tk.ID); len(got.DeclaredFiles) != 1 {
		t.Fatalf("a seat widened the task: %+v", got.DeclaredFiles)
	}
	srv.Close()
	t.Setenv("AM_ME", "")
	// Another squad's task, from the lead's pane.
	tk2, _ := s.Add(task.AddInput{Title: "y", Class: "implement", DeclaredFiles: []string{"c.go"}, ProjectPath: other}, time.Now())
	if r := leadDecision(t, sel, proj, schedproc.WidenRequest{Kind: schedproc.WidenRequestKind, Task: tk2.ID, Files: []string{"d.go"}, Reason: "r"}); r.Code != "not_squad" {
		t.Fatalf("another squad's task: %+v", r)
	}
	// The lead's pane, through the verb.
	leadServer(t, sel, proj, true)
	if err := widen(); err != nil {
		t.Fatalf("the lead's widening: %v", err)
	}
	if got, _ := s.Get(tk.ID); len(got.DeclaredFiles) != 2 || got.ScopeChanges[0].Actor != "lead" {
		t.Fatalf("after the lead's widening: %+v", got)
	}
	if argAfter(sentArgs, "--to") != schedproc.Handle || !strings.Contains(argAfter(sentArgs, "--subject"), "(record)") {
		t.Fatalf("no AMQ record of the decision: %q", sentArgs)
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
	prevPanes, prevReach, prevSelf, prevStart, prevTTY, prevDev := seatPanes, chainReaches, selfPID, selfStart, openOwnerTTY, ttyDevice
	t.Cleanup(func() {
		seatPanes, chainReaches, selfPID, selfStart, openOwnerTTY, ttyDevice = prevPanes, prevReach, prevSelf, prevStart, prevTTY, prevDev
	})
	seatPanes = func() ([]panechain.Pane, error) {
		var out []panechain.Pane
		for pid, agent := range seatPanesBy {
			out = append(out, panechain.Pane{Shell: pid, Start: fmt.Sprintf("s%d", pid), Agent: agent, TTY: fmt.Sprintf("/dev/pane%d", pid)})
		}
		return out, nil
	}
	// The owner's terminal: device 7, the confirmation "s1"; a pane's tty is
	// device 1000+pid.
	openOwnerTTY = func() (ownerTTY, error) { return &fakeTTY{dev: 7, in: strings.NewReader("s1\n")}, nil }
	ttyDevice = func(path string) (uint64, error) {
		var pid int
		fmt.Sscanf(path, "/dev/pane%d", &pid)
		return uint64(1000 + pid), nil
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
	if err := clear(); !errors.Is(err, errOwnerUnknown) {
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
	if _, err := s.CompleteBound(old.ID, "o/r", 7, strings.Repeat("a", 40), time.Now()); err != nil {
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

// fakeTTY is a controlling terminal with scripted input.
type fakeTTY struct {
	dev uint64
	in  *strings.Reader
	out strings.Builder
}

func (f *fakeTTY) Read(b []byte) (int, error)  { return f.in.Read(b) }
func (f *fakeTTY) Write(b []byte) (int, error) { return f.out.Write(b) }
func (f *fakeTTY) Close() error                { return nil }
func (f *fakeTTY) Device() (uint64, error)     { return f.dev, nil }

// R5 (seat_owner.go:43, :82): a double-forked process reparented to PID 1
// has no pane ancestor, and an inaccessible tmux server read as no panes.
// seat clear requires a controlling terminal that is not any seat pane's
// pty and the seat handle typed on it; an unreadable tmux server or tty is
// owner_proof_unknown.
func TestSeatClearNeedsTheOwnersOwnTerminal(t *testing.T) {
	root, s, tk := quarantinedSeat(t)
	clear := func() error {
		_, _, err := captureOutput(t, func() error { return runSeatClear([]string{"--seat", "s1", "--root", root}, os.Stdout) })
		return err
	}
	// Reparented to init: no pane ancestor and no real pty on stdin.
	ownerProcess(t, map[int]string{80: "lead"}, false)
	chainReaches = func(int, string, []panechain.Pane) (*panechain.Pane, bool) { return nil, true }
	openOwnerTTY = func() (ownerTTY, error) { return nil, errNoPTY }
	if err := clear(); !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "not a real pty") {
		t.Fatalf("a reparented process cleared: %v", err)
	}
	// A terminal that cannot be opened is owner_proof_unknown (R9).
	openOwnerTTY = func() (ownerTTY, error) { return nil, errors.New("device not configured") }
	if err := clear(); !errors.Is(err, errOwnerUnknown) {
		t.Fatalf("an unopenable terminal: %v", err)
	}
	// Its terminal is the lead's pane.
	ownerProcess(t, map[int]string{80: "lead"}, false)
	chainReaches = func(int, string, []panechain.Pane) (*panechain.Pane, bool) { return nil, true }
	openOwnerTTY = func() (ownerTTY, error) { return &fakeTTY{dev: 1080, in: strings.NewReader("s1\n")}, nil }
	if err := clear(); !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "seat lead's pane") {
		t.Fatalf("a process on the lead's pty cleared: %v", err)
	}
	// An unreadable tmux server.
	ownerProcess(t, nil, false)
	seatPanes = func() ([]panechain.Pane, error) { return nil, panechain.ErrUnknown }
	if err := clear(); !errors.Is(err, errOwnerUnknown) {
		t.Fatalf("an unreadable tmux server passed: %v", err)
	}
	// The wrong confirmation.
	ownerProcess(t, nil, false)
	openOwnerTTY = func() (ownerTTY, error) { return &fakeTTY{dev: 7, in: strings.NewReader("yes\n")}, nil }
	if err := clear(); !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("a wrong confirmation cleared: %v", err)
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateBlocked {
		t.Fatalf("a refused clear changed the task: %+v", got)
	}
	ownerProcess(t, map[int]string{70: "lead"}, false)
	if err := clear(); err != nil {
		t.Fatalf("the owner's clear: %v", err)
	}
}

// D2, the local_push_ack row: a bare-path push target parses only with the
// owner's ack (the launch refuses it otherwise, seatcommit's rows), and with
// the ack doctor warns "push target has no server enforcement" (the merge
// train refuses the real run, 3-2's side). With no gh pin, doctor proposes
// one and fails when the grant pushes over https.
func TestDoctorWarnsOfALocalPushTargetAndTheGhPin(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	ownerGrant(t, "/srv/git/r.git")
	checks := pushTargetChecks(home, "")
	found := map[string]doctorCheck{}
	for _, c := range checks {
		found[c.Name] = c
	}
	if c := found["push target"]; c.Status != doctorWarn || !strings.Contains(c.Detail, "push target has no server enforcement") {
		t.Fatalf("local push target row: %+v", c)
	}
	if c := found["gh pin"]; c.Status != doctorWarn || !strings.Contains(c.Detail, "tool_unpinned") {
		t.Fatalf("gh pin row: %+v", c)
	}
	os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("schema: 1\nmerge:\n  - repository: o/r\n    base_branch: main\n    size_cap_lines: 300\n    push_url: https://github.com/o/r.git\n"), 0o600)
	for _, c := range pushTargetChecks(home, "") {
		if c.Name == "gh pin" && c.Status != doctorFail {
			t.Fatalf("an https push without a pinned gh: %+v", c)
		}
	}
}

// R9 (seat_owner_unix.go:25, :85, :100): /dev/tty's device is, on Linux,
// the alias every controlling terminal shares, and a reparented child keeps
// the seat's terminal. The owner's terminal is stdin's real pty, compared by
// device with every pane pty; a stdin that is not a pty is refused (as in
// go test, whose stdin is not a terminal); the confirmation waits at most
// the timeout.
func TestOwnerTerminalIsTheRealPtyOfStdin(t *testing.T) {
	if _, err := openControllingTTY(); !errors.Is(err, errNoPTY) {
		if err == nil {
			t.Skip("stdin is a terminal here")
		}
		t.Fatalf("a non-pty stdin: %v", err)
	}
	root, _, _ := quarantinedSeat(t)
	ownerProcess(t, nil, false)
	blocked, _ := io.Pipe()
	openOwnerTTY = func() (ownerTTY, error) { return &pipeTTY{r: blocked}, nil }
	prev := confirmTimeout
	confirmTimeout = 200 * time.Millisecond
	t.Cleanup(func() { confirmTimeout = prev })
	done := make(chan error, 1)
	go func() { done <- runSeatClear([]string{"--seat", "s1", "--root", root}, io.Discard) }()
	select {
	case err := <-done:
		if !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "no confirmation within") {
			t.Fatalf("an unanswered confirmation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the confirmation waited past its timeout")
	}
}

// pipeTTY is a terminal whose input never arrives.
type pipeTTY struct{ r *io.PipeReader }

func (p *pipeTTY) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeTTY) Write(b []byte) (int, error) { return len(b), nil }
func (p *pipeTTY) Close() error                { return nil }
func (p *pipeTTY) Device() (uint64, error)     { return 7, nil }
