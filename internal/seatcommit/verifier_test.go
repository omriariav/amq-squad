package seatcommit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/ledger"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The verdict producer's rows (v3-5-verifier). Each verifier is a /bin/sh
// script the test writes outside the seat's tree and names in the grant by
// its absolute path; PATH inside the verifier is the tools directory only,
// so the scripts call tools by absolute path.

// script writes an executable /bin/sh script outside the repository.
func (f *fixture) script(name, body string) string {
	f.t.Helper()
	dir := filepath.Join(f.dir, "verifiers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// runWithin runs the step with a deadline; past it the process in pidFile
// is killed so the step returns, and ok is false.
func (f *fixture) runWithin(tk task.Task, d time.Duration, pidFile string) (Result, bool) {
	done := make(chan Result, 1)
	go func() { done <- f.run(tk) }()
	select {
	case res := <-done:
		return res, true
	case <-time.After(d):
		killPIDFile(pidFile)
		return <-done, false
	}
}

// killPIDFile kills the process whose pid a verifier wrote.
func killPIDFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); pid > 1 {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	}
}

// verifying configures the grant's implement verifier.
func (f *fixture) verifying(command string) {
	f.grants.Verifiers = map[string]string{"implement": command}
}

// reportedBare is reported with no verdict recorded: the commit step is the
// verdict's producer.
func (f *fixture) reportedBare(files ...string) task.Task {
	f.t.Helper()
	tk, err := f.store.Add(task.AddInput{Title: "the task", Class: "implement", DeclaredFiles: files, ProjectPath: f.main}, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
		f.t.Fatal(err)
	}
	if tk, err = f.store.Done(tk.ID, f.handle, "", "", f.now); err != nil {
		f.t.Fatal(err)
	}
	return tk
}

// evidence is the verifier's line in the seat's evidence ledger.
func (f *fixture) evidence() (verifierLine, bool) {
	f.t.Helper()
	fh, err := os.Open(ledger.Path(f.root, f.handle))
	if err != nil {
		return verifierLine{}, false
	}
	defer fh.Close()
	var last verifierLine
	found := false
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var l verifierLine
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Event == EventVerifier {
			last, found = l, true
		}
	}
	return last, found
}

func (f *fixture) lease(id string) *task.Lease {
	f.t.Helper()
	got, err := f.store.Get(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return got.Lease
}

// Rule 1: the verifier runs after the snapshot in the shadow worktree,
// checked out at the snapshot tree, never in the seat's live worktree: an
// ignored file of the live worktree is not there, the seat's change is, and
// the working directory is not the worktree.
func TestVerifierRunsInTheShadowWorktreeAtTheSnapshotTree(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// changed by the seat\n")
	write(t, filepath.Join(f.wt, ".gitignore"), "scratch.txt\n")
	write(t, filepath.Join(f.wt, "scratch.txt"), "live worktree only\n")
	f.verifying(f.script("v", "pwd\nif [ -e scratch.txt ]; then echo scratch-present; else echo scratch-absent; fi\n/bin/cat cmd/root.go\n"))
	tk := f.reportedBare("cmd/**", ".gitignore")
	res := f.run(tk)
	if !res.OK || res.Verifier == nil || res.Verifier.Result != task.VerdictPass {
		t.Fatalf("the step: ok %v %s %s", res.OK, res.ReasonCode, res.Detail)
	}
	l, ok := f.evidence()
	if !ok {
		t.Fatal("no verifier line in the evidence ledger")
	}
	out := l.Payload.Output
	first := strings.SplitN(out, "\n", 2)[0]
	if first == f.wt || strings.HasPrefix(first, f.wt+"/") || !strings.HasSuffix(first, "/work") {
		t.Fatalf("the verifier ran in %q, not a shadow worktree", first)
	}
	if !strings.Contains(out, "scratch-absent") || !strings.Contains(out, "// changed by the seat") {
		t.Fatal("the verifier's checkout is not the snapshot tree")
	}
	if l.Payload.Tree != res.Tree || f.lease(tk.ID).Verdict.Tree != res.Tree {
		t.Fatal("the verdict does not name the snapshot tree")
	}
}

func TestVerifierCheckoutNeverMaterializesARefusedSymlink(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink("CLAUDE.md", filepath.Join(f.wt, "bad-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rp, err := worktreePaths(f.wt)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := newShadow(gitBin(t), f.testGitEnv(), rp)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.close()
	tree, err := snapshot(sh, f.base)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(f.dir, "refused-checkout")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	err = checkoutTree(f.opts(task.Task{}), sh, tree, out)
	if !errors.Is(err, errLinkTargetRefused) || !strings.Contains(err.Error(), "bad-link") || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Fatalf("checkout refusal = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(out, "bad-link")); !os.IsNotExist(err) {
		t.Fatalf("refused link was materialized: %v", err)
	}
}

// Rule 1, the exit code is the verdict: exit 3 is a fail, exit 0 a pass.
func TestVerifierExitCodeIsTheVerdict(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// red\n")
	f.verifying(f.script("v", "echo failing\nexit 3\n"))
	tk := f.reportedBare("cmd/**")
	res := f.run(tk)
	if res.OK || res.ReasonCode != task.BlockVerdictFailed || res.Verifier == nil || res.Verifier.ExitCode != 3 {
		t.Fatalf("exit 3: ok %v %s", res.OK, res.ReasonCode)
	}
	if v := f.lease(tk.ID).Verdict; v == nil || v.Result != task.VerdictFail || v.CommandDigest != task.CommandDigest(f.grants.Verifiers["implement"]) {
		t.Fatal("exit 3 recorded no fail verdict with the command's digest")
	}
}

// Rule 1, the wall-time bound: over verifier_timeout is a fail verdict,
// verifier_timeout.
func TestVerifierTimeoutIsAFailVerdict(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// slow\n")
	pidf := filepath.Join(f.dir, "hung.pid")
	t.Cleanup(func() { killPIDFile(pidf) })
	f.verifying(f.script("v", "echo $$ > "+pidf+"\nexec /bin/sleep 600\n"))
	f.grants.VerifierTimeout = time.Second
	tk := f.reportedBare("cmd/**")
	start := time.Now()
	res, ok := f.runWithin(tk, 30*time.Second, pidf)
	if !ok {
		t.Fatal("the verifier ran past its bound")
	}
	if res.ReasonCode != task.BlockVerdictFailed || res.Verifier == nil || res.Verifier.Reason != ReasonVerifierTimeout {
		t.Fatalf("a hung verifier: %s %+v", res.ReasonCode, res.Verifier)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatalf("the bound did not hold: %s", time.Since(start).Round(time.Second))
	}
	if f.originBranch() != "" {
		t.Fatal("a timed-out verifier's diff was pushed")
	}
}

// Rule 1, the output bound and the evidence: over verifier_output_limit is a
// fail verdict, verifier_output_exceeded; the ledger keeps the output
// truncated at the limit with the marker, through the redaction step.
func TestVerifierOutputIsBoundedAndRedactedIntoTheLedger(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// loud\n")
	// A credential-shaped value built at run time (never a literal in the
	// source): the redaction step must replace it.
	shaped := "xox" + "b-" + "Zq81Rt0LmN4pW7"
	f.verifying(f.script("v", "echo token="+shaped+"\ni=0\nwhile [ $i -lt 2000 ]; do echo 0123456789abcdef0123456789abcdef; i=$((i+1)); done\n"))
	f.grants.VerifierOutputLimit = 4096
	tk := f.reportedBare("cmd/**")
	res := f.run(tk)
	if res.ReasonCode != task.BlockVerdictFailed || res.Verifier == nil || res.Verifier.Reason != ReasonVerifierOutput || !res.Verifier.Truncated {
		t.Fatalf("a verifier over its output limit: %s %+v", res.ReasonCode, res.Verifier)
	}
	l, ok := f.evidence()
	if !ok {
		t.Fatal("no verifier line in the evidence ledger")
	}
	out := l.Payload.Output
	if strings.Contains(out, shaped) || !(strings.Contains(out, redact.Placeholder) || strings.Contains(out, "token=[redacted]")) {
		t.Fatal("the ledger's verifier output did not pass the redaction step")
	}
	if !strings.Contains(out, "[amq-squad: verifier output truncated at 4096 bytes; the partial last line was dropped]") || len(out) > 4096+200 {
		t.Fatalf("the output is not truncated at the limit with the marker (%d bytes)", len(out))
	}
}

// Rule 4, the lease-time change: a class the squad's gate names with no
// grant command (the grant changed after the dispatch) refuses at the commit
// step with verdict_missing; nothing is pushed.
func TestLeaseTimeVerifierChangeStillBlocksTheDoneReport(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// x\n")
	tk := f.reportedBare("cmd/**")
	f.grants.VerifierGate = []string{"implement"}
	res := f.run(tk)
	if res.OK || res.ReasonCode != task.BlockVerdictMissing || !strings.Contains(res.Detail, "names no verifier command") {
		t.Fatalf("a gated class with no command at the commit step: ok %v %s", res.OK, res.ReasonCode)
	}
	if f.originBranch() != "" {
		t.Fatal("a done report with no verdict was pushed")
	}
	after, err := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	if err != nil || after.State != task.StateBlocked || after.BlockReason != task.BlockVerdictMissing {
		t.Fatalf("the done report did not block: %s %s %v", after.State, after.BlockReason, err)
	}
}

// Rule 5, at the commit step: a class with no configured verifier and not
// gated runs nothing and commits and pushes as before.
func TestUnconfiguredVerifierRunsNothingAtTheCommitStep(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// y\n")
	tk := f.reported("cmd/**")
	res := f.run(tk)
	if !res.OK || !res.Pushed || res.Verifier != nil {
		t.Fatalf("no verifier configured: ok %v pushed %v %+v", res.OK, res.Pushed, res.Verifier)
	}
	if _, found := f.evidence(); found {
		t.Fatal("a verifier ran with none configured")
	}
}

// Rule 6: a fail verdict blocks verdict_failed with at most 4 KiB of the
// redacted output's tail in the block detail, the full output in the
// evidence ledger, the attempt failed (its verdict fail), and no push. A
// pass completes and the commit step pushes.
func TestFailVerdictBlocksWithTheTailAndNoPush(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// red\n")
	shaped := "xox" + "b-" + "Zq81Rt0LmN4pW7" // built at run time, never a literal
	f.verifying(f.script("v", "echo FIRST-LINE\ni=0\nwhile [ $i -lt 400 ]; do echo line-$i-0123456789abcdef; i=$((i+1)); done\necho seen "+shaped+"\necho LAST-LINE\nexit 1\n"))
	tk := f.reportedBare("cmd/**")
	res := f.run(tk)
	if res.ReasonCode != task.BlockVerdictFailed || res.Pushed || f.originBranch() != "" {
		t.Fatalf("a fail verdict: %s pushed %v", res.ReasonCode, res.Pushed)
	}
	after, err := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if after.BlockReason != task.BlockVerdictFailed || after.BlockRoute != task.RouteLead || len(after.BlockDetail) > task.VerdictDetailCap {
		t.Fatalf("the block: %s %s, %d bytes of detail", after.BlockReason, after.BlockRoute, len(after.BlockDetail))
	}
	if strings.Contains(after.BlockDetail, shaped) {
		t.Fatal("the block detail carries a credential: the tail is not the redacted output")
	}
	if !strings.Contains(after.BlockDetail, "LAST-LINE") || strings.Contains(after.BlockDetail, "FIRST-LINE") {
		t.Fatal("the block detail is not the output's tail")
	}
	l, _ := f.evidence()
	if !strings.Contains(l.Payload.Output, "FIRST-LINE") || !strings.Contains(l.Payload.Output, "LAST-LINE") {
		t.Fatal("the evidence ledger does not keep the full output")
	}
	if n := len(after.Attempts); n == 0 || after.Attempts[n-1].Verdict == nil || after.Attempts[n-1].Verdict.Result != task.VerdictFail {
		t.Fatal("the attempt does not carry the fail verdict for escalation")
	}
}

func TestPassVerdictCompletesAndPushes(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// green\n")
	f.verifying(f.script("v", "echo ok\n"))
	tk := f.reportedBare("cmd/**")
	res := f.run(tk)
	if !res.OK || !res.Pushed || f.originBranch() != res.Commit {
		t.Fatalf("a pass verdict: ok %v pushed %v %s", res.OK, res.Pushed, res.Detail)
	}
	after, err := f.store.CompleteBound(tk.ID, fixtureRepo, 0, res.Commit, f.now)
	if err != nil || after.State != task.StateCompleted {
		t.Fatalf("a pass verdict did not complete: %s %s %v", after.State, after.BlockReason, err)
	}
	if v := after.Attempts[len(after.Attempts)-1].Verdict; v == nil || v.Tag == "" || v.Tree != res.Tree || v.Source != VerifierSource {
		t.Fatal("the completed attempt does not carry the commit step's sealed verdict")
	}
}

// Rule 7, the order: the diff check refuses before the verifier runs, and
// the verifier runs before the push.
func TestVerifierOrderInsideTheCommitStep(t *testing.T) {
	f := newFixture(t)
	ref := filepath.Join(f.origin, "refs", "heads", f.branch)
	f.verifying(f.script("v", "if [ -e "+ref+" ]; then echo pushed-before-the-verifier; else echo not-pushed-yet; fi\n"))
	write(t, filepath.Join(f.wt, "README.md"), "outside the declared set\n")
	tk := f.reportedBare("cmd/**")
	if res := f.run(tk); res.ReasonCode != ReasonOutside {
		t.Fatalf("a diff outside the declared set: %s", res.ReasonCode)
	}
	if _, found := f.evidence(); found {
		t.Fatal("the verifier ran before the diff check refused")
	}
	g := newFixture(t)
	ref = filepath.Join(g.origin, "refs", "heads", TaskBranch(g.handle, "t1"))
	g.verifying(g.script("v", "if [ -e "+ref+" ]; then echo pushed-before-the-verifier; else echo not-pushed-yet; fi\n"))
	write(t, filepath.Join(g.wt, "cmd", "root.go"), "package cmd\n\n// z\n")
	res := g.run(g.reportedBare("cmd/**"))
	l, _ := g.evidence()
	if !res.OK || !res.Pushed || !strings.Contains(l.Payload.Output, "not-pushed-yet") {
		t.Fatalf("the verifier did not run before the push: ok %v pushed %v", res.OK, res.Pushed)
	}
}

// Rule 8: the verifier's environment is a seat's: none of the harness's
// identity or credential names, a scratch HOME, GOCACHE and GOTMPDIR under
// the private TMPDIR, GOENV=off, PATH the tools directory only, and the
// hardened git variables. Failures name the variable, never a value.
func TestVerifierEnvironmentIsASeats(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"AM_ME", "AM_ROOT", "AMQ_SQUAD_BIN", "CLAUDE_CONFIG_DIR", "GH_TOKEN", "SSH_AUTH_SOCK", "AMQ_SQUAD_SEAT"} {
		t.Setenv(name, "set-by-the-test")
	}
	t.Setenv("GOCACHE", filepath.Join(f.dir, "owner-cache"))
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// env\n")
	f.verifying(f.script("v", "/usr/bin/env\n"))
	res := f.run(f.reportedBare("cmd/**"))
	if !res.OK {
		t.Fatalf("the step: %s %s", res.ReasonCode, res.Detail)
	}
	l, _ := f.evidence()
	env := map[string]string{}
	for _, line := range strings.Split(l.Payload.Output, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for name := range env {
		if strings.HasPrefix(name, "AM_") || strings.HasPrefix(name, "AMQ_") || strings.HasPrefix(name, "CLAUDE_") || strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "SSH_") || redact.CredentialName(name) {
			t.Errorf("the verifier's environment carries %s", name)
		}
	}
	tmp := env["TMPDIR"]
	under := func(name string) {
		if tmp == "" || !strings.HasPrefix(env[name], filepath.Dir(tmp)+"/") {
			t.Errorf("%s is not under the verifier's private TMPDIR", name)
		}
	}
	for _, name := range []string{"HOME", "GOCACHE", "GOTMPDIR"} {
		under(name)
	}
	if env["GOENV"] != "off" || env["PATH"] != ToolsDir(f.root) || env["GIT_CONFIG_GLOBAL"] != os.DevNull || env["GIT_CONFIG_NOSYSTEM"] != "1" {
		t.Error("GOENV, PATH or the hardened git variables are not the seat class's")
	}
}

// Rule 9: argv[0] is an absolute path in the grant or a registry pin; a
// `go test` with no pin for go is grants_invalid (nothing from PATH); a
// relative path is grants_invalid; a pin runs.
func TestVerifierToolIsAnAbsolutePathOrAPin(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// pin\n")
	tk := f.reportedBare("cmd/**")
	f.verifying("go test ./...")
	if res := f.run(tk); res.ReasonCode != ReasonGrants || !strings.Contains(res.Detail, `"go"`) {
		t.Fatalf("go test with no pin for go: %s", res.ReasonCode)
	}
	f.verifying("./run-tests.sh")
	if res := f.run(tk); res.ReasonCode != ReasonGrants {
		t.Fatalf("a relative path: %s", res.ReasonCode)
	}
	p := f.script("vt", "echo pinned-ran\n")
	sum, err := registry.FileSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Write(registry.Path(f.home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "vt", Path: p, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	f.verifying("vt --flag")
	res := f.run(tk)
	l, _ := f.evidence()
	if !res.OK || !strings.Contains(l.Payload.Output, "pinned-ran") {
		t.Fatalf("a pinned verifier: ok %v %s", res.OK, res.ReasonCode)
	}
}

// Rule 12, concurrency: past verifier_parallelism the commit step waits with
// a status row, and runs once a slot frees.
func TestVerifierParallelismWaitsWithAStatusRow(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// queued\n")
	f.verifying(f.script("v", "echo ran\n"))
	f.grants.VerifierParallelism = 1
	tk := f.reportedBare("cmd/**")
	if err := os.MkdirAll(VerifierDir(f.root), 0o700); err != nil {
		t.Fatal(err)
	}
	held, ok, err := flock.TryExclusive(filepath.Join(VerifierDir(f.root), "slot-0.lock"), true)
	if err != nil || !ok {
		t.Skipf("no advisory lock: %v", err)
	}
	prev := verifierSlotPoll
	verifierSlotPoll = 20 * time.Millisecond
	t.Cleanup(func() { verifierSlotPoll = prev })
	done := make(chan Result, 1)
	go func() { done <- f.run(tk) }()
	waited := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && !waited; time.Sleep(20 * time.Millisecond) {
		for _, w := range VerifierWaiting(f.root) {
			waited = waited || (w.Seat == f.handle && w.Task == tk.ID && w.Parallelism == 1)
		}
		select {
		case res := <-done:
			held.Close()
			t.Fatalf("the step ran past the parallelism bound: ok %v", res.OK)
		default:
		}
	}
	held.Close()
	res := <-done
	if !waited {
		t.Fatal("no status row while every verifier slot was held")
	}
	if !res.OK || len(VerifierWaiting(f.root)) != 0 {
		t.Fatalf("after the slot freed: ok %v, %d waiting rows", res.OK, len(VerifierWaiting(f.root)))
	}
}
