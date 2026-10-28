//go:build unix

package seatexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

type fixture struct {
	t                         *testing.T
	base, root, home, bin, wt string
	rec                       launch.Record
	role                      []byte
	execPath                  string
	execArgv, execEnv         []string
	drainEnv                  []string
	drainArgs                 []string
	drainFail                 func(env []string) error
	proofs                    []launch.ProofMessage
	proofReply                *launch.ProofReply
}

func newFixture(t *testing.T, runtime string) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, base: base, root: filepath.Join(base, "proj", ".agent-mail", "s1"), home: filepath.Join(base, "home"), bin: filepath.Join(base, "bin"), wt: filepath.Join(base, "wt")}
	for _, d := range []string{filepath.Join(f.root, "agents", "cto"), launch.StagedDir(f.root, "cto"), filepath.Join(f.home, ".claude"), f.bin, f.wt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// MkdirAll leaves the umask's mode on intermediate dirs; make them 0700.
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	for _, name := range []string{"claude", "codex", "pi"} {
		write(t, filepath.Join(f.bin, name), "#!/bin/sh\nexit 0\n", 0o700)
	}
	write(t, filepath.Join(f.home, ".claude", "settings.json"), ownerSettings, 0o600)
	rules := filepath.Join(base, "proj", "team-rules.md")
	write(t, rules, "# rules\n", 0o600)
	f.role = []byte("# Role: cto\n\nYou are the cto seat.\n")
	f.rec = launch.Record{
		Handle: "cto", Root: f.root, BaseRoot: filepath.Dir(f.root), Session: "s1",
		Runtime: runtime, Model: "m", Effort: "medium", RoleSHA256: sha(f.role),
		RulesPath: rules, RulesSHA256: sha([]byte("# rules\n")), Branch: "seat/cto", Worktree: f.wt,
		AMQPath: "/fake/amq",
		Proof:   &launch.ProofTicket{Socket: "/fake/proof.sock", Dir: "/fake", Name: "proof.sock", Token: "tok-0123456789", IssuedAt: time.Now()},
	}
	if runtime == "claude" {
		f.rec.PermissionMode, f.rec.LaunchID = "dontAsk", "launch-1"
		// A Claude seat runs the registry's claude pin (the phase 5 re-pass's
		// HIGH): the fixture pins its fake claude.
		f.pinClaude()
		// The owner's token file (0600 in 0700): a canary that must reach the
		// provider environment and nothing else.
		if err := os.MkdirAll(filepath.Join(f.home, ".amq-squad"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, claudeseat.TokenPath(f.home), canaryToken+"\n", 0o600)
	}
	return f
}

// ownerSettings is the test HOME's ~/.claude/settings.json: the owner's own
// user-scope allow rule, which never reaches a seat (its config dir is its own).
// canaryToken is the test token: it may appear only in the provider's
// environment.
const canaryToken = "sk-ant-oat01-CANARY-2b2b-9f3c"

const ownerSettings = `{"owner":true,"permissions":{"allow":["Bash(echo:*)"]}}`

// pinClaude writes the registry pin of the fixture's claude (its path and
// current sha256) into the fixture's HOME.
func (f *fixture) pinClaude() {
	f.t.Helper()
	claude := filepath.Join(f.bin, "claude")
	sum, err := registry.FileSHA256(claude)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := registry.Write(registry.Path(f.home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "claude", Path: claude, SHA256: sum}}}); err != nil {
		f.t.Fatal(err)
	}
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) stage() {
	f.t.Helper()
	if f.rec.Runtime == "claude" && f.rec.ClaudeConfig == "" {
		// What the launch writes: the seat's own config dir and its digest.
		dir := claudeseat.Dir(f.root, "cto", f.rec.LaunchID)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			w, err := claudeseat.Write(dir, f.rec.PermissionMode, f.wt, time.Unix(100, 0))
			if err != nil {
				f.t.Fatal(err)
			}
			f.rec.ClaudeConfig = w.Digest
		}
	}
	if f.rec.Runtime == "claude" && f.rec.ClaudePin == "" {
		// What the launch records: the digest of the claude pin it probed.
		if pin, ok := f.pinOf(); ok {
			f.rec.ClaudePin = pin.SHA256
		}
	}
	b, err := launch.Marshal(f.rec)
	if err != nil {
		f.t.Fatal(err)
	}
	dir := launch.StagedDir(f.root, "cto")
	write(f.t, filepath.Join(dir, launch.FileName), string(b), 0o600)
	write(f.t, filepath.Join(dir, launch.RoleFileName), string(f.role), 0o600)
}

func (f *fixture) env(extra ...string) []string {
	return append([]string{"AM_ME=cto", "AM_ROOT=" + f.root, "PATH=/usr/bin:/bin", "LANG=C"}, extra...)
}

func (f *fixture) run(o Options) error {
	if o.Seat == "" {
		o.Seat = "cto"
	}
	if o.Argv == nil {
		o.Argv = []string{filepath.Join(f.bin, f.rec.Runtime), "--model", "m"}
		if f.rec.Runtime == "codex" {
			o.Argv = append(o.Argv, "--sandbox", "workspace-write", "--ask-for-approval", "on-request")
			// The pins the compiler puts in the launch request (brief 5-amq-A).
			o.Argv = append(o.Argv, CodexSandboxPins(slices.Contains(f.rec.EnvNeeds, CodexNetworkNeed))...)
		}
		if f.rec.Runtime == "claude" {
			o.Argv = append(o.Argv, "--permission-mode", "dontAsk")
		}
	}
	if o.Env == nil {
		o.Env = f.env()
	}
	o.Home, o.Now = f.home, func() time.Time { return time.Unix(100, 0) }
	if o.UID == 0 {
		o.UID = os.Getuid()
	}
	o.RunAMQ = func(_ context.Context, amq string, args, env []string, dir string) ([]byte, error) {
		f.drainArgs, f.drainEnv = append([]string{amq}, args...), env
		if f.drainFail != nil {
			if err := f.drainFail(env); err != nil {
				return []byte("refused"), err
			}
		}
		// What amq env --json prints for the environment it was given.
		return []byte(fmt.Sprintf(`{"root":%q,"me":%q,"session_name":%q}`, Getenv(env, "AM_ROOT"), Getenv(env, "AM_ME"), Getenv(env, "AM_SESSION"))), nil
	}
	if o.SendProof == nil {
		o.SendProof = func(tk launch.ProofTicket, msg launch.ProofMessage) (launch.ProofReply, error) {
			f.proofs = append(f.proofs, msg)
			if f.proofReply != nil {
				return *f.proofReply, nil
			}
			return launch.ProofReply{OK: true}, nil
		}
	}
	o.Exec = func(path string, argv, env []string) error {
		f.execPath, f.execArgv, f.execEnv = path, argv, env
		return nil
	}
	return Run(o)
}

func refusalCode(err error) string {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

func (f *fixture) published() launch.Record {
	f.t.Helper()
	rec, err := launch.Read(filepath.Join(f.root, "agents", "cto"))
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

func TestSeatExecSetsIdentityRoleAndExecsInPlace(t *testing.T) {
	f := newFixture(t, "claude")
	f.rec.RequestID = "seat-req-inc1-7"
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if f.execPath != filepath.Join(f.bin, "claude") || strings.Join(f.execArgv, " ") != filepath.Join(f.bin, "claude")+" --model m --permission-mode dontAsk" {
		t.Fatalf("exec %s %v: the argv must pass through unchanged", f.execPath, f.execArgv)
	}
	for name, want := range map[string]string{"AM_ME": "cto", "AM_ROOT": f.root, "AM_BASE_ROOT": filepath.Dir(f.root), "AM_SESSION": "s1",
		"AMQ_SQUAD_BRANCH": "seat/cto", "AMQ_SQUAD_REQUEST_ID": "seat-req-inc1-7", "AMQ_SQUAD_RULES_PATH": filepath.Join(launch.ExtensionDir(filepath.Join(f.root, "agents", "cto")), "rules.md"), "AMQ_SQUAD_RULES_SHA256": f.rec.RulesSHA256} {
		if got := Getenv(f.execEnv, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	rolePath := Getenv(f.execEnv, "AMQ_SQUAD_ROLE_PATH")
	if b, err := os.ReadFile(rolePath); err != nil || string(b) != string(f.role) {
		t.Fatalf("published role %s: %q %v", rolePath, b, err)
	}
	pub := f.published()
	if pub.Exec == nil || pub.Exec.Outcome != "exec" || pub.Exec.IdentityProof != "amq env" || pub.RolePath != rolePath {
		t.Fatalf("published record exec block %+v", pub.Exec)
	}
	if strings.Join(f.drainArgs, " ") != "/fake/amq env --me cto --json" || Getenv(f.drainEnv, "AM_SESSION") != "s1" {
		t.Fatalf("identity proof %v with AM_SESSION=%q", f.drainArgs, Getenv(f.drainEnv, "AM_SESSION"))
	}
}

func TestSeatExecRoleIntegrity(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	// One changed byte in the staged role.md.
	write(t, filepath.Join(launch.StagedDir(f.root, "cto"), launch.RoleFileName), "# Role: cto\n\nYou are the CTO seat.\n", 0o600)
	if err := f.run(Options{}); refusalCode(err) != "role_mismatch" || f.execPath != "" {
		t.Fatalf("changed role byte: %v (exec %q)", err, f.execPath)
	}
	os.Remove(filepath.Join(launch.StagedDir(f.root, "cto"), launch.RoleFileName))
	if err := f.run(Options{}); refusalCode(err) != "role_missing" || f.execPath != "" {
		t.Fatalf("no role.md: %v", err)
	}
}

func TestSeatExecRulesDigestRecordedAtLaunch(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	write(t, f.rec.RulesPath, "# rules changed after launch\n", 0o600)
	if err := f.run(Options{}); refusalCode(err) != "rules_mismatch" {
		t.Fatalf("rules drift: %v", err)
	}
}

func TestSeatExecTraversalRefusals(t *testing.T) {
	t.Run("symlink in the initial path", func(t *testing.T) {
		f := newFixture(t, "claude")
		f.stage()
		link := filepath.Join(f.base, "link")
		if err := os.Symlink(filepath.Join(f.base, "proj"), link); err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(link, ".agent-mail", "s1")
		if err := f.run(Options{Env: []string{"AM_ME=cto", "AM_ROOT=" + linked}}); refusalCode(err) != "symlink_in_path" {
			t.Fatalf("symlinked root: %v", err)
		}
	})
	t.Run("group-writable existing directory", func(t *testing.T) {
		f := newFixture(t, "claude")
		f.stage()
		if err := os.Chmod(filepath.Join(f.root, "agents"), 0o770); err != nil {
			t.Fatal(err)
		}
		if err := f.run(Options{}); refusalCode(err) != "dir_writable_by_others" {
			t.Fatalf("group-writable agents dir: %v", err)
		}
	})
	t.Run("wrong owner", func(t *testing.T) {
		f := newFixture(t, "claude")
		f.stage()
		if err := f.run(Options{UID: os.Getuid() + 1}); refusalCode(err) != "wrong_owner" {
			t.Fatalf("wrong owner: %v", err)
		}
	})
	t.Run("symlinked staged record", func(t *testing.T) {
		f := newFixture(t, "claude")
		f.stage()
		staged := filepath.Join(launch.StagedDir(f.root, "cto"), launch.FileName)
		other := filepath.Join(f.base, "other.json")
		b, _ := os.ReadFile(staged)
		write(t, other, string(b), 0o600)
		os.Remove(staged)
		os.Symlink(other, staged)
		if err := f.run(Options{}); refusalCode(err) != "symlink_in_path" {
			t.Fatalf("symlinked record: %v", err)
		}
	})
}

func TestSeatExecScrubsCredentials(t *testing.T) {
	secrets := []string{"GEMINI_API_KEY=g", "GH_TOKEN=t", "AWS_SECRET_ACCESS_KEY=a", "SSH_AUTH_SOCK=/tmp/agent", "OPENAI_API_KEY=o", "MY_SECRET=s"}
	f := newFixture(t, "codex")
	f.stage()
	if err := f.run(Options{Env: f.env(secrets...)}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range secrets {
		name, _, _ := strings.Cut(kv, "=")
		if Getenv(f.execEnv, name) != "" || Getenv(f.drainEnv, name) != "" {
			t.Errorf("%s reached the seat", name)
		}
	}
	if Getenv(f.execEnv, "LANG") != "C" || Getenv(f.execEnv, "PATH") == "" {
		t.Fatal("ordinary variables must stay")
	}

	g := newFixture(t, "codex")
	g.rec.CredNeeds = []string{"OPENAI_API_KEY"}
	g.stage()
	if err := g.run(Options{Env: g.env(secrets...)}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range secrets {
		name, value, _ := strings.Cut(kv, "=")
		got := Getenv(g.execEnv, name)
		if (name == "OPENAI_API_KEY") != (got == value) {
			t.Errorf("declared need: %s = %q", name, got)
		}
	}
}

func TestSeatExecIdentityFromRecordOnly(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	// An inherited, conflicting pin is refused, never repaired.
	if err := f.run(Options{Env: f.env("AM_SESSION=other")}); refusalCode(err) != "identity_mismatch" {
		t.Fatalf("conflicting AM_SESSION: %v", err)
	}
	if err := f.run(Options{Env: []string{"AM_ME=qa", "AM_ROOT=" + f.root}}); refusalCode(err) != "identity_mismatch" {
		t.Fatalf("AM_ME disagrees with --seat: %v", err)
	}
	g := newFixture(t, "claude")
	g.rec.Session = ""
	g.stage()
	if err := g.run(Options{}); refusalCode(err) != IdentityIncomplete {
		t.Fatalf("record without session: %v", err)
	}
}

func TestSeatExecFailedDrainIsNotReportedUp(t *testing.T) {
	// S6 finding A: amq refused every first drain when AM_SESSION was missing.
	f := newFixture(t, "codex")
	f.stage()
	f.drainFail = func(env []string) error {
		if Getenv(env, "AM_SESSION") == "" {
			return errors.New("exit status 2")
		}
		return nil
	}
	drop := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if !strings.HasPrefix(kv, "AM_SESSION=") {
				out = append(out, kv)
			}
		}
		return out
	}
	err := f.run(Options{MutateEnv: drop})
	if refusalCode(err) != "identity_proof_failed" || f.execPath != "" {
		t.Fatalf("failed drain: %v (exec %q)", err, f.execPath)
	}
	if pub := f.published(); pub.Exec == nil || pub.Exec.Outcome != "refused" || pub.Exec.ReasonCode != "identity_proof_failed" {
		t.Fatalf("the published record must carry the refusal: %+v", pub.Exec)
	}
}

func TestSeatExecIdentityProofNeverConsumesWaitingMail(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	os.MkdirAll(filepath.Join(f.root, "agents", "cto", "inbox", "new"), 0o700)
	write(t, filepath.Join(f.root, "agents", "cto", "inbox", "new", "m1.md"), "x", 0o600)
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if f.drainArgs[1] != "env" {
		t.Fatalf("with mail waiting the proof must be read-only: %v", f.drainArgs)
	}
}

func TestSeatExecRuntimeMismatch(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Argv: []string{filepath.Join(f.bin, "codex")}}); refusalCode(err) != "runtime_mismatch" {
		t.Fatalf("provider is not the recorded runtime: %v", err)
	}
}

func TestSeatExecPreflightCopy(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(PreflightDir(f.home), "claude-settings.json")
	if b, err := os.ReadFile(copyPath); err != nil || string(b) != ownerSettings {
		t.Fatalf("preflight copy: %q %v", b, err)
	}
	// A later seat start never overwrites the copy taken first.
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"changed":true,"enabledPlugins":{"amq-squad@amq-squad":true}}`, 0o600)
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(copyPath); string(b) != ownerSettings {
		t.Fatalf("seat-exec refreshed the preflight copy: %s", b)
	}
	if _, err := os.Stat(filepath.Join(PreflightDir(f.home), "codex-config.toml")); !os.IsNotExist(err) {
		t.Fatalf("no codex config, so no copy: %v", err)
	}
}

func TestSeatExecPiResumeRefusal(t *testing.T) {
	ledger := func(f *fixture, line string) {
		dir := filepath.Join(f.root, "extensions", "amq-squad", "ledger")
		os.MkdirAll(dir, 0o700)
		write(f.t, filepath.Join(dir, "cto.jsonl"), line+"\n", 0o600)
	}
	piArgv := func(f *fixture) []string { return []string{filepath.Join(f.bin, "pi"), "--session-id", "nonce-1"} }
	toolResult := `{"schema":1,"seq":1,"event":"tool_result","tool":"bash","outcome":"ok"}`

	f := newFixture(t, "pi")
	f.rec.Resume = true
	f.stage()
	ledger(f, toolResult)
	if err := f.run(Options{Argv: piArgv(f)}); refusalCode(err) != "pi_resume_context_lost" || f.execPath != "" {
		t.Fatalf("tool result and no session file: %v", err)
	}

	g := newFixture(t, "pi")
	g.rec.Resume = true
	g.stage()
	ledger(g, `{"schema":1,"seq":1,"event":"before_agent_start"}`)
	if err := g.run(Options{Argv: piArgv(g)}); err != nil {
		t.Fatalf("no tool result resumes: %v", err)
	}
	for name, want := range map[string]string{"PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0", "PI_CODING_AGENT_DIR": filepath.Join(g.home, ".amq-squad", "pi-agent", "cto")} {
		if got := Getenv(g.execEnv, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if strings.Join(g.execArgv[1:], " ") != "--session-id nonce-1" {
		t.Fatalf("pi carries no provider arguments of ours: %v", g.execArgv)
	}

	h := newFixture(t, "pi")
	h.rec.Resume = true
	h.stage()
	ledger(h, toolResult)
	// The pinned pi 1.3.0 layout (4-1): the session file lives in the
	// folder of the seat's cwd (the worktree); a file for the same id in
	// another cwd's folder does not count.
	agentDir := filepath.Join(h.home, ".amq-squad", "pi-agent", "cto")
	other := filepath.Join(agentDir, "sessions", "--elsewhere--")
	os.MkdirAll(other, 0o700)
	write(t, filepath.Join(other, "2026-10-24T10-00-00-000Z_nonce-1.jsonl"), "{}\n", 0o600)
	if err := h.run(Options{Argv: piArgv(h)}); err == nil {
		t.Fatal("a session file for another cwd counted as the seat's")
	}
	sessions := piruntime.SessionDir(agentDir, h.wt)
	os.MkdirAll(sessions, 0o700)
	write(t, filepath.Join(sessions, "2026-10-24T10-00-00-000Z_nonce-1.jsonl"), "{}\n", 0o600)
	if err := h.run(Options{Argv: piArgv(h)}); err != nil {
		t.Fatalf("session file present resumes: %v", err)
	}
}

func TestScrubbedNames(t *testing.T) {
	for name, want := range map[string]bool{"GEMINI_API_KEY": true, "GH_TOKEN": true, "AWS_SECRET_ACCESS_KEY": true, "SSH_AUTH_SOCK": true,
		"DB_PASSWORD": true, "PATH": false, "HOME": false, "TERM": false, "AM_ME": false, "TOKENIZERS_PARALLELISM": false} {
		if Scrubbed(name) != want {
			t.Errorf("Scrubbed(%s) = %v", name, !want)
		}
	}
}

// pinOf is the fixture registry's claude pin.
func (f *fixture) pinOf() (registry.Pin, bool) {
	f.t.Helper()
	reg, err := registry.Read(registry.Path(f.home))
	if err != nil {
		f.t.Fatal(err)
	}
	return reg.PinFor("claude")
}
