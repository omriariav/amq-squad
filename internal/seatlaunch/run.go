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
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// R51 (temporary, until upstream PR #1186 ships an AMQPath for embedders):
// the in-process launchapi resolves the AMQ executable with os.Executable(),
// which inside amq-squad is amq-squad, so panes would run `amq-squad
// __launch-exec` and fail (spike log 2026-10-22 06:24). The runner therefore
// drives Prepare and Apply through the installed amq CLI (`amq launch
// --request - --prepare --json`, `amq launch --apply - --json`), so the pane
// runs the real amq; the request and result types are the library's.

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
}

// SeatState is one seat's state after the launch.
type SeatState struct {
	Handle        string `json:"handle"`
	Up            bool   `json:"up"`
	State         string `json:"state"` // up | refused | not_started | pending
	ReasonCode    string `json:"reason_code,omitempty"`
	Detail        string `json:"detail,omitempty"`
	IdentityProof string `json:"identity_proof,omitempty"`
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
	}
	if err := c.Request.Validate(); err != nil {
		return res, fmt.Errorf("compiled launch request: %w", err)
	}
	env := LaunchEnv(o.Env)
	prepared, detail, err := prepareCLI(ctx, q, o, c.Request, env)
	if err != nil {
		return res, err
	}
	res.Prepare, res.FailureDetail = &prepared, detail
	if prepared.Outcome != launchapi.PrepareOutcomeReady && prepared.Outcome != launchapi.PrepareOutcomeActionRequired {
		res.Stage = "prepare_refused"
		res.NextActions = []string{"fix the cause named by the prepare reason (" + prepared.Reason + "), then run the same amq-squad launch again"}
		return res, nil
	}
	if err := CheckArgumentForms(q, prepared.Preview.Capabilities); err != nil {
		return res, err
	}
	decisions, missing, err := ResolveDecisions(prepared.RequiredActions, o.Decisions)
	if err != nil {
		return res, err
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
	if err := seatexec.WritePreflight(o.Home, true); err != nil {
		return res, err
	}
	if err := Stage(c); err != nil {
		return res, err
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
	res.Apply, res.FailureDetail = &applied, detail
	recordApply(c, applied, detail, o.Now())
	if applied.Outcome != "applied" {
		res.Stage = "apply_failed"
		res.NextActions = []string{"fix the cause named by reason_code and failure_detail, then run the same amq-squad launch again"}
		return res, nil
	}
	res.Stage = "applied"
	res.Seats = WaitSeats(ctx, q, launchID, o.Wait, o.Poll)
	return res, nil
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
	if jerr := json.Unmarshal(out, &res); jerr != nil || res.ResultVersion == 0 {
		return res, "", fmt.Errorf("amq launch --prepare exited %d without a PrepareResult: %s", code, strings.TrimSpace(string(errOut)))
	}
	return res, strings.TrimSpace(string(errOut)), nil
}

func applyCLI(ctx context.Context, q Squad, o Options, req launchapi.ApplyRequestV1, env []string) (launchapi.ApplyResultV1, string, error) {
	var res launchapi.ApplyResultV1
	body, _ := json.Marshal(req)
	out, errOut, code, err := o.RunAMQ(ctx, q.AMQPath, []string{"launch", "--apply", "-", "--json"}, body, env, q.ProjectRoot)
	if err != nil {
		return res, "", fmt.Errorf("amq launch --apply: %w", err)
	}
	detail := strings.TrimSpace(string(errOut))
	if jerr := json.Unmarshal(out, &res); jerr != nil || res.ResultVersion == 0 {
		return res, "", fmt.Errorf("amq launch --apply exited %d without an ApplyResult: %s", code, detail)
	}
	// FailureDetail is not in the JSON DTO; the CLI prints it on stderr (R48).
	res.FailureDetail = detail
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
		rec.LaunchNonce = r.BindingGeneration
		rec.Apply = &launch.Apply{Outcome: r.Outcome, ReasonCode: r.ReasonCode, FailureDetail: detail, At: now.UTC()}
		_ = launch.WriteFile(filepath.Join(launch.StagedDir(rec.Root, rec.Handle), launch.FileName), rec)
	}
}

// WaitSeats reports a seat up only when seat-exec published its exec block
// with an identity proof (R52); a refusal is a launch failure with its
// reason. Only a published record that carries this launch's id and the
// seat's request id counts, so a record left by an earlier launch of the
// same handle (even one second earlier) never reports a seat up.
func WaitSeats(ctx context.Context, q Squad, launchID string, wait, poll time.Duration) []SeatState {
	deadline := time.Now().Add(wait)
	states := make([]SeatState, len(q.Seats))
	for {
		done := true
		for i, s := range q.Seats {
			states[i] = seatState(q, s, launchID)
			if states[i].State == "pending" {
				done = false
			}
		}
		if done || time.Now().After(deadline) || ctx.Err() != nil {
			for i := range states {
				if states[i].State == "pending" {
					states[i].State, states[i].ReasonCode = "not_started", "no_exec_record"
					states[i].Detail = fmt.Sprintf("seat-exec published no exec record within %s", wait)
				}
			}
			return states
		}
		time.Sleep(poll)
	}
}

func seatState(q Squad, s Seat, launchID string) SeatState {
	st := SeatState{Handle: s.Handle, State: "pending"}
	rec, err := launch.Read(filepath.Join(q.SessionRoot(), "agents", s.Handle))
	if err != nil || rec.Exec == nil || launchID == "" || rec.LaunchID != launchID || rec.RequestID != s.RequestID {
		return st
	}
	switch rec.Exec.Outcome {
	case "exec":
		if rec.Exec.IdentityProof != "" {
			st.Up, st.State, st.IdentityProof = true, "up", rec.Exec.IdentityProof
		}
	case "refused":
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
