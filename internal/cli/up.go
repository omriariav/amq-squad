package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// `amq-squad up` (brief 31-1, section 3; plan 7.4): a squad.yaml line to
// every seat live with no owner step between. Seven steps, in order, each
// refusing by name: (1) install the tagged pi binary onto PATH by temp name
// and rename; (2) the doctor checks of that binary; (3) the AMQ root and
// session; (4) squad.yaml compiled; (5) the recorded lead under the one
// rule (a lead whose pid is alive and whose pane exists is never stopped);
// (6) the lead in its own tmux session; (7) the roster's outcome from
// decisions.jsonl, then the roster from status and the attach command.

const upUsage = `amq-squad up - start this repository's squad from squad.yaml

Usage:
  amq-squad up [--project DIR] [--binary PATH | --skip-install] [--to PATH] [--wait 120s] [--json]

Seven steps, in order; each refuses by name and nothing after it runs:
  1. install: the tagged pi build (scripts/build-pi-runtime.sh at a tag;
     default <repo>/build/pi/amq-squad, or --binary PATH) is copied next to
     the amq-squad on PATH (or --to PATH) under a temporary name and renamed
     over it, never written over it in place. Refused: pi_binary_missing,
     pi_binary_untagged, pi_not_embedded, pi_runtime_rejected,
     install_target_unknown. --skip-install keeps the installed binary when
     it already reports a tag.
  2. doctor: the installed binary's doctor; a failing row refuses by name.
  3. session: the AMQ root and the squad's session (amq coop init, amq
     session create) when absent.
  4. compile: squad.yaml through the seats' roles and the squad's checks.
  5. lead: a recorded lead whose pid is alive and whose tmux pane exists is
     never stopped (refused lead_running, with the pid, the pane and the
     attach command). A lead whose pid is dead or whose pane is gone, and a
     scheduler whose lead is gone, are stopped, each stop a decision.
  6. tmux: the lead starts in its own tmux session, squad-lead-<repo>-<sha5>
     (never AMQ's amq-* sessions), running amq-squad lead --team in the
     repository, with AMQ_SQUAD_START set to this command's start.
  7. roster: decisions.jsonl until roster_launched, roster_adopted,
     roster_incomplete or roster_launch_failed (at most --wait), then the
     roster from status and the attach command.

--json prints each step and its outcome.
`

// upStep is one step's outcome.
type upStep struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // ok | skipped | refused
	Refusal string `json:"refusal,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// upReport is what `up --json` prints.
type upReport struct {
	Project     string        `json:"project"`
	Root        string        `json:"root,omitempty"`
	Session     string        `json:"session,omitempty"`
	Lead        string        `json:"lead,omitempty"`
	Binary      string        `json:"binary,omitempty"`
	Start       time.Time     `json:"start"`
	TmuxSession string        `json:"tmux_session,omitempty"`
	Attach      string        `json:"attach,omitempty"`
	Steps       []upStep      `json:"steps"`
	Roster      []status.Seat `json:"roster,omitempty"`
}

// upRefusal stops `up` at a step.
type upRefusal struct{ Name, Detail string }

func (e *upRefusal) Error() string { return e.Name + ": " + e.Detail }

func refuse(name, format string, args ...any) *upRefusal {
	return &upRefusal{Name: name, Detail: fmt.Sprintf(format, args...)}
}

// squadRunner runs one command in dir with env and returns its combined
// output: amq, tmux and the installed amq-squad (a test seam: no test runs
// the real ones).
type squadRunner func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

func runSquadCommand(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	return cmd.CombinedOutput()
}

// Seams for tests.
var (
	squadRun     squadRunner = runSquadCommand
	upLookPath               = exec.LookPath
	upPIDAlive               = procinfo.Alive
	upStartToken             = procinfo.StartToken
	upProcArgs               = procinfo.Args
	upSignal                 = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	upNow                    = time.Now
	upPoll                   = 200 * time.Millisecond
	upStopWait               = 15 * time.Second
)

// amqChildEnv is the environment of an amq child: no AMQ identity of the
// caller and no update check.
func amqChildEnv() []string { return amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ())) }

func runUp(args []string, version string) error {
	start := upNow().UTC()
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", "", "the repository whose squad.yaml is the squad (default: the repository of the working directory)")
	binary := fs.String("binary", "", "the tagged pi build to install (default <repo>/build/pi/amq-squad)")
	skipInstall := fs.Bool("skip-install", false, "keep the installed amq-squad (it must report a tag)")
	to := fs.String("to", "", "where to install (default: the amq-squad on PATH)")
	wait := fs.Duration("wait", 120*time.Second, "how long to wait for the roster's outcome")
	jsonOut := fs.Bool("json", false, "print the steps and their outcomes as JSON")
	fs.Usage = func() { fmt.Fprint(os.Stderr, upUsage) }
	if err := parseFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
		}
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("up takes no positional arguments; got %q", fs.Arg(0))
	}
	if *skipInstall && *binary != "" {
		return usageErrorf("up: --binary and --skip-install are exclusive")
	}
	dir := *project
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = projectTop(cwd)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	u := &upRun{ctx: context.Background(), report: upReport{Project: dir, Start: start, Steps: []upStep{}},
		binary: *binary, skipInstall: *skipInstall, to: *to, wait: *wait, version: version}
	runErr := u.run()
	out := os.Stdout
	if *jsonOut {
		if err := writeJSONEnvelope(out, "up", u.report); err != nil {
			return err
		}
	} else {
		writeUpText(out, u.report)
	}
	if runErr != nil {
		return fmt.Errorf("up: %w", runErr)
	}
	return nil
}

// upRun is one `up`.
type upRun struct {
	ctx                 context.Context
	report              upReport
	binary, to, version string
	skipInstall         bool
	wait                time.Duration
	installed           string // the amq-squad the lead runs
	squad               team.Squad
	q                   seatlaunch.Squad
	decisionsFrom       int64 // decisions.jsonl's size before the lead started
	logFrom             int64 // scheduler.log's size before the lead started
}

func (u *upRun) run() error {
	for _, s := range []struct {
		name string
		fn   func() (string, error)
	}{
		{"install", u.install},
		{"doctor", u.doctor},
		{"session", u.session},
		{"compile", u.compile},
		{"lead", u.lead},
		{"tmux", u.tmux},
		{"roster", u.roster},
	} {
		detail, err := s.fn()
		if err != nil {
			step := upStep{Name: s.name, Outcome: "refused", Detail: err.Error()}
			var r *upRefusal
			if errors.As(err, &r) {
				step.Refusal, step.Detail = r.Name, r.Detail
			}
			u.report.Steps = append(u.report.Steps, step)
			return err
		}
		outcome := "ok"
		if strings.HasPrefix(detail, "skipped") {
			outcome = "skipped"
		}
		u.report.Steps = append(u.report.Steps, upStep{Name: s.name, Outcome: outcome, Detail: detail})
	}
	return nil
}

// tagPattern is a version that names a tag exactly: vX.Y.Z with an optional
// prerelease, never `git describe`'s -N-g<sha> or -dirty.
var tagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

func isTag(v string) bool { return tagPattern.MatchString(v) && !strings.HasSuffix(v, "-dirty") }

// checkPiBuild refuses a binary that is not a tagged pi build: its version
// must name a tag (R54: a build of the working tree never runs a squad),
// and its pi verify must name no pi_not_embedded or pi_runtime_rejected (an
// unpacked-yet runtime is fine: the first lead unpacks it).
func (u *upRun) checkPiBuild(bin string) (string, error) {
	out, err := squadRun(u.ctx, u.report.Project, os.Environ(), bin, "version")
	v := parseAmqSquadVersion(string(out))
	if err != nil || !isTag(v) {
		return "", refuse("pi_binary_untagged", "%s reports version %q; up installs only a build of scripts/build-pi-runtime.sh at a tag (R54)", bin, v)
	}
	out, _ = squadRun(u.ctx, u.report.Project, os.Environ(), bin, "pi", "verify", "--json")
	switch code := piruntime.RefusalCodeText(string(out)); code {
	case piruntime.ReasonNotEmbedded:
		return "", refuse(code, "%s carries no pi runtime: build it with scripts/build-pi-runtime.sh at a tag", bin)
	case piruntime.ReasonRejected:
		return "", refuse(code, "%s: %s", bin, strings.TrimSpace(lastLine(string(out))))
	}
	return v, nil
}

// install is step 1.
func (u *upRun) install() (string, error) {
	target := u.to
	if target == "" {
		if p, err := upLookPath("amq-squad"); err == nil {
			target = p
		}
	}
	if u.skipInstall {
		if target == "" {
			return "", refuse("install_target_unknown", "no amq-squad on PATH to keep; install one (or pass --to PATH)")
		}
		v, err := u.checkPiBuild(target)
		if err != nil {
			return "", err
		}
		u.installed, u.report.Binary = target, target
		return "skipped: " + target + " already reports " + v, nil
	}
	src := u.binary
	if src == "" {
		src = filepath.Join(u.report.Project, "build", "pi", "amq-squad")
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		return "", refuse("pi_binary_missing", "%s is not a built binary; build it at a tag with scripts/build-pi-runtime.sh, or pass --binary PATH", src)
	}
	v, err := u.checkPiBuild(src)
	if err != nil {
		return "", err
	}
	if target == "" {
		return "", refuse("install_target_unknown", "no amq-squad on PATH to replace; pass --to PATH (a directory on PATH)")
	}
	if !filepath.IsAbs(target) {
		if target, err = filepath.Abs(target); err != nil {
			return "", err
		}
	}
	if a, aerr := os.Stat(src); aerr == nil {
		if b, berr := os.Stat(target); berr == nil && os.SameFile(a, b) {
			u.installed, u.report.Binary = target, target
			return fmt.Sprintf("%s %s is already the installed binary", target, v), nil
		}
	}
	if err := installByRename(src, target); err != nil {
		return "", refuse("install_failed", "%v", err)
	}
	u.installed, u.report.Binary = target, target
	return fmt.Sprintf("installed %s (%s) at %s by temp name and rename", src, v, target), nil
}

// installByRename copies src to a temporary name in target's directory and
// renames it over target: the old file is never written in place (the
// macOS signature cache kills a binary copied over in place; follow-up
// "Install and self-update"), and a reader sees the old or the new file,
// never a partial one.
func installByRename(src, target string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	tmp := filepath.Join(filepath.Dir(target), ".amq-squad.up-"+hex.EncodeToString(nonce[:]))
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// doctor is step 2: the installed binary's doctor, a failing row refusing
// by its name.
func (u *upRun) doctor() (string, error) {
	out, _ := squadRun(u.ctx, u.report.Project, os.Environ(), u.installed, "doctor", "--json", "--project", u.report.Project)
	var env struct {
		Data doctorEnvelopeData `json:"data"`
	}
	raw := out
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		raw = out[i:]
	}
	// The envelope is the first JSON value: a failing doctor exits non-zero
	// and its error line follows it in the combined output.
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&env); err != nil || len(env.Data.Checks) == 0 {
		return "", refuse("doctor_unreadable", "%s doctor --json printed no checks: %s", u.installed, strings.TrimSpace(lastLine(string(out))))
	}
	warn := 0
	for _, c := range env.Data.Checks {
		switch c.Status {
		case doctorFail:
			return "", refuse(firstNonEmptyStr(c.Kind, "doctor_failed"), "doctor row %q failed: %s", c.Name, c.Detail)
		case doctorWarn:
			warn++
		}
	}
	return fmt.Sprintf("%d checks, none failed (%d warnings)", len(env.Data.Checks), warn), nil
}

// session is step 3.
func (u *upRun) session() (string, error) {
	s, err := team.LoadSquad(u.report.Project)
	if err != nil {
		return "", refuse("squad_invalid", "%v", err)
	}
	if s.Session == "" || strings.TrimSpace(s.Lead) == "" {
		return "", refuse("squad_invalid", "%s names no session or no lead (session: <name>, lead: <seat handle>)", team.SquadPath(u.report.Project))
	}
	u.squad = s
	amq, err := upLookPath("amq")
	if err != nil {
		return "", refuse("amq_missing", "the installed amq is required on PATH (R51): %v", err)
	}
	q := seatlaunch.Squad{ProjectRoot: u.report.Project, Session: s.Session}
	q.BaseRoot = filepath.Join(q.ProjectRoot, ".agent-mail")
	q.Operator = "user"
	handles := []string{s.Lead}
	for _, seat := range s.Seats {
		handles = append(handles, seat.Handle)
	}
	done, err := ensureSquadSession(u.ctx, amq, q, handles)
	if err != nil {
		return "", refuse("session_create_failed", "%v", err)
	}
	u.report.Root, u.report.Session, u.report.Lead = q.SessionRoot(), s.Session, s.Lead
	if len(done) == 0 {
		return "skipped: the session root " + q.SessionRoot() + " exists", nil
	}
	_ = schedproc.AppendDecision(q.SessionRoot(), upNow(), scheduler.Decision{Kind: KindSessionCreated, Reason: "up: " + strings.Join(done, "; ")})
	return strings.Join(done, "; "), nil
}

// KindSessionCreated is the decision of a session created before a launch
// (by up, or by scheduler run on an absent session root).
const KindSessionCreated = "session_created"

// ensureSquadSession creates an absent session: the squad's AMQ root first
// when it has no config (amq coop init, run in the repository), then the
// session (amq session create), through squadRun; it returns what it ran
// (nothing when the session root exists). One function for `up`
// and `scheduler run` (brief 31-1, item 7: the missing session gives way to
// its creation).
func ensureSquadSession(ctx context.Context, amq string, q seatlaunch.Squad, handles []string) ([]string, error) {
	var done []string
	if st, err := os.Stat(q.SessionRoot()); err == nil && st.IsDir() {
		return nil, nil // the session exists: nothing to create
	}
	env := amqChildEnv()
	if _, err := os.Stat(filepath.Join(q.BaseRoot, "meta", "config.json")); errors.Is(err, os.ErrNotExist) {
		root := q.BaseRoot
		if rel, err := filepath.Rel(q.ProjectRoot, q.BaseRoot); err == nil && filepath.IsLocal(rel) {
			root = rel // the repository's .amqrc names it relative, as amq coop init does
		}
		agents := slices.Compact(slices.Sorted(slices.Values(append(append([]string{}, handles...), firstNonEmptyStr(q.Operator, "user")))))
		args := []string{"coop", "init", "--root", root, "--agents", strings.Join(agents, ","), "--no-gitignore", "--json"}
		if out, err := squadRun(ctx, q.ProjectRoot, env, amq, args...); err != nil {
			return done, fmt.Errorf("amq %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(lastLine(string(out))))
		}
		done = append(done, "amq "+strings.Join(args[:2], " ")+" --root "+root)
	}
	if st, err := os.Stat(q.SessionRoot()); err != nil || !st.IsDir() { // amq coop init made only its default session
		args := []string{"session", "create", q.Session, "--root", q.BaseRoot}
		if out, err := squadRun(ctx, q.ProjectRoot, env, amq, args...); err != nil {
			return done, fmt.Errorf("amq %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(lastLine(string(out))))
		}
		done = append(done, "amq "+strings.Join(args, " "))
	}
	if st, err := os.Stat(q.SessionRoot()); err != nil || !st.IsDir() {
		return done, fmt.Errorf("the session root %s is still absent after %s", q.SessionRoot(), strings.Join(done, "; "))
	}
	return done, nil
}

// compile is step 4: squad.yaml as scheduler run reads it.
func (u *upRun) compile() (string, error) {
	q, _, err := squadOfProject(u.report.Project)
	if err != nil {
		return "", refuse("squad_invalid", "%v", err)
	}
	q.Wrapper = u.installed
	if q.AMQPath, err = upLookPath("amq"); err != nil {
		return "", refuse("amq_missing", "%v", err)
	}
	if q, err = q.Normalize(); err != nil {
		return "", refuse("squad_invalid", "%v", err)
	}
	u.q = q
	return fmt.Sprintf("%d seats, lead %s, session %s", len(q.Seats), u.squad.Lead, q.Session), nil
}

// lockHeld probes a flock without keeping it: held is true when another
// process holds it. An absent file is not held.
func lockHeld(path string) (bool, error) {
	lock, acquired, err := flock.TryExclusive(path, false)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case errors.Is(err, flock.ErrUnsupported):
		return false, nil
	case err != nil:
		return false, err
	case !acquired:
		return true, nil
	}
	_ = lock.Close()
	return false, nil
}

// paneExists asks tmux whether a pane id is live.
func (u *upRun) paneExists(pane string) bool {
	out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "display-message", "-p", "-t", pane, "#{pane_id}")
	return err == nil && strings.TrimSpace(string(out)) == pane
}

// tmuxSessionExists asks tmux whether a session of exactly this name is live.
func (u *upRun) tmuxSessionExists(name string) bool {
	_, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "has-session", "-t", "="+name)
	return err == nil
}

// stopPID sends SIGTERM and waits for held (the lock the process holds) to
// free, then SIGKILL and waits again.
func stopPID(pid int, held func() bool) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := upSignal(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal %d to pid %d: %w", sig, pid, err)
		}
		for deadline := upNow().Add(upStopWait); upNow().Before(deadline); time.Sleep(upPoll) {
			if !held() {
				return nil
			}
		}
	}
	return fmt.Errorf("pid %d still holds its lock after SIGTERM and SIGKILL", pid)
}

// lead is step 5, under the one rule of brief 31-1 section 4.
func (u *upRun) lead() (string, error) {
	root := u.q.SessionRoot()
	leadLock := filepath.Join(schedproc.Dir(root), schedproc.LeadLockName)
	runLock := filepath.Join(schedproc.Dir(root), "run.lock")
	name := upTmuxSessionName(u.report.Project)
	var did []string
	held, err := lockHeld(leadLock)
	if err != nil {
		return "", refuse("lead_unknown", "the lead lock %s cannot be probed: %v", leadLock, err)
	}
	rec, recErr := schedproc.ReadLeadRecord(root)
	if held {
		switch {
		case recErr != nil || rec.PID <= 0:
			return "", refuse(schedproc.ReasonLeadRunning, "%v", &schedproc.LeadRunningError{Root: root, Starting: true})
		case rec.Pane == "":
			// A lead outside tmux has no pane to be gone: it is never stopped.
			return "", refuse(schedproc.ReasonLeadRunning, "%v; it runs outside tmux, so up never stops it", &schedproc.LeadRunningError{Root: root, Record: rec})
		case u.paneExists(rec.Pane):
			return "", refuse(schedproc.ReasonLeadRunning, "%v", &schedproc.LeadRunningError{Root: root, Record: rec})
		}
		// The pid holds the lock (alive) and its pane is gone: stop it.
		if stamp, ok := upStartToken(rec.PID); !ok || stamp != rec.Start {
			return "", refuse("lead_unknown", "the lead lock is held but pid %d does not carry the recorded start stamp; up stops only the recorded lead", rec.PID)
		}
		if err := stopPID(rec.PID, func() bool { h, _ := lockHeld(leadLock); return h }); err != nil {
			return "", refuse("lead_stop_failed", "%v", err)
		}
		why := fmt.Sprintf("pid %d: its tmux pane %s is gone", rec.PID, rec.Pane)
		_ = schedproc.AppendDecision(root, upNow(), scheduler.Decision{Kind: KindUpStoppedLead, Reason: why})
		did = append(did, "stopped the lead ("+why+")")
	} else if u.tmuxSessionExists(name) {
		// The lead lock is free, so no lead lives; its tmux session is a
		// dead lead's leftover.
		if out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "kill-session", "-t", "="+name); err != nil {
			return "", refuse("lead_stop_failed", "tmux kill-session %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
		why := fmt.Sprintf("tmux session %s: its lead is dead (the lead lock is free)", name)
		if recErr == nil {
			why = fmt.Sprintf("tmux session %s: its lead pid %d is dead (the lead lock is free)", name, rec.PID)
		}
		_ = schedproc.AppendDecision(root, upNow(), scheduler.Decision{Kind: KindUpStoppedLead, Reason: why})
		did = append(did, "stopped "+why)
	}
	// A scheduler still holding run.lock now serves a lead that is gone:
	// its lead_gone check passes, so it is stopped.
	if held, err := lockHeld(runLock); err != nil {
		return "", refuse("scheduler_unknown", "the scheduler lock %s cannot be probed: %v", runLock, err)
	} else if held {
		lr, err := schedproc.ReadLaunchRecord(root)
		if err != nil || lr.PID <= 0 {
			return "", refuse("scheduler_running", "%v (its launch record is unreadable, so up does not stop it)", schedproc.ErrRunning)
		}
		if recErr != nil {
			return "", refuse("scheduler_running", "%v (pid %d, started without a recorded lead, so up does not stop it)", schedproc.ErrRunning, lr.PID)
		}
		if st, why := schedproc.LeadState(rec.PID, rec.Start, upStartToken, upPIDAlive); st != schedproc.LeadIsGone {
			return "", refuse("scheduler_running", "%v (pid %d): its lead check has not passed: %s", schedproc.ErrRunning, lr.PID, firstNonEmptyStr(why, "the lead is alive"))
		}
		if args, ok := upProcArgs(lr.PID); !ok || !strings.Contains(args, "scheduler run") {
			return "", refuse("scheduler_unknown", "the scheduler lock is held but pid %d is not an amq-squad scheduler run", lr.PID)
		}
		if err := stopPID(lr.PID, func() bool { h, _ := lockHeld(runLock); return h }); err != nil {
			return "", refuse("scheduler_stop_failed", "%v", err)
		}
		why := fmt.Sprintf("pid %d: %s: its lead pid %d is gone", lr.PID, schedproc.RefusedLeadGone, rec.PID)
		_ = schedproc.AppendDecision(root, upNow(), scheduler.Decision{Kind: KindUpStoppedScheduler, Reason: why})
		did = append(did, "stopped the scheduler ("+why+")")
	}
	if len(did) == 0 {
		return "no recorded lead or scheduler is running", nil
	}
	return strings.Join(did, "; "), nil
}

// The decisions of what up stopped.
const (
	KindUpStoppedLead      = "up_stopped_lead"
	KindUpStoppedScheduler = "up_stopped_scheduler"
)

var tmuxNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

// upTmuxSessionName is the lead's tmux session: squad-lead-<repo, at most
// 30 characters>-<sha256(repo path)[:5]>. It never matches AMQ's amq-*
// sessions (AMQ v0.116.2 internal/launch/tmux_names.go:11-23), and the
// lead stays out of the roster's session.
func upTmuxSessionName(project string) string {
	slug := strings.Trim(tmuxNameRE.ReplaceAllString(strings.ToLower(filepath.Base(project)), "-"), "-")
	if len(slug) > 30 {
		slug = strings.Trim(slug[:30], "-")
	}
	if slug == "" {
		slug = "repo"
	}
	sum := sha256.Sum256([]byte(project))
	return "squad-lead-" + slug + "-" + hex.EncodeToString(sum[:])[:5]
}

// tmux is step 6.
func (u *upRun) tmux() (string, error) {
	name := upTmuxSessionName(u.report.Project)
	u.report.TmuxSession, u.report.Attach = name, "tmux attach -t "+name
	u.decisionsFrom = schedproc.DecisionsSize(u.q.SessionRoot())
	u.logFrom = fileSize(filepath.Join(schedproc.Dir(u.q.SessionRoot()), "scheduler.log"))
	args := []string{"new-session", "-d", "-s", name, "-c", u.report.Project,
		"-e", leadStartEnv + "=" + u.report.Start.Format(time.RFC3339Nano),
		// the lead's identity comes from squad.yaml; a stale pin in the tmux
		// server's environment must not disagree with it
		"-e", "AM_ME=", "-e", "AM_ROOT=", "-e", "AM_BASE_ROOT=", "-e", "AM_SESSION=",
		u.installed, "lead", "--team"}
	if out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", args...); err != nil {
		text := strings.TrimSpace(string(out))
		return "", refuse(firstNonEmptyStr(namedRefusal(text), "tmux_failed"), "tmux new-session: %v: %s", err, text)
	}
	return fmt.Sprintf("tmux session %s runs %s lead --team in %s", name, u.installed, u.report.Project), nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// rosterOutcomes are the decisions step 7 waits for.
var rosterOutcomes = []string{"roster_launched", "roster_adopted", "roster_incomplete", "roster_launch_failed"}

// namedRefusals are the refusals of the scheduler and the roster launch
// that up surfaces by name (brief 31-1, section 3), with the text that
// carries each.
var namedRefusals = []struct{ name, text string }{
	{schedproc.ReasonLeadRunning, schedproc.ReasonLeadRunning},
	{"scheduler_running", schedproc.ErrRunning.Error()},
	{schedproc.RefusedLeadGone, schedproc.RefusedLeadGone},
	{schedproc.RefusedStarting, schedproc.RefusedStarting},
	{"seat_replacement_resource_present", "seat_replacement_resource_present"},
	{"binding_present_without_resumable_conversation", "binding_present_without_resumable_conversation"},
	{scheduler.ReasonSeatRelaunch, scheduler.ReasonSeatRelaunch},
}

var (
	amDisagreeRE = regexp.MustCompile(`AM_[A-Z_]+=.*disagrees with|disagrees with AM_[A-Z_]+`)
	tmuxExistsRE = regexp.MustCompile(`tmux session .*already exists|duplicate session`)
)

// namedRefusal is the first named refusal the text carries, "" for none.
func namedRefusal(text string) string {
	for _, r := range namedRefusals {
		if strings.Contains(text, r.text) {
			return r.name
		}
	}
	switch {
	case amDisagreeRE.MatchString(text):
		return "am_identity_disagrees"
	case tmuxExistsRE.MatchString(text):
		return "tmux_session_exists"
	}
	return ""
}

// roster is step 7: decisions.jsonl from where it stood before the lead
// started, until the roster's outcome; the lead's tmux session ending and
// the scheduler's log refusals stop the wait by name.
func (u *upRun) roster() (string, error) {
	root := u.q.SessionRoot()
	logPath := filepath.Join(schedproc.Dir(root), "scheduler.log")
	off := u.decisionsFrom
	deadline := upNow().Add(u.wait)
	for {
		lines, next, _ := schedproc.ReadDecisionsFrom(root, off)
		off = next
		for _, d := range lines {
			switch {
			case d.Kind == "roster_launch_failed":
				return "", refuse(firstNonEmptyStr(namedRefusal(d.Reason), "roster_launch_failed"), "%s", d.Reason)
			case slices.Contains(rosterOutcomes, d.Kind):
				u.report.Roster = buildStatusContract(root, u.report.Project, u.version, upNow().UTC()).Seats
				return d.Kind + ": " + firstNonEmptyStr(d.Reason, d.Seat), nil
			case d.Kind == "scheduler_stopped" && d.Reason != "":
				return "", refuse(firstNonEmptyStr(namedRefusal(d.Reason), "scheduler_stopped"), "%s", d.Reason)
			}
		}
		if text := readFrom(logPath, u.logFrom); namedRefusal(text) != "" {
			return "", refuse(namedRefusal(text), "the scheduler's log (%s): %s", logPath, lastLine(text))
		}
		if !u.tmuxSessionExists(u.report.TmuxSession) {
			text := readFrom(logPath, u.logFrom)
			return "", refuse(firstNonEmptyStr(namedRefusal(text), "lead_exited"), "the lead's tmux session %s ended before the roster's outcome; the scheduler's log: %s", u.report.TmuxSession, firstNonEmptyStr(lastLine(text), "(empty)"))
		}
		if !upNow().Before(deadline) {
			return "", refuse("roster_wait_timeout", "no roster outcome in decisions.jsonl after %s; attach with: %s", u.wait, u.report.Attach)
		}
		time.Sleep(upPoll)
	}
}

func readFrom(p string, off int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(b)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func writeUpText(out io.Writer, r upReport) {
	for _, s := range r.Steps {
		line := fmt.Sprintf("up: %-8s %s", s.Name, s.Outcome)
		if s.Refusal != "" {
			line += " " + s.Refusal
		}
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		fmt.Fprintln(out, line)
	}
	for _, s := range r.Roster {
		outcome, live, first := "-", "-", "-"
		if s.Launch != nil {
			outcome = s.Launch.Outcome
			live, first = msText(s.Launch.Timing.ToSeatLiveMS), msText(s.Launch.Timing.ToFirstMessageMS)
		}
		fmt.Fprintf(out, "seat %-12s %s/%s  %s  %s  seat live %s, first message %s\n", s.Handle, s.Runtime, s.Model, s.State, outcome, live, first)
		for _, w := range s.Warnings {
			fmt.Fprintln(out, "  warning: "+w)
		}
	}
	if r.Attach != "" {
		fmt.Fprintln(out, "attach to the lead: "+r.Attach)
	}
}

func msText(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return fmt.Sprintf("%.3f s", float64(*ms)/1000)
}
