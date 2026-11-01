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
	"sync"
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
)

// `amq-squad up` (brief 31-1, section 3; plan 7.4): a squad.yaml line to
// every seat live with no owner step between. Seven steps, in order, each
// refusing by name: (1) squad.yaml compiled, before anything is created;
// (2) install the tagged pi binary onto PATH by temp name and rename; (3)
// the AMQ root and session; (4) the doctor checks of that binary, in the
// root; (5) the recorded lead under the one
// rule (a lead whose pid is alive and whose pane exists is never stopped);
// (6) the lead in its own tmux session; (7) the roster's outcome from
// decisions.jsonl, then the roster from status and the attach command.

const upUsage = `amq-squad up - start this repository's squad from squad.yaml

Usage:
  amq-squad up [--project DIR] [--binary PATH | --skip-install] [--to PATH] [--wait 120s] [--json]

Seven steps, in order; each refuses by name and nothing after it runs:
  1. compile: squad.yaml through the seats' roles and the squad's checks,
     before anything is installed or created.
  2. install: the tagged pi build (scripts/build-pi-runtime.sh at a tag;
     default <repo>/build/pi/amq-squad, or --binary PATH) is copied next to
     the amq-squad on PATH (or --to PATH) under a temporary name and renamed
     over it, never written over it in place. Refused: pi_binary_missing,
     pi_binary_untagged, pi_not_embedded, pi_runtime_rejected,
     install_target_unknown. --skip-install keeps the installed binary when
     it already reports a tag.
  3. session: the AMQ root and the squad's session (amq coop init, amq
     session create) when absent.
  4. doctor: the installed binary's doctor; a failing row refuses by name.
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
	upProcArgv               = procinfo.Argv
	upSignal                 = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	upNow                    = time.Now
	upPoll                   = 200 * time.Millisecond
	upStopWait               = 15 * time.Second
	// test hooks: before each signal's identity check, and after the lead's
	// tmux session started (the window before the new lead takes its lock)
	upBeforeSignal   func(pid int, sig syscall.Signal)
	upAfterLeadStart func()
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
	leadHandle          string // squad.yaml's lead handle
	q                   seatlaunch.Squad
	upLock, leadLock    *flock.Exclusive // held by the lead step; the lead lock is dropped as the lead starts
	decisionsFrom       int64            // decisions.jsonl's size before the lead started
	logFrom             int64            // scheduler.log's size before the lead started
}

func (u *upRun) run() error {
	defer func() {
		u.leadLock.Close()
		u.upLock.Close()
	}()
	for _, s := range []struct {
		name string
		fn   func() (string, error)
	}{
		// compile first: a squad.yaml that does not compile installs and
		// creates nothing; the session before doctor, whose amq ops row runs
		// amq env and amq doctor --ops in the AMQ root
		{"compile", u.compile},
		{"install", u.install},
		{"session", u.session},
		{"doctor", u.doctor},
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
// and its `pi path --json` (which unpacks the embedded runtime when absent
// and re-hashes it, as the lead's first start would) must exit 0 with its
// JSON envelope. ErrNotEmbedded's text refuses pi_not_embedded; any other
// non-zero exit or unparseable reply refuses pi_runtime_rejected. (`pi
// verify` is not used: it exits non-zero by design for a runtime not
// unpacked yet, the state of every fresh tagged build.)
func (u *upRun) checkPiBuild(bin string) (string, error) {
	out, err := squadRun(u.ctx, u.report.Project, os.Environ(), bin, "version")
	v := parseAmqSquadVersion(string(out))
	if err != nil || !isTag(v) {
		return "", refuse("pi_binary_untagged", "%s reports version %q; up installs only a build of scripts/build-pi-runtime.sh at a tag (R54)", bin, v)
	}
	out, err = squadRun(u.ctx, u.report.Project, os.Environ(), bin, "pi", "path", "--json")
	if code := piruntime.RefusalCodeText(string(out)); code == piruntime.ReasonNotEmbedded {
		return "", refuse(code, "%s carries no pi runtime: build it with scripts/build-pi-runtime.sh at a tag", bin)
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Runtime string `json:"runtime"`
		} `json:"data"`
	}
	raw := out
	if i := bytes.IndexByte(out, '{'); i > 0 {
		raw = out[i:]
	}
	derr := json.NewDecoder(bytes.NewReader(raw)).Decode(&env)
	switch {
	case err != nil:
		return "", refuse(piruntime.ReasonRejected, "%s pi path: %v: %s", bin, err, firstNonEmptyStr(lastLine(string(out)), "(no output)"))
	case derr != nil || env.Kind != "pi_path" || env.Data.Runtime == "":
		return "", refuse(piruntime.ReasonRejected, "%s pi path printed no runtime: %s", bin, firstNonEmptyStr(lastLine(string(out)), "(no output)"))
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

// doctor is step 4.
func (u *upRun) doctor() (string, error) { return UpDoctor(u.ctx, u.report.Project, u.installed) }

// UpDoctor is the doctor step (shared by up and the lead's start of a
// confirmed team, brief 31-3): the installed binary's doctor in the
// repository, a failing row refusing by its name.
func UpDoctor(ctx context.Context, project, installed string) (string, error) {
	countShared("doctor")
	out, _ := squadRun(ctx, project, os.Environ(), installed, "doctor", "--json", "--project", project)
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
		return "", refuse("doctor_unreadable", "%s doctor --json printed no checks: %s", installed, strings.TrimSpace(lastLine(string(out))))
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
func (u *upRun) session() (string, error) { return UpSession(u.ctx, u.q, u.leadHandle) }

// UpSession is the session step (shared by up and the lead's start of a
// confirmed team): the AMQ root and the squad's session created when
// absent, the creation a decision.
func UpSession(ctx context.Context, q seatlaunch.Squad, leadHandle string) (string, error) {
	countShared("session")
	handles := []string{leadHandle}
	for _, seat := range q.Seats {
		handles = append(handles, seat.Handle)
	}
	done, err := ensureSquadSession(ctx, q.AMQPath, q, handles)
	var named *upRefusal
	switch {
	case errors.As(err, &named):
		return "", named
	case err != nil:
		return "", refuse("session_create_failed", "%v", err)
	}
	if len(done) == 0 {
		return "skipped: the session root " + q.SessionRoot() + " exists", nil
	}
	if err := schedproc.AppendDecision(q.SessionRoot(), upNow(), scheduler.Decision{Kind: KindSessionCreated, Reason: "up: " + strings.Join(done, "; ")}); err != nil {
		return "", refuse("decision_unrecorded", "the session was created (%s) but its decision could not be recorded: %v", strings.Join(done, "; "), err)
	}
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
	// A symlinked AMQ root or session root is refused by name (Lstat: never
	// followed), before anything is created or read through it.
	for _, p := range []string{q.BaseRoot, q.SessionRoot()} {
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, refuse("amq_root_symlinked", "%s is a symbolic link; up and scheduler run use only a real directory as the AMQ root or the session root", p)
		}
	}
	if st, err := os.Lstat(q.SessionRoot()); err == nil && st.IsDir() {
		return nil, nil // the session exists: nothing to create
	}
	env := amqChildEnv()
	if _, err := os.Lstat(filepath.Join(q.BaseRoot, "meta", "config.json")); errors.Is(err, os.ErrNotExist) {
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
	if st, err := os.Lstat(q.SessionRoot()); err != nil || !st.IsDir() { // amq coop init made only its default session
		args := []string{"session", "create", q.Session, "--root", q.BaseRoot}
		if out, err := squadRun(ctx, q.ProjectRoot, env, amq, args...); err != nil {
			return done, fmt.Errorf("amq %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(lastLine(string(out))))
		}
		done = append(done, "amq "+strings.Join(args, " "))
	}
	if st, err := os.Lstat(q.SessionRoot()); err != nil || !st.IsDir() {
		return done, fmt.Errorf("the session root %s is still absent after %s", q.SessionRoot(), strings.Join(done, "; "))
	}
	return done, nil
}

// compile is the first step: squad.yaml as scheduler run reads it, before
// anything is installed or created. The wrapper is the binary the install
// step will put in place (its target), else this one.
func (u *upRun) compile() (string, error) {
	q, lead, err := UpCompile(u.report.Project, u.to)
	if err != nil {
		return "", err
	}
	u.q, u.leadHandle = q, lead
	u.report.Root, u.report.Session, u.report.Lead = q.SessionRoot(), q.Session, lead
	return fmt.Sprintf("%d seats, lead %s, session %s", len(q.Seats), lead, q.Session), nil
}

// UpCompile is the compile step (shared by up and the lead's start of a
// confirmed team): squad.yaml as scheduler run reads it, the wrapper (the
// given path, else the amq-squad on PATH, else this binary) and the
// installed amq, normalized. It returns the squad and its lead handle.
func UpCompile(project, wrapper string) (seatlaunch.Squad, string, error) {
	countShared("compile")
	q, lead, err := squadOfProject(project)
	if err != nil {
		return q, "", refuse("squad_invalid", "%v", err)
	}
	q.Wrapper = wrapper
	if q.Wrapper == "" {
		if p, err := upLookPath("amq-squad"); err == nil {
			q.Wrapper = p
		} else if self, err := os.Executable(); err == nil {
			q.Wrapper = self
		}
	}
	if q.Wrapper, err = filepath.Abs(q.Wrapper); err != nil {
		return q, "", refuse("squad_invalid", "%v", err)
	}
	if q.AMQPath, err = upLookPath("amq"); err != nil {
		return q, "", refuse("amq_missing", "the installed amq is required on PATH (R51): %v", err)
	}
	if q, err = q.Normalize(); err != nil {
		return q, "", refuse("squad_invalid", "%v", err)
	}
	return q, lead, nil
}

// The shared steps (brief 31-3, the plan reviewer's edit 1): `up` and the
// lead's start of a confirmed team (team apply under the solo lead) run one
// implementation of the compile, the session, the doctor, the lead lock,
// the scheduler and the roster's outcome. `up` alone installs and starts
// the tmux lead (whose `lead --team` takes the lead lock and starts the
// scheduler through the same two functions); the start of a confirmed team
// alone takes the lead lock and starts the scheduler in the lead's own
// process. Each shared step counts its calls (a test reads the counter to
// show both paths run the same functions).
var (
	sharedMu    sync.Mutex
	sharedCalls = map[string]int{}
)

func countShared(step string) {
	sharedMu.Lock()
	sharedCalls[step]++
	sharedMu.Unlock()
}

// UpLeadLock is the lead lock step: the root's lead lock for this process
// (one lead per root; a held lock is lead_running with the first lead's pid
// and pane), held for the process's life.
func UpLeadLock(root string) (*schedproc.LeadLock, error) {
	countShared("lead_lock")
	return takeLeadLock(root)
}

// UpScheduler is the scheduler step: `amq-squad scheduler run --project`
// supervised by this process (leadSchedulerCommand), restarted on a crash,
// stopped with it.
func UpScheduler(project, root string, pools []string) (*supervisor, error) {
	countShared("scheduler")
	return startLeadScheduler(project, root, pools)
}

// upTryLock is the flock up's locks go through (a test seam).
var upTryLock = flock.TryExclusive

// lockUnsupported is the refusal of a lock this filesystem cannot take: no
// advisory lock means up cannot tell which lead or scheduler holds the root
// (or whether another up runs), so it refuses by name and stops nothing,
// never reading the missing lock as "held" or as "free".
func lockUnsupported(path string) *upRefusal {
	return refuse("lock_unsupported", "%s: this filesystem has no advisory locks (flock), so up cannot tell whether a lead, a scheduler or another up holds it; nothing was stopped", path)
}

// lockHeld probes a flock without keeping it: held is true when another
// process holds it. An absent file is not held; a filesystem without
// advisory locks is lock_unsupported.
func lockHeld(path string) (bool, error) {
	lock, acquired, err := upTryLock(path, false)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case errors.Is(err, flock.ErrUnsupported):
		return false, lockUnsupported(path)
	case err != nil:
		return false, err
	case !acquired:
		return true, nil
	}
	_ = lock.Close()
	return false, nil
}

// paneState is what tmux says about the lead's recorded pane.
type paneState int

const (
	paneListed      paneState = iota // tmux lists the pane: the lead's pane exists
	paneGone                         // tmux lists the lead's session and not the pane
	paneUnknown                      // tmux answered but lists neither: up cannot tell (another socket?)
	tmuxUnavailable                  // tmux could not be asked: no tmux, no server, an error, an unparseable reply
)

var paneIDRE = regexp.MustCompile(`^%[0-9]+$`)

// leadPane asks tmux for every pane of its server (list-panes -a) and
// places the recorded pane. "Gone" is only an answer that lists the lead's
// session (up's squad-lead-* session) and not the pane. A missing tmux, an
// error or an unparseable reply is tmuxUnavailable; an answer that lists
// neither is paneUnknown. Only paneGone lets up stop a lead.
func (u *upRun) leadPane(pane, session string) (paneState, string) {
	out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "list-panes", "-a", "-F", "#{session_name}\t#{pane_id}")
	if err != nil {
		return tmuxUnavailable, fmt.Sprintf("tmux list-panes -a: %v: %s", err, firstNonEmptyStr(lastLine(string(out)), "(no output)"))
	}
	// Line by line, nothing trimmed: a row is exactly "<session>\t<pane>",
	// so a leading tab or a wrong field count is unparseable, never a
	// shifted field read as the lead's session (Codex P1 on #829).
	lines := strings.Split(string(out), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // the reply's final newline
	}
	if len(lines) == 0 {
		return tmuxUnavailable, "tmux list-panes -a: an empty reply"
	}
	sessionListed := false
	for _, ln := range lines {
		fields := strings.Split(ln, "\t")
		if len(fields) != 2 || fields[0] == "" || !paneIDRE.MatchString(fields[1]) {
			return tmuxUnavailable, fmt.Sprintf("tmux list-panes -a: unparseable reply line %q", ln)
		}
		name, id := fields[0], fields[1]
		if id == pane {
			return paneListed, ""
		}
		sessionListed = sessionListed || name == session
	}
	if sessionListed {
		return paneGone, ""
	}
	return paneUnknown, fmt.Sprintf("tmux lists neither the lead's pane %s nor its session %s", pane, session)
}

// tmuxSessionExists asks tmux whether a session of exactly this name is live.
func (u *upRun) tmuxSessionExists(name string) bool {
	_, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "has-session", "-t", "="+name)
	return err == nil
}

// signalSame sends sig to pid only if same() holds, checked immediately
// before the kill in one call. The window that remains, a pid ending and
// being reused between the check and the kill, is inherent to Unix
// signals; the recorded start stamp in same() bounds it to the same
// process incarnation up decided on.
func signalSame(pid int, sig syscall.Signal, same func() bool) error {
	if upBeforeSignal != nil {
		upBeforeSignal(pid, sig)
	}
	if !same() {
		return fmt.Errorf("pid %d is no longer the recorded process; not sent %s", pid, sig)
	}
	if err := upSignal(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal %d to pid %d: %w", sig, pid, err)
	}
	return nil
}

// stopProcess stops the recorded process at pid: SIGTERM, a grace for held
// (the lock it holds) to free, then SIGKILL; each signal goes through
// signalSame, so a changed identity is never signalled and SIGKILL follows
// only when the same identity still holds after the grace.
func stopProcess(pid int, same func() bool, held func() bool) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := signalSame(pid, sig, same); err != nil {
			return err
		}
		for deadline := upNow().Add(upStopWait); upNow().Before(deadline); time.Sleep(upPoll) {
			if !held() {
				return nil
			}
		}
	}
	return fmt.Errorf("pid %d still holds its lock after SIGTERM and SIGKILL", pid)
}

// upLockName serializes two ups on one root: held from the lead step to
// the roster's outcome.
const upLockName = "up.lock"

// takeLock takes a flock and keeps it; nil when another process holds it,
// lock_unsupported on a filesystem without advisory locks.
func takeLock(path string) (*flock.Exclusive, error) {
	l, ok, err := upTryLock(path, true)
	switch {
	case errors.Is(err, flock.ErrUnsupported):
		return nil, lockUnsupported(path)
	case err != nil:
		return nil, err
	case !ok:
		return nil, nil
	}
	return l, nil
}

// schedulerArgvIs reports whether argv is exactly the scheduler the lead's
// team mode starts for this project (leadSchedulerCommand): amq-squad
// scheduler run --project <project>, then only --lead-pool pairs. Each
// argument is compared whole (procinfo.Argv), so a path with a space, a
// second --project or a reordered argv does not match.
func schedulerArgvIs(argv []string, project string) bool {
	if len(argv) < 5 || filepath.Base(argv[0]) != "amq-squad" || !slices.Equal(argv[1:5], []string{"scheduler", "run", "--project", project}) {
		return false
	}
	rest := argv[5:]
	if len(rest)%2 != 0 {
		return false
	}
	for i := 0; i < len(rest); i += 2 {
		if rest[i] != "--lead-pool" {
			return false
		}
	}
	return true
}

// lead is step 5, under the one rule of brief 31-1 section 4. One up per
// root at a time (up.lock, held to the roster's outcome). The lead lock is
// held across each decision and its stop: taken when free, else taken once
// the recorded lead is stopped. The record is re-read before every signal;
// a stop that needs lead.json refuses lead_record_unreadable without it.
// Every stop is a decision, and a decision that cannot be recorded refuses
// before the next step.
func (u *upRun) lead() (string, error) {
	root := u.q.SessionRoot()
	dir := schedproc.Dir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", refuse("lead_unknown", "%v", err)
	}
	ul, err := takeLock(filepath.Join(dir, upLockName))
	var named *upRefusal
	switch {
	case errors.As(err, &named):
		return "", named
	case err != nil:
		return "", refuse("up_unknown", "the up lock cannot be taken: %v", err)
	case ul == nil:
		return "", refuse("up_running", "another amq-squad up runs for %s; nothing was stopped", root)
	}
	u.upLock = ul
	leadLockPath := filepath.Join(dir, schedproc.LeadLockName)
	runLock := filepath.Join(dir, "run.lock")
	name := upTmuxSessionName(u.report.Project)
	stopped := func(kind, why string) error {
		if err := schedproc.AppendDecision(root, upNow(), scheduler.Decision{Kind: kind, Reason: why}); err != nil {
			return refuse("decision_unrecorded", "%s (%s) could not be recorded as a decision: %v", kind, why, err)
		}
		return nil
	}
	var did []string
	// run.lock is probed once before anything is stopped, so a filesystem
	// without advisory locks refuses before the first stop, not after it
	if _, err := lockHeld(runLock); errors.As(err, &named) {
		return "", named
	}
	if u.leadLock, err = takeLock(leadLockPath); errors.As(err, &named) {
		return "", named
	} else if err != nil {
		return "", refuse("lead_unknown", "the lead lock %s cannot be taken: %v", leadLockPath, err)
	}
	rec, recErr := schedproc.ReadLeadRecord(root)
	if u.leadLock == nil {
		// The lock is held: a lead lives (the flock is its pid's liveness).
		switch {
		case recErr != nil || rec.PID <= 0:
			return "", refuse(schedproc.ReasonLeadRunning, "%v", &schedproc.LeadRunningError{Root: root, Starting: true})
		case rec.Pane == "":
			// A lead outside tmux has no pane to be gone: it is never stopped.
			return "", refuse(schedproc.ReasonLeadRunning, "%v; it runs outside tmux, so up never stops it", &schedproc.LeadRunningError{Root: root, Record: rec})
		}
		switch st, why := u.leadPane(rec.Pane, name); st {
		case paneListed:
			return "", refuse(schedproc.ReasonLeadRunning, "%v", &schedproc.LeadRunningError{Root: root, Record: rec})
		case tmuxUnavailable:
			return "", refuse("tmux_unavailable", "up cannot ask tmux whether the lead's pane %s (pid %d) exists: %s; nothing was stopped", rec.Pane, rec.PID, why)
		case paneUnknown:
			return "", refuse("lead_pane_unknown", "up cannot tell whether the lead's pane %s (pid %d) is gone: %s; nothing was stopped", rec.Pane, rec.PID, why)
		}
		same := func() bool {
			now, err := schedproc.ReadLeadRecord(root)
			stamp, ok := upStartToken(rec.PID)
			return err == nil && now.PID == rec.PID && now.Start == rec.Start && ok && stamp == rec.Start
		}
		if !same() {
			return "", refuse("lead_unknown", "the lead lock is held but pid %d does not carry the recorded start stamp; up stops only the recorded lead", rec.PID)
		}
		if err := stopProcess(rec.PID, same, func() bool { h, _ := lockHeld(leadLockPath); return h }); err != nil {
			return "", refuse("lead_stop_failed", "%v", err)
		}
		if u.leadLock, err = takeLock(leadLockPath); err != nil || u.leadLock == nil {
			return "", refuse("lead_stop_failed", "the lead lock was not free after pid %d stopped (%v)", rec.PID, err)
		}
		why := fmt.Sprintf("pid %d: its tmux pane %s is gone", rec.PID, rec.Pane)
		if u.tmuxSessionExists(name) {
			// the lead's session outlived its pane: it goes with the lead
			if out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "kill-session", "-t", "="+name); err != nil {
				return "", refuse("lead_stop_failed", "tmux kill-session %s: %v: %s", name, err, strings.TrimSpace(string(out)))
			}
			why += "; its tmux session " + name + " killed"
		}
		if err := stopped(KindUpStoppedLead, why); err != nil {
			return "", err
		}
		did = append(did, "stopped the lead ("+why+")")
	} else if u.tmuxSessionExists(name) {
		// Under the lead lock no lead lives: the session is a dead lead's
		// leftover, killed only when its record says whose.
		if recErr != nil {
			return "", refuse("lead_record_unreadable", "the tmux session %s is present and no lead holds the lead lock, but lead.json cannot be read (%v); nothing was killed", name, recErr)
		}
		if out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", "kill-session", "-t", "="+name); err != nil {
			return "", refuse("lead_stop_failed", "tmux kill-session %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
		why := fmt.Sprintf("tmux session %s: its lead pid %d is dead (the lead lock is free)", name, rec.PID)
		if err := stopped(KindUpStoppedLead, why); err != nil {
			return "", err
		}
		did = append(did, "stopped "+why)
	}
	// A scheduler still holding run.lock serves a lead that is gone: its
	// lead_gone check has passed, so it is stopped by its recorded identity:
	// the pid, its recorded start stamp and this project in its argv.
	if held, err := lockHeld(runLock); errors.As(err, &named) {
		return "", named
	} else if err != nil {
		return "", refuse("scheduler_unknown", "the scheduler lock %s cannot be probed: %v", runLock, err)
	} else if held {
		if recErr != nil {
			return "", refuse("lead_record_unreadable", "%v, and lead.json cannot be read (%v), so its lead check cannot pass; nothing was stopped", schedproc.ErrRunning, recErr)
		}
		lr, err := schedproc.ReadLaunchRecord(root)
		if err != nil || lr.PID <= 0 || lr.ProcessStart == "" {
			return "", refuse("scheduler_unknown", "%v, and its launch record names no pid with its start stamp (%v); nothing was stopped", schedproc.ErrRunning, err)
		}
		if st, why := schedproc.LeadState(rec.PID, rec.Start, upStartToken, upPIDAlive); st != schedproc.LeadIsGone {
			return "", refuse("scheduler_running", "%v (pid %d): its lead check has not passed: %s", schedproc.ErrRunning, lr.PID, firstNonEmptyStr(why, "the lead is alive"))
		}
		same := func() bool {
			stamp, ok := upStartToken(lr.PID)
			argv, aok := upProcArgv(lr.PID)
			return upPIDAlive(lr.PID) && ok && stamp == lr.ProcessStart && aok && schedulerArgvIs(argv, u.report.Project)
		}
		if !same() {
			return "", refuse("scheduler_unknown", "the scheduler lock is held but pid %d is not the recorded scheduler of %s (its start stamp and its scheduler run --project argv must match); nothing was stopped", lr.PID, u.report.Project)
		}
		if err := stopProcess(lr.PID, same, func() bool { h, _ := lockHeld(runLock); return h }); err != nil {
			return "", refuse("scheduler_stop_failed", "%v", err)
		}
		why := fmt.Sprintf("pid %d: %s: its lead pid %d is gone", lr.PID, schedproc.RefusedLeadGone, rec.PID)
		if err := stopped(KindUpStoppedScheduler, why); err != nil {
			return "", err
		}
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
	// The new lead takes the lead lock itself; up keeps up.lock until the
	// roster's outcome, so no other up can take the lead's session for a
	// dead lead's leftover in between.
	u.leadLock.Close()
	u.leadLock = nil
	if out, err := squadRun(u.ctx, u.report.Project, os.Environ(), "tmux", args...); err != nil {
		text := strings.TrimSpace(string(out))
		return "", refuse(firstNonEmptyStr(namedRefusal(text), "tmux_failed"), "tmux new-session: %v: %s", err, text)
	}
	if upAfterLeadStart != nil {
		upAfterLeadStart()
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
	detail, seats, err := UpRosterOutcome(u.q.SessionRoot(), u.report.Project, u.version, u.decisionsFrom, u.logFrom, u.wait, func() error {
		if u.tmuxSessionExists(u.report.TmuxSession) {
			return nil
		}
		text := readFrom(filepath.Join(schedproc.Dir(u.q.SessionRoot()), "scheduler.log"), u.logFrom)
		return refuse(firstNonEmptyStr(namedRefusal(text), "lead_exited"), "the lead's tmux session %s ended before the roster's outcome; the scheduler's log: %s", u.report.TmuxSession, firstNonEmptyStr(lastLine(text), "(empty)"))
	}, "attach with: "+u.report.Attach)
	u.report.Roster = seats
	return detail, err
}

// UpRosterOutcome is the roster step (shared by up and the lead's start of
// a confirmed team): decisions.jsonl from decisionsFrom until the roster's
// outcome (at most wait), the scheduler's log refusals from logFrom by
// name, and leadGone (nil: no check) stopping the wait by its refusal. It
// returns the outcome line and the roster from status.
func UpRosterOutcome(root, project, version string, decisionsFrom, logFrom int64, wait time.Duration, leadGone func() error, hint string) (string, []status.Seat, error) {
	countShared("roster")
	logPath := filepath.Join(schedproc.Dir(root), "scheduler.log")
	off := decisionsFrom
	deadline := upNow().Add(wait)
	for {
		lines, next, _ := schedproc.ReadDecisionsFrom(root, off)
		off = next
		for _, d := range lines {
			switch {
			case d.Kind == "roster_launch_failed":
				return "", nil, refuse(firstNonEmptyStr(namedRefusal(d.Reason), "roster_launch_failed"), "%s", d.Reason)
			case slices.Contains(rosterOutcomes, d.Kind):
				return d.Kind + ": " + firstNonEmptyStr(d.Reason, d.Seat), buildStatusContract(root, project, version, upNow().UTC()).Seats, nil
			case d.Kind == "scheduler_stopped" && d.Reason != "":
				return "", nil, refuse(firstNonEmptyStr(namedRefusal(d.Reason), "scheduler_stopped"), "%s", d.Reason)
			}
		}
		if text := readFrom(logPath, logFrom); namedRefusal(text) != "" {
			return "", nil, refuse(namedRefusal(text), "the scheduler's log (%s): %s", logPath, lastLine(text))
		}
		if leadGone != nil {
			if err := leadGone(); err != nil {
				return "", nil, err
			}
		}
		if !upNow().Before(deadline) {
			return "", nil, refuse("roster_wait_timeout", "no roster outcome in decisions.jsonl after %s; %s", wait, hint)
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
