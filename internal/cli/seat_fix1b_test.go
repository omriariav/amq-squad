package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
	reply, err := schedproc.SendDecision(s.Root(), s.Path(), req)
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

// writeSquadProject writes a squad.yaml and its custom roles into proj
// (brief 31-1: squad.yaml is the one source of scheduler run).
func writeSquadProject(t *testing.T, proj, squadYAML string, roles ...string) {
	t.Helper()
	mustWrite(t, team.SquadPath(proj), squadYAML)
	for _, r := range roles {
		mustWrite(t, team.CustomRolePath(proj, r), "# Role: "+r+"\n\nYou are the "+r+" of this squad.\n")
	}
}

// fixtureSquadYAML is the scheduler fixtures' squad.yaml: the lead "lead"
// (a pi seat, outside the roster) and one Claude seat in wt-impl-1.
const fixtureSquadYAML = `schema: 1
session: s1
lead: lead
seats:
  - handle: lead
    role: lead
    runtime: pi
  - handle: impl-1
    role: implementer
    runtime: claude
    model: sonnet
    effort: medium
    worktree: wt-impl-1
`

// schedulerFixture is a squad for runSchedulerRun with a fake launcher; it
// returns the repository whose squad.yaml is the squad.
func schedulerFixture(t *testing.T) (home, root, proj string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ = filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(proj, ".agent-mail", "s1")
	os.MkdirAll(filepath.Join(root, "agents"), 0o700)
	writeSquadProject(t, proj, fixtureSquadYAML, "implementer")
	// The roster launches at start (3.0.1 t16), so the seat's worktree must
	// exist as the slots expect it.
	os.MkdirAll(filepath.Join(proj, "wt-impl-1"), 0o755)
	prevL, prevS, prevStarted := schedulerAMQLauncher, schedulerSendRun, schedulerStarted
	t.Cleanup(func() { schedulerAMQLauncher, schedulerSendRun, schedulerStarted = prevL, prevS, prevStarted })
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		return &cliFakeLauncher{sl: sl}
	}
	schedulerSendRun = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	return home, root, proj
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

// pipeTTY is a terminal whose input never arrives.
type pipeTTY struct{ r *io.PipeReader }

func (p *pipeTTY) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeTTY) Write(b []byte) (int, error) { return len(b), nil }
func (p *pipeTTY) Close() error                { return nil }
func (p *pipeTTY) Device() (uint64, error)     { return 7, nil }
