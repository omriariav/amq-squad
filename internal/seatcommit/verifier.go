package seatcommit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/ledger"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The verdict producer (v3-5-verifier). The verifier executes code the seat
// wrote (its tests, its build scripts, its go:generate directives) under the
// owner's UID: it is the one step of the harness where seat-authored code
// runs with the harness's standing, so it is treated like a seat, not like a
// harness step.
//
//   - the command and its arguments come only from the owner grant
//     (`verifiers:`, class to one-line command, split on blanks, no shell);
//     nothing is read from the seat's tree;
//   - argv[0] is an absolute path in the grant or the name of a registry pin
//     (registry.OpenPinned: re-hashed now); nothing from PATH, and PATH in
//     the verifier is the core's tools directory only;
//   - it runs in the shadow worktree: a fresh directory under a private
//     TMPDIR, checked out from the snapshot tree through the harness's own
//     object reads (no filter, attribute or hook of the repository), never
//     the seat's live worktree;
//   - its environment is a seat's, built from nothing: a scratch HOME under
//     the private TMPDIR, no credential of any kind, no AMQ_SQUAD_BIN, AM_*
//     or CLAUDE_CONFIG_DIR, GOENV=off with GOCACHE, GOTMPDIR and GOPATH
//     under the private TMPDIR (never reused by the harness), and the
//     hardened git variables;
//   - bounded: the grant's verifier_timeout (default 10 minutes) and
//     verifier_output_limit (default 1 MiB of combined output); it runs in
//     its own process group, killed whole at either bound, and Wait gives up
//     on a pipe an escaped descendant holds after verifierWaitDelay;
//   - at most verifier_parallelism (default 2) run at once on the host
//     (lock-file slots under the session root); a commit step past that
//     waits with a status row;
//   - the exit code is the verdict (0 pass); over a bound is fail with
//     verifier_timeout or verifier_output_exceeded. The output goes to the
//     seat's evidence ledger through the redaction step, truncated at the
//     limit with a marker; the lead's block detail carries its 4 KiB tail.

// Reasons of a fail verdict that are not the command's own exit code.
const (
	ReasonVerifierTimeout = "verifier_timeout"
	ReasonVerifierOutput  = "verifier_output_exceeded"
	ReasonVerifierStart   = "verifier_start_failed"
	// ReasonVerifierInterrupted defers a step whose verifier started on this
	// snapshot and never recorded (the harness stopped mid-run): it never
	// runs again on the same snapshot by itself; the lead's reset (a new
	// lease) or the committer's deferral limit (commit_failed) ends it.
	ReasonVerifierInterrupted = "verifier_interrupted"
	// ReasonVerifierCancelled: the task was cancelled while its verifier ran;
	// the group is killed and no verdict is recorded.
	ReasonVerifierCancelled = "verifier_cancelled"
)

// VerifierSource is the source the commit step's verdicts carry.
const VerifierSource = "seat commit verifier"

// verifierWaitDelay bounds how long Wait waits for the output pipe after the
// verifier's process exited or was killed (an escaped descendant holding it).
var verifierWaitDelay = 2 * time.Second

// verifierSlotPoll is how often a commit step waiting for a slot retries.
var verifierSlotPoll = 250 * time.Millisecond

// VerifierOutcome is the verifier's run in the step's result.
type VerifierOutcome struct {
	Class         string `json:"class"`
	CommandDigest string `json:"command_digest"`
	Tree          string `json:"tree"`
	Result        string `json:"result"` // pass | fail
	ExitCode      int    `json:"exit_code"`
	Reason        string `json:"reason,omitempty"` // verifier_timeout | verifier_output_exceeded | verifier_start_failed
	DurationMS    int64  `json:"duration_ms"`
	OutputBytes   int    `json:"output_bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	Reused        bool   `json:"reused,omitempty"` // the attempt's recorded verdict on this tree
	Evidence      string `json:"evidence,omitempty"`
}

// verifierPlan is the resolved command of a task's class.
type verifierPlan struct {
	class, command, digest string
	argv                   []string
	tool                   *registry.VerifiedTool // the pinned argv[0]; nil for an absolute path
	timeout                time.Duration
	limit, parallelism     int
}

func (p *verifierPlan) close() {
	if p != nil {
		p.tool.Close()
	}
}

// planVerifier resolves the verifier of the task's class from the grant: nil
// when the class has none and is not gated; a refusal (code, route, detail)
// for a gated class with no command (verdict_missing: a configuration change
// during the lease, since such a task is never dispatched) and for an argv[0]
// that is neither an absolute path nor a pin (grants_invalid).
func planVerifier(o Options) (*verifierPlan, string, string, string) {
	class := o.Task.Class
	if !task.MechanicalVerifier(task.ClassVerifier(class)) {
		return nil, "", "", ""
	}
	g := o.Grants.Grants
	cmd := strings.TrimSpace(g.VerifierFor(class))
	if cmd == "" {
		if g.VerifierRequired(class) {
			return nil, task.BlockVerdictMissing, task.RouteLead, fmt.Sprintf("class %s is in the squad's verifier gate and the owner grant names no verifier command for it (verifiers.%s): no verdict, nothing committed or pushed", class, class)
		}
		return nil, "", "", ""
	}
	timeout, limit, par := g.VerifierBounds()
	p := &verifierPlan{class: class, command: cmd, digest: task.CommandDigest(cmd), argv: g.VerifierArgs(class), timeout: timeout, limit: limit, parallelism: par}
	name := p.argv[0]
	switch {
	case filepath.IsAbs(name):
		if filepath.Clean(name) != name {
			return nil, ReasonGrants, task.RouteLead, fmt.Sprintf("verifiers.%s: the command's path %q is not clean", class, name)
		}
		st, err := os.Stat(name)
		if err != nil || !st.Mode().IsRegular() {
			return nil, ReasonGrants, task.RouteLead, fmt.Sprintf("verifiers.%s: the command's path %q is not a regular file", class, name)
		}
	case strings.ContainsAny(name, `/\`):
		return nil, ReasonGrants, task.RouteLead, fmt.Sprintf("verifiers.%s: the command %q is a relative path; the grant names an absolute path or a registry pin", class, name)
	default:
		reg, err := registry.Read(registry.Path(o.Home))
		pin, ok := reg.PinFor(name)
		if err != nil || !ok {
			return nil, ReasonGrants, task.RouteLead, fmt.Sprintf("verifiers.%s: %q is neither an absolute path nor a registry pin (registry.yaml pins: name %s with its path and sha256); nothing is looked up on PATH", class, name, name)
		}
		tool, code, detail := registry.OpenPinned(name, pin.Path, pin.SHA256)
		if tool == nil {
			return nil, code, task.RouteOwner, "verifiers." + class + ": " + detail
		}
		p.tool = tool
	}
	return p, "", "", ""
}

// verify runs the class's verifier on the snapshot tree and records the
// verdict (rule 7: after the diff checks and the snapshot, before the push).
// It returns the result and whether the step ends here (a refusal, a
// deferral, a quarantine); a pass, or a class with no verifier, continues
// to the commit and the push.
func verify(o Options, sh *shadow, res Result) (Result, bool) {
	p, code, route, detail := planVerifier(o)
	if code != "" {
		return res.refuse(code, route, detail), true
	}
	if p == nil {
		return res, false
	}
	defer p.close()
	store, err := task.Open(o.Root)
	if err != nil {
		return res.defer_("the verifier: " + err.Error()), true
	}
	cur, err := store.Get(o.Task.ID)
	if err != nil {
		return res.defer_("the verifier: " + err.Error()), true
	}
	if cur.Lease == nil {
		return res.defer_(fmt.Sprintf("the verifier: task %s holds no lease to record a verdict on", cur.ID)), true
	}
	out := &VerifierOutcome{Class: p.class, CommandDigest: p.digest, Tree: res.Tree}
	// A re-run of the step (a deferred push or PR) finds the attempt's
	// verdict on this tree and this command: it is not run again. Only a
	// sealed verdict is reused (P1-a): one written into the lease without
	// the session seed is no verdict, and the verifier runs.
	v, unsealed := store.SealedVerdict(cur)
	if v != nil && v.Tree == res.Tree && v.CommandDigest == p.digest {
		out.Result, out.Reused = v.Result, true
		res.Verifier = out
		if v.Result != task.VerdictPass {
			return res.refuse(task.BlockVerdictFailed, task.RouteLead, v.Detail), true
		}
		res.Notes = append(res.Notes, "the verifier's sealed pass verdict on tree "+res.Tree+" was already recorded")
		return res, false
	}
	if cur.Lease.Verdict != nil && v == nil {
		res.Notes = append(res.Notes, task.BlockVerdictMissing+": the lease's verdict is not the harness's ("+unsealed+"); the verifier runs")
	}
	// A sealed snapshot of this tree and command with no verdict of it: the
	// verifier started and never recorded (the harness stopped mid-run). It
	// never runs again on the same snapshot by itself.
	if cur.Lease.Snapshot == res.Tree && cur.Lease.SnapshotCommand == p.digest && store.SnapshotSealed(cur) {
		r := res.defer_(fmt.Sprintf("%s: the verifier started on tree %s and recorded no verdict (the harness stopped mid-run); it does not run again on this snapshot: the lead's reset gives a new lease", ReasonVerifierInterrupted, res.Tree))
		r.ReasonCode = ReasonVerifierInterrupted
		return r, true
	}
	// The snapshot is recorded, sealed, just before the verifier starts.
	started := func() error {
		if _, err := store.RecordSnapshot(o.Task.ID, res.Tree, p.digest, o.Now()); err != nil {
			return err
		}
		return nil
	}
	run, err := runVerifier(o, sh, res.Parent, res.Tree, p, started)
	if errors.Is(err, task.ErrVerdictTreeMismatch) {
		return res.refuse(task.BlockVerdictTreeMismatch, task.RouteLead, err.Error()), true
	}
	if errors.Is(err, errLinkTargetRefused) {
		return res.refuse(ReasonLinkTarget, task.RouteLead, err.Error()), true
	}
	if err != nil {
		return res.defer_("the verifier: " + err.Error()), true
	}
	if run.reason == ReasonVerifierCancelled {
		r := res.defer_(ReasonVerifierCancelled + ": the task was cancelled while its verifier ran; the process group was killed, no verdict is recorded, nothing is pushed")
		r.ReasonCode = ReasonStepCancelled
		return r, true
	}
	out.ExitCode, out.Reason, out.DurationMS, out.OutputBytes, out.Truncated = run.exit, run.reason, run.dur.Milliseconds(), len(run.output), run.truncated
	out.Result = task.VerdictFail
	if run.exit == 0 && run.reason == "" {
		out.Result = task.VerdictPass
	}
	res.Verifier = out
	// The full redacted output stays in the evidence ledger.
	path := ledger.Path(o.Root, o.Handle)
	if err := writeVerifierEvidence(path, o, cur, out, run.output); err != nil {
		return res.defer_("the verifier's evidence: " + err.Error()), true
	}
	out.Evidence = path
	detail = verdictDetail(p, run)
	verdict := task.Verdict{Verifier: task.ClassVerifier(p.class), Result: out.Result, Source: VerifierSource, Detail: detail, Tree: res.Tree, CommandDigest: p.digest}
	if _, err := store.RecordVerdict(o.Task.ID, verdict, o.Now()); err != nil {
		if errors.Is(err, task.ErrVerdictTreeMismatch) {
			return res.refuse(task.BlockVerdictTreeMismatch, task.RouteLead, err.Error()), true
		}
		return res.defer_("record the verdict: " + err.Error()), true
	}
	if out.Result != task.VerdictPass {
		return res.refuse(task.BlockVerdictFailed, task.RouteLead, detail), true
	}
	return res, false
}

// verdictDetail is the lead's block detail: what ran and how it ended, then
// the tail of the redacted output, all within task.VerdictDetailCap.
func verdictDetail(p *verifierPlan, run verifierRun) string {
	head := fmt.Sprintf("verifiers.%s (%s) exited %d", p.class, filepath.Base(p.argv[0]), run.exit)
	switch run.reason {
	case ReasonVerifierTimeout:
		head = fmt.Sprintf("verifiers.%s: %s after %s, the process group was killed", p.class, ReasonVerifierTimeout, p.timeout)
	case ReasonVerifierOutput:
		head = fmt.Sprintf("verifiers.%s: %s, over %d bytes of output, the process group was killed", p.class, ReasonVerifierOutput, p.limit)
	case ReasonVerifierStart:
		head = fmt.Sprintf("verifiers.%s: %s: %s", p.class, ReasonVerifierStart, run.startErr)
	}
	head += "; the full redacted output is in the seat's evidence ledger; output tail:\n"
	tail := string(run.output)
	if room := task.VerdictDetailCap - len(head); len(tail) > room {
		// The tail never starts inside a line (P1-b).
		tail = tail[len(tail)-max(room, 0):]
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		} else {
			tail = ""
		}
	}
	return strings.TrimRight(head+tail, "\n")
}

// verifierRun is one run's end.
type verifierRun struct {
	exit      int
	reason    string
	startErr  string
	output    []byte // redacted, truncated at the limit with the marker
	truncated bool
	dur       time.Duration
}

// runVerifier takes a slot, checks the tree out into a fresh shadow
// worktree under a private TMPDIR, and runs the command there.
func runVerifier(o Options, sh *shadow, parent, tree string, p *verifierPlan, started func() error) (verifierRun, error) {
	release, err := verifierSlot(o, p)
	if err != nil {
		return verifierRun{}, err
	}
	defer release()
	private, err := os.MkdirTemp("", "amq-squad-verifier-*")
	if err != nil {
		return verifierRun{}, err
	}
	defer removeAllWritable(private)
	if private, err = filepath.EvalSymlinks(private); err != nil {
		return verifierRun{}, err
	}
	work := filepath.Join(private, "work")
	for _, d := range []string{"work", "home", "tmp", "gocache", "gotmp", "gopath"} {
		if err := os.Mkdir(filepath.Join(private, d), 0o700); err != nil {
			return verifierRun{}, err
		}
	}
	if err := checkoutTree(o, sh, parent, tree, work); err != nil {
		return verifierRun{}, fmt.Errorf("check out the snapshot tree %s: %w", tree, err)
	}
	var cmd *exec.Cmd
	if p.tool != nil {
		cmd = p.tool.Command(p.argv[1:]...)
	} else {
		cmd = exec.Command(p.argv[0], p.argv[1:]...)
	}
	modcache, err := verifierModCache(o.Root)
	if err != nil {
		return verifierRun{}, err
	}
	cmd.Dir, cmd.Env, cmd.Stdin = work, verifierEnv(o.Root, private, modcache, o.Grants.Grants.VerifierGoproxy), nil
	buf := &boundedOutput{limit: p.limit}
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.WaitDelay = verifierWaitDelay
	ownGroup(cmd)
	// The kill is wired before the start: the output's copier calls it.
	var mu sync.Mutex
	killed := ""
	kill := func(why string) {
		mu.Lock()
		defer mu.Unlock()
		if killed == "" {
			killed = why
			killGroup(cmd)
		}
	}
	buf.over = func() { kill(ReasonVerifierOutput) }
	if err := started(); err != nil {
		return verifierRun{}, err
	}
	start := time.Now()
	run := verifierRun{}
	if err := cmd.Start(); err != nil {
		run.exit, run.reason, run.startErr = -1, ReasonVerifierStart, err.Error()
		run.dur = time.Since(start)
		return run, nil
	}
	timer := time.AfterFunc(p.timeout, func() { kill(ReasonVerifierTimeout) })
	stop := make(chan struct{})
	if o.Ctx != nil {
		go func() {
			select {
			case <-o.Ctx.Done():
				kill(ReasonVerifierCancelled)
			case <-stop:
			}
		}()
	}
	werr := cmd.Wait()
	close(stop)
	timer.Stop()
	killGroup(cmd) // a descendant still in the group goes with it
	run.dur = time.Since(start)
	mu.Lock()
	run.reason = killed
	mu.Unlock()
	run.exit = cmd.ProcessState.ExitCode()
	if werr != nil && run.exit == 0 && !errors.Is(werr, exec.ErrWaitDelay) {
		run.exit = -1
	}
	raw, truncated := buf.bytes()
	run.truncated = truncated
	run.output = boundedRedacted(raw, truncated, p.limit)
	return run, nil
}

// boundedRedacted is the output as the ledger keeps it (P1-b): a truncated
// capture loses its partial last line first (a value cut at the cap would
// escape the redaction's matchers), then every line is redacted (binary
// output too: no NUL exemption), then the result is cut again at a line end
// within the limit, and the marker says so.
func boundedRedacted(raw []byte, truncated bool, limit int) []byte {
	if truncated {
		if i := bytes.LastIndexByte(raw, '\n'); i >= 0 {
			raw = raw[:i+1]
		} else {
			raw = nil
		}
	}
	out := redact.Block(string(raw))
	if len(out) > limit {
		out = out[:limit]
		truncated = true
		if i := strings.LastIndexByte(out, '\n'); i >= 0 {
			out = out[:i+1]
		} else {
			out = ""
		}
	}
	if truncated {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += fmt.Sprintf("[amq-squad: verifier output truncated at %d bytes; the partial last line was dropped]\n", limit)
	}
	return []byte(out)
}

// boundedOutput keeps the first limit bytes of the combined output and
// calls over once past it (the caller kills the group).
type boundedOutput struct {
	mu        sync.Mutex
	limit     int
	buf       bytes.Buffer
	truncated bool
	over      func()
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	room := b.limit - b.buf.Len()
	fire := false
	if len(p) > room {
		b.buf.Write(p[:max(room, 0)])
		fire = !b.truncated
		b.truncated = true
	} else {
		b.buf.Write(p)
	}
	over := b.over
	b.mu.Unlock()
	if fire && over != nil {
		over()
	}
	return len(p), nil
}

func (b *boundedOutput) bytes() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.truncated
}

// verifierEnv is the seat class of environment, built from nothing (rule
// 8): no variable of the harness's own environment is carried. GOCACHE,
// GOTMPDIR and GOPATH are private per run; the module cache is the
// verifier's own under the session root (never the owner's or the
// harness's), read-only to the build (-mod=readonly), checked against the
// checksum database (GOSUMDB is left at Go's default, on); GOPROXY is the
// grant's verifier_goproxy, else Go's default.
func verifierEnv(root, private, modcache, goproxy string) []string {
	tmp := filepath.Join(private, "tmp")
	env := []string{
		"PATH=" + ToolsDir(root),
		"HOME=" + filepath.Join(private, "home"),
		"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp,
		"LC_ALL=C", "LANG=C",
		"GOENV=off", "GOCACHE=" + filepath.Join(private, "gocache"), "GOTMPDIR=" + filepath.Join(private, "gotmp"),
		"GOPATH=" + filepath.Join(private, "gopath"), "GOMODCACHE=" + modcache, "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly",
		"GIT_TERMINAL_PROMPT=0", "GIT_CEILING_DIRECTORIES=" + private,
	}
	if goproxy != "" {
		env = append(env, "GOPROXY="+goproxy)
	}
	return append(env, hardenedVars...)
}

// verifierModCache is the verifier-only module cache under the session root
// (verifiers/gomodcache, 0700): created when absent; a symbolic link, a
// non-directory or a directory open to others is refused.
func verifierModCache(root string) (string, error) {
	dir := filepath.Join(VerifierDir(root), "gomodcache")
	if err := os.MkdirAll(VerifierDir(root), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	st, err := os.Lstat(dir)
	switch {
	case err != nil:
		return "", err
	case !st.IsDir():
		return "", fmt.Errorf("the verifier's module cache %s is not a plain directory", dir)
	case st.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows":
		return "", fmt.Errorf("the verifier's module cache %s has mode %04o, want 0700", dir, st.Mode().Perm())
	}
	return dir, nil
}

// verifierSlot takes one of the parallelism lock-file slots under the
// session root, waiting with a status row (verifiers/waiting/<seat>.json)
// while every slot is held. The wait is bounded by twice the verifier's
// timeout plus a minute; past it the step defers.
func verifierSlot(o Options, p *verifierPlan) (func(), error) {
	dir := VerifierDir(o.Root)
	if err := os.MkdirAll(filepath.Join(dir, "waiting"), 0o700); err != nil {
		return nil, err
	}
	waiting := filepath.Join(dir, "waiting", o.Handle+".json")
	deadline := time.Now().Add(2*p.timeout + time.Minute)
	wrote := false
	defer func() {
		if wrote {
			os.Remove(waiting)
		}
	}()
	for {
		for i := 0; i < p.parallelism; i++ {
			lock, ok, err := flock.TryExclusive(filepath.Join(dir, "slot-"+strconv.Itoa(i)+".lock"), true)
			if errors.Is(err, flock.ErrUnsupported) {
				return func() {}, nil // no advisory locks on this platform: no bound
			}
			if err != nil {
				return nil, err
			}
			if ok {
				return func() { lock.Close() }, nil
			}
		}
		if !wrote {
			row := VerifierWait{Seat: o.Handle, Task: o.Task.ID, Since: o.Now().UTC(), Parallelism: p.parallelism}
			raw, _ := json.Marshal(row)
			if err := atomicfile.Write(waiting, raw, 0o600); err != nil {
				return nil, err
			}
			wrote = true
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("every one of the %d verifier slots stayed busy past %s", p.parallelism, deadline.Format(time.RFC3339))
		}
		time.Sleep(verifierSlotPoll)
	}
}

// VerifierDir is the verifier's directory under the session root: the
// parallelism slots and the waiting rows.
func VerifierDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "verifiers")
}

// VerifierWait is the status row of a commit step waiting for a verifier
// slot.
type VerifierWait struct {
	Seat        string    `json:"seat"`
	Task        string    `json:"task"`
	Since       time.Time `json:"since"`
	Parallelism int       `json:"parallelism"`
}

// VerifierWaiting reads the waiting rows, sorted by seat.
func VerifierWaiting(root string) []VerifierWait {
	paths, _ := filepath.Glob(filepath.Join(VerifierDir(root), "waiting", "*.json"))
	var out []VerifierWait
	for _, p := range paths {
		var w VerifierWait
		if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &w) == nil && w.Seat != "" {
			out = append(out, w)
		}
	}
	return out
}

// checkoutTree writes the tree's files into dir from the object store
// through the shadow (ls-tree and one cat-file --batch): no filter,
// attribute or hook applies, so the files are the snapshot's bytes. A
// symbolic link is written as one; a gitlink is an empty directory. Every
// path is checked: no empty, ".", ".." or .git component, and no parent
// that is not a real directory.
func checkoutTree(o Options, sh *shadow, parent, tree, dir string) error {
	diff, err := sh.g.run(nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", "--raw", parent, tree)
	if err != nil {
		return err
	}
	changed, err := parseRaw(diff)
	if err != nil {
		return err
	}
	ps := protectedFor(o.Grants.Grants, sh.rp.Worktree, o.Home, filepath.Join(o.Root, "extensions", "amq-squad")+"/", "~/.amq-squad/")
	source := o.TreeObjects
	if source == nil {
		source = gitTreeObjectSource{g: sh.g}
	}
	refused, err := treeLinkRefusals(source, tree, changed, ps)
	if err != nil {
		return err
	}
	if len(refused) > 0 {
		return fmt.Errorf("%w: %s", errLinkTargetRefused, strings.Join(refused, "; "))
	}
	list, err := sh.g.run(nil, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return err
	}
	type entry struct{ mode, oid, path string }
	var entries []entry
	for _, rec := range strings.Split(string(list), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		for _, part := range strings.Split(p, "/") {
			if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") || strings.ContainsRune(part, '\\') {
				return fmt.Errorf("the tree has a path the checkout refuses: %q", p)
			}
		}
		entries = append(entries, entry{mode: f[0], oid: f[2], path: p})
	}
	batch := exec.Command(sh.g.bin, append(append(safeConfig(), sh.g.pre...), "cat-file", "--batch")...)
	batch.Dir, batch.Env = sh.g.dir, sh.g.env
	in, err := batch.StdinPipe()
	if err != nil {
		return err
	}
	outPipe, err := batch.StdoutPipe()
	if err != nil {
		return err
	}
	if err := batch.Start(); err != nil {
		return err
	}
	defer func() {
		in.Close()
		_ = batch.Wait()
	}()
	rd := bufio.NewReader(outPipe)
	read := func(oid string) ([]byte, error) {
		if _, err := io.WriteString(in, oid+"\n"); err != nil {
			return nil, err
		}
		head, err := rd.ReadString('\n')
		if err != nil {
			return nil, err
		}
		f := strings.Fields(head)
		if len(f) != 3 || f[0] != oid || f[1] != "blob" {
			return nil, fmt.Errorf("cat-file: %q", strings.TrimSpace(head))
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("cat-file size %q", f[2])
		}
		body := make([]byte, n+1)
		if _, err := io.ReadFull(rd, body); err != nil {
			return nil, err
		}
		return body[:n], nil
	}
	for _, e := range entries {
		abs := filepath.Join(dir, filepath.FromSlash(e.path))
		if err := realParents(dir, e.path); err != nil {
			return err
		}
		switch e.mode {
		case "160000":
			if err := os.Mkdir(abs, 0o755); err != nil {
				return err
			}
		case "120000":
			target, err := read(e.oid)
			if err != nil {
				return err
			}
			if err := os.Symlink(string(target), abs); err != nil {
				return err
			}
		case "100644", "100755":
			body, err := read(e.oid)
			if err != nil {
				return err
			}
			perm := fs.FileMode(0o644)
			if e.mode == "100755" {
				perm = 0o755
			}
			f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
			if err != nil {
				return err
			}
			_, werr := f.Write(body)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return werr
			}
		default:
			return fmt.Errorf("the tree entry %s has mode %s", e.path, e.mode)
		}
	}
	return nil
}

// realParents creates the parents of rel under root, each a real directory
// (an existing symbolic link or file there refuses).
func realParents(root, rel string) error {
	parts := strings.Split(rel, "/")
	cur := root
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("%s: a parent is not a real directory", rel)
		}
	}
	return nil
}

// removeAllWritable removes a tree the verifier may have left read-only
// (the module cache's files are 0444 in 0555 directories).
func removeAllWritable(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// verifierLine is the verifier's line in the seat's evidence ledger (the
// hook ledger's schema, event harness_verifier).
type verifierLine struct {
	Schema  int              `json:"schema"`
	Seq     uint64           `json:"seq"`
	At      string           `json:"at"`
	Handle  string           `json:"handle"`
	Event   string           `json:"event"`
	Outcome string           `json:"outcome"`
	Payload verifierEvidence `json:"payload"`
}

type verifierEvidence struct {
	Task          string `json:"task"`
	Attempt       int    `json:"attempt"`
	Class         string `json:"class"`
	CommandDigest string `json:"command_digest"`
	Tree          string `json:"tree"`
	ExitCode      int    `json:"exit_code"`
	Reason        string `json:"reason,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	Truncated     bool   `json:"truncated,omitempty"`
	Output        string `json:"output"` // redacted
}

// EventVerifier is the evidence ledger event of the verifier's line.
const EventVerifier = "harness_verifier"

func writeVerifierEvidence(path string, o Options, t task.Task, out *VerifierOutcome, output []byte) error {
	_, err := ledger.Append(path, func(seq uint64) any {
		return verifierLine{Schema: ledger.LineSchema, Seq: seq, At: o.Now().UTC().Format(time.RFC3339Nano), Handle: o.Handle, Event: EventVerifier, Outcome: out.Result,
			Payload: verifierEvidence{Task: t.ID, Attempt: t.Attempt, Class: out.Class, CommandDigest: out.CommandDigest, Tree: out.Tree, ExitCode: out.ExitCode,
				Reason: out.Reason, DurationMS: out.DurationMS, Truncated: out.Truncated, Output: string(output)}}
	})
	return err
}
