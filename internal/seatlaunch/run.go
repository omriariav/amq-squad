package seatlaunch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// R51 (temporary, until upstream PR #1186 ships an AMQPath for embedders):
// the in-process launchapi resolves the AMQ executable with os.Executable(),
// which inside amq-squad is amq-squad, so panes would run `amq-squad
// __launch-exec` and fail (spike log 2026-10-22 06:24). The runner therefore
// drives Prepare and Apply through the installed amq CLI (`amq launch
// --request - --prepare --json`, `amq launch --apply - --json`), so the pane
// runs the real amq; the request and result types are the library's.

// defaultPiShim refuses a build without the embedded runtime (the shim's
// probes would fail inside AMQ's Prepare with a less useful reason) and
// writes the shim.
func defaultPiShim(home, wrapper string) (string, error) {
	if _, err := piruntime.Load(); err != nil {
		return "", fmt.Errorf("this amq-squad build carries no embedded pi runtime: %w", err)
	}
	return piruntime.EnsureShim(home, wrapper)
}

// AMQRunner runs the amq CLI with stdin and returns stdout, stderr and the
// exit code (a non-zero exit is not an error: exit 6 is action_required).
type AMQRunner func(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) (stdout, stderr []byte, code int, err error)

// Options are the inputs of one launch.
type Options struct {
	Decisions map[string]string // ACTION_ID -> CHOICE, explicit operator answers
	DryRun    bool              // Prepare only
	Wait      time.Duration     // how long to wait for the seats to come up
	Home      string
	Env       []string
	Now       func() time.Time
	LookPath  func(string) (string, error)
	RunAMQ    AMQRunner
	Poll      time.Duration
	// PaneShell finds a seat's pane shell pid and its start stamp (default:
	// the read-only tmux pane query); a test seam, as is CloseProcess.
	PaneShell    func(handle string) (pid int, start string, ok bool)
	CloseProcess func(pid int, start string) error
	ProofClock   func() time.Time // the token window's clock (default time.Now, monotonic)
	// KeepPreflight fills only missing preflight copies instead of
	// refreshing them (the scheduler process, which refreshes once at start).
	KeepPreflight bool
	// Standing maps a required action kind to the owner's standing choice
	// for it (the scheduler process: `scheduler run --trust` is the owner's
	// standing trust of this squad's launch subjects). An explicit Decisions
	// entry wins; a standing choice the action does not allow is not used.
	Standing map[string]string
	// PermitWorktreeAllowRules relaxes seat-exec's claude_allow_rules_present
	// refusal (the one named switch of the 2026-10-24 ruling, for when the
	// live probes show a launch can override worktree allow rules).
	PermitWorktreeAllowRules bool
	// Target is the launch's record of the owner's grant for the commit
	// step's push (repository, base branch, push URL); nil reads the grant
	// of the squad's project root.
	Target *seatcommit.Target
	// ProbeMode asks the pinned Claude Code for the effective permission
	// mode of a Claude seat's config dir (default: the registry's claude
	// pin through claudeseat.ProbeMode); a test seam.
	ProbeMode func(ctx context.Context, pin registry.Pin, dir, mode string) (claudeseat.Probe, error)
	// PinFor is the registry's claude pin for a Claude seat (default: the
	// registry under Home, read once per launch); a test seam.
	PinFor func(s Seat) (registry.Pin, error)
	// PiShim writes the pi launch shim for the wrapper and returns its path
	// (default: the embedded runtime must load, then piruntime.EnsureShim
	// under Home); a test seam.
	PiShim func(home, wrapper string) (string, error)
	// BeforeApply checks a ready Prepare before anything is staged or
	// applied (3.0.1 t16, the roster launch): a non-empty reason stops the
	// launch at stage resource_present with the detail, so AMQ never sees
	// an Apply it would refuse.
	BeforeApply func(prepared launchapi.PrepareResultV1) (reason, detail string)
	// PaneID is the tmux pane id whose shell is pid (default: a read-only
	// tmux list-panes); a test seam.
	PaneID func(shell int) string
}

// SeatState is one seat's state after the launch.
type SeatState struct {
	Handle        string `json:"handle"`
	Up            bool   `json:"up"`
	State         string `json:"state"` // up | refused | not_started | pending
	ReasonCode    string `json:"reason_code,omitempty"`
	Detail        string `json:"detail,omitempty"`
	IdentityProof string `json:"identity_proof,omitempty"`
	PID           int    `json:"pid,omitempty"`   // from the accepted proof
	Start         string `json:"start,omitempty"` // its raw start stamp
}

// Result is what the operator sees (R48, R50).
type Result struct {
	LaunchID      string                       `json:"launch_id,omitempty"`
	ApplyStarted  bool                         `json:"apply_started,omitempty"` // amq launch --apply ran (a pane may exist)
	Stage         string                       `json:"stage"`                   // prepared | prepare_refused | decisions_pending | applied | apply_failed
	Prepare       *launchapi.PrepareResultV1   `json:"prepare,omitempty"`
	Apply         *launchapi.ApplyResultV1     `json:"apply,omitempty"`
	FailureDetail string                       `json:"failure_detail,omitempty"`
	Pending       []launchapi.RequiredActionV1 `json:"pending_actions,omitempty"`
	Seats         []SeatState                  `json:"seats,omitempty"`
	NextActions   []string                     `json:"next_actions,omitempty"`

	proof *proofServer
}

// WaitProofWindow blocks until the launch's proof socket has closed: the
// token window (launch.ProofTTL from minting) during which a second valid
// proof is still detected and alarmed. A caller that exits (the CLI) waits.
func (r Result) WaitProofWindow() {
	if r.proof != nil {
		r.proof.wait()
	}
}

func (o Options) withDefaults() Options {
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.RunAMQ == nil {
		o.RunAMQ = runAMQCLI
	}
	if o.Poll == 0 {
		o.Poll = 100 * time.Millisecond
	}
	if o.ProbeMode == nil {
		o.ProbeMode = probePinned
	}
	if o.PiShim == nil {
		o.PiShim = defaultPiShim
	}
	if o.PinFor == nil {
		home := o.Home
		var once sync.Once
		var pin registry.Pin
		var perr error
		o.PinFor = func(Seat) (registry.Pin, error) {
			once.Do(func() { pin, perr = registryClaudePin(home) })
			return pin, perr
		}
	}
	return o
}

// Launch compiles, stages, prepares and (with every required decision
// supplied) applies one squad, then waits for each seat's identity proof.
func Launch(ctx context.Context, q Squad, o Options) (Result, error) {
	o = o.withDefaults()
	var res Result
	q, err := q.Normalize()
	if err != nil {
		return res, err
	}
	rulesSHA, err := FileDigest(q.RulesPath)
	if err != nil {
		return res, fmt.Errorf("rules file: %w", err)
	}
	// A Claude seat's executable is the registry's claude pin (the narrow
	// pass's HIGH): Compile names it and refuses another provider, the probe
	// runs it, and seat-exec re-hashes it before exec.
	for _, s := range q.Seats {
		if s.Runtime != "claude" {
			continue
		}
		pin, err := o.PinFor(s)
		if err != nil {
			return res, fmt.Errorf("seat %s: %w", s.Handle, err)
		}
		if q.ClaudePins == nil {
			q.ClaudePins = map[string]registry.Pin{}
		}
		q.ClaudePins[s.Handle] = pin
	}
	// pi seats run the embedded runtime: the launch names the pi launch
	// shim as their executable (the file AMQ's pi adapter probes).
	for _, s := range q.Seats {
		if s.Runtime == "pi" {
			if q.PiShim, err = o.PiShim(o.Home, q.Wrapper); err != nil {
				return res, fmt.Errorf("seat %s: %s: %w", s.Handle, ReasonPiShimMissing, err)
			}
			break
		}
	}
	c, err := Compile(q, rulesSHA, o.LookPath, o.Now())
	if err != nil {
		return res, err
	}
	// A fresh nonce per launch: only a published record that carries it
	// (and the seat's request id) is this launch's proof.
	launchID := newLaunchID()
	res.LaunchID = launchID
	for i := range c.Records {
		c.Records[i].LaunchID = launchID
		if c.Records[i].Runtime == "pi" {
			// The seat's pi agent dir under the launch's HOME, recorded so
			// seat-exec and the launcher never derive it from theirs.
			c.Records[i].PiAgentDir = piruntime.AgentDirFor(o.Home, c.Records[i].Handle)
		}
	}
	if err := c.Request.Validate(); err != nil {
		return res, fmt.Errorf("compiled launch request: %w", err)
	}
	env := LaunchEnv(o.Env)
	prepared, detail, err := prepareCLI(ctx, q, o, c.Request, env)
	if err != nil {
		return res, err
	}
	res.Prepare, res.FailureDetail = &prepared, amqDetail(detail)
	if prepared.Outcome != launchapi.PrepareOutcomeReady && prepared.Outcome != launchapi.PrepareOutcomeActionRequired {
		res.Stage = "prepare_refused"
		res.NextActions = []string{"fix the cause named by the prepare reason (" + prepared.Reason + "), then run the same amq-squad launch again"}
		return res, nil
	}
	if err := CheckArgumentForms(q, prepared.Preview.Capabilities); err != nil {
		return res, err
	}
	decisions, missing, err := ResolveDecisions(prepared.RequiredActions, withStanding(prepared.RequiredActions, o.Decisions, o.Standing))
	if err != nil {
		return res, err
	}
	if o.BeforeApply != nil && !o.DryRun {
		if reason, detail := o.BeforeApply(prepared); reason != "" {
			res.Stage, res.FailureDetail = "resource_present", amqDetail(reason+": "+detail)
			res.NextActions = []string{"the named panes must end before the launch can apply (" + reason + ")"}
			return res, nil
		}
	}
	if o.DryRun {
		res.Stage = "prepared"
		res.Pending = missing
		res.NextActions = nextActions(q, missing, "")
		return res, nil
	}
	if len(missing) > 0 {
		res.Stage, res.Pending = "decisions_pending", missing
		res.NextActions = nextActions(q, missing, prepared.SubjectDigest)
		return res, nil
	}
	// The scheduler refreshes the preflight copies once at its start; a
	// refresh per seat launch would absorb a change a running seat made
	// before that seat's exit diff (amendment 28).
	if err := seatexec.WritePreflight(o.Home, !o.KeepPreflight); err != nil {
		return res, err
	}
	// Each Claude seat gets its own CLAUDE_CONFIG_DIR for this launch: the
	// hooks plugin and the permission mode as one unit, no owner rules. The
	// effective permission mode is probed on the pinned Claude Code before
	// any ticket is minted and before Apply (the fix round's probe ruling):
	// a setting that forces another mode would turn dontAsk into prompts.
	// Seats whose settings are identical are probed once.
	probed := map[string]claudeseat.Probe{}
	writtenAt := o.Now() // one time for every seat's config dir, so equal seats are equal
	for i, rec := range c.Records {
		if rec.Runtime != "claude" {
			continue
		}
		w, err := claudeseat.Write(claudeseat.Dir(rec.Root, rec.Handle, launchID), rec.PermissionMode, rec.Worktree, writtenAt)
		if err != nil {
			return res, fmt.Errorf("seat %s: the Claude config dir: %w", rec.Handle, err)
		}
		c.Records[i].ClaudeConfig = w.Digest
		pin := q.ClaudePins[rec.Handle]
		c.Records[i].ClaudePin = pin.SHA256 // what seat-exec compares the registry's pin with before exec
		kind, err := claudeseat.ProbeKind(w.Dir, rec.PermissionMode, pin.SHA256)
		if err != nil {
			return res, fmt.Errorf("seat %s: %s: %v", rec.Handle, claudeseat.ModeUnverified, err)
		}
		probe, done := probed[kind]
		if !done {
			if probe, err = o.ProbeMode(ctx, pin, w.Dir, rec.PermissionMode); err != nil {
				return res, fmt.Errorf("seat %s: %s: %v", rec.Handle, claudeseat.ModeUnverified, err)
			}
			probed[kind] = probe
		}
		if probe.Mode != rec.PermissionMode {
			return res, fmt.Errorf("seat %s: %s: the pinned Claude Code %s runs the seat's settings in permission mode %q, not the launch's %q", rec.Handle, claudeseat.ModeUnverified, probe.Version, probe.Mode, rec.PermissionMode)
		}
		if o.PermitWorktreeAllowRules {
			c.Records[i].AllowRules = "permit"
		}
	}
	// The proof socket and the seats' one-time tokens (item 4): a seat
	// counts up only from its proof over this socket. Minted only now, after
	// every probe, so a slow probe never eats a ticket's 60 s window.
	srv, err := startProofServer(q, launchID, o)
	if err != nil {
		return res, fmt.Errorf("proof socket: %w", err)
	}
	res.proof = srv // open for the whole token window; WaitProofWindow waits for its end
	for i := range c.Records {
		c.Records[i].Proof = srv.ticket(c.Records[i].Handle, c.Records[i].RequestID)
	}
	if err := Stage(c); err != nil {
		srv.close()
		return res, err
	}
	// The commit step's diff base is the core's observation of each seat
	// worktree before the seat runs (internal/seatcommit ledger). A worktree
	// that is not a git checkout records nothing; its commit step refuses
	// with base_unrecorded.
	// With it the launch records the owner's grant for the push (R2 of the
	// milestone B first pass): the repository, base branch, push URL and
	// local ack, and the pinned gh (D2). A target the launch refuses
	// (push_target_unacknowledged, an unpinned gh for an https push) stops
	// the launch before any seat applies.
	target := seatcommit.Target{}
	if o.Target != nil {
		target = *o.Target
	} else if g, err := grants.Load(q.ProjectRoot); err == nil {
		if target, err = seatcommit.LaunchTarget(g, o.Home, seatcommit.ToolsDir(q.SessionRoot())); err != nil {
			srv.close()
			return res, err
		}
	}
	if err := seatcommit.CheckTarget(target); err != nil {
		srv.close()
		return res, err
	}
	for _, rec := range c.Records {
		if rec.Branch != "" {
			_, _ = seatcommit.RecordWorktreeBase("", rec.Root, rec.Handle, rec.Worktree, rec.Branch, target, o.Now())
		}
	}
	apply := launchapi.ApplyRequestV1{
		RequestVersion: launchapi.RequestVersionV1, Prepare: c.Request,
		SubjectSchema: prepared.SubjectSchema, SubjectDigest: prepared.SubjectDigest, Decisions: decisions,
	}
	res.ApplyStarted = true
	applied, detail, err := applyCLI(ctx, q, o, apply, env)
	if err != nil {
		return res, err
	}
	res.Apply, res.FailureDetail = &applied, amqDetail(detail)
	recordApply(c, applied, detail, o.Now())
	if applied.Outcome != "applied" {
		res.Stage = "apply_failed"
		res.NextActions = []string{"fix the cause named by reason_code and failure_detail, then run the same amq-squad launch again"}
		return res, nil
	}
	res.Stage = "applied"
	res.Seats = WaitSeats(ctx, q, launchID, srv, o.Wait, o.Poll)
	return res, nil
}

// registryClaudePin is the claude pin of the registry under home.
func registryClaudePin(home string) (registry.Pin, error) {
	reg, err := registry.Read(registry.Path(home))
	if err != nil {
		return registry.Pin{}, fmt.Errorf("%s: the registry: %v", registry.PinMissing, err)
	}
	pin, ok := reg.PinFor("claude")
	if !ok {
		return registry.Pin{}, fmt.Errorf("%s: the registry pins no claude (registry.yaml pins: name claude, its real path and sha256)", registry.PinMissing)
	}
	return pin, nil
}

// probePinned runs the mode probe on the seat's pinned claude (opened
// through the pin and re-hashed, never PATH).
func probePinned(ctx context.Context, pin registry.Pin, dir, mode string) (claudeseat.Probe, error) {
	tool, code, detail := registry.OpenPinned("claude", pin.Path, pin.SHA256)
	if tool == nil {
		return claudeseat.Probe{}, fmt.Errorf("%s: %s", code, detail)
	}
	defer tool.Close()
	return claudeseat.ProbeMode(ctx, tool.Command, dir, mode)
}

// LaunchEnv is the environment of the amq launch subprocess: the caller's
// minus the AMQ identity, so a decision send never registers the caller (a
// lead) as live (R49: spike log 08:07, a decision send wrote an active lead
// presence and the next Apply was refused duplicate_live).
func LaunchEnv(parent []string) []string {
	var out []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(seatexec.IdentityVars, name) || name == "AM_ROOT_ID" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "AMQ_NO_UPDATE_CHECK=1")
}

func prepareCLI(ctx context.Context, q Squad, o Options, req launchapi.PrepareRequestV1, env []string) (launchapi.PrepareResultV1, string, error) {
	var res launchapi.PrepareResultV1
	body, _ := json.Marshal(req)
	out, errOut, code, err := o.RunAMQ(ctx, q.AMQPath, []string{"launch", "--request", "-", "--prepare", "--json"}, body, env, q.ProjectRoot)
	if err != nil {
		return res, "", fmt.Errorf("amq launch --prepare: %w", err)
	}
	// AMQ's stderr through the chokepoint, before anything wraps it
	// (amqDetail, 3.0.1 t17).
	stderr := amqDetail(string(errOut))
	if jerr := json.Unmarshal(out, &res); jerr != nil || res.ResultVersion == 0 {
		return res, "", fmt.Errorf("amq launch --prepare exited %d without a PrepareResult: %s", code, stderr)
	}
	return res, stderr, nil
}

func applyCLI(ctx context.Context, q Squad, o Options, req launchapi.ApplyRequestV1, env []string) (launchapi.ApplyResultV1, string, error) {
	var res launchapi.ApplyResultV1
	body, _ := json.Marshal(req)
	out, errOut, code, err := o.RunAMQ(ctx, q.AMQPath, []string{"launch", "--apply", "-", "--json"}, body, env, q.ProjectRoot)
	if err != nil {
		return res, "", fmt.Errorf("amq launch --apply: %w", err)
	}
	detail := amqDetail(string(errOut)) // the chokepoint, before the prefix
	if jerr := json.Unmarshal(out, &res); jerr != nil || res.ResultVersion == 0 {
		return res, "", fmt.Errorf("amq launch --apply exited %d without an ApplyResult: %s", code, detail)
	}
	// FailureDetail is not in the JSON DTO; the CLI prints it on stderr (R48).
	res.FailureDetail = amqDetail(detail)
	return res, detail, nil
}

// runAMQCLI captures stdout and stderr in temp files, not pipes: amq may
// start a tmux server that inherits the descriptors and outlives amq, and a
// pipe would hold Wait open for that server's whole life.
func runAMQCLI(ctx context.Context, amq string, args []string, stdin []byte, env []string, dir string) ([]byte, []byte, int, error) {
	var files [2]*os.File
	for i := range files {
		f, err := os.CreateTemp("", "amq-squad-launch-*")
		if err != nil {
			return nil, nil, 0, err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		files[i] = f
	}
	cmd := exec.CommandContext(ctx, amq, args...)
	cmd.Env, cmd.Dir, cmd.Stdin = env, dir, bytes.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = files[0], files[1]
	err := cmd.Run()
	stdout, _ := os.ReadFile(files[0].Name())
	stderr, _ := os.ReadFile(files[1].Name())
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout, stderr, exit.ExitCode(), nil
	}
	return stdout, stderr, 0, err
}

// recordApply writes the AMQ apply outcome and the launch nonce into each
// staged record (R48: the record keeps outcome, reason and detail).
func recordApply(c Compiled, r launchapi.ApplyResultV1, detail string, now time.Time) {
	for _, rec := range c.Records {
		// Through the one staged-record writer (fix 3c (f)): only the apply
		// fields change; the ticket stays as the launcher left it.
		_ = launch.UpdateStaged(rec.Root, rec.Handle, func(cur *launch.Record) error {
			cur.LaunchNonce = r.BindingGeneration
			cur.Apply = &launch.Apply{Outcome: r.Outcome, ReasonCode: r.ReasonCode, FailureDetail: amqDetail(detail), At: now.UTC()}
			return nil
		})
	}
}

// WaitSeats reports a seat up only when the launcher accepted its proof on
// the socket (item 4); the seat's published record is evidence only: it can
// name a refusal (this launch's id and the seat's request id), never an up.
func WaitSeats(ctx context.Context, q Squad, launchID string, srv *proofServer, wait, poll time.Duration) []SeatState {
	deadline := time.Now().Add(wait)
	states := make([]SeatState, len(q.Seats))
	for {
		done := true
		for i, s := range q.Seats {
			states[i] = seatState(q, s, launchID, srv)
			if states[i].State == "pending" {
				done = false
			}
		}
		if done || time.Now().After(deadline) || ctx.Err() != nil {
			for i := range states {
				if states[i].State == "pending" {
					states[i].State, states[i].ReasonCode = "not_started", "no_proof"
					states[i].Detail = fmt.Sprintf("no accepted proof within %s", wait)
				}
			}
			return states
		}
		time.Sleep(poll)
	}
}

func seatState(q Squad, s Seat, launchID string, srv *proofServer) SeatState {
	st := SeatState{Handle: s.Handle, State: "pending"}
	if srv != nil {
		up, pid, start, refused, detail := srv.state(s.Handle)
		if up {
			st.Up, st.State, st.IdentityProof, st.PID, st.Start = true, "up", "proof accepted", pid, start
			return st
		}
		if refused != "" {
			st.State, st.ReasonCode, st.Detail = "refused", refused, detail
			return st
		}
	}
	// Evidence only: seat-exec's own refusal for this launch.
	rec, err := launch.Read(filepath.Join(q.SessionRoot(), "agents", s.Handle))
	if err == nil && rec.Exec != nil && rec.Exec.Outcome == "refused" && launchID != "" && rec.LaunchID == launchID && rec.RequestID == s.RequestID {
		st.State, st.ReasonCode, st.Detail = "refused", rec.Exec.ReasonCode, rec.Exec.Detail
	}
	return st
}

// ResolveDecisions matches explicit decisions to Prepare's required actions:
// a supplied answer for an unknown action is stale and refused, a choice
// outside the allowed set is refused, and an unanswered action stays
// pending. Nothing is ever decided on the operator's behalf.
func ResolveDecisions(actions []launchapi.RequiredActionV1, supplied map[string]string) ([]launchapi.DecisionV1, []launchapi.RequiredActionV1, error) {
	known := map[string]bool{}
	for _, a := range actions {
		known[a.ActionID] = true
	}
	for id := range supplied {
		if !known[id] {
			return nil, nil, fmt.Errorf("--decision %s matches no action this Prepare returned (stale answer)", id)
		}
	}
	decisions := []launchapi.DecisionV1{}
	var missing []launchapi.RequiredActionV1
	for _, a := range actions {
		raw, ok := supplied[a.ActionID]
		if !ok {
			missing = append(missing, a)
			continue
		}
		if !slices.Contains(a.AllowedDecisions, launchapi.DecisionChoiceV1(raw)) {
			return nil, nil, fmt.Errorf("--decision %s=%s is not allowed for %s; allowed: %v", a.ActionID, raw, a.Kind, a.AllowedDecisions)
		}
		decisions = append(decisions, launchapi.DecisionV1{ActionID: a.ActionID, Choice: launchapi.DecisionChoiceV1(raw)})
	}
	return decisions, missing, nil
}

// withStanding adds the owner's standing choice for each required action
// that has no explicit answer, when the action allows that choice.
func withStanding(actions []launchapi.RequiredActionV1, supplied, standing map[string]string) map[string]string {
	if len(standing) == 0 {
		return supplied
	}
	out := map[string]string{}
	for k, v := range supplied {
		out[k] = v
	}
	for _, a := range actions {
		choice, ok := standing[string(a.Kind)]
		if _, answered := out[a.ActionID]; ok && !answered && slices.Contains(a.AllowedDecisions, launchapi.DecisionChoiceV1(choice)) {
			out[a.ActionID] = choice
		}
	}
	return out
}

// nextActions names only flags and commands that work on this path (R50).
func nextActions(q Squad, pending []launchapi.RequiredActionV1, subject string) []string {
	var out []string
	for _, a := range pending {
		choices := make([]string, len(a.AllowedDecisions))
		for i, c := range a.AllowedDecisions {
			choices[i] = string(c)
		}
		out = append(out, fmt.Sprintf("answer %s (%s, handles %s): re-run amq-squad launch with --decision %s=<%s>",
			a.Kind, a.ReasonCode, strings.Join(a.Handles, ","), a.ActionID, strings.Join(choices, "|")))
	}
	if subject != "" && len(pending) > 0 {
		out = append(out, "the answers bind subject "+subject+"; if the squad changes, Prepare returns new action ids")
	}
	return out
}

func newLaunchID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported hosts
	}
	return hex.EncodeToString(b)
}
