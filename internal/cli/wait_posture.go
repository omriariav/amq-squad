package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const maxOwnPaneLeadWait = 120 * time.Second

const (
	defaultAMQWatchWait         = time.Minute
	defaultAMQReceiptWait       = time.Minute
	defaultOwnedSendReceiptWait = 2 * time.Minute
)

type waitPostureRequest struct {
	Command    string
	WaitKind   string
	ProjectDir string
	Profile    string
	Session    string
	Root       string
	Timeout    time.Duration
	Unbounded  bool
	Blocking   bool
	Override   bool
	Reason     string
}

type waitPostureAuditRecord struct {
	At        time.Time `json:"at"`
	Command   string    `json:"command"`
	WaitKind  string    `json:"wait_kind"`
	Project   string    `json:"project"`
	Profile   string    `json:"profile"`
	Session   string    `json:"session"`
	Namespace string    `json:"namespace"`
	Root      string    `json:"root"`
	Actor     string    `json:"actor"`
	PaneID    string    `json:"pane_id"`
	Timeout   string    `json:"timeout"`
	Gates     []string  `json:"gates,omitempty"`
	Reason    string    `json:"reason"`
}

var (
	waitPostureReadTeam            = team.ReadProfile
	waitPostureResolveCurrentActor = defaultVerifiedCurrentPaneActor
	waitPostureLoadSnapshot        = defaultWaitPostureSnapshot
	waitPostureAppendAudit         = writeWaitPostureAudit
	waitPostureNow                 = time.Now
	waitPostureAuditWrite          = func(f *os.File, p []byte) (int, error) { return f.Write(p) }
	waitPostureAuditSync           = func(f *os.File) error { return f.Sync() }
)

func guardOwnedWait(req waitPostureRequest) error {
	if !req.Blocking {
		return nil
	}
	cfg, err := waitPostureReadTeam(req.ProjectDir, req.Profile)
	if err != nil {
		// Without a readable team there is no evidence that this is the configured
		// lead in lead_pane mode. Preserve the pre-#416 behavior.
		return nil
	}
	op := team.EffectiveOperator(cfg)
	if !op.Enabled || op.InteractionMode != team.OperatorInteractionLeadPane {
		return nil
	}
	// Outside tmux this process cannot be the lead's own answer-delivery pane,
	// so preserve the existing wait behavior. A nonempty but unknown pane is
	// different: it might be the configured lead and therefore fails closed
	// through the verified-current-actor resolver below.
	if strings.TrimSpace(os.Getenv("TMUX_PANE")) == "" {
		return nil
	}
	leadRole := strings.TrimSpace(cfg.Lead)
	lead, ok := teamMemberByRole(cfg, leadRole)
	if !ok {
		return nil
	}
	leadHandle := memberHandle(lead)

	profile := squadnamespace.NormalizeProfile(req.Profile)
	actor, err := waitPostureResolveCurrentActor(req.ProjectDir, profile, req.Session, cfg)
	if err != nil {
		return waitPostureUncertain(req.Command, fmt.Sprintf("current roster actor/pane could not be verified: %v", err))
	}
	if actor.Handle != leadHandle || actor.Role != leadRole {
		return nil
	}
	if strings.TrimSpace(actor.PaneID) == "" || actor.Session != req.Session || !squadnamespace.ProfilesEqual(actor.Profile, profile) || !rootsMatch(actor.Root, req.Root) {
		return waitPostureUncertain(req.Command, "current lead actor/pane evidence does not match the selected profile/session/root")
	}

	snap, err := waitPostureLoadSnapshot(req.ProjectDir, op.Handle)
	if err != nil {
		return waitPostureUncertain(req.Command, fmt.Sprintf("collapsed operator-gate state could not be read: %v", err))
	}
	gates, found := callerOpenGates(snap, profile, req.Session, req.Root, actor.Handle)
	if !found {
		return waitPostureUncertain(req.Command, "the selected session was absent from collapsed runtime state")
	}
	overLimit := req.Unbounded || req.Timeout > maxOwnPaneLeadWait
	if len(gates) == 0 && !overLimit {
		return nil
	}
	if !req.Override {
		parts := make([]string, 0, 2)
		if len(gates) > 0 {
			parts = append(parts, "unresolved caller-raised operator gates: "+strings.Join(gates, ", "))
		}
		if overLimit {
			parts = append(parts, fmt.Sprintf("requested wait %s exceeds the own-pane lead maximum %s", waitPostureTimeout(req), maxOwnPaneLeadWait))
		}
		return usageErrorf("refusing %s before blocking in verified lead pane %s: %s. Park/end the turn now so operator input can be processed; inspect and reconcile the named gate threads before continuing. For a deliberate exception, pass --override-wait-posture --wait-posture-reason <why>.", waitPostureCommand(req.Command), actor.PaneID, strings.Join(parts, "; "))
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return usageErrorf("%s --override-wait-posture requires --wait-posture-reason <why>", waitPostureCommand(req.Command))
	}
	rec := waitPostureAuditRecord{
		At: waitPostureNow().UTC(), Command: waitPostureCommand(req.Command), WaitKind: strings.TrimSpace(req.WaitKind),
		Project: req.ProjectDir, Profile: profile, Session: req.Session,
		Namespace: squadnamespace.ID(profile, req.Session), Root: req.Root,
		Actor: actor.Handle, PaneID: actor.PaneID, Timeout: waitPostureTimeout(req),
		Gates: append([]string(nil), gates...), Reason: reason,
	}
	if err := waitPostureAppendAudit(rec); err != nil {
		return fmt.Errorf("refusing %s: persist wait-posture override audit before blocking: %w", rec.Command, err)
	}
	return nil
}

func waitPostureForContext(command, waitKind string, ctx amqContext, timeout time.Duration, unbounded, blocking, override bool, reason string) waitPostureRequest {
	return waitPostureRequest{
		Command: command, WaitKind: waitKind, ProjectDir: ctx.ProjectDir,
		Profile: ctx.Profile, Session: ctx.Session, Root: ctx.Root,
		Timeout: timeout, Unbounded: unbounded, Blocking: blocking,
		Override: override, Reason: reason,
	}
}

func parseWaitPostureDuration(command, raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, usageErrorf("%s invalid wait timeout %q: %v", command, raw, err)
	}
	if timeout < 0 {
		return 0, usageErrorf("%s wait timeout must be non-negative", command)
	}
	return timeout, nil
}

func durableSendWaitPosture(command string, ctx amqContext, args []string, override bool, reason string) (waitPostureRequest, error) {
	waitFor := strings.ToLower(strings.TrimSpace(lastAMQSendFlagValue(args, "wait-for")))
	blocking := waitFor != "" && waitFor != dispatchNoWait
	if !blocking {
		return waitPostureForContext(command, "delivery_receipt", ctx, 0, false, false, override, reason), nil
	}
	timeout, err := parseWaitPostureDuration(command, lastAMQSendFlagValue(args, "wait-timeout"), defaultOwnedSendReceiptWait)
	if err != nil {
		return waitPostureRequest{}, err
	}
	return waitPostureForContext(command, "delivery_receipt", ctx, timeout, timeout == 0, true, override, reason), nil
}

// lastAMQSendFlagValue mirrors the external AMQ flag parser's last-value-wins
// behavior while respecting values (such as a subject literally equal to
// "--wait-for") that belong to a preceding send flag.
func lastAMQSendFlagValue(args []string, target string) string {
	var last string
	for i := 0; i < len(args); i++ {
		name, inline, joined := amqFlagName(args[i])
		if joined {
			if name == target {
				last = inline
			}
			continue
		}
		if amqSendPassthroughFlagConsumesValue(name, false) && i+1 < len(args) {
			if name == target {
				last = args[i+1]
			}
			i++
		}
	}
	return last
}

func lastAMQWatchTimeout(args []string) string {
	var last string
	for i := 0; i < len(args); i++ {
		name, inline, joined := amqFlagName(args[i])
		if name != "timeout" {
			continue
		}
		if joined {
			last = inline
			continue
		}
		if i+1 < len(args) {
			last = args[i+1]
			i++
		}
	}
	return last
}

func waitPostureUncertain(command, detail string) error {
	return usageErrorf("refusing %s before blocking: %s. This may be the configured lead in lead_pane mode; park/end the turn and inspect status/gates instead.", waitPostureCommand(command), detail)
}

func waitPostureCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "wait"
	}
	return command
}

func waitPostureTimeout(req waitPostureRequest) string {
	if req.Unbounded {
		return "unbounded"
	}
	return req.Timeout.String()
}

func callerOpenGates(snap state.Snapshot, profile, session, root, actor string) ([]string, bool) {
	var gates []string
	for _, candidate := range snap.Sessions {
		if candidate.Name != session || !squadnamespace.ProfilesEqual(candidate.TeamProfile, profile) || !rootsMatch(candidate.Root, root) {
			continue
		}
		for _, thread := range candidate.Coordination.Threads {
			if thread.OperatorGate == nil || thread.OperatorGate.From != actor || !strings.HasPrefix(thread.ID, "gate/") {
				continue
			}
			gates = append(gates, thread.ID)
		}
		sort.Strings(gates)
		return gates, true
	}
	return nil, false
}

func defaultWaitPostureSnapshot(projectDir, operatorHandle string) (state.Snapshot, error) {
	baseRoot, err := resolveOperatorActorBaseRoot(projectDir)
	if err != nil {
		return state.Snapshot{}, err
	}
	return state.BuildWithThresholds(projectDir, baseRoot, state.DefaultProbe, state.Thresholds{OperatorHandle: operatorHandle})
}

func writeWaitPostureAudit(rec waitPostureAuditRecord) error {
	dir := filepath.Join(rec.Project, team.DirName, "wait-posture-audit", squadnamespace.NormalizeProfile(rec.Profile))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure wait-posture audit dir: %w", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal wait-posture audit: %w", err)
	}
	path := filepath.Join(dir, sanitizeWorkstreamName(rec.Session)+".jsonl")
	return flock.WithLock(path+".lock", func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open wait-posture audit: %w", err)
		}
		line := append(b, '\n')
		n, writeErr := waitPostureAuditWrite(f, line)
		if writeErr == nil && n != len(line) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			_ = f.Close()
			return fmt.Errorf("write wait-posture audit: %w", writeErr)
		}
		if err := waitPostureAuditSync(f); err != nil {
			_ = f.Close()
			return fmt.Errorf("sync wait-posture audit: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close wait-posture audit: %w", err)
		}
		return nil
	})
}
