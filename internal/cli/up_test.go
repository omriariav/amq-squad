package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// upFake is `up`'s world: a fake amq (coop init and session create make the
// directories), a fake tmux (panes and sessions in maps; new-session plays
// the lead by logging the roster's outcome), the tagged binary's version,
// pi verify and doctor, and the signals sent. No real tmux, AMQ or process
// is touched.
type upFake struct {
	t        *testing.T
	mu       sync.Mutex
	proj     string
	root     string
	calls    []string
	panes    map[string]bool
	sessions map[string]bool
	signals  []string
	onSignal func(pid int, sig syscall.Signal)
	newEnv   []string // the -e values of tmux new-session
	newCmd   []string
	version  string
	outcome  string // the decision the lead's scheduler logs once new-session runs
}

func (f *upFake) run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := filepath.Base(name)
	f.calls = append(f.calls, base+" "+strings.Join(args, " "))
	fail := fmt.Errorf("exit status 1")
	switch {
	case base == "amq-squad" && len(args) > 0 && args[0] == "version":
		return []byte("amq-squad " + f.version + "\n"), nil
	case base == "amq-squad" && len(args) > 1 && args[0] == "pi" && args[1] == "verify":
		return []byte(`{"schema_version":1,"kind":"pi_verify","data":{"ok":false,"error":"runtime x is not unpacked yet"}}` + "\nerror: runtime x is not unpacked yet\n"), fail
	case base == "amq-squad" && len(args) > 0 && args[0] == "doctor":
		return []byte(`{"schema_version":1,"kind":"doctor","data":{"team_home":"x","checks":[{"name":"amq version","status":"ok"},{"name":"codex trust","status":"warn"}]}}`), nil
	case base == "amq" && args[0] == "coop" && args[1] == "init":
		r := args[slices.Index(args, "--root")+1]
		if !filepath.IsAbs(r) {
			r = filepath.Join(dir, r)
		}
		mustWrite(f.t, filepath.Join(r, "meta", "config.json"), `{"version":1,"agents":["lead"]}`)
		return []byte(`{}`), nil
	case base == "amq" && args[0] == "session" && args[1] == "create":
		mustMkdir(f.t, filepath.Join(args[slices.Index(args, "--root")+1], args[2], "agents"))
		return []byte("Created session\n"), nil
	case base == "tmux" && args[0] == "display-message":
		pane := args[slices.Index(args, "-t")+1]
		if f.panes[pane] {
			return []byte(pane + "\n"), nil
		}
		return []byte("can't find pane: " + pane), fail
	case base == "tmux" && args[0] == "has-session":
		if f.sessions[strings.TrimPrefix(args[2], "=")] {
			return nil, nil
		}
		return []byte("can't find session"), fail
	case base == "tmux" && args[0] == "kill-session":
		delete(f.sessions, strings.TrimPrefix(args[2], "="))
		return nil, nil
	case base == "tmux" && args[0] == "new-session":
		name := args[slices.Index(args, "-s")+1]
		if f.sessions[name] {
			return []byte("duplicate session: " + name), fail
		}
		f.sessions[name] = true
		for i, a := range args {
			if a == "-e" {
				f.newEnv = append(f.newEnv, args[i+1])
			}
		}
		f.newCmd = args[len(args)-3:]
		if f.outcome != "" {
			_ = schedproc.AppendDecision(f.root, time.Now(), scheduler.Decision{Kind: f.outcome, Reason: "launch L1: 1 seats in one apply"})
		}
		return nil, nil
	}
	return nil, fmt.Errorf("upFake: unexpected %s %v", name, args)
}

func (f *upFake) callIndex(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// upWorld sets up a repository with the fixture squad.yaml, a tagged build
// at build/pi/amq-squad, an installed amq-squad on a temp PATH dir, and
// every seam of up faked.
func upWorld(t *testing.T) (*upFake, string) {
	t.Helper()
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	writeSquadProject(t, proj, fixtureSquadYAML, "implementer")
	mustMkdir(t, filepath.Join(proj, "wt-impl-1"))
	mustWrite(t, filepath.Join(proj, "build", "pi", "amq-squad"), "#!/bin/sh\necho tagged build\n")
	bin, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(bin, "amq-squad")
	mustWrite(t, target, "#!/bin/sh\necho old build\n")
	f := &upFake{t: t, proj: proj, root: filepath.Join(proj, ".agent-mail", "s1"), panes: map[string]bool{}, sessions: map[string]bool{},
		version: "v3.1.0", outcome: "roster_launched"}
	prev := []any{squadRun, upLookPath, upPIDAlive, upStartToken, upProcArgs, upSignal, upPoll, upStopWait}
	t.Cleanup(func() {
		squadRun = prev[0].(squadRunner)
		upLookPath = prev[1].(func(string) (string, error))
		upPIDAlive = prev[2].(func(int) bool)
		upStartToken = prev[3].(func(int) (string, bool))
		upProcArgs = prev[4].(func(int) (string, bool))
		upSignal = prev[5].(func(int, syscall.Signal) error)
		upPoll, upStopWait = prev[6].(time.Duration), prev[7].(time.Duration)
	})
	squadRun = f.run
	upLookPath = func(name string) (string, error) {
		switch name {
		case "amq-squad":
			return target, nil
		case "amq":
			return "/opt/fake/bin/amq", nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
	upPIDAlive = func(int) bool { return false }
	upStartToken = func(int) (string, bool) { return "", false }
	upProcArgs = func(int) (string, bool) { return "", false }
	upSignal = func(pid int, sig syscall.Signal) error {
		f.mu.Lock()
		f.signals = append(f.signals, fmt.Sprintf("%d %s", pid, sig))
		on := f.onSignal
		f.mu.Unlock()
		if on != nil {
			on(pid, sig)
		}
		return nil
	}
	upPoll, upStopWait = 5*time.Millisecond, 2*time.Second
	return f, target
}

func runUpJSON(t *testing.T, proj string, extra ...string) (upReport, error) {
	t.Helper()
	stdout, _, err := captureOutput(t, func() error {
		return runUp(append([]string{"--project", proj, "--json", "--wait", "5s"}, extra...), "v3.1.0")
	})
	var env struct {
		Data upReport `json:"data"`
	}
	if jerr := json.Unmarshal([]byte(stdout), &env); jerr != nil {
		t.Fatalf("up --json: %v\n%s", jerr, stdout)
	}
	return env.Data, err
}

func stepNames(r upReport) string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name+":"+s.Outcome)
	}
	return strings.Join(out, " ")
}

func decisionKinds(t *testing.T, root string) []schedproc.DecisionLine {
	t.Helper()
	lines, err := schedproc.ReadDecisionsTail(root)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func holdLock(t *testing.T, path string) *flock.Exclusive {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	l, ok, err := flock.TryExclusive(path, true)
	if err != nil || !ok {
		t.Fatalf("hold %s: %v %v", path, ok, err)
	}
	return l
}

func writeLeadRecord(t *testing.T, root string, rec schedproc.LeadRecord) {
	t.Helper()
	rec.Schema = 1
	raw, _ := json.Marshal(rec)
	mustWrite(t, filepath.Join(schedproc.Dir(root), schedproc.LeadRecordName), string(raw))
}

// Brief 31-1's second PR, row 7: `up` on a repository with no AMQ root
// creates the root (amq coop init in the repository) and the session (amq
// session create), records it as a decision, and launches: the lead starts
// in its tmux session and the roster's outcome ends the wait.
func TestUpOnAProjectWithNoAMQRootCreatesItAndLaunches(t *testing.T) {
	f, _ := upWorld(t)
	if _, err := os.Stat(filepath.Join(f.proj, ".agent-mail")); err == nil {
		t.Fatal("the fixture has an AMQ root")
	}
	r, err := runUpJSON(t, f.proj)
	if err != nil {
		t.Fatalf("up: %v\n%+v", err, r.Steps)
	}
	if f.callIndex("amq coop init --root .agent-mail --agents impl-1,lead,user") < 0 || f.callIndex("amq session create s1 --root "+filepath.Join(f.proj, ".agent-mail")) < 0 {
		t.Fatalf("calls %q", f.calls)
	}
	if st, err := os.Stat(f.root); err != nil || !st.IsDir() {
		t.Fatalf("session root %s: %v", f.root, err)
	}
	created := false
	for _, d := range decisionKinds(t, f.root) {
		created = created || (d.Kind == KindSessionCreated && strings.Contains(d.Reason, "amq session create s1"))
	}
	if !created || f.callIndex("tmux new-session") < 0 || r.Steps[len(r.Steps)-1].Detail == "" || !strings.HasPrefix(r.Steps[len(r.Steps)-1].Detail, "roster_launched") {
		t.Fatalf("created %v; steps %+v; calls %q", created, r.Steps, f.calls)
	}
}

// Row 13: the seven steps run in order against the fake tmux and AMQ; the
// install copies the tagged build to a temporary name and renames it over
// the installed binary, never writing the old file in place (its inode is
// replaced and an open descriptor still reads the old bytes); the lead's
// tmux session never matches AMQ's amq-* sessions and carries the start
// stamp; a second run on a session root that exists skips its creation.
func TestUpRunsItsSevenStepsInOrderAndInstallsByRename(t *testing.T) {
	f, target := upWorld(t)
	old, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before, _ := os.Stat(target)
	r, err := runUpJSON(t, f.proj)
	if err != nil {
		t.Fatalf("up: %v\n%+v", err, r.Steps)
	}
	if got := stepNames(r); got != "install:ok doctor:ok session:ok compile:ok lead:ok tmux:ok roster:ok" {
		t.Fatalf("steps %s", got)
	}
	order := []string{"amq-squad version", "amq-squad pi verify", "amq-squad doctor --json", "amq coop init", "amq session create", "tmux has-session", "tmux new-session"}
	last := -1
	for _, c := range order {
		i := f.callIndex(c)
		if i <= last {
			t.Fatalf("%q at %d after %d: calls %q", c, i, last, f.calls)
		}
		last = i
	}
	after, _ := os.Stat(target)
	if os.SameFile(before, after) {
		t.Fatal("the installed binary keeps its inode: it was written over in place")
	}
	oldBytes, _ := io.ReadAll(old)
	newBytes, _ := os.ReadFile(target)
	if string(oldBytes) != "#!/bin/sh\necho old build\n" || string(newBytes) != "#!/bin/sh\necho tagged build\n" || after.Mode().Perm()&0o111 == 0 {
		t.Fatalf("old descriptor reads %q, target %q mode %v", oldBytes, newBytes, after.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".amq-squad.up-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
	name := upTmuxSessionName(f.proj)
	if r.TmuxSession != name || strings.HasPrefix(name, "amq-") || !strings.HasPrefix(name, "squad-lead-") || r.Attach != "tmux attach -t "+name {
		t.Fatalf("tmux session %q attach %q", r.TmuxSession, r.Attach)
	}
	if !slices.Contains(f.newEnv, leadStartEnv+"="+r.Start.Format(time.RFC3339Nano)) || !slices.Equal(f.newCmd, []string{target, "lead", "--team"}) {
		t.Fatalf("new-session env %q cmd %q", f.newEnv, f.newCmd)
	}
	for _, p := range []string{"amq-squad", "amq-x", "AMQ-Thing", strings.Repeat("amq-", 20)} {
		n := upTmuxSessionName(filepath.Join("/repos", p))
		if strings.HasPrefix(n, "amq-") || !strings.HasPrefix(n, "squad-lead-") || len(n) > len("squad-lead-")+30+6 {
			t.Errorf("session name %q for %q", n, p)
		}
	}
	// A second up: the root exists (no creation), the dead lead's tmux
	// session is a leftover and is stopped, and the lead starts again.
	f.calls = nil
	r, err = runUpJSON(t, f.proj, "--skip-install")
	if err != nil {
		t.Fatalf("second up: %v %+v", err, r.Steps)
	}
	if got := stepNames(r); got != "install:skipped doctor:ok session:skipped compile:ok lead:ok tmux:ok roster:ok" || f.callIndex("amq coop init") >= 0 || f.callIndex("tmux kill-session -t ="+name) < 0 {
		t.Fatalf("second run steps %s; calls %q", got, f.calls)
	}
}

// Row 13 (the stops): a lead whose pid is dead (its lead lock free) leaves
// its tmux session behind, which up kills; the scheduler still holding
// run.lock serves that dead lead, so its lead_gone check has passed and up
// stops it; each stop is a decision.
func TestUpStopsADeadLeadsSessionAndItsScheduler(t *testing.T) {
	f, _ := upWorld(t)
	mustMkdir(t, filepath.Join(f.root, "agents"))
	mustWrite(t, filepath.Join(f.proj, ".agent-mail", "meta", "config.json"), `{}`)
	writeLeadRecord(t, f.root, schedproc.LeadRecord{PID: 999991, Start: "s1", Pane: "%7", At: time.Now()})
	if err := schedproc.WriteLaunchRecord(f.root, schedproc.LaunchRecord{Lead: "lead", Root: f.root, PID: 999992, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.sessions[upTmuxSessionName(f.proj)] = true
	run := holdLock(t, filepath.Join(schedproc.Dir(f.root), "run.lock"))
	upPIDAlive = func(pid int) bool { return pid == 999992 }
	upProcArgs = func(pid int) (string, bool) {
		return "/usr/local/bin/amq-squad scheduler run --project " + f.proj, pid == 999992
	}
	f.onSignal = func(pid int, sig syscall.Signal) {
		if pid == 999992 && sig == syscall.SIGTERM {
			_ = run.Close() // the scheduler exits on SIGTERM
		}
	}
	r, err := runUpJSON(t, f.proj)
	if err != nil {
		t.Fatalf("up: %v %+v", err, r.Steps)
	}
	if !slices.Equal(f.signals, []string{"999992 terminated"}) || f.callIndex("tmux kill-session") < 0 {
		t.Fatalf("signals %q calls %q", f.signals, f.calls)
	}
	var stopped []string
	for _, d := range decisionKinds(t, f.root) {
		if d.Kind == KindUpStoppedLead || d.Kind == KindUpStoppedScheduler {
			stopped = append(stopped, d.Kind+": "+d.Reason)
		}
	}
	if len(stopped) != 2 || !strings.Contains(stopped[0], "lead pid 999991 is dead") || !strings.Contains(stopped[1], "pid 999992: lead_gone") {
		t.Fatalf("stop decisions %q", stopped)
	}
}

// Row 13 (the stops): a lead whose pid is alive but whose tmux pane is gone
// is stopped (SIGTERM to the recorded pid with its start stamp), and the
// stop is a decision.
func TestUpStopsALeadWhosePaneIsGone(t *testing.T) {
	f, _ := upWorld(t)
	mustMkdir(t, filepath.Join(f.root, "agents"))
	mustWrite(t, filepath.Join(f.proj, ".agent-mail", "meta", "config.json"), `{}`)
	lead := holdLock(t, filepath.Join(schedproc.Dir(f.root), schedproc.LeadLockName))
	writeLeadRecord(t, f.root, schedproc.LeadRecord{PID: 999993, Start: "st", Pane: "%9", At: time.Now()})
	upPIDAlive = func(pid int) bool { return pid == 999993 }
	upStartToken = func(pid int) (string, bool) { return "st", pid == 999993 }
	f.onSignal = func(pid int, sig syscall.Signal) {
		if pid == 999993 {
			_ = lead.Close()
		}
	}
	r, err := runUpJSON(t, f.proj)
	if err != nil {
		t.Fatalf("up: %v %+v", err, r.Steps)
	}
	if !slices.Equal(f.signals, []string{"999993 terminated"}) {
		t.Fatalf("signals %q", f.signals)
	}
	found := false
	for _, d := range decisionKinds(t, f.root) {
		found = found || (d.Kind == KindUpStoppedLead && strings.Contains(d.Reason, "its tmux pane %9 is gone"))
	}
	if !found {
		t.Fatalf("no stop decision: %+v", decisionKinds(t, f.root))
	}
}

// Row 18: against a live lead (its lead lock held, its pid alive, its tmux
// pane present) up refuses lead_running with the pid, the pane and the
// attach command, and touches neither the lead nor its scheduler: no
// signal, no tmux kill, no new session, both locks still held.
func TestUpAgainstALiveLeadRefusesLeadRunningAndTouchesNothing(t *testing.T) {
	f, _ := upWorld(t)
	mustMkdir(t, filepath.Join(f.root, "agents"))
	mustWrite(t, filepath.Join(f.proj, ".agent-mail", "meta", "config.json"), `{}`)
	leadLock := filepath.Join(schedproc.Dir(f.root), schedproc.LeadLockName)
	runLock := filepath.Join(schedproc.Dir(f.root), "run.lock")
	lead := holdLock(t, leadLock)
	defer lead.Close()
	sched := holdLock(t, runLock)
	defer sched.Close()
	writeLeadRecord(t, f.root, schedproc.LeadRecord{PID: 999994, Start: "st", Pane: "%4", At: time.Now()})
	if err := schedproc.WriteLaunchRecord(f.root, schedproc.LaunchRecord{Lead: "lead", Root: f.root, PID: 999995, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.panes["%4"] = true
	f.sessions[upTmuxSessionName(f.proj)] = true
	upPIDAlive = func(int) bool { return true }
	upStartToken = func(pid int) (string, bool) { return "st", true }
	r, err := runUpJSON(t, f.proj)
	if err == nil {
		t.Fatalf("up against a live lead succeeded: %+v", r.Steps)
	}
	step := r.Steps[len(r.Steps)-1]
	if step.Name != "lead" || step.Refusal != schedproc.ReasonLeadRunning || !strings.Contains(step.Detail, "pid 999994") ||
		!strings.Contains(step.Detail, "tmux pane %4") || !strings.Contains(step.Detail, "tmux attach -t %4") {
		t.Fatalf("refusal %+v", step)
	}
	if len(f.signals) != 0 || f.callIndex("tmux kill-session") >= 0 || f.callIndex("tmux new-session") >= 0 {
		t.Fatalf("the live lead was touched: signals %q calls %q", f.signals, f.calls)
	}
	for _, p := range []string{leadLock, runLock} {
		if held, _ := lockHeld(p); !held {
			t.Fatalf("%s is no longer held", p)
		}
	}
	for _, d := range decisionKinds(t, f.root) {
		if d.Kind == KindUpStoppedLead || d.Kind == KindUpStoppedScheduler {
			t.Fatalf("a stop was recorded: %+v", d)
		}
	}
}

// The install refuses by name: a build that does not name a tag, one that
// carries no pi runtime, and a rejected runtime.
func TestUpInstallRefusesAnUntaggedOrUnembeddedBuildByName(t *testing.T) {
	for _, tc := range []struct {
		version, verify, want string
	}{
		{"v3.0.2-14-gabcdef1", "", "pi_binary_untagged"},
		{"v3.1.0-dirty", "", "pi_binary_untagged"},
		{"dev", "", "pi_binary_untagged"},
		{"v3.1.0", "error: this amq-squad build carries no pi runtime (build with scripts/build-pi-runtime.sh, tag amqpi)", "pi_not_embedded"},
		{"v3.1.0", "error: pi runtime /h/.amq-squad/runtime/x rejected (run `amq-squad doctor --fix` or remove it explicitly to repair): changed", "pi_runtime_rejected"},
	} {
		f, target := upWorld(t)
		f.version = tc.version
		if tc.verify != "" {
			inner := f.run
			squadRun = func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
				if len(args) > 1 && args[0] == "pi" {
					return []byte(tc.verify), fmt.Errorf("exit status 1")
				}
				return inner(ctx, dir, env, name, args...)
			}
		}
		before, _ := os.ReadFile(target)
		r, err := runUpJSON(t, f.proj)
		if err == nil || len(r.Steps) != 1 || r.Steps[0].Refusal != tc.want {
			t.Errorf("%s / %q: %v %+v", tc.version, tc.verify, err, r.Steps)
		}
		if after, _ := os.ReadFile(target); string(after) != string(before) {
			t.Errorf("%s: the installed binary changed on a refusal", tc.version)
		}
	}
}

// Step 2 refuses on a failing doctor row by its name, read from the
// envelope a failing doctor prints before its error line.
func TestUpDoctorRefusesAFailingRowByName(t *testing.T) {
	f, _ := upWorld(t)
	inner := f.run
	squadRun = func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "doctor" {
			return []byte(`{"schema_version":1,"kind":"doctor","data":{"team_home":"x","checks":[{"name":"amq version","status":"fail","detail":"amq 0.99.0 is below the floor 0.116.2"}]}}` +
				"\nerror: doctor: 1 check(s) failed\n"), fmt.Errorf("exit status 2")
		}
		return inner(ctx, dir, env, name, args...)
	}
	r, err := runUpJSON(t, f.proj)
	if err == nil || stepNames(r) != "install:ok doctor:refused" || r.Steps[1].Refusal != "doctor_failed" || !strings.Contains(r.Steps[1].Detail, `"amq version"`) {
		t.Fatalf("%v %+v", err, r.Steps)
	}
	if f.callIndex("amq coop init") >= 0 || f.callIndex("tmux") >= 0 {
		t.Fatalf("a step ran after the refusal: %q", f.calls)
	}
}
