package seatlaunch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

var advertised = []launchapi.ProviderCapabilitiesV1{
	{Provider: "claude", GrammarVersion: 3, AllowedArgumentForms: []string{"--allowedTools", "--effort", "--model", "--name", "--permission-mode", "-n"}},
	{Provider: "codex", GrammarVersion: 3, AllowedArgumentForms: []string{"--ask-for-approval", "--model", "--sandbox", "-c"},
		ConfigOverrides: []launchapi.ConfigOverrideCapabilityV1{{Key: "model_reasoning_effort", AllowedValues: []string{"low", "medium", "high"}}}},
}

func testSquad(t *testing.T) Squad {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := Squad{
		ProjectRoot: filepath.Join(base, "proj"), Session: "s1", Wrapper: "/opt/amq-squad/bin/amq-squad", AMQPath: "/opt/homebrew/bin/amq",
		Seats: []Seat{
			{Handle: "cto", Runtime: "claude", Model: "sonnet", Effort: "medium", Role: "# Role\n\nYou are the cto seat of this squad.\n", Worktree: filepath.Join(base, "wt-cto"), Branch: "seat/cto"},
			{Handle: "dev", Runtime: "codex", Model: "gpt-6-astra", Effort: "medium", Role: "# Role\n\nYou are the dev seat of this squad.\n", Worktree: filepath.Join(base, "wt-dev"), Branch: "seat/dev"},
		},
	}
	if err := os.MkdirAll(filepath.Join(q.ProjectRoot, ".agent-mail", "s1", "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	q, err = q.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func lookPath(name string) (string, error) { return "/usr/local/bin/" + name, nil }

func TestCompileOneRequestWithTheWrapper(t *testing.T) {
	q := testSquad(t)
	c, err := Compile(q, "sha256:r", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Request.Validate(); err != nil {
		t.Fatal(err)
	}
	ps := c.Request.Intent.Participants
	if len(ps) != 3 || ps[0].Handle != "user" || ps[0].Runnable {
		t.Fatalf("participants %+v", ps)
	}
	want := map[string][]string{
		"cto": {"--model", "sonnet", "--effort", "medium"},
		"dev": {"--model", "gpt-6-astra", "-c", "model_reasoning_effort=medium", "--sandbox", "workspace-write", "--ask-for-approval", "on-request"},
	}
	for _, p := range ps[1:] {
		if !slices.Equal(p.Args, want[p.Handle]) {
			t.Errorf("%s args %q, want %q", p.Handle, p.Args, want[p.Handle])
		}
		if p.Wrapper == nil || p.Wrapper.Executable != q.Wrapper || !slices.Equal(p.Wrapper.Args, []string{"seat-exec", "--seat", p.Handle}) {
			t.Errorf("%s wrapper %+v", p.Handle, p.Wrapper)
		}
		if p.InitialInput == nil || p.InitialInput.Text != RolePointer {
			t.Errorf("%s initial input %+v, want only the role pointer", p.Handle, p.InitialInput)
		}
	}
	if c.Request.Target.SessionRoot != filepath.Join(q.BaseRoot, "s1") || c.Records[0].RoleSHA256 != digest([]byte(q.Seats[0].Role)) || c.Records[1].RulesSHA256 != "sha256:r" {
		t.Fatalf("target %+v records %+v", c.Request.Target, c.Records)
	}
}

// amendment 23 (b): pi seats carry no provider arguments and no initial
// input. The pinned library (v0.107.0) has no pi adapter yet (PR #1188 is
// unreleased), so a pi seat is refused with that reason instead of failing
// inside AMQ's validation.
func TestPiSeatShape(t *testing.T) {
	pi := Seat{Handle: "pi1", Runtime: "pi", Model: "qwen", Effort: "low", Role: "# Role\n\npi seat role text here.\n", Worktree: "/w/pi"}
	if args := ProviderArgs(pi); args != nil {
		t.Fatalf("pi provider args %q", args)
	}
	q := testSquad(t)
	q.Seats = append(q.Seats, pi)
	_, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if PiAdapterInLibrary {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "#1188") {
		t.Fatalf("pi seat on a library without the adapter: %v", err)
	}
}

func TestRoleDeliveryRules(t *testing.T) {
	q := testSquad(t)
	q.Seats[0].InitialInput = "Start with the task. You are the cto seat of this squad."
	if _, err := q.Normalize(); err == nil || !strings.Contains(err.Error(), "carries role text") {
		t.Fatalf("role text in initial_input: %v", err)
	}
	q = testSquad(t)
	q.Seats[0].Role, q.Seats[0].InitialInput = "", "You are the cto. Do the work."
	if _, err := q.Normalize(); err == nil || !strings.Contains(err.Error(), "without a role.md") {
		t.Fatalf("role text without role.md: %v", err)
	}
	q = testSquad(t)
	q.Seats[0].InitialInput = "The task is in tasks/t1.json."
	if _, err := q.Normalize(); err != nil {
		t.Fatalf("a pointer is allowed: %v", err)
	}
}

func TestArgumentFormsComeFromAMQ(t *testing.T) {
	q := testSquad(t)
	if err := CheckArgumentForms(q, advertised); err != nil {
		t.Fatal(err)
	}
	q.Seats[1].Effort = "ultra"
	if err := CheckArgumentForms(q, advertised); err == nil {
		t.Fatal("an effort AMQ does not admit passed")
	}
	if err := CheckArgumentForms(testSquad(t), advertised[:1]); err == nil {
		t.Fatal("no advertised forms for codex passed")
	}
}

// fakeAMQ answers amq launch --prepare and --apply like the CLI does.
type fakeAMQ struct {
	t        *testing.T
	prepare  launchapi.PrepareResultV1
	apply    launchapi.ApplyResultV1
	applyErr string
	onApply  func()
	calls    [][]string
	envs     [][]string
	applied  *launchapi.ApplyRequestV1
}

func (f *fakeAMQ) run(_ context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
	f.calls, f.envs = append(f.calls, args), append(f.envs, env)
	if slices.Contains(args, "--prepare") {
		var req launchapi.PrepareRequestV1
		if err := json.Unmarshal(stdin, &req); err != nil || req.Validate() != nil {
			f.t.Fatalf("prepare request: %v", err)
		}
		b, _ := json.Marshal(f.prepare)
		code := 0
		if f.prepare.Outcome == "action_required" {
			code = 6
		}
		return b, nil, code, nil
	}
	var req launchapi.ApplyRequestV1
	if err := json.Unmarshal(stdin, &req); err != nil {
		f.t.Fatal(err)
	}
	f.applied = &req
	if f.onApply != nil {
		f.onApply()
	}
	b, _ := json.Marshal(f.apply)
	return b, []byte(f.applyErr), 6, nil
}

var trustAction = launchapi.RequiredActionV1{ActionID: "sha256:a1", Kind: "trust_confirmation", Handles: []string{"cto", "dev"},
	AllowedDecisions: []launchapi.DecisionChoiceV1{"trust_exact_subject", "deny"}, ReasonCode: "untrusted_config_digest"}

func preparedResult() launchapi.PrepareResultV1 {
	return launchapi.PrepareResultV1{ResultVersion: 1, SubjectSchema: 2, Outcome: "action_required", Reason: "untrusted_config_digest",
		SubjectDigest: "sha256:subj", RequiredActions: []launchapi.RequiredActionV1{trustAction}, Preview: launchapi.PreviewV1{Capabilities: advertised}}
}

func launchOpts(t *testing.T, f *fakeAMQ, decisions map[string]string) Options {
	return Options{Decisions: decisions, Home: t.TempDir(), LookPath: lookPath, RunAMQ: f.run, Wait: 2 * time.Second, Poll: 10 * time.Millisecond,
		Env: []string{"PATH=/usr/bin", "AM_ME=lead", "AM_ROOT=/r/s0", "AM_SESSION=s0", "AM_BASE_ROOT=/r"}}
}

func TestPendingDecisionsStopBeforeApplyAndSendNothing(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult()}
	res, err := Launch(context.Background(), q, launchOpts(t, f, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage != "decisions_pending" || len(f.calls) != 1 || f.applied != nil {
		t.Fatalf("stage %s, %d amq calls", res.Stage, len(f.calls))
	}
	// R49: the launch subprocess never carries the caller's AMQ identity, so
	// it cannot register a lead presence.
	for _, kv := range f.envs[0] {
		if strings.HasPrefix(kv, "AM_") {
			t.Fatalf("amq launch saw %s", kv)
		}
	}
	if _, err := os.Stat(launch.StagedDir(q.SessionRoot(), "cto")); !os.IsNotExist(err) {
		t.Fatalf("nothing is staged before the decisions: %v", err)
	}
	var out bytes.Buffer
	Report(&out, res)
	text := out.String()
	if !strings.Contains(text, "--decision sha256:a1=<trust_exact_subject|deny>") || strings.Contains(text, "--force-duplicate") {
		t.Fatalf("next actions (R50):\n%s", text)
	}
	if _, err := Launch(context.Background(), q, launchOpts(t, f, map[string]string{"sha256:old": "trust_exact_subject"})); err == nil {
		t.Fatal("a stale decision was accepted")
	}
	if _, err := Launch(context.Background(), q, launchOpts(t, f, map[string]string{"sha256:a1": "yes"})); err == nil {
		t.Fatal("a choice outside the allowed set was accepted")
	}
}

func TestForcedBackendCreateFailedShowsOutcomeReasonAndDetail(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(),
		apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", ReasonCode: "backend_create_failed", Disposition: "rolled_back", Backend: "tmux",
			Commands: []launchapi.CommandV1{}},
		applyErr: "launch: tmux create failed: pane exited: amq-squad coop exec: unknown command\n"}
	res, err := Launch(context.Background(), q, launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"}))
	if err != nil {
		t.Fatal(err)
	}
	if f.applied == nil || len(f.applied.Decisions) != 1 || f.applied.SubjectDigest != "sha256:subj" || f.applied.SubjectSchema != 2 {
		t.Fatalf("apply request %+v", f.applied)
	}
	if res.Stage != "apply_failed" || !res.Failed() {
		t.Fatalf("stage %s", res.Stage)
	}
	var out bytes.Buffer
	Report(&out, res)
	for _, want := range []string{"apply: outcome action_required, reason_code backend_create_failed", "failure_detail launch: tmux create failed: pane exited: amq-squad coop exec: unknown command"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report lacks %q:\n%s", want, out.String())
		}
	}
	rec, err := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "cto"), launch.FileName))
	if err != nil || !strings.Contains(string(rec), `"reason_code": "backend_create_failed"`) {
		t.Fatalf("staged record keeps the apply outcome: %s %v", rec, err)
	}
}

func TestSeatIsUpOnlyWithItsDrainProof(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed", BindingGeneration: "nonce-1"}}
	f.onApply = func() {
		// What seat-exec publishes: cto proved its drain, dev was refused.
		for handle, exec := range map[string]*launch.Exec{
			"cto": {At: time.Now(), Outcome: "exec", DrainProof: "amq drain", PID: 1},
			"dev": {At: time.Now(), Outcome: "refused", ReasonCode: "drain_failed", Detail: "amq drain failed: exit status 5"},
		} {
			dir := filepath.Join(q.SessionRoot(), "agents", handle)
			if err := launch.WriteUnderRecordLock(dir, launch.Record{Handle: handle, Exec: exec}); err != nil {
				t.Fatal(err)
			}
		}
	}
	res, err := Launch(context.Background(), q, launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SeatState{}
	for _, s := range res.Seats {
		got[s.Handle] = s
	}
	if !got["cto"].Up || got["dev"].Up || got["dev"].ReasonCode != "drain_failed" || !res.Failed() {
		t.Fatalf("seats %+v", res.Seats)
	}
	staged, _ := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "cto"), launch.FileName))
	if !strings.Contains(string(staged), `"launch_nonce": "nonce-1"`) {
		t.Fatalf("staged record lacks the nonce: %s", staged)
	}
}

func TestStaleExecRecordNeverReportsUp(t *testing.T) {
	q := testSquad(t)
	dir := filepath.Join(q.SessionRoot(), "agents", "cto")
	launch.WriteUnderRecordLock(dir, launch.Record{Handle: "cto", Exec: &launch.Exec{At: time.Now().Add(-time.Hour), Outcome: "exec", DrainProof: "amq drain"}})
	states := WaitSeats(context.Background(), Squad{BaseRoot: q.BaseRoot, Session: q.Session, Seats: q.Seats[:1]}, time.Now(), 50*time.Millisecond, 10*time.Millisecond)
	if states[0].Up || states[0].State != "not_started" {
		t.Fatalf("an earlier launch's record counted: %+v", states[0])
	}
}
